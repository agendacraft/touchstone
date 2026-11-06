package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
)

const syntheticDiff = `diff --git a/src/example.go b/src/example.go
--- a/src/example.go
+++ b/src/example.go
@@ -1,2 +1,2 @@
 package example
-func divide(n int) int { if n == 0 { return 0 }; return 10/n }
+func divide(n int) int { return 10/n }
`

const candidate = `[{"id":"synthetic-1","severity":"error","category":"logic","file":"src/example.go","line":2,"title":"Zero input panics","evidence":"divide(0) now evaluates 10/0.","confidence":0.95}]`

func policy(t *testing.T, timeout string) config.Config {
	t.Helper()
	c, err := config.Parse([]byte(`
[layers.correctness]
model = "correctness-test"
timeout = "` + timeout + `"
rules = ["Consider zero input."]
[layers.security]
model = "security-test"
timeout = "` + timeout + `"
`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The oracle is two independent specialists, both active before either is
// released. A serial runner cannot reach the barrier and fails the deadline.
func TestParallelLayersAndScratchBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	dirs := make(chan string, 2)
	fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
		data, err := os.ReadFile(filepath.Join(r.WorkingDirectory, "diff.patch"))
		if err != nil || string(data) != syntheticDiff {
			t.Errorf("agent cannot read exact diff by path: %v", err)
		}
		if !r.RawOutput || r.Model == "" || r.ReasoningEffort != "low" || r.Timeout <= 0 {
			t.Errorf("wrong agent policy: %+v", r)
		}
		// This is the generated agent interface, not prompt-source inspection.
		if strings.Contains(r.Prompt, syntheticDiff) || !strings.Contains(r.Prompt, filepath.Join(r.WorkingDirectory, "diff.patch")) {
			t.Error("diff must be referenced by path, never embedded")
		}
		if r.Model == "correctness-test" && !strings.Contains(r.Prompt, "Consider zero input.") {
			t.Error("configured rules lost at the agent boundary")
		}
		for file, want := range map[string]os.FileMode{r.WorkingDirectory: 0o700, filepath.Join(r.WorkingDirectory, "diff.patch"): 0o600} {
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != want {
				t.Errorf("unsafe scratch permissions: %v", err)
			}
		}
		dirs <- r.WorkingDirectory
		started <- r.Model
		select {
		case <-release:
			return agent.Result{Output: candidate}, nil
		case <-ctx.Done():
			return agent.Result{}, ctx.Err()
		}
	}}
	done := make(chan Report, 1)
	cfg := policy(t, "2s")
	go func() {
		report, err := (Runner{Agent: fake}).Run(ctx, []byte(syntheticDiff), cfg)
		if err != nil {
			t.Error(err)
		}
		done <- report
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("layers were not concurrent")
		}
	}
	close(release)
	report := <-done
	if report.Outcome != "findings" || len(report.Findings) != 2 || report.Findings[0].Layer != "correctness" || report.Findings[1].Layer != "security" {
		t.Fatalf("unstable or lost results: %+v", report)
	}
	// IDs are intentionally layer-scoped: no cross-layer dedupe in S1.
	for _, layer := range report.Layers {
		if layer.Status != "complete" || layer.Error != "" || len(layer.Attempts) != 1 || layer.Attempts[0].Cost != nil {
			t.Fatalf("incorrect completed layer: %+v", layer)
		}
	}
	for range 2 {
		if _, err := os.Stat(<-dirs); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("scratch data leaked after review")
		}
	}
}

func TestConcurrencyLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var active, maximum atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	fake := agent.Fake{Respond: func(ctx context.Context, _ agent.Request) (agent.Result, error) {
		n := active.Add(1)
		maximum.Store(max(maximum.Load(), n))
		defer active.Add(-1)
		started <- struct{}{}
		select {
		case <-release:
			return agent.Result{Output: "[]"}, nil
		case <-ctx.Done():
			return agent.Result{}, ctx.Err()
		}
	}}
	cfg := policy(t, "1s")
	done := make(chan Report, 1)
	go func() {
		report, err := (Runner{Agent: fake, Concurrency: 1}).Run(ctx, []byte(syntheticDiff), cfg)
		if err != nil {
			t.Error(err)
		}
		done <- report
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first layer never started")
	}
	select {
	case <-started:
		t.Error("second layer exceeded concurrency cap before first finished")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	report := <-done
	if report.Outcome != "clean" || maximum.Load() != 1 || len(report.Layers) != 2 {
		t.Fatalf("concurrency cap or clean status: %+v, max=%d", report, maximum.Load())
	}
}

func TestRepairAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, outcome string
		attempts                     int
	}{
		{"fenced rejected", "```json\n" + candidate + "\n```", "```json\n" + candidate + "\n```", "incomplete", 2},
		{"repair", "No findings: []", candidate, "findings", 2},
		{"prose exhausted", "No findings: []", "No findings: []", "incomplete", 2},
		{"exhausted", `[{`, "still malformed", "incomplete", 2},
		{"invalid schema", strings.Replace(candidate, `"confidence":0.95`, `"confidence":2`, 1), "[]", "clean", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			var deadline time.Time
			var firstPrompt string
			tokens := int64(12)
			fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
				if r.Model == "security-test" {
					return agent.Result{Output: "[]"}, nil
				}
				calls++
				d, _ := ctx.Deadline()
				if calls == 1 {
					deadline, firstPrompt = d, r.Prompt
					return agent.Result{Output: tc.first, Usage: agent.Usage{InputTokens: &tokens}}, nil
				}
				if d != deadline || r.Prompt == firstPrompt {
					t.Error("repair must receive feedback and share the original layer deadline")
				}
				return agent.Result{Output: tc.second, Usage: agent.Usage{InputTokens: &tokens}}, nil
			}}
			report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), policy(t, "1s"))
			if err != nil || report.Outcome != tc.outcome || calls != tc.attempts || len(report.Layers) != 2 {
				t.Fatalf("repair result: %+v, %v, calls=%d", report, err, calls)
			}
			layer := report.Layers[0]
			if len(layer.Attempts) != calls || *layer.Attempts[0].Usage.InputTokens != 12 {
				t.Fatal("attempt usage lost")
			}
			if tc.outcome == "incomplete" && (layer.Status != "incomplete" || layer.Error != "invalid_output" || len(report.Findings) != 0) {
				t.Fatal("malformed layer was silently accepted or partially salvaged")
			}
		})
	}
}

func TestTimeoutFailureAndCancellation(t *testing.T) {
	for _, kind := range []string{"timeout", "process_failure", "invalid_output", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			var calls atomic.Int32
			fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
				calls.Add(1)
				if r.Model == "security-test" {
					return agent.Result{Output: candidate}, nil
				}
				if kind == "timeout" {
					<-ctx.Done()
					return agent.Result{}, ctx.Err()
				}
				return agent.Result{}, &agent.RunError{Kind: agent.ErrorKind(kind), ExitCode: 1}
			}}
			start := time.Now()
			report, err := (Runner{Agent: fake}).Run(ctx, []byte(syntheticDiff), policy(t, "30ms"))
			if err != nil || report.Outcome != "incomplete" || len(report.Layers) != 2 || report.Layers[0].Error != kind || report.Layers[0].Status != "incomplete" {
				t.Fatalf("failure disappeared: %+v, %v", report, err)
			}
			if time.Since(start) > 2*time.Second || calls.Load() > 2 {
				t.Fatal("failure was retried or timeout did not bound execution")
			}
			if kind != "canceled" && (len(report.Findings) != 1 || report.Findings[0].Layer != "security") {
				t.Fatal("failed layer discarded successful peer findings")
			}
			if kind == "canceled" && calls.Load() != 0 {
				t.Fatal("canceled review started model calls")
			}
		})
	}
}

func TestReviewRejectsInvalidSetup(t *testing.T) {
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("invalid setup invoked agent")
		return agent.Result{}, nil
	}}
	for _, tc := range []struct {
		runner Runner
		diff   []byte
	}{
		{Runner{}, []byte(syntheticDiff)},
		{Runner{Agent: fake, Concurrency: -1}, []byte(syntheticDiff)},
		{Runner{Agent: fake}, nil},
		{Runner{Agent: fake}, []byte(" \n")},
		{Runner{Agent: fake}, make([]byte, (16<<20)+1)},
	} {
		if _, err := tc.runner.Run(context.Background(), tc.diff, config.Defaults()); err == nil {
			t.Fatal("invalid setup accepted")
		}
	}
}

func TestMaximumDiffAndConcurrency(t *testing.T) {
	// Public input limits: exactly 16 MiB is accepted and an arbitrarily large
	// positive worker limit still allocates only two specialist slots.
	diff := []byte(syntheticDiff + strings.Repeat(" ", (16<<20)-len(syntheticDiff)))
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		calls.Add(1)
		info, err := os.Stat(filepath.Join(r.WorkingDirectory, "diff.patch"))
		if err != nil || info.Size() != 16<<20 || len(r.Prompt) > 16<<10 {
			t.Error("maximum diff was truncated or inlined into the prompt")
		}
		return agent.Result{Output: "[]"}, nil
	}}
	report, err := (Runner{Agent: fake, Concurrency: int(^uint(0) >> 1)}).Run(context.Background(), diff, config.Defaults())
	if err != nil || report.Outcome != "clean" || calls.Load() != 2 {
		t.Fatalf("valid limit boundary failed: %+v, %v", report, err)
	}
}

func TestRepairTimeoutSharesBudget(t *testing.T) {
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
		if r.Model == "security-test" {
			return agent.Result{Output: "[]"}, nil
		}
		if calls.Add(1) == 1 {
			return agent.Result{Output: "malformed"}, nil
		}
		<-ctx.Done()
		// A late otherwise-valid response must not turn a timeout into clean.
		return agent.Result{Output: "[]"}, nil
	}}
	report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), policy(t, "50ms"))
	if err != nil || report.Outcome != "incomplete" || report.Layers[0].Error != "timeout" || calls.Load() != 2 || len(report.Layers[0].Attempts) != 2 {
		t.Fatalf("repair deadline not enforced: %+v, %v", report, err)
	}
}

func TestCancellationWhileLayerIsQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	fake := agent.Fake{Respond: func(ctx context.Context, _ agent.Request) (agent.Result, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return agent.Result{}, ctx.Err()
	}}
	done := make(chan Report, 1)
	go func() {
		report, err := (Runner{Agent: fake, Concurrency: 1}).Run(ctx, []byte(syntheticDiff), config.Defaults())
		if err != nil {
			t.Error(err)
		}
		done <- report
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("active layer never started")
	}
	cancel()
	report := <-done
	if report.Outcome != "incomplete" || calls.Load() != 1 {
		t.Fatalf("queued layer started after cancellation: %+v", report)
	}
	attempts := 0
	for _, layer := range report.Layers {
		if layer.Status != "incomplete" || layer.Error != "canceled" {
			t.Fatalf("canceled layer disappeared: %+v", layer)
		}
		attempts += len(layer.Attempts)
	}
	if attempts != 1 {
		t.Fatal("queued layer fabricated usage for an invocation that never ran")
	}
}

func TestLiveDroidSmoke(t *testing.T) {
	if os.Getenv("TOUCHSTONE_LIVE_SMOKE") != "1" {
		t.Skip("set TOUCHSTONE_LIVE_SMOKE=1 to run paid, authenticated headless Droid calls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report, err := (Runner{Agent: agent.Droid{}}).Run(ctx, []byte(syntheticDiff), config.Defaults())
	if err != nil || report.Outcome == "incomplete" || len(report.Layers) != 2 {
		t.Fatalf("live Droid smoke did not complete: %+v, %v", report, err)
	}
	for _, layer := range report.Layers {
		if layer.Status != "complete" || len(layer.Attempts) == 0 {
			t.Fatalf("live layer incomplete: %+v", layer)
		}
	}
	// Smoke tests transport and schema, not the truth or quality of findings.
}

func TestConcurrentReviewsHaveIndependentScratch(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		mu.Lock()
		seen[r.WorkingDirectory] = true
		mu.Unlock()
		return agent.Result{Output: "[]"}, nil
	}}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), config.Defaults()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(seen) != 2 {
		t.Fatalf("reviews shared scratch: %v", seen)
	}
}
