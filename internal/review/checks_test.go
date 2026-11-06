package review

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/process"
	"github.com/agendacraft/touchstone/internal/testutil"
)

func TestCheckProcess(t *testing.T) { testutil.CheckProcess() }

func TestLocalChecks(t *testing.T) {
	t.Setenv("TOUCHSTONE_CHECK_HELPER", "1")
	t.Setenv("TOUCHSTONE_CHECK_SECRET", "must-not-inherit")
	root := t.TempDir()
	realRoot, _ := filepath.EvalSymlinks(root)
	for _, tc := range []struct {
		mode, status, error string
		findings            int
	}{
		{"pass", "complete", "", 0},
		{"fail", "failed", "", 1},
		{"timeout", "incomplete", "check timeout", 0},
		{"missing", "incomplete", "command could not start", 0},
		{"output", "incomplete", "check output size cap exceeded", 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			check := config.Check{Name: "synthetic " + tc.mode, Args: testutil.CheckCommand(t, tc.mode), MaxOutputBytes: 1024, LocationRegex: `(?m)^(?P<file>[^:\r\n]+):(?P<line>[0-9]+):`}
			if tc.mode == "timeout" {
				check.Timeout = "100ms"
			}
			if tc.mode == "missing" {
				check.Args = []string{filepath.Join(root, "missing-binary")}
			}
			report := EmptyReport()
			start := time.Now()
			(ChecksRunner{}).Run(context.Background(), root, config.Checks{EnvAllowlist: []string{"TOUCHSTONE_CHECK_HELPER"}, Commands: []config.Check{check}}, true, &report)
			elapsed := time.Since(start)
			if (tc.mode == "timeout" || tc.mode == "output") && elapsed > 3*time.Second {
				t.Fatalf("check resource limit did not stop the process: %s", elapsed)
			}
			t.Logf("status=%s elapsed=%s captured_bytes=%d retained_bytes=%d", report.Checks[0].Status, elapsed, report.Checks[0].OutputBytes, len(report.Checks[0].Output))
			if len(report.Checks) != 1 || len(report.Findings) != tc.findings {
				t.Fatalf("lost execution: %+v", report)
			}
			got := report.Checks[0]
			if got.Status != tc.status || got.Error != tc.error || got.Local != (tc.mode != "missing") || !reflect.DeepEqual(got.Args, check.Args) || len(got.Output) > 1024 {
				t.Fatalf("bad check audit: %+v", got)
			}
			if tc.status == "incomplete" && report.Outcome != "incomplete" {
				t.Fatal("failed execution appeared clean")
			}
			if tc.mode == "pass" && (!strings.Contains(got.Output, "cwd="+realRoot+" secret=\n") || strings.Contains(got.Output, "must-not-inherit")) {
				t.Fatalf("root/env boundary failed: %+v", got)
			}
			if tc.mode == "output" && got.OutputBytes <= 1024 {
				t.Fatalf("output not counted/capped: %+v", got)
			}
			if tc.findings > 0 {
				f := report.Findings[0]
				if f.File != "src/example.go" || f.Line != 7 || f.Category != "deterministic" || f.Confidence != 1 || got.ExitCode != 4 || report.Outcome != "findings" {
					t.Fatalf("failure not normalized: %+v", report)
				}
			}
		})
	}
}

type fakeCommands struct {
	called  *bool
	request *process.Request
}

func (f fakeCommands) Run(_ context.Context, request process.Request) (process.Result, error) {
	*f.called, *f.request = true, request
	return process.Result{Output: []byte("unstructured failure"), Bytes: 20, Started: true, ExitCode: 1}, nil
}

func TestChecksAreOptInAndKeepLiteralArguments(t *testing.T) {
	var called bool
	var request process.Request
	runner := ChecksRunner{Runner: fakeCommands{&called, &request}}
	cfg := config.Checks{Commands: []config.Check{{Name: "literal", Args: []string{"synthetic", "$(touch must-not-run)", "a; b", "*.go"}}}}
	report := EmptyReport()
	runner.Run(context.Background(), "/synthetic/root", cfg, false, &report)
	if called || report.Checks[0].Local || report.Checks[0].Status != "disabled" {
		t.Fatal("disabled checks executed")
	}
	report = EmptyReport()
	runner.Run(context.Background(), "/synthetic/root", cfg, true, &report)
	if !called || request.Dir != "/synthetic/root" || request.Env == nil || len(request.Env) != 0 || !reflect.DeepEqual(request.Args, cfg.Commands[0].Args) {
		t.Fatalf("command vector/root/empty environment changed: %+v", request)
	}
	if len(report.Findings) != 1 || report.Findings[0].File != "" || report.Findings[0].Line != 0 {
		t.Fatalf("undeclared output guessed a location: %+v", report)
	}
}

func TestCheckLocationFallback(t *testing.T) {
	pattern := `(?m)^(?P<file>[^:\r\n]+):(?P<line>[0-9]+):`
	for _, output := range []string{"no location", "/tmp/private.go:2: bad", "../outside.go:4: bad", "src/a.go:0: bad", "src/a.go:999999999999999999999: bad"} {
		if file, line := checkLocation(pattern, output); file != "" || line != 0 {
			t.Fatalf("unsafe/invalid location accepted: %q %d", file, line)
		}
	}
}

func TestCanceledCheckCannotPass(t *testing.T) {
	t.Setenv("TOUCHSTONE_CHECK_HELPER", "1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := EmptyReport()
	(ChecksRunner{}).Run(ctx, t.TempDir(), config.Checks{Commands: []config.Check{{Name: "pass", Args: testutil.CheckCommand(t, "pass")}}}, true, &report)
	if report.Outcome != "incomplete" || report.Checks[0].Status != "incomplete" {
		t.Fatal("canceled check appeared clean")
	}
}

func TestEmptyAndOversizedReviewsNeverCallModel(t *testing.T) {
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("empty or oversized diff invoked a model")
		return agent.Result{}, nil
	}}
	for _, data := range [][]byte{nil, []byte(" \n")} {
		report, err := (Runner{Agent: fake}).Run(context.Background(), data, config.Defaults())
		if err != nil || report.Outcome != "clean" || len(report.Layers) != 0 {
			t.Fatalf("empty review: %+v %v", report, err)
		}
	}
	report, err := (Runner{Agent: fake, MaxDiffBytes: 4}).Run(context.Background(), []byte("12345"), config.Defaults())
	if err != nil || report.Outcome != "incomplete" || report.Layers[0].Error != "diff too large, 5 bytes" {
		t.Fatalf("oversized review: %+v %v", report, err)
	}
}

func TestChecksKeepFailuresWhenAnotherCannotStart(t *testing.T) {
	t.Setenv("TOUCHSTONE_CHECK_HELPER", "1")
	report := EmptyReport()
	root := t.TempDir()
	checks := config.Checks{EnvAllowlist: []string{"TOUCHSTONE_CHECK_HELPER"}, Commands: []config.Check{
		{Name: "missing", Args: []string{filepath.Join(root, "missing")}},
		{Name: "failure", Args: testutil.CheckCommand(t, "fail")},
		{Name: "pass", Args: testutil.CheckCommand(t, "pass")},
	}}
	(ChecksRunner{}).Run(context.Background(), root, checks, true, &report)
	if report.Outcome != "incomplete" || len(report.Findings) != 1 || report.Findings[0].ID != "check-2" ||
		len(report.Checks) != 3 || report.Checks[2].Status != "complete" || report.Layers[0].Status != "incomplete" {
		t.Fatalf("partial check results lost: %+v", report)
	}
}
