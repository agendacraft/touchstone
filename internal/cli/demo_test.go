package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
)

func TestDemoReplay(t *testing.T) {
	// This public output snapshot is the documented demo, not implementation
	// source. A missing layer, lost finding, or accidental live call fails it.
	sample, err := os.ReadFile("../../docs/demo-output.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	noCalls := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("replay invoked the supplied live agent")
		return agent.Result{}, errors.New("live calls forbidden")
	}}
	err = RunContext(context.Background(), []string{"demo", "--replay"}, nil, &out, io.Discard, "test", noCalls)
	if err != nil || !bytes.Equal(out.Bytes(), sample) {
		t.Fatalf("recorded demo drifted: %v\n%s", err, out.String())
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(readme), "## Try the demo\n")
	if !ok {
		t.Fatal("README demo section missing")
	}
	_, sampleBlock, ok := strings.Cut(section, "```text\n")
	readmeSample, _, closed := strings.Cut(sampleBlock, "```\n")
	if !ok || !closed || readmeSample != string(sample) {
		t.Fatal("README recorded output differs from the public demo snapshot")
	}
}

func TestDemoLive(t *testing.T) {
	var calls int
	dirs := make(chan string, 8)
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		dirs <- r.WorkingDirectory
		if r.Model != "glm-5.3-flash" {
			t.Errorf("live demo used replay model %q", r.Model)
		}
		diff, err := os.ReadFile(filepath.Join(r.WorkingDirectory, "diff.patch"))
		if err != nil || !bytes.Contains(diff, []byte("+\treturn signedIn || user == owner")) {
			t.Errorf("agent did not receive the bundled buggy diff: %v", err)
		}
		if candidates, err := os.ReadFile(filepath.Join(r.WorkingDirectory, "candidates.json")); err == nil {
			if !bytes.Contains(candidates, []byte(`"id":"correctness:demo-1"`)) {
				t.Errorf("verifier did not receive layer-qualified candidates: %s", candidates)
			}
			return agent.Result{Output: `[{"id":"correctness:demo-1","verdict":"confirmed","reason":"The deleted empty-input guard allowed a zero denominator."}]`}, nil
		}
		if strings.HasPrefix(r.Prompt, "You are an independent correctness reviewer.") {
			return agent.Result{Output: `[{"id":"demo-1","severity":"error","category":"logic","file":"ledger.go","line":9,"title":"Empty average panics","evidence":"The removed guard allows division by zero.","confidence":1}]`}, nil
		}
		return agent.Result{Output: "[]"}, nil
	}}
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"demo"}, nil, &out, io.Discard, "test", fake)
	close(dirs)
	for dir := range dirs {
		calls++
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("review scratch was not removed: %v", err)
		}
	}
	if err != nil || calls != 3 || !strings.Contains(out.String(), "LIVE:") ||
		strings.Contains(out.String(), "RECORDED") || !strings.Contains(out.String(), "executed locally: true") {
		t.Fatalf("live demo failed: %v\n%s", err, out.String())
	}
}

func TestDemoIsolation(t *testing.T) {
	caller, scratch := t.TempDir(), t.TempDir()
	// Invalid caller config and inherited Git selectors must not affect the
	// bundled repository, and successful runs must remove all scratch files.
	if err := os.WriteFile(filepath.Join(caller, "touchstone.toml"), []byte("invalid config"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(caller)
	t.Setenv("TMPDIR", scratch)
	t.Setenv("GIT_DIR", filepath.Join(caller, ".git"))
	t.Setenv("GIT_WORK_TREE", caller)
	var out bytes.Buffer
	if err := RunContext(context.Background(), []string{"demo", "--replay"}, nil, &out, io.Discard, "test", nil); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(scratch)
	if err != nil || len(files) != 0 {
		t.Fatalf("demo left scratch files: %v, %v", files, err)
	}
	files, err = os.ReadDir(caller)
	if err != nil || len(files) != 1 || files[0].Name() != "touchstone.toml" {
		t.Fatalf("demo changed the caller directory: %v, %v", files, err)
	}
}

func TestDemoReplayMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"demo", "--replay"}, nil, &out, io.Discard, "test", nil)
	if ExitCode(err) != 3 || !strings.Contains(err.Error(), "Git required") || out.Len() != 0 {
		t.Fatalf("missing Git was not a setup error: %v\n%s", err, out.String())
	}
}

func TestDemoFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		a    agent.Agent
		code int
	}{
		{"missing agent", []string{"demo"}, nil, 3},
		{"missing Droid", []string{"demo"}, agent.Droid{Binary: filepath.Join(t.TempDir(), "missing-droid")}, 3},
		{"agent failure", []string{"demo"}, agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
			return agent.Result{}, &agent.RunError{Kind: agent.ProcessFailure}
		}}, 2},
		{"positional argument", []string{"demo", "extra"}, nil, 3},
		{"unknown flag", []string{"demo", "--unknown"}, nil, 3},
		{"help", []string{"demo", "--help"}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RunContext(context.Background(), tc.args, nil, &out, io.Discard, "test", tc.a)
			if ExitCode(err) != tc.code {
				t.Fatalf("exit = %d, want %d: %v", ExitCode(err), tc.code, err)
			}
			if tc.name == "missing Droid" && !strings.Contains(err.Error(), "demo --replay") {
				t.Fatal("missing agent error did not offer credential-free replay")
			}
			if tc.code == 2 && !strings.Contains(out.String(), "INCOMPLETE") {
				t.Fatal("failed agent was presented as a complete review")
			}
		})
	}
}

func TestDemoOutputAndCancellation(t *testing.T) {
	if err := RunContext(context.Background(), []string{"demo", "--replay"}, nil, brokenWriter{}, io.Discard, "test", nil); ExitCode(err) != 3 {
		t.Fatalf("output error was hidden: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunContext(ctx, []string{"demo", "--replay"}, nil, io.Discard, io.Discard, "test", nil); err == nil {
		t.Fatal("canceled demo succeeded")
	}
}
