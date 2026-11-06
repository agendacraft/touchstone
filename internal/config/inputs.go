package config

import (
	"errors"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/agendacraft/touchstone/internal/findings"
)

const DefaultMaxDiffBytes = 16 << 20
const HardMaxDiffBytes = 256 << 20

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Input struct {
	MaxDiffBytes int      `toml:"max_diff_bytes"`
	Ignore       []string `toml:"ignore"`
}

func (i Input) Validate() error {
	if i.MaxDiffBytes < 1 || i.MaxDiffBytes > HardMaxDiffBytes {
		return errors.New("max-diff-bytes must be between 1 and 268435456")
	}
	for _, pattern := range i.Ignore {
		if err := ValidateGlob(pattern); err != nil {
			return err
		}
	}
	return nil
}

func ValidateGlob(pattern string) error {
	if !findings.ValidPath(pattern) {
		return errors.New("path pattern must be repository-relative and normalized")
	}
	if _, err := path.Match(pattern, "example"); err != nil {
		return errors.New("invalid path glob")
	}
	if strings.Contains(pattern, "**") {
		return errors.New("recursive ** globs are not supported; use slash-separated path.Match patterns")
	}
	return nil
}

type Checks struct {
	Enabled      bool     `toml:"enabled"`
	EnvAllowlist []string `toml:"env_allowlist"`
	Commands     []Check  `toml:"commands"`
}

type Check struct {
	Name           string   `toml:"name"`
	Args           []string `toml:"args"`
	Timeout        string   `toml:"timeout"`
	MaxOutputBytes int      `toml:"max_output_bytes"`
	LocationRegex  string   `toml:"location_regex"`
}

func (c Check) Duration() time.Duration {
	if c.Timeout == "" {
		return time.Minute
	}
	d, _ := time.ParseDuration(c.Timeout)
	return d
}

func (c Check) OutputLimit() int {
	if c.MaxOutputBytes == 0 {
		return 64 << 10
	}
	return c.MaxOutputBytes
}

func (c Checks) validate() error {
	if len(c.Commands) > 64 {
		return errors.New("at most 64 checks may be configured")
	}
	for _, name := range c.EnvAllowlist {
		if !environmentName.MatchString(name) {
			return errors.New("invalid check environment allowlist name")
		}
	}
	seen := map[string]bool{}
	for _, command := range c.Commands {
		if strings.TrimSpace(command.Name) == "" || seen[command.Name] {
			return errors.New("checks require unique nonempty names")
		}
		seen[command.Name] = true
		if len(command.Args) == 0 || strings.TrimSpace(command.Args[0]) == "" {
			return errors.New("check args must be a nonempty argument vector")
		}
		for _, arg := range command.Args {
			if strings.ContainsRune(arg, 0) {
				return errors.New("check arguments must not contain NUL")
			}
		}
		if command.Duration() <= 0 || command.Duration() > 30*time.Minute {
			return errors.New("check timeout must be a positive duration up to 30m")
		}
		if command.OutputLimit() < 1 || command.OutputLimit() > 1<<20 {
			return errors.New("check max_output_bytes must be between 1 and 1048576")
		}
		if command.LocationRegex != "" {
			re, err := regexp.Compile(command.LocationRegex)
			if err != nil {
				return errors.New("invalid check location_regex")
			}
			if re.SubexpIndex("file") < 1 || re.SubexpIndex("line") < 1 {
				return errors.New("check location_regex requires named file and line captures")
			}
		}
	}
	return nil
}

func (c Config) Input() Input {
	input := c.input
	input.Ignore = append([]string(nil), input.Ignore...)
	return input
}

func (c Config) Checks() Checks {
	checks := c.checks
	checks.EnvAllowlist = append([]string(nil), checks.EnvAllowlist...)
	checks.Commands = append([]Check(nil), checks.Commands...)
	for i := range checks.Commands {
		checks.Commands[i].Args = append([]string(nil), checks.Commands[i].Args...)
	}
	return checks
}
