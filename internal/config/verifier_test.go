package config

import (
	"testing"
	"time"
)

func TestVerifierPolicy(t *testing.T) {
	base := Defaults().Verification()
	if !base.Enabled || base.Model != "glm-5.3-flash" || base.ReasoningEffort != "low" ||
		base.Autonomy != "off" || !base.ReadOnly || base.Timeout != 5*time.Minute {
		t.Fatalf("wrong verifier defaults: %+v", base)
	}
	cfg, err := Parse([]byte(`
[layers.verification]
enabled = false
model = "synthetic-verifier"
timeout = "30s"
rules = ["Disprove before confirming."]
[[paths]]
pattern = "src/*.go"
[paths.layers.verification]
model = "path-model-not-used-by-review"
`))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Verification()
	if got.Enabled || got.Model != "synthetic-verifier" || got.ReasoningEffort != "low" || got.Timeout != 30*time.Second {
		t.Fatalf("verifier overrides lost: %+v", got)
	}
	got.Rules[0] = "mutated"
	if cfg.Verification().Rules[0] != "Disprove before confirming." {
		t.Fatal("verifier policy aliases config rules")
	}
	for _, data := range []string{
		"[layers.verification]\nautonomy='low'",
		"[layers.verification]\nread_only=false",
		"[layers.verification]\nreasoning_effort='invalid'",
		"[layers.verification]\ntimeout='0s'",
		"[layers.verification]\nenabled='false'",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("invalid verifier policy accepted: %s", data)
		}
	}
	cfg, err = Parse([]byte("[layers.verification]\nreasoning_effort='high'"))
	if err != nil || cfg.Verification().Model != "glm-5.3-flash" || cfg.Verification().ReasoningEffort != "high" || !cfg.Verification().Enabled {
		t.Fatal("effort-only override lost independent defaults")
	}
}
