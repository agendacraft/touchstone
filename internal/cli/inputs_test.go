package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/review"
	"github.com/agendacraft/touchstone/internal/testutil"
	"github.com/pelletier/go-toml/v2"
)

func TestCheckProcess(t *testing.T) { testutil.CheckProcess() }

func runSyntheticReview(t *testing.T, args []string, input string, fake agent.Agent) (review.Report, int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	err := RunContext(context.Background(), append([]string{"review"}, args...), strings.NewReader(input), &out, &stderr, "test", fake)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	var report review.Report
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
	}
	diagnostic := ""
	if err != nil {
		diagnostic = err.Error()
	}
	return report, ExitCode(err), diagnostic
}

func TestGitReviewCLI(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "src/example.go", "old\n")
	base := testutil.Commit(t, dir, "")
	testutil.Git(t, dir, "checkout", "-b", "feature")
	testutil.Write(t, dir, "src/example.go", "new\n")
	head := testutil.Commit(t, dir, base)
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		calls.Add(1)
		return agent.Result{Output: "[]"}, nil
	}}
	for _, args := range [][]string{
		{"--cwd", dir, "--base", base, "--head", head},
		{"--cwd", dir, "--base", "main"},
		{"--cwd", filepath.Join(dir, "src")},
		{"--cwd", dir, "--head", head},
	} {
		before := calls.Load()
		report, code, err := runSyntheticReview(t, args, "", fake)
		if code != 0 || report.Outcome != "clean" || calls.Load()-before != 2 {
			t.Fatalf("git CLI: %+v %d %s", report, code, err)
		}
	}
	for _, tc := range []struct {
		args             []string
		code             int
		outcome, message string
	}{
		{[]string{"--base", head, "--head", head}, 0, "clean", ""},
		{[]string{"--base", base, "--ignore", "src/*"}, 0, "clean", ""},
		{[]string{"--base", base, "--max-diff-bytes", "8"}, 2, "incomplete", "diff too large,"},
		{[]string{"--diff", "-", "--base", base}, 3, "", "mutually exclusive"},
		{[]string{"--diff", "-", "--head", "HEAD"}, 3, "", "mutually exclusive"},
		{[]string{"--base", "missing"}, 3, "", "unknown git ref"},
		{[]string{"--base", base, "--head", "missing"}, 3, "", "unknown git ref"},
		{[]string{"--base", ""}, 3, "", "input flags must not be empty"},
		{[]string{"--base", base, "--max-diff-bytes", "0"}, 3, "", "max-diff-bytes must be"},
		{[]string{"--base", base, "--ignore", "**"}, 3, "", "recursive **"},
	} {
		before := calls.Load()
		report, code, message := runSyntheticReview(t, append([]string{"--cwd", dir}, tc.args...), cliDiff, fake)
		if report.Outcome == "incomplete" {
			message = report.Layers[0].Error
		}
		if code != tc.code || report.Outcome != tc.outcome || !strings.Contains(message, tc.message) || calls.Load() != before {
			t.Fatalf("bad input/error handling: %+v code=%d %q", report, code, message)
		}
	}
}

func TestChecksCLITrustGate(t *testing.T) {
	t.Setenv("TOUCHSTONE_CHECK_HELPER", "1")
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "example.go", "old\n")
	base := testutil.Commit(t, dir, "")
	testutil.Write(t, dir, "example.go", "new\n")
	testutil.Commit(t, dir, base)
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		calls.Add(1)
		return agent.Result{Output: "[]"}, nil
	}}
	for _, tc := range []struct {
		enabled bool
		flag    []string
		status  string
		code    int
	}{
		{false, nil, "disabled", 0},
		{false, []string{"--run-checks"}, "failed", 1},
		{true, nil, "failed", 1},
		{true, []string{"--run-checks=false"}, "disabled", 0},
	} {
		cfg := struct {
			Checks config.Checks `toml:"checks"`
		}{
			Checks: config.Checks{Enabled: tc.enabled, EnvAllowlist: []string{"TOUCHSTONE_CHECK_HELPER"}, Commands: []config.Check{{Name: "synthetic failure", Args: testutil.CheckCommand(t, "fail")}}},
		}
		data, err := toml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		testutil.Write(t, dir, config.FileName, string(data))
		args := append([]string{"--cwd", dir, "--base", base}, tc.flag...)
		report, code, message := runSyntheticReview(t, args, "", fake)
		if code != tc.code || len(report.Checks) != 1 || report.Checks[0].Status != tc.status {
			t.Fatalf("trust gate ignored: %+v %d %s", report, code, message)
		}
		if tc.code == 1 && (len(report.Findings) != 1 || report.Findings[0].File != "" || report.Findings[0].Category != "deterministic") {
			t.Fatalf("local failure missing: %+v", report)
		}
	}
	// The same --diff entry point may opt into checks, but they still use the
	// repository root, not the patch's directory or a private model scratch dir.
	report, code, message := runSyntheticReview(t, []string{"--cwd", dir, "--diff", "-", "--run-checks"}, cliDiff, fake)
	if code != 1 || report.Checks[0].Status != "failed" {
		t.Fatalf("diff checks: %+v %s", report, message)
	}
	before := calls.Load()
	report, code, _ = runSyntheticReview(t, []string{"--cwd", dir, "--base", "HEAD", "--run-checks"}, "", fake)
	if code != 0 || len(report.Checks) != 0 || calls.Load() != before {
		t.Fatal("empty diff executed checks or model")
	}
}

func TestChecksAreDescribedInHumanOutput(t *testing.T) {
	t.Setenv("TOUCHSTONE_CHECK_HELPER", "1")
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "example.go", "old\n")
	base := testutil.Commit(t, dir, "")
	testutil.Write(t, dir, "example.go", "new\n")
	testutil.Commit(t, dir, base)
	data, err := toml.Marshal(struct {
		Checks config.Checks `toml:"checks"`
	}{Checks: config.Checks{EnvAllowlist: []string{"TOUCHSTONE_CHECK_HELPER"}, Commands: []config.Check{{Name: "synthetic", Args: testutil.CheckCommand(t, "fail")}}}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Write(t, dir, config.FileName, string(data))
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) { return agent.Result{Output: "[]"}, nil }}
	for _, format := range []string{"terminal", "markdown"} {
		var out bytes.Buffer
		err := RunContext(context.Background(), []string{"review", "--cwd", dir, "--base", base, "--run-checks", "--format", format}, strings.NewReader(""), &out, io.Discard, "test", fake)
		if ExitCode(err) != 1 || !strings.Contains(out.String(), "executed locally: true") || !strings.Contains(out.String(), "argv:") || !strings.Contains(out.String(), "(repository)") {
			t.Fatalf("local command audit missing: %s %v", out.String(), err)
		}
	}
}
