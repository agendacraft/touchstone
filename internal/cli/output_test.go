package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/review"
)

func TestOutputCLIThresholds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte("[output]\nmin_severity='error'\nmin_confidence=0.95\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	finding := strings.ReplaceAll(strings.ReplaceAll(cliFinding, `"error"`, `"warning"`), `"confidence":1`, `"confidence":0.8`)
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{Output: finding}, nil
	}}
	for _, tc := range []struct {
		name                  string
		extra                 []string
		code, shown, filtered int
	}{
		{"config", nil, 0, 0, 2},
		{"severity override still filtered by confidence", []string{"--min-severity", "info"}, 0, 0, 2},
		{"confidence override still filtered by severity", []string{"--min-confidence", "0"}, 0, 0, 2},
		{"both overrides", []string{"--min-severity", "warning", "--min-confidence", "0.8"}, 1, 2, 0},
		{"explicit zero overrides config", []string{"--min-severity", "info", "--min-confidence", "0"}, 1, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"review", "--diff", "-", "--cwd", dir, "--no-verify"}, tc.extra...)
			var out bytes.Buffer
			err := RunContext(context.Background(), args, strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
			var report review.Report
			if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if ExitCode(err) != tc.code || len(report.Findings) != tc.shown || report.Triage == nil ||
				report.Triage.Filtered != tc.filtered {
				t.Fatalf("wrong post-triage filtering: %s (%v)", out.String(), err)
			}
		})
	}
}

func TestOutputCLIFormatsAndErrors(t *testing.T) {
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		calls.Add(1)
		return agent.Result{Output: cliFinding}, nil
	}}
	for _, tc := range []struct{ format, prefix string }{
		{"terminal", "Touchstone: findings"}, {"json", "{"}, {"markdown", "## Touchstone review: findings"},
	} {
		var out bytes.Buffer
		err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", t.TempDir(), "--no-verify", "--format", tc.format},
			strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
		if ExitCode(err) != 1 || !strings.HasPrefix(out.String(), tc.prefix) {
			t.Fatalf("wrong format/exit: %s, %v", out.String(), err)
		}
	}
	for _, flags := range [][]string{
		{"--format", "xml"}, {"--min-severity", "critical"}, {"--min-severity", ""},
		{"--min-confidence", "-0.1"}, {"--min-confidence", "1.1"},
		{"--min-confidence", "NaN"}, {"--min-confidence", "+Inf"},
	} {
		before := calls.Load()
		args := append([]string{"review", "--diff", "-", "--cwd", t.TempDir()}, flags...)
		var out bytes.Buffer
		err := RunContext(context.Background(), args, strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
		if ExitCode(err) != 3 || out.Len() != 0 || calls.Load() != before {
			t.Fatalf("bad output flag invoked agent or emitted report: %v", err)
		}
	}
}

func TestFilteredIncompleteCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte("[layers.correctness]\nmodel='seed'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		if r.Model == "seed" {
			return agent.Result{Output: cliFinding}, nil
		}
		return agent.Result{Output: "malformed"}, nil
	}}
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", dir, "--no-verify", "--min-confidence", "1", "--min-severity", "error"},
		strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
	if ExitCode(err) != 2 {
		t.Fatal("incomplete layer did not dominate findings exit")
	}
	// Raise the effective severity threshold above a warning, filtering every claim.
	fake = agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		if r.Model == "seed" {
			return agent.Result{Output: strings.ReplaceAll(cliFinding, `"error"`, `"warning"`)}, nil
		}
		return agent.Result{Output: "malformed"}, nil
	}}
	out.Reset()
	err = RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", dir, "--no-verify", "--min-severity", "error"},
		strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
	var report review.Report
	if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if ExitCode(err) != 2 || report.Outcome != "incomplete" || len(report.Findings) != 0 || report.Triage.Filtered != 1 {
		t.Fatalf("threshold concealed incomplete review: %s (%v)", out.String(), err)
	}
}
