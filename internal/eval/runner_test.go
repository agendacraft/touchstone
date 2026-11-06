package eval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/review"
)

func repairingAgent(t *testing.T) agent.Agent {
	t.Helper()
	var mu sync.Mutex
	attempts := map[string]int{}
	return agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		if r.Model != "glm-5.3-flash" || !r.RawOutput {
			t.Errorf("real agent interface/settings changed: %+v", r)
		}
		entries, err := os.ReadDir(r.WorkingDirectory)
		if err != nil {
			return agent.Result{}, err
		}
		for _, entry := range entries {
			if entry.Name() != "diff.patch" && entry.Name() != "candidates.json" {
				t.Errorf("ground truth or recording leaked into model scratch: %s", entry.Name())
			}
		}
		output := "[]"
		if _, err := os.Stat(filepath.Join(r.WorkingDirectory, "candidates.json")); err == nil {
			output = `[{"id":"correctness:c1","verdict":"confirmed","reason":"The boundary admits i == len(xs), causing an out-of-range access."}]`
		} else if strings.HasPrefix(r.Prompt, "You are an independent correctness reviewer.") {
			mu.Lock()
			attempts[r.WorkingDirectory]++
			n := attempts[r.WorkingDirectory]
			mu.Unlock()
			output = "malformed first attempt"
			if n > 1 {
				output = `[{"id":"c1","severity":"error","category":"bounds","file":"sample.go","line":4,"title":"Index at length panics","evidence":"The new comparison allows i == len(xs).","confidence":1}]`
			}
		}
		input := int64(10)
		credits := int64(20)
		return agent.Result{Output: output, Usage: agent.Usage{InputTokens: &input, FactoryCredits: &credits}, Cost: &agent.Cost{Amount: 0.25, Currency: "USD"}}, nil
	}}
}

func TestRunRecordReplayWithRepair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recordings")
	live, err := Run(context.Background(), Options{Cases: singleCase(t), RecordDir: dir, Repeats: 3}, repairingAgent(t))
	if err != nil || !live.Complete || live.Mode != "live" || len(live.Cases) != 3 {
		t.Fatalf("live: %+v, %v", live, err)
	}
	noCalls := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("replay made a live call")
		return agent.Result{}, errors.New("forbidden")
	}}
	replay, err := Run(context.Background(), Options{Cases: singleCase(t), Recordings: os.DirFS(dir), Repeats: 3, Replay: true}, noCalls)
	if err != nil || !replay.Complete || replay.Mode != "recorded" || !reflect.DeepEqual(live.Scores, replay.Scores) {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	for i, c := range live.Cases {
		if c.Cost == nil || c.Cost.Amount != 1 || c.LatencyMS == nil || replay.Cases[i].LatencyMS != nil {
			t.Fatalf("cost/latency: live %+v replay %+v", c, replay.Cases[i])
		}
		if c.FactoryCredits == nil || *c.FactoryCredits != 80 || *replay.Cases[i].FactoryCredits != 80 {
			t.Fatal("repair credits were not included in per-review cost evidence")
		}
		if c.Report.Layers[0].Attempts[0].Usage.InputTokens == nil || len(c.Report.Layers[0].Attempts) != 2 {
			t.Fatal("repair or usage evidence lost")
		}
		assertRatio(t, c.Scores.After["overall"].Recall, 1)
	}
	if replay.Spread["overall/after/overall/recall"].N != 3 {
		t.Fatal("missing repeated-run spread")
	}
	if replay.Spread["case/one/after/overall/recall"].N != 3 ||
		replay.Spread["case/one/cost_per_review/USD"].Mean != 1 {
		t.Fatal("missing per-case metric or cost spread")
	}
	if _, ok := replay.Spread["latency_ms"]; ok {
		t.Fatal("replay speed presented as model latency")
	}
}

func TestReplayRejectsRecordingDrift(t *testing.T) {
	for _, mutate := range []func(*Recording){
		func(r *Recording) { r.Fingerprint = "changed" },
		func(r *Recording) { r.Version++ },
		func(r *Recording) { delete(r.Policy, "security") },
		func(r *Recording) { r.Turns["other"] = []Turn{} },
		func(r *Recording) { r.Turns["correctness"][0].Request = "different prompt" },
		func(r *Recording) { r.Turns["verification"] = append(r.Turns["verification"], Turn{}) },
		func(r *Recording) { r.Turns["correctness"] = nil },
	} {
		dir := filepath.Join(t.TempDir(), "recordings")
		if _, err := Run(context.Background(), Options{Cases: singleCase(t), RecordDir: dir, Repeats: 1}, repairingAgent(t)); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "one", "recording-1.json")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var recording Recording
		if err := json.Unmarshal(data, &recording); err != nil {
			t.Fatal(err)
		}
		mutate(&recording)
		if err := writeJSON(file, recording); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(context.Background(), Options{Cases: singleCase(t), Recordings: os.DirFS(dir), Repeats: 1, Replay: true}, nil); err == nil {
			t.Fatal("recording drift was hidden as a valid score")
		}
	}
}

func TestRecordedFailureRemainsIncomplete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recordings")
	failed := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{}, &agent.RunError{Kind: agent.Timeout, ExitCode: -1}
	}}
	for _, replay := range []bool{false, true} {
		o := Options{Cases: singleCase(t), Repeats: 1, Replay: replay}
		if replay {
			o.Recordings = os.DirFS(dir)
		} else {
			o.RecordDir = dir
		}
		s, err := Run(context.Background(), o, failed)
		if err != nil || s.Complete || s.Cases[0].Report.Outcome != "incomplete" {
			t.Fatalf("failure became a clean score: %+v %v", s, err)
		}
	}
}

func TestRunErrors(t *testing.T) {
	for _, o := range []Options{
		{Repeats: 0}, {Repeats: 21}, {Repeats: 1},
		{Repeats: 1, Replay: true, RecordDir: "forbidden"},
		{Repeats: 1, Replay: true, Cases: fstest.MapFS{}},
		{Repeats: 1, Replay: true, Cases: singleCase(t)},
	} {
		if _, err := Run(context.Background(), o, nil); err == nil {
			t.Fatalf("invalid options accepted: %+v", o)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Options{Cases: singleCase(t), Repeats: 1}, repairingAgent(t)); err == nil {
		t.Fatal("cancellation hidden")
	}
}

func TestReviewCostUnknownAndMixedCurrencies(t *testing.T) {
	for _, costs := range [][]*agent.Cost{
		nil, {nil}, {{Amount: 1, Currency: "USD"}, nil},
		{{Amount: 1, Currency: "USD"}, {Amount: 1, Currency: "EUR"}},
	} {
		report := review.Report{Verifier: review.LayerResult{Attempts: []review.Attempt{}}}
		for _, cost := range costs {
			report.Verifier.Attempts = append(report.Verifier.Attempts, review.Attempt{Cost: cost})
		}
		if reviewCost(report) != nil {
			t.Fatalf("invented cost from %v", costs)
		}
	}
}

func TestRunProgressWriterError(t *testing.T) {
	_, err := Run(context.Background(), Options{Cases: singleCase(t), Repeats: 1, Progress: errorWriter{}}, repairingAgent(t))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("progress error hidden: %v", err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
