// Package eval measures review claims against explicit, versioned ground truth.
package eval

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/agendacraft/touchstone/internal/patch"
)

//go:embed cases
var bundled embed.FS

func Bundled() fs.FS {
	sub, _ := fs.Sub(bundled, "cases")
	return sub
}

type Expected struct {
	File     string   `json:"file"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases,omitempty"`
	Kind     string   `json:"kind"`
	Severity string   `json:"severity,omitempty"`
}

type Truth struct {
	Version    int        `json:"version"`
	Difficulty string     `json:"difficulty"`
	Holdout    bool       `json:"holdout"`
	Source     string     `json:"source"`
	Rationale  string     `json:"rationale"`
	Findings   []Expected `json:"findings"`
}

type Case struct {
	Name        string
	Diff        []byte
	Truth       Truth
	Fingerprint string
}

func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON document")
	}
	return nil
}

func Load(source fs.FS) ([]Case, error) {
	if source == nil {
		return nil, errors.New("case source required")
	}
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		c, err := loadCase(source, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("case %s: %w", entry.Name(), err)
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, errors.New("no eval cases")
	}
	return cases, nil
}

func loadCase(source fs.FS, name string) (Case, error) {
	c := Case{Name: name}
	data, err := fs.ReadFile(source, name+"/expected.json")
	if err != nil {
		return c, err
	}
	if err := decode(data, &c.Truth); err != nil {
		return c, errors.New("invalid expected.json")
	}
	if err := c.Truth.validate(); err != nil {
		return c, err
	}
	c.Diff, err = fs.ReadFile(source, name+"/diff.patch")
	if err != nil {
		return c, err
	}
	files, err := patch.Split(c.Diff)
	if err != nil || len(files) == 0 || len(c.Diff) > 16<<20 {
		return c, errors.New("expected nonempty unified text diff under 16 MiB")
	}
	paths := map[string]bool{}
	for _, file := range files {
		if file.Binary {
			return c, errors.New("binary eval input is unsupported")
		}
		paths[file.Path] = true
	}
	for _, e := range c.Truth.Findings {
		if !paths[e.File] {
			return c, errors.New("expected finding outside diff paths")
		}
	}
	sum := sha256.New()
	_, _ = sum.Write(c.Diff)
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write(data)
	c.Fingerprint = hex.EncodeToString(sum.Sum(nil))
	return c, nil
}

func (t Truth) validate() error {
	if t.Version != 1 || t.Source == "" || t.Rationale == "" || t.Findings == nil ||
		(t.Difficulty != "easy" && t.Difficulty != "medium" && t.Difficulty != "hard") {
		return errors.New("invalid case metadata")
	}
	for i, e := range t.Findings {
		if !findings.ValidPath(e.File) || e.Start < 1 || e.End < e.Start || norm(e.Category) == "" ||
			(e.Kind != "must-find" && e.Kind != "must-not-flag") {
			return errors.New("invalid expected finding")
		}
		if e.Kind == "must-find" && e.Severity != "error" && e.Severity != "warning" && e.Severity != "info" {
			return errors.New("must-find requires expected severity")
		}
		for _, alias := range e.Aliases {
			if norm(alias) == "" {
				return errors.New("empty category alias")
			}
		}
		for _, prior := range t.Findings[:i] {
			if e.File == prior.File && e.Start <= prior.End && prior.Start <= e.End && categoriesOverlap(e, prior) {
				return errors.New("ambiguous overlapping expectations")
			}
		}
	}
	return nil
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func categoryMatch(e Expected, category string) bool {
	for _, label := range append([]string{e.Category}, e.Aliases...) {
		if norm(label) == norm(category) {
			return true
		}
	}
	return false
}

func categoriesOverlap(a, b Expected) bool {
	for _, label := range append([]string{a.Category}, a.Aliases...) {
		if categoryMatch(b, label) {
			return true
		}
	}
	return false
}
