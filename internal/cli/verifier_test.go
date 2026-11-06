package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/review"
)

func TestVerifierCLIOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, verdict, status, outcome string
		code, kept, rejected           int
		noVerify, configDisabled       bool
	}{
		{"confirmed", "confirmed", "complete", "findings", 1, 1, 0, false, false},
		{"rejected", "rejected", "complete", "clean", 0, 0, 1, false, false},
		{"uncertain", "uncertain", "complete", "findings", 1, 1, 0, false, false},
		{"malformed", "malformed", "incomplete", "incomplete", 2, 1, 0, false, false},
		{"timeout", "timeout", "incomplete", "incomplete", 2, 1, 0, false, false},
		{"disabled flag", "", "disabled", "findings", 1, 1, 0, true, false},
		{"disabled config", "", "disabled", "findings", 1, 1, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			policy := "[layers.correctness]\nmodel='seed'\n[layers.verification]\nmodel='verify'\ntimeout='30ms'\n"
			if tc.configDisabled {
				policy += "enabled=false\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte(policy), 0o600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			fake := agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
				if r.Model == "seed" {
					return agent.Result{Output: cliFinding}, nil
				}
				if r.Model != "verify" {
					return agent.Result{Output: "[]"}, nil
				}
				calls.Add(1)
				if tc.verdict == "timeout" {
					<-ctx.Done()
					return agent.Result{}, ctx.Err()
				}
				output := `[{"id":"correctness:demo-1","verdict":"` + tc.verdict + `","reason":"The changed expression divides by zero."}]`
				return agent.Result{Output: output}, nil
			}}
			args := []string{"review", "--diff", "-", "--cwd", dir}
			if tc.noVerify {
				args = append(args, "--no-verify")
			}
			var out bytes.Buffer
			err := RunContext(context.Background(), args, strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
			var report review.Report
			if decodeErr := json.Unmarshal(out.Bytes(), &report); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if ExitCode(err) != tc.code || report.SchemaVersion != 2 || report.Outcome != tc.outcome ||
				report.Verifier.Status != tc.status || len(report.Findings) != tc.kept || len(report.Rejected) != tc.rejected {
				t.Fatalf("CLI verification contract failed: %s, %v", out.String(), err)
			}
			wantCalls := int32(1)
			if tc.status == "disabled" {
				wantCalls = 0
			}
			if tc.verdict == "malformed" {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Fatalf("unexpected verifier calls: %d", calls.Load())
			}
			for _, f := range report.Findings {
				wantVerdict, wantRank := tc.verdict, "normal"
				if tc.status != "complete" {
					wantVerdict = "unverified"
				}
				if wantVerdict == "uncertain" {
					wantRank = "lower"
				}
				if f.Verification.Verdict != wantVerdict || f.Rank != wantRank {
					t.Fatalf("wrong public verification metadata: %+v", f)
				}
			}
			// Literal field names specify the public finding and verification
			// contracts independently of the report's Go struct definitions.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			for _, section := range []string{"findings", "rejected"} {
				var entries []map[string]json.RawMessage
				if err := json.Unmarshal(raw[section], &entries); err != nil || entries == nil {
					t.Fatal("finding sections must be arrays, not null")
				}
				for _, entry := range entries {
					assertKeys(t, entry, []string{"layer", "id", "severity", "category", "file", "line", "title", "evidence", "confidence", "verification", "rank"})
					var v map[string]json.RawMessage
					if err := json.Unmarshal(entry["verification"], &v); err != nil {
						t.Fatal(err)
					}
					assertKeys(t, v, []string{"verdict", "reason"})
				}
			}
		})
	}
}

func assertKeys(t *testing.T, fields map[string]json.RawMessage, want []string) {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("serialized keys changed: %v, want %v", keys, want)
	}
}

func TestVerifierTransportDiagnosticsAreSafe(t *testing.T) {
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		if _, err := os.Stat(filepath.Join(r.WorkingDirectory, "candidates.json")); err == nil {
			return agent.Result{}, errors.New("private-verifier-diagnostic")
		}
		return agent.Result{Output: cliFinding}, nil
	}}
	var out, stderr bytes.Buffer
	err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", t.TempDir()}, strings.NewReader(cliDiff), &out, &stderr, "test", fake)
	if ExitCode(err) != 2 || strings.Contains(out.String()+stderr.String(), "private-verifier-diagnostic") {
		t.Fatal("verifier failure was hidden or leaked private diagnostics")
	}
}
