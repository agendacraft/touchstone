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
	"strings"
	"testing"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/review"
)

const cliDiff = "--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-return 0\n+return 1/0\n"
const cliFinding = `[{"id":"demo-1","severity":"error","category":"logic","file":"example.go","line":1,"title":"Division by zero","evidence":"The changed expression divides by zero.","confidence":1}]`

func TestReviewCLIOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		failure      error
		code         int
		outcome      string
	}{
		{"clean", "[]", nil, 0, "clean"},
		{"findings", cliFinding, nil, 1, "findings"},
		{"malformed", "No JSON", nil, 2, "incomplete"},
		{"process failure", "", errors.New("private diagnostic must not leak"), 2, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var out, stderr bytes.Buffer
			fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
				return agent.Result{Output: tc.output}, tc.failure
			}}
			err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", dir, "--no-verify"}, strings.NewReader(cliDiff), &out, &stderr, "test", fake)
			if ExitCode(err) != tc.code || stderr.Len() != 0 || strings.Contains(out.String(), "private diagnostic") {
				t.Fatalf("wrong command status or leaked diagnostics: %v, %q", err, out.String())
			}
			var report review.Report
			if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.SchemaVersion != 2 || report.Outcome != tc.outcome || report.Findings == nil || len(report.Layers) != 2 {
				t.Fatalf("invalid public JSON: %q, %v", out.String(), err)
			}
		})
	}
}

func TestReviewCLIFileAndConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "synthetic.patch")
	if err := os.WriteFile(file, []byte(cliDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte("[layers.correctness]\nmodel='synthetic-override'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	models := make(chan string, 2)
	fake := agent.Fake{Respond: func(_ context.Context, r agent.Request) (agent.Result, error) {
		models <- r.Model
		return agent.Result{Output: "[]"}, nil
	}}
	var out, stderr bytes.Buffer
	err := RunContext(context.Background(), []string{"review", "--diff", file, "--cwd", dir, "--concurrency", "1"}, strings.NewReader("stdin must not be used"), &out, &stderr, "test", fake)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{<-models: true}
	got[<-models] = true
	if !got["synthetic-override"] || !got["glm-5.3-flash"] {
		t.Fatalf("CLI ignored repository policy: %v", got)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic write failure") }

func TestReviewCLICommandErrors(t *testing.T) {
	dir := t.TempDir()
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		t.Error("invalid command invoked model")
		return agent.Result{Output: "[]"}, nil
	}}
	for _, tc := range []struct {
		args []string
		in   io.Reader
	}{
		{[]string{"review"}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", "-", "extra"}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", "-", "--unknown"}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", "-", "--concurrency", "0"}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", "-", "--cwd", filepath.Join(dir, "missing")}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", filepath.Join(dir, "missing")}, strings.NewReader(cliDiff)},
		{[]string{"review", "--diff", "-"}, strings.NewReader("")},
		{[]string{"review", "--diff", "-"}, strings.NewReader(strings.Repeat("x", (16<<20)+1))},
		{[]string{"review", "--diff", "-"}, brokenReader{}},
	} {
		var out, stderr bytes.Buffer
		err := RunContext(context.Background(), tc.args, tc.in, &out, &stderr, "test", fake)
		if ExitCode(err) != 3 || out.Len() != 0 {
			t.Fatalf("command error produced a report: %v, %q", err, out.String())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte("unknown=true"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", dir}, strings.NewReader(cliDiff), &out, &stderr, "test", fake)
	if ExitCode(err) != 3 || out.Len() != 0 {
		t.Fatal("invalid configuration did not fail before model calls")
	}
}

func TestReviewCLIOutputFailure(t *testing.T) {
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{Output: "[]"}, nil
	}}
	err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", t.TempDir()}, strings.NewReader(cliDiff), brokenWriter{}, io.Discard, "test", fake)
	if ExitCode(err) != 3 {
		t.Fatal("failed output was reported as a clean review")
	}
}

func TestReviewJSONMetadataContract(t *testing.T) {
	tokens := int64(23)
	fake := agent.Fake{Respond: func(context.Context, agent.Request) (agent.Result, error) {
		return agent.Result{
			Output: "[]", Usage: agent.Usage{InputTokens: &tokens},
			Cost: &agent.Cost{Amount: 0.01, Currency: "USD"},
		}, nil
	}}
	var out bytes.Buffer
	err := RunContext(context.Background(), []string{"review", "--diff", "-", "--cwd", t.TempDir()}, strings.NewReader(cliDiff), &out, io.Discard, "test", fake)
	if err != nil {
		t.Fatal(err)
	}
	// This literal is the public serialized contract, independent of the Go
	// report types. It rejects missing/renamed keys and zeroed unknown usage.
	const expected = `{
		"schema_version":2,"outcome":"clean","findings":[],
		"rejected":[],"verifier":{"name":"verification","status":"not_needed","error":"","attempts":[]},
		"layers":[
			{"name":"correctness","status":"complete","error":"","attempts":[
				{"usage":{"input_tokens":23,"output_tokens":null,"cache_read_input_tokens":null,"cache_creation_input_tokens":null,"factory_credits":null,"thinking_tokens":null},"cost":{"amount":0.01,"currency":"USD"},"duration_ms":0}
			]},
			{"name":"security","status":"complete","error":"","attempts":[
				{"usage":{"input_tokens":23,"output_tokens":null,"cache_read_input_tokens":null,"cache_creation_input_tokens":null,"factory_credits":null,"thinking_tokens":null},"cost":{"amount":0.01,"currency":"USD"},"duration_ms":0}
			]}
		]
	}`
	var got, want any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	for _, layer := range got.(map[string]any)["layers"].([]any) {
		for _, attempt := range layer.(map[string]any)["attempts"].([]any) {
			fields := attempt.(map[string]any)
			duration, ok := fields["duration_ms"].(float64)
			if !ok || duration < 0 || duration != float64(int64(duration)) {
				t.Fatal("duration_ms must be a nonnegative integer")
			}
			fields["duration_ms"] = float64(0)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("review JSON contract changed: %s", out.String())
	}
}
