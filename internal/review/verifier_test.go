package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/findings"
)

func verifierPolicy(t *testing.T, timeout string) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`
[layers.correctness]
model = "correctness-test"
[layers.security]
model = "security-test"
[layers.verification]
model = "verifier-test"
reasoning_effort = "high"
timeout = "` + timeout + `"
rules = ["Inspect the removed guard."]
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const confirmed = `[{"id":"correctness:synthetic-1","verdict":"confirmed","reason":"Removing the zero guard makes divide(0) evaluate 10/0 at line 2."}]`

// The merge oracle is the task's rules, not model judgment: confirmed stays,
// rejected moves to the audit section, and uncertain stays with lower rank.
func TestVerifierMergeAndBoundary(t *testing.T) {
	for _, verdict := range []string{"confirmed", "rejected", "uncertain"} {
		t.Run(verdict, func(t *testing.T) {
			var completed atomic.Int32
			var calls int
			var scratch string
			credits := int64(31)
			fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
				if r.Model != "verifier-test" {
					// Specialists must never see the other sessions' findings.
					if _, err := os.Stat(filepath.Join(r.WorkingDirectory, "candidates.json")); !errors.Is(err, os.ErrNotExist) {
						t.Error("candidate file exists before specialists finish")
					}
					completed.Add(1)
					return agent.Result{Output: candidate}, nil
				}
				calls++
				scratch = r.WorkingDirectory
				if completed.Load() != 2 || !r.RawOutput || r.ReasoningEffort != "high" || r.Timeout <= 0 {
					t.Error("verifier ran before specialists or lost policy")
				}
				for _, name := range []string{"diff.patch", "candidates.json"} {
					file := filepath.Join(scratch, name)
					info, err := os.Stat(file)
					if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(r.Prompt, file) {
						t.Errorf("missing private input file: %s, %v", name, err)
					}
				}
				diff, err := os.ReadFile(filepath.Join(scratch, "diff.patch"))
				if err != nil || string(diff) != syntheticDiff {
					t.Error("verifier did not receive the same diff")
				}
				data, err := os.ReadFile(filepath.Join(scratch, "candidates.json"))
				if err != nil {
					t.Fatal(err)
				}
				var inputs []struct {
					Layer string `json:"layer"`
					findings.Candidate
				}
				if err := json.Unmarshal(data, &inputs); err != nil || len(inputs) != 2 {
					t.Fatalf("invalid candidate file: %s, %v", data, err)
				}
				if inputs[0].ID != "correctness:synthetic-1" || inputs[1].ID != "security:synthetic-1" ||
					inputs[0].Evidence != "divide(0) now evaluates 10/0." || inputs[0].Layer != "correctness" {
					t.Fatal("layer-scoped identity or evidence lost")
				}
				// The final emitted prompt is an owned agent interface. It must
				// reference files, not include prior responses/conversation.
				if strings.Contains(r.Prompt, syntheticDiff) || strings.Contains(r.Prompt, "divide(0) now evaluates 10/0.") ||
					!strings.Contains(r.Prompt, "Inspect the removed guard.") {
					t.Error("prompt inlined conversation or lost configured rules")
				}
				output := strings.TrimSuffix(confirmed, "]") + "," +
					strings.TrimPrefix(strings.ReplaceAll(confirmed, "correctness:", "security:"), "[")
				// Reverse response order to prove merging uses identity, not position.
				output = strings.ReplaceAll(output, `"confirmed"`, `"`+verdict+`"`)
				var entries []findings.Verdict
				_ = json.Unmarshal([]byte(output), &entries)
				entries[0], entries[1] = entries[1], entries[0]
				reversed, _ := json.Marshal(entries)
				return agent.Result{
					Output: string(reversed), Usage: agent.Usage{FactoryCredits: &credits},
					Cost: &agent.Cost{Amount: 0.02, Currency: "USD"},
				}, nil
			}}
			report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), verifierPolicy(t, "1s"))
			if err != nil || report.Verifier.Status != "complete" || calls != 1 || report.SchemaVersion != 2 {
				t.Fatalf("verification failed: %+v, %v", report, err)
			}
			wantOutcome, wantKept, wantRejected, rank := "findings", 2, 0, "normal"
			if verdict == "rejected" {
				wantOutcome, wantKept, wantRejected = "clean", 0, 2
			}
			if verdict == "uncertain" {
				rank = "lower"
			}
			if report.Outcome != wantOutcome || len(report.Findings) != wantKept || len(report.Rejected) != wantRejected {
				t.Fatalf("wrong merge: %+v", report)
			}
			merged := append(append([]Finding{}, report.Findings...), report.Rejected...)
			for i, f := range merged {
				if f.ID != "synthetic-1" || f.Verification.Verdict != verdict || f.Verification.Reason == "" ||
					f.Rank != rank || f.Confidence != 0.95 || f.Severity != "error" ||
					f.Layer != []string{"correctness", "security"}[i] {
					t.Fatalf("merge changed candidate or lost verdict: %+v", f)
				}
			}
			attempt := report.Verifier.Attempts[0]
			if attempt.Usage.FactoryCredits == nil || *attempt.Usage.FactoryCredits != 31 ||
				attempt.Cost == nil || attempt.Cost.Amount != 0.02 || attempt.DurationMS < 0 {
				t.Fatal("verifier cost/latency lost")
			}
			if _, err := os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("verifier scratch leaked")
			}
		})
	}
}

func TestVerifierRepairAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, errorCode string
		failure                        error
		attempts                       int
	}{
		{"fenced", "```json\n" + confirmed + "\n```", "", "", nil, 1},
		{"repair", "private-malformed", confirmed, "", nil, 2},
		{"prose exhausted", "No verdicts: []", "No verdicts: []", "invalid_output", nil, 2},
		{"missing", "[]", "[]", "invalid_output", nil, 2},
		{"unknown", strings.Replace(confirmed, "correctness:", "security:", 1), "[]", "invalid_output", nil, 2},
		{"duplicate", strings.TrimSuffix(confirmed, "]") + "," + strings.TrimPrefix(confirmed, "["), "[]", "invalid_output", nil, 2},
		{"transport", "", "", "process_failure", &agent.RunError{Kind: agent.ProcessFailure}, 1},
		{"bad envelope", "", "", "invalid_output", &agent.RunError{Kind: agent.InvalidOutput}, 1},
		{"agent", "", "", "agent_error", errors.New("private diagnostic"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var deadline time.Time
			var firstPrompt string
			fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
				switch r.Model {
				case "correctness-test":
					return agent.Result{Output: candidate}, nil
				case "security-test":
					return agent.Result{Output: "[]"}, nil
				}
				calls++
				d, _ := ctx.Deadline()
				if calls == 1 {
					deadline, firstPrompt = d, r.Prompt
					return agent.Result{Output: tc.first}, tc.failure
				}
				if d != deadline || r.Prompt == firstPrompt || strings.Count(r.Prompt, tc.first) > strings.Count(firstPrompt, tc.first) {
					t.Error("repair lost deadline/feedback or reinserted malformed response")
				}
				return agent.Result{Output: tc.second}, nil
			}}
			report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), verifierPolicy(t, "1s"))
			if err != nil || calls != tc.attempts || len(report.Verifier.Attempts) != tc.attempts || report.Verifier.Error != tc.errorCode {
				t.Fatalf("wrong bounded verification: %+v, %v, calls=%d", report, err, calls)
			}
			if tc.errorCode == "" {
				if report.Outcome != "findings" || report.Findings[0].Verification.Verdict != "confirmed" {
					t.Fatal("valid verdict lost")
				}
			} else if report.Outcome != "incomplete" || report.Verifier.Status != "incomplete" ||
				len(report.Findings) != 1 || len(report.Rejected) != 0 || report.Findings[0].Verification.Verdict != "unverified" ||
				report.Findings[0].Verification.Reason != "" {
				t.Fatalf("failed verifier dropped or verified a candidate: %+v", report)
			}
		})
	}
}

func TestVerifierDeadlineAndCancellation(t *testing.T) {
	for _, kind := range []string{"timeout", "repair timeout", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
				switch r.Model {
				case "correctness-test":
					return agent.Result{Output: candidate}, nil
				case "security-test":
					return agent.Result{Output: "[]"}, nil
				}
				calls++
				if kind == "repair timeout" && calls == 1 {
					return agent.Result{Output: "malformed"}, nil
				}
				if kind == "canceled" {
					cancel()
				}
				<-ctx.Done()
				// A valid late response must not become confirmed.
				return agent.Result{Output: confirmed}, nil
			}}
			start := time.Now()
			report, err := (Runner{Agent: fake}).Run(ctx, []byte(syntheticDiff), verifierPolicy(t, "40ms"))
			wantError, wantCalls := "timeout", 1
			if kind == "repair timeout" {
				wantCalls = 2
			}
			if kind == "canceled" {
				wantError = "canceled"
			}
			if err != nil || report.Outcome != "incomplete" || report.Verifier.Error != wantError || calls != wantCalls ||
				len(report.Findings) != 1 || report.Findings[0].Verification.Verdict != "unverified" || time.Since(start) > time.Second {
				t.Fatalf("verifier deadline/cancellation failed: %+v, %v", report, err)
			}
			if kind != "canceled" && report.Verifier.Attempts[len(report.Verifier.Attempts)-1].DurationMS < 20 {
				t.Fatal("verifier latency was not measured")
			}
		})
	}
}

func TestDisabledAndEmptyVerifier(t *testing.T) {
	for _, mode := range []string{"flag", "config", "empty"} {
		t.Run(mode, func(t *testing.T) {
			cfg := verifierPolicy(t, "1s")
			if mode == "config" {
				var err error
				cfg, err = config.Parse([]byte("[layers.verification]\nenabled=false"))
				if err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			fake := agent.Fake{Respond: func(_ context.Context, _ agent.Request) (agent.Result, error) {
				calls.Add(1)
				output := candidate
				if mode == "empty" {
					output = "[]"
				}
				return agent.Result{Output: output}, nil
			}}
			report, err := (Runner{Agent: fake, DisableVerifier: mode == "flag"}).Run(context.Background(), []byte(syntheticDiff), cfg)
			wantStatus := "disabled"
			if mode == "empty" {
				wantStatus = "not_needed"
			}
			if err != nil || calls.Load() != 2 || report.Verifier.Status != wantStatus || len(report.Verifier.Attempts) != 0 {
				t.Fatalf("disabled/empty verification made calls: %+v, %v", report, err)
			}
			for _, f := range report.Findings {
				if f.Verification.Verdict != "unverified" || f.Rank != "normal" {
					t.Fatal("disabled verifier labeled finding verified")
				}
			}
		})
	}
}

func TestPartialSpecialistFailureStillIncomplete(t *testing.T) {
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		switch r.Model {
		case "correctness-test":
			return agent.Result{Output: candidate}, nil
		case "security-test":
			return agent.Result{}, errors.New("synthetic failure")
		default:
			return agent.Result{Output: confirmed}, nil
		}
	}}
	report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), verifierPolicy(t, "1s"))
	if err != nil || report.Outcome != "incomplete" || report.Verifier.Status != "complete" ||
		len(report.Findings) != 1 || report.Findings[0].Verification.Verdict != "confirmed" {
		t.Fatalf("verification hid a failed specialist: %+v, %v", report, err)
	}
}

func TestInvalidCoverageCannotPartiallyReject(t *testing.T) {
	rejection := strings.Replace(confirmed, `"confirmed"`, `"rejected"`, 1)
	for _, output := range []string{
		rejection, // One valid rejection, but the other layer's verdict is missing.
		strings.TrimSuffix(rejection, "]") + "," + strings.TrimPrefix(rejection, "["),
		strings.TrimSuffix(rejection, "]") + "," + strings.TrimPrefix(strings.Replace(confirmed, "correctness:", "unknown:", 1), "["),
	} {
		calls := 0
		fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
			if r.Model != "verifier-test" {
				return agent.Result{Output: candidate}, nil
			}
			calls++
			return agent.Result{Output: output}, nil
		}}
		report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), verifierPolicy(t, "1s"))
		if err != nil || report.Outcome != "incomplete" || report.Verifier.Error != "invalid_output" ||
			calls != 2 || len(report.Findings) != 2 || len(report.Rejected) != 0 {
			t.Fatalf("partial malformed response applied: %+v, %v", report, err)
		}
		for _, f := range report.Findings {
			if f.Verification.Verdict != "unverified" || f.Verification.Reason != "" {
				t.Fatalf("partial verdict was presented as verified: %+v", f)
			}
		}
	}
}

// This is a development-only paid smoke, not an eval suite. Its independent
// oracle is Go integer arithmetic: zero panics after guard removal, whereas
// truncation for n=3 is pre-existing. Only the verifier uses real headless Droid.
func TestLiveDroidVerifierSmoke(t *testing.T) {
	if os.Getenv("TOUCHSTONE_LIVE_VERIFIER_SMOKE") != "1" {
		t.Skip("set TOUCHSTONE_LIVE_VERIFIER_SMOKE=1 for paid authenticated Droid verification")
	}
	cfg := config.Defaults()
	seed, err := findings.ParseCandidates([]byte(candidate))
	if err != nil {
		t.Fatal(err)
	}
	falsePositive := seed[0]
	falsePositive.ID = "synthetic-false"
	falsePositive.Title = "Guard removal introduces integer truncation"
	falsePositive.Evidence = "divide(3) returns 3 instead of an exact fraction because the zero guard was removed."
	seeds, _ := json.Marshal(append(seed, falsePositive))
	var specialistCalls atomic.Int32
	droid := agent.Droid{}
	routed := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
		if _, err := os.Stat(filepath.Join(r.WorkingDirectory, "candidates.json")); err == nil {
			return droid.Run(ctx, r)
		}
		if specialistCalls.Add(1) == 1 {
			return agent.Result{Output: string(seeds)}, nil
		}
		return agent.Result{Output: "[]"}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report, err := (Runner{Agent: routed}).Run(ctx, []byte(syntheticDiff), cfg)
	if err != nil || report.Outcome != "findings" || report.Verifier.Status != "complete" || len(report.Findings) != 1 || len(report.Rejected) != 1 {
		t.Fatalf("live verifier did not separate real and false claims: %+v, %v", report, err)
	}
	if report.Findings[0].ID != "synthetic-1" || report.Findings[0].Verification.Verdict != "confirmed" ||
		report.Rejected[0].ID != "synthetic-false" || report.Rejected[0].Verification.Verdict != "rejected" {
		t.Fatalf("live verifier got planted claims wrong: %+v", report)
	}
	for _, f := range append(report.Findings, report.Rejected...) {
		if strings.TrimSpace(f.Verification.Reason) == "" {
			t.Fatal("live verifier omitted concrete reason")
		}
	}
	data, _ := json.Marshal(report)
	t.Log(string(data))
}

func TestMixedVerdictsPreserveOrderAndIdentity(t *testing.T) {
	var inputs []findings.Candidate
	original, err := findings.ParseCandidates([]byte(candidate))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		f := original[0]
		f.ID = id
		inputs = append(inputs, f)
	}
	data, _ := json.Marshal(inputs)
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		switch r.Model {
		case "correctness-test":
			return agent.Result{Output: string(data)}, nil
		case "security-test":
			return agent.Result{Output: "[]"}, nil
		}
		return agent.Result{Output: `[
			{"id":"correctness:three","verdict":"uncertain","reason":"Caller context missing."},
			{"id":"correctness:one","verdict":"confirmed","reason":"Zero guard removed at line 2."},
			{"id":"correctness:two","verdict":"rejected","reason":"10/n already truncated before this diff."}
		]`}, nil
	}}
	report, err := (Runner{Agent: fake}).Run(context.Background(), []byte(syntheticDiff), verifierPolicy(t, "1s"))
	if err != nil || report.Outcome != "findings" || len(report.Findings) != 2 || len(report.Rejected) != 1 {
		t.Fatalf("mixed verdicts lost: %+v, %v", report, err)
	}
	got := []string{report.Findings[0].ID, report.Findings[0].Rank, report.Findings[1].ID, report.Findings[1].Rank, report.Rejected[0].ID}
	if !reflect.DeepEqual(got, []string{"one", "normal", "three", "lower", "two"}) {
		t.Fatalf("unexpected reordering/ranking: %v", got)
	}
}
