package config

import (
	"reflect"
	"testing"
	"time"
)

func TestPathEnablePrecedence(t *testing.T) {
	cfg, err := Parse([]byte(`
[layers.correctness]
enabled = false
rules = ["repository"]
[[paths]]
pattern = "src/exact.go"
[paths.layers.correctness]
enabled = true
rules = ["exact"]
[[paths]]
pattern = "src/*.go"
[paths.layers.correctness]
enabled = true
rules = ["broad"]
[[paths]]
pattern = "src/test*.go"
[paths.layers.correctness]
enabled = false
rules = ["specific"]
[[paths]]
pattern = "src/test*.go"
[paths.layers.correctness]
enabled = true
rules = ["later tie"]
[[paths]]
pattern = "src/exact.go"
[paths.layers.verification]
enabled = false
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file            string
		enabled, verify bool
		rules           []string
	}{
		{"other.go", false, true, []string{"repository"}},
		{"src/other.go", true, true, []string{"repository", "broad"}},
		{"src/exact.go", true, false, []string{"repository", "broad", "exact"}},
		{"src/testcase.go", true, true, []string{"repository", "broad", "specific", "later tie"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			layers, err := cfg.ForPath(tc.file)
			if err != nil || layers["correctness"].Enabled != tc.enabled || layers["verification"].Enabled != tc.verify || !reflect.DeepEqual(layers["correctness"].Rules, tc.rules) {
				t.Fatalf("wrong precedence: %+v %v", layers, err)
			}
		})
	}
}

func TestInputAndChecksConfig(t *testing.T) {
	defaults := Defaults()
	if defaults.Input().MaxDiffBytes != 16<<20 || defaults.Checks().Enabled || len(defaults.Checks().Commands) != 0 ||
		!reflect.DeepEqual(defaults.Checks().EnvAllowlist, []string{"PATH"}) {
		t.Fatal("unsafe input/check defaults")
	}
	cfg, err := Parse([]byte(`
[input]
max_diff_bytes = 1024
ignore = ["generated/*", "*.bin"]
[checks]
enabled = true
env_allowlist = []
[[checks.commands]]
name = "synthetic"
args = ["tool", "literal; argument"]
timeout = "2s"
max_output_bytes = 128
location_regex = '^(?P<file>[^:]+):(?P<line>[0-9]+):'
`))
	if err != nil {
		t.Fatal(err)
	}
	checks := cfg.Checks()
	if !checks.Enabled || len(checks.EnvAllowlist) != 0 || checks.Commands[0].Duration() != 2*time.Second || checks.Commands[0].OutputLimit() != 128 || cfg.Input().MaxDiffBytes != 1024 {
		t.Fatalf("lost settings: %+v", checks)
	}
	checks.Commands[0].Args[0] = "mutated"
	if cfg.Checks().Commands[0].Args[0] != "tool" {
		t.Fatal("mutable check args leaked")
	}
	for _, data := range []string{
		"[input]\nmax_diff_bytes=0",
		"[input]\nmax_diff_bytes=268435457",
		"[input]\nignore=['../*']",
		"[input]\nignore=['**']",
		"[checks]\nenv_allowlist=['BAD=NAME']",
		"[[checks.commands]]\nname='x'\nargs=[]",
		"[[checks.commands]]\nargs=['tool']",
		"[[checks.commands]]\nname='x'\nargs=['tool']\ntimeout='never'",
		"[[checks.commands]]\nname='x'\nargs=['tool']\ntimeout='31m'",
		"[[checks.commands]]\nname='x'\nargs=['tool']\nmax_output_bytes=-1",
		"[[checks.commands]]\nname='x'\nargs=['tool']\nmax_output_bytes=1048577",
		"[[checks.commands]]\nname='x'\nargs=['tool']\nlocation_regex='['",
		"[[checks.commands]]\nname='x'\nargs=['tool']\nlocation_regex='(file):(line)'",
		"[[checks.commands]]\nname='x'\nargs=['tool']\n[[checks.commands]]\nname='x'\nargs=['tool']",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("unsafe/invalid config accepted: %s", data)
		}
	}
}
