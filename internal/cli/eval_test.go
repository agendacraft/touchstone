package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/eval"
)

func externalEvalCase(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sample"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"expected.json", "diff.patch"} {
		data, err := fs.ReadFile(eval.Bundled(), "01-index-boundary/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "sample", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestEvalCLIRecordReplayBaseline(t *testing.T) {
	cases := externalEvalCase(t)
	records := filepath.Join(t.TempDir(), "recordings")
	var calls atomic.Int64
	live := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		calls.Add(1)
		return agent.Result{Output: "[]"}, nil
	}}
	var out, stderr bytes.Buffer
	err := RunContext(context.Background(), []string{"eval", "--cases", cases, "--record", records}, nil, &out, &stderr, "test", live)
	var result eval.Suite
	if err != nil || json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatalf("live CLI: %v\n%s", err, out.String())
	}
	if result.Mode != "live" || result.Repeats != 3 || len(result.Cases) != 3 || calls.Load() != 6 {
		t.Fatalf("default repetitions or real pipeline bypassed: %+v, calls %d", result, calls.Load())
	}
	if result.Scores.After["overall"].Found != 0 || result.Scores.After["overall"].Bugs != 3 {
		t.Fatal("missed bugs were hidden")
	}
	noCalls := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("recorded CLI called live agent")
		return agent.Result{}, errors.New("forbidden")
	}}
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	baseArgs := []string{"eval", "--replay", "--cases", cases, "--recordings", records}
	out.Reset()
	if err := RunContext(context.Background(), append(baseArgs, "--write-baseline", baseline), nil, &out, io.Discard, "test", noCalls); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Mode != "recorded" || result.Cases[0].LatencyMS != nil {
		t.Fatalf("replay not labelled recorded: %+v %v", result, err)
	}
	if err := RunContext(context.Background(), append(baseArgs, "--baseline", baseline), nil, io.Discard, io.Discard, "test", noCalls); err != nil {
		t.Fatal(err)
	}
	saved, err := eval.LoadBaseline(baseline)
	if err != nil {
		t.Fatal(err)
	}
	b := saved.Cases["sample/1"]
	m := b.Scores.After["overall"]
	one := 1.0
	m.Found, m.Recall = 1, &one
	b.Scores.After["overall"] = m
	saved.Cases["sample/1"] = b
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseline, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunContext(context.Background(), append(baseArgs, "--baseline", baseline), nil, io.Discard, io.Discard, "test", noCalls); ExitCode(err) != 1 {
		t.Fatalf("baseline regression exit = %d: %v", ExitCode(err), err)
	}
	// Existing directories are never overwritten by --record.
	if err := RunContext(context.Background(), []string{"eval", "--cases", cases, "--record", records}, nil, io.Discard, io.Discard, "test", live); ExitCode(err) != 3 {
		t.Fatalf("existing recordings overwritten: %v", err)
	}
}

func TestEvalBundledReplayWithoutCredentials(t *testing.T) {
	noCalls := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("bundled replay invoked a live agent")
		return agent.Result{}, errors.New("forbidden")
	}}
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"eval", "--replay"}, nil, &out, io.Discard, "test", noCalls)
	var suite eval.Suite
	if ExitCode(err) != 2 || json.Unmarshal(out.Bytes(), &suite) != nil {
		t.Fatalf("recorded corpus failed to replay: %v", err)
	}
	// The original public run has 42 captured reviews and two schema failures.
	if suite.Mode != "recorded" || suite.Complete || suite.Repeats != 3 ||
		suite.Scores.Reviews != 42 || suite.Scores.Incomplete != 2 {
		t.Fatalf("recorded reliability evidence changed: %+v", suite.Scores)
	}
}

func TestEvalCLIErrors(t *testing.T) {
	for _, args := range [][]string{
		{"eval", "extra"}, {"eval", "--unknown"}, {"eval", "--repeats", "0"}, {"eval", "--repeats", "21"},
		{"eval", "--margin", "NaN"}, {"eval", "--margin", "-0.1"}, {"eval", "--margin", "1.1"},
		{"eval", "--replay", "--record", t.TempDir()}, {"eval", "--recordings", t.TempDir()},
		{"eval", "--write-baseline", filepath.Join(t.TempDir(), "baseline.json")},
		{"eval", "--replay", "--write-baseline", "x", "--baseline", "y"},
		{"eval", "--cases", t.TempDir()}, {"eval", "--baseline", filepath.Join(t.TempDir(), "missing")},
	} {
		err := RunContext(context.Background(), args, nil, io.Discard, io.Discard, "test", nil)
		if ExitCode(err) != 3 {
			t.Errorf("%v: exit = %d, want 3: %v", args, ExitCode(err), err)
		}
	}
	if err := RunContext(context.Background(), []string{"eval", "--help"}, nil, io.Discard, io.Discard, "test", nil); err != nil {
		t.Fatal(err)
	}
	if err := RunContext(context.Background(), []string{"eval", "--replay"}, nil, brokenWriter{}, brokenWriter{}, "test", nil); ExitCode(err) != 3 {
		t.Fatalf("writer error swallowed: %v", err)
	}
}

func TestEvalCLIIncompleteIsNotClean(t *testing.T) {
	failed := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{}, &agent.RunError{Kind: agent.ProcessFailure, ExitCode: -1}
	}}
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"eval", "--cases", externalEvalCase(t), "--repeats", "1"},
		nil, &out, io.Discard, "test", failed)
	var suite eval.Suite
	if ExitCode(err) != 2 || json.Unmarshal(out.Bytes(), &suite) != nil || suite.Complete {
		t.Fatalf("incomplete evaluation hidden: %v\n%s", err, out.String())
	}
}

func TestEvalCLIKnownIncompletenessStillChecksBaseline(t *testing.T) {
	cases := externalEvalCase(t)
	records := filepath.Join(t.TempDir(), "records")
	failed := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{}, &agent.RunError{Kind: agent.ProcessFailure, ExitCode: -1}
	}}
	args := []string{"eval", "--cases", cases, "--repeats", "1", "--record", records}
	if err := RunContext(context.Background(), args, nil, io.Discard, io.Discard, "test", failed); ExitCode(err) != 2 {
		t.Fatal(err)
	}
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	args = []string{"eval", "--replay", "--cases", cases, "--recordings", records, "--repeats", "1"}
	if err := RunContext(context.Background(), append(args, "--write-baseline", baseline), nil, io.Discard, io.Discard, "test", nil); ExitCode(err) != 2 {
		t.Fatal(err)
	}
	if err := RunContext(context.Background(), append(args, "--baseline", baseline), nil, io.Discard, io.Discard, "test", nil); ExitCode(err) != 2 {
		t.Fatalf("known incomplete replay did not preserve exit 2: %v", err)
	}
	saved, err := eval.LoadBaseline(baseline)
	if err != nil {
		t.Fatal(err)
	}
	b := saved.Cases["sample/1"]
	b.Completion.Layers[0].Error = "timeout"
	saved.Cases["sample/1"] = b
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseline, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunContext(context.Background(), append(args, "--baseline", baseline), nil, io.Discard, io.Discard, "test", nil); ExitCode(err) != 1 {
		t.Fatalf("completion drift accepted as known incompleteness: %v", err)
	}
}
