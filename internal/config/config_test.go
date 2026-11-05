package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
)

func TestDefaultAndRepositoryPrecedence(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	layers, err := c.ForPath("src/example.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("want three built-in layers, got %d", len(layers))
	}
	for _, name := range []string{"correctness", "security", "verification"} {
		l := layers[name]
		if l.Model != "gpt-6.1-sol" || l.ReasoningEffort != "medium" ||
			l.Timeout != 5*time.Minute || !l.ReadOnly || l.Autonomy != agent.Off {
			t.Fatalf("unexpected %s default: %+v", name, l)
		}
	}
	data := []byte(`
version = 1
[layers.correctness]
model = "synthetic-repo-model"
timeout = "90s"
rules = ["repository rule"]

# Deliberately declared most-specific first.
[[paths]]
pattern = "src/example.go"
[paths.layers.correctness]
model = "synthetic-exact-model"
rules = ["exact rule"]

[[paths]]
pattern = "src/*.go"
[paths.layers.correctness]
model = "synthetic-glob-model"
reasoning_effort = "high"
rules = ["glob rule"]
`)
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file, model, effort string
		rules               []string
	}{
		{"src/example.go", "synthetic-exact-model", "high", []string{"repository rule", "glob rule", "exact rule"}},
		{"src/other.go", "synthetic-glob-model", "high", []string{"repository rule", "glob rule"}},
		{"src/nested/other.go", "synthetic-repo-model", "medium", []string{"repository rule"}},
		{"README.md", "synthetic-repo-model", "medium", []string{"repository rule"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			layers, err := c.ForPath(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			l := layers["correctness"]
			if l.Model != tc.model || l.ReasoningEffort != tc.effort || l.Timeout != 90*time.Second || !reflect.DeepEqual(l.Rules, tc.rules) {
				t.Fatalf("wrong resolved policy: %+v", l)
			}
			if layers["security"].Model != "gpt-6.1-sol" {
				t.Fatal("override leaked to another layer")
			}
		})
	}
	// Returned slices must not let callers mutate future policy resolutions.
	first, _ := c.ForPath("src/example.go")
	first["correctness"].Rules[0] = "mutated"
	again, _ := c.ForPath("src/example.go")
	if again["correctness"].Rules[0] != "repository rule" {
		t.Fatal("policy aliases returned rules")
	}
}

func TestSpecificityAndStableTies(t *testing.T) {
	c, err := Parse([]byte(`
[[paths]]
pattern = "src/test*.go"
[paths.layers.security]
model = "synthetic-specific"
[[paths]]
pattern = "src/*.go"
[paths.layers.security]
model = "synthetic-broad"
[[paths]]
pattern = "src/test*.go"
[paths.layers.security]
model = "synthetic-later-tie"
`))
	if err != nil {
		t.Fatal(err)
	}
	layers, err := c.ForPath("src/testcase.go")
	if err != nil || layers["security"].Model != "synthetic-later-tie" {
		t.Fatalf("specificity/tie precedence: %+v, %v", layers, err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for name, data := range map[string]string{
		"syntax":          "[",
		"unknown key":     "typo = true",
		"version":         "version = 2",
		"version type":    `version = "1"`,
		"unknown layer":   "[layers.style]\nmodel='demo'",
		"layer typo":      "[layers.security]\nmodle='demo'",
		"empty model":     "[layers.security]\nmodel=''",
		"effort":          "[layers.security]\nreasoning_effort='impossible'",
		"timeout":         "[layers.security]\ntimeout='never'",
		"zero timeout":    "[layers.security]\ntimeout='0s'",
		"negative":        "[layers.security]\ntimeout='-1s'",
		"overflow":        "[layers.security]\ntimeout='999999999999999999h'",
		"write autonomy":  "[layers.security]\nautonomy='low'",
		"write intent":    "[layers.security]\nread_only=false",
		"empty rule":      "[layers.security]\nrules=[' ']",
		"duplicate":       "version=1\nversion=1",
		"missing pattern": "[[paths]]\n[paths.layers.security]\nmodel='demo'",
		"absolute":        "[[paths]]\npattern='/tmp/*'\n[paths.layers.security]\nmodel='demo'",
		"traversal":       "[[paths]]\npattern='../*'\n[paths.layers.security]\nmodel='demo'",
		"malformed glob":  "[[paths]]\npattern='src/['\n[paths.layers.security]\nmodel='demo'",
		"recursive glob":  "[[paths]]\npattern='src/**'\n[paths.layers.security]\nmodel='demo'",
		"empty override":  "[[paths]]\npattern='src/*'",
		"path unknown":    "[[paths]]\npattern='src/*'\n[paths.layers.style]\nmodel='demo'",
		"path invalid":    "[[paths]]\npattern='src/*'\n[paths.layers.security]\ntimeout='0s'",
		"oversize":        strings.Repeat("#", (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	if _, err := Defaults().ForPath("../example.go"); err == nil {
		t.Fatal("accepted path traversal")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("unreadable config treated as absent")
	}
}
