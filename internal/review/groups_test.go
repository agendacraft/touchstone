package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
)

func TestPathGroupsAtAgentBoundary(t *testing.T) {
	cfg, err := config.Parse([]byte(`
[layers.correctness]
model = "correct"
rules = ["Repository guidance."]
[layers.security]
enabled = false
[layers.verification]
model = "verify"
[[paths]]
pattern = "src/skip.go"
[paths.layers.correctness]
enabled = false
[[paths]]
pattern = "src/special.go"
[paths.layers.correctness]
rules = ["Specific guidance."]
[paths.layers.security]
enabled = true
model = "security"
[paths.layers.verification]
enabled = false
`))
	if err != nil {
		t.Fatal(err)
	}
	var diff strings.Builder
	for _, name := range []string{"src/normal.go", "src/special.go", "src/skip.go", "src/also.go"} {
		diff.WriteString("diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name + "\n@@ -1 +1 @@\n-old\n+new\n")
	}
	var mu sync.Mutex
	calls := map[string]int{}
	fake := agent.Fake{Respond: func(_ context.Context, request agent.Request) (agent.Result, error) {
		data, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "diff.patch"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		mu.Lock()
		calls[request.Model]++
		mu.Unlock()
		if strings.Contains(text, "src/skip.go") {
			t.Error("disabled path reached a model")
		}
		if request.Model == "verify" {
			if !strings.Contains(text, "src/normal.go") || !strings.Contains(text, "src/also.go") || strings.Contains(text, "src/special.go") {
				t.Error("equal-policy group not isolated")
			}
			return agent.Result{Output: `[{"id":"correctness:reused","verdict":"confirmed","reason":"Synthetic evidence."}]`}, nil
		}
		if !strings.Contains(request.Prompt, contract) || (request.Model == "correct" && !strings.Contains(request.Prompt, "Repository guidance.")) {
			t.Error("built-in contract or additive repository guidance lost")
		}
		file := "src/normal.go"
		if strings.Contains(text, "src/special.go") {
			file = "src/special.go"
			if strings.Contains(text, "src/normal.go") || strings.Contains(text, "src/also.go") {
				t.Error("different policy paths mixed")
			}
			if request.Model == "correct" && !strings.Contains(request.Prompt, "Specific guidance.") {
				t.Error("path guidance missing")
			}
		} else if strings.Contains(request.Prompt, "Specific guidance.") {
			t.Error("path guidance leaked to other group")
		}
		data, _ = json.Marshal([]map[string]any{{"id": "reused", "severity": "warning", "category": "logic", "file": file, "line": 1, "title": "Synthetic claim", "evidence": "Synthetic change.", "confidence": 0.9}})
		return agent.Result{Output: string(data)}, nil
	}}
	report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(diff.String()), cfg)
	if err != nil || report.Outcome != "findings" || len(report.Findings) != 3 || len(report.Layers) != 6 ||
		calls["correct"] != 2 || calls["security"] != 1 || calls["verify"] != 1 {
		t.Fatalf("group orchestration: %+v %v calls=%v", report, err, calls)
	}
	for _, f := range report.Findings {
		if f.ID != "reused" || f.Group == "" {
			t.Fatalf("source ID changed or group scope missing: %+v", f)
		}
		if (f.File == "src/special.go") != (f.Verification.Verdict == "unverified") {
			t.Fatalf("path verifier enable ignored: %+v", f)
		}
	}
}

func TestGroupFailurePreservesOtherGroups(t *testing.T) {
	cfg, err := config.Parse([]byte(`
[layers.security]
enabled=false
[layers.verification]
enabled=false
[[paths]]
pattern="src/failed.go"
[paths.layers.correctness]
model="fails"
`))
	if err != nil {
		t.Fatal(err)
	}
	diff := syntheticDiff + "\ndiff --git a/src/failed.go b/src/failed.go\n--- a/src/failed.go\n+++ b/src/failed.go\n@@ -1 +1 @@\n-old\n+new\n"
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		if r.Model == "fails" {
			return agent.Result{}, errors.New("synthetic process failure")
		}
		return agent.Result{Output: candidate}, nil
	}}
	report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(diff), cfg)
	if err != nil || report.Outcome != "incomplete" || len(report.Findings) != 1 || report.Findings[0].ID != "synthetic-1" ||
		len(report.Layers) != 4 || report.Layers[2].Status != "incomplete" {
		t.Fatalf("partial groups lost: %+v %v", report, err)
	}
}
