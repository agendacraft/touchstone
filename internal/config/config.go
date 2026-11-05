// Package config loads repository policy over built-in layer defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/pelletier/go-toml/v2"
)

const FileName = "touchstone.toml"
const maxBytes = 1 << 20

type Layer struct {
	Model           string
	ReasoningEffort string
	Autonomy        string
	Timeout         time.Duration
	ReadOnly        bool
	Rules           []string
}

type override struct {
	Model           *string  `toml:"model"`
	ReasoningEffort *string  `toml:"reasoning_effort"`
	Autonomy        *string  `toml:"autonomy"`
	Timeout         *string  `toml:"timeout"`
	ReadOnly        *bool    `toml:"read_only"`
	Rules           []string `toml:"rules"`
}

type pathRule struct {
	Pattern string              `toml:"pattern"`
	Layers  map[string]override `toml:"layers"`
}

type document struct {
	Version *int                `toml:"version"`
	Layers  map[string]override `toml:"layers"`
	Paths   []pathRule          `toml:"paths"`
}

// Config is validated at load time; callers resolve policy for a single path.
type Config struct {
	layers map[string]Layer
	paths  []pathRule
}

var layerNames = []string{"correctness", "security", "verification"}

func Defaults() Config {
	c := Config{layers: make(map[string]Layer)}
	for _, name := range layerNames {
		c.layers[name] = Layer{
			Model: "gpt-6.1-sol", ReasoningEffort: "medium",
			Autonomy: "off", Timeout: 5 * time.Minute, ReadOnly: true,
		}
	}
	return c
}

// Load uses defaults when the repository file is absent. Other read errors fail.
func Load(repository string) (Config, error) {
	file := filepath.Join(repository, FileName)
	f, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(file); errors.Is(statErr, os.ErrNotExist) {
			return Defaults(), nil
		} else if statErr != nil {
			return Config{}, statErr
		}
	}
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (Config, error) {
	if len(data) > maxBytes {
		return Config{}, errors.New("configuration exceeds size limit")
	}
	var doc document
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		// Parser diagnostics can quote repository data; do not echo it.
		return Config{}, errors.New("invalid TOML or unknown configuration field")
	}
	if doc.Version != nil && *doc.Version != 1 {
		return Config{}, errors.New("unsupported configuration version")
	}
	c := Defaults()
	for name, o := range doc.Layers {
		base, ok := c.layers[name]
		if !ok {
			return Config{}, errors.New("unknown review layer")
		}
		layer, err := apply(base, o)
		if err != nil {
			return Config{}, fmt.Errorf("layer %s: %w", name, err)
		}
		c.layers[name] = layer
	}
	for _, rule := range doc.Paths {
		if !findings.ValidPath(rule.Pattern) {
			return Config{}, errors.New("path pattern must be repository-relative and normalized")
		}
		if _, err := path.Match(rule.Pattern, "example"); err != nil {
			return Config{}, errors.New("invalid path glob")
		}
		if strings.Contains(rule.Pattern, "**") {
			return Config{}, errors.New("recursive ** globs are not supported; use slash-separated path.Match patterns")
		}
		if len(rule.Layers) == 0 {
			return Config{}, errors.New("path rule requires layer overrides")
		}
		for name, o := range rule.Layers {
			base, ok := c.layers[name]
			if !ok {
				return Config{}, errors.New("unknown path-rule layer")
			}
			if _, err := apply(base, o); err != nil {
				return Config{}, fmt.Errorf("path-rule layer %s: %w", name, err)
			}
		}
	}
	c.paths = doc.Paths
	// Stable ties retain declaration order, so the later rule wins.
	sort.SliceStable(c.paths, func(i, j int) bool {
		return specificity(c.paths[i].Pattern) < specificity(c.paths[j].Pattern)
	})
	return c, nil
}

func apply(base Layer, o override) (Layer, error) {
	base.Rules = append([]string(nil), base.Rules...)
	if o.Model != nil {
		base.Model = *o.Model
	}
	if o.ReasoningEffort != nil {
		base.ReasoningEffort = *o.ReasoningEffort
	}
	if o.Autonomy != nil {
		base.Autonomy = *o.Autonomy
	}
	if o.ReadOnly != nil {
		base.ReadOnly = *o.ReadOnly
	}
	if o.Timeout != nil {
		d, err := time.ParseDuration(*o.Timeout)
		if err != nil || d <= 0 {
			return Layer{}, errors.New("timeout must be a positive duration")
		}
		base.Timeout = d
	}
	if strings.TrimSpace(base.Model) == "" || !agent.ValidEffort(base.ReasoningEffort) {
		return Layer{}, errors.New("invalid model or reasoning effort")
	}
	if base.Autonomy != "off" || !base.ReadOnly {
		return Layer{}, errors.New("repository review layers must remain read-only with autonomy off")
	}
	for _, rule := range o.Rules {
		if strings.TrimSpace(rule) == "" {
			return Layer{}, errors.New("rules must not be empty")
		}
	}
	base.Rules = append(base.Rules, o.Rules...)
	return base, nil
}

func specificity(pattern string) int {
	// Exact paths outrank globs. Within globs, more literal characters win.
	literal := 0
	for _, c := range pattern {
		if !strings.ContainsRune("*?[]", c) {
			literal++
		}
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return 1<<30 + literal
	}
	return literal
}

func (c Config) ForPath(file string) (map[string]Layer, error) {
	if !findings.ValidPath(file) {
		return nil, errors.New("path must be repository-relative and normalized")
	}
	layers := make(map[string]Layer, len(c.layers))
	for name, layer := range c.layers {
		layer.Rules = append([]string(nil), layer.Rules...)
		layers[name] = layer
	}
	for _, rule := range c.paths {
		matched, err := path.Match(rule.Pattern, file)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		for name, o := range rule.Layers {
			layer, err := apply(layers[name], o)
			if err != nil {
				return nil, err
			}
			layers[name] = layer
		}
	}
	return layers, nil
}
