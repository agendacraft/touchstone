package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func request(t *testing.T) Request {
	t.Helper()
	return Request{
		Prompt: "Synthetic input; $(must-not-run) 'quoted text'",
		Model:  "synthetic-model", ReasoningEffort: "high",
		WorkingDirectory: t.TempDir(), Timeout: time.Second,
	}
}

// Recorded synthetic Droid 1.4.1 output is the external protocol oracle.
func TestRecordedEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, session string
		credits                int64
	}{
		{"clean", "clean", "synthetic-clean-session", 10088},
		{"finding", "findings", "synthetic-finding-session", 10405},
		{"incomplete", "incomplete", "synthetic-incomplete-session", 10092},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseEnvelope(fixture(t, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			if result.Report.Outcome != tc.outcome || result.SessionID != tc.session ||
				result.Usage.FactoryCredits == nil || *result.Usage.FactoryCredits != tc.credits || result.Cost != nil {
				t.Fatalf("incorrect recorded result: %+v", result)
			}
			if tc.name == "finding" {
				if len(result.Report.Findings) != 1 {
					t.Fatal("lost recorded finding")
				}
				f := result.Report.Findings[0]
				if f.ID != "demo-1" || f.File != "src/example.go" || f.Line != 7 || f.Layer != "correctness" {
					t.Fatalf("incorrect recorded location: %+v", f)
				}
			}
		})
	}
	result, err := parseEnvelope(fixture(t, "permission-failure"))
	if err == nil || result.Report.Outcome != "" || result.SessionID != "synthetic-permission-failure-session" ||
		result.Usage.FactoryCredits == nil || *result.Usage.FactoryCredits != 10906 {
		t.Fatalf("failure became clean or lost metadata: %+v, %v", result, err)
	}
}

func TestCommandConstructionAndFreshSessions(t *testing.T) {
	r := request(t)
	var dirs []string
	calls := 0
	d := Droid{Binary: "synthetic-droid", run: func(ctx context.Context, binary string, args []string, cwd string, out, stderr io.Writer) error {
		calls++
		if binary != "synthetic-droid" || cwd != r.WorkingDirectory {
			t.Fatal("binary or cwd changed")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing per-run deadline")
		}
		if len(args) < 16 {
			t.Fatalf("incomplete command: %v", args)
		}
		promptPath, settingsPath := args[4], args[12]
		expected := []string{"exec", "-o", "json", "-f", promptPath,
			"--cwd", r.WorkingDirectory, "-m", r.Model, "-r", r.ReasoningEffort,
			"--settings", settingsPath, "--disable-builtin-skills"}
		expected = append(expected, "--only-tools", "Read,Grep,Glob,LS")
		if !reflect.DeepEqual(args, expected) {
			t.Fatalf("command differs from CLI contract:\ngot %v\nwant %v", args, expected)
		}
		prompt, err := os.ReadFile(promptPath)
		if err != nil || string(prompt) != r.Prompt {
			t.Fatalf("prompt did not survive file boundary: %v", err)
		}
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			HooksDisabled    bool `json:"hooksDisabled"`
			CloudSessionSync bool `json:"cloudSessionSync"`
			Session          struct {
				Autonomy string `json:"autonomyLevel"`
				Mode     string `json:"interactionMode"`
			} `json:"sessionDefaultSettings"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if !settings.HooksDisabled || settings.CloudSessionSync ||
			settings.Session.Autonomy != "off" || settings.Session.Mode != "auto" {
			t.Fatalf("wrong runtime settings: %+v", settings)
		}
		for _, file := range []string{promptPath, settingsPath} {
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("temporary file permissions: %v", err)
			}
		}
		dir := filepath.Dir(promptPath)
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("temporary directory permissions: %v", err)
		}
		if filepath.Dir(settingsPath) != dir || dir == cwd {
			t.Fatal("runtime files are not isolated from repository")
		}
		dirs = append(dirs, dir)
		_, err = out.Write(fixture(t, "clean"))
		return err
	}}
	for range 2 {
		result, err := d.Run(context.Background(), r)
		if err != nil || result.Report.Outcome != "clean" {
			t.Fatalf("run failed: %+v, %v", result, err)
		}
	}
	if calls != 2 || dirs[0] == dirs[1] {
		t.Fatal("runs shared runtime state")
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temporary prompt/settings files leaked")
		}
	}
}

func assertKind(t *testing.T, err error, kind ErrorKind) *RunError {
	t.Helper()
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != kind {
		t.Fatalf("want %s, got %v", kind, err)
	}
	return runErr
}

func TestMalformedEnvelopeAndModelOutput(t *testing.T) {
	valid := fixture(t, "clean")
	var obj map[string]any
	if err := json.Unmarshal(valid, &obj); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"empty": nil, "text": []byte("not JSON"), "trailing": append(bytes.Clone(valid), []byte("{}")...),
		"duplicate": []byte(`{"type":"result","type":"other"}`),
	}
	for _, field := range []string{"type", "subtype", "is_error", "result", "session_id"} {
		var changed map[string]any
		if err := json.Unmarshal(valid, &changed); err != nil {
			t.Fatal(err)
		}
		delete(changed, field)
		data, _ := json.Marshal(changed)
		cases["missing "+field] = data
	}
	for name, change := range map[string]func(map[string]any){
		"free text":      func(o map[string]any) { o["result"] = "No issues." },
		"fenced":         func(o map[string]any) { o["result"] = "```json\n" + o["result"].(string) + "\n```" },
		"null text":      func(o map[string]any) { o["result"] = nil },
		"object text":    func(o map[string]any) { o["result"] = map[string]any{} },
		"error true":     func(o map[string]any) { o["is_error"] = true },
		"negative usage": func(o map[string]any) { o["usage"] = map[string]any{"input_tokens": -1} },
		"bad usage":      func(o map[string]any) { o["usage"] = "unknown" },
	} {
		var changed map[string]any
		if err := json.Unmarshal(valid, &changed); err != nil {
			t.Fatal(err)
		}
		change(changed)
		data, _ := json.Marshal(changed)
		cases[name] = data
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			r := request(t)
			d := Droid{run: func(_ context.Context, _ string, _ []string, _ string, out, _ io.Writer) error {
				_, err := out.Write(data)
				return err
			}}
			result, err := d.Run(context.Background(), r)
			_ = assertKind(t, err, InvalidOutput)
			if result.Report.Outcome != "" {
				t.Fatal("malformed output became a review")
			}
		})
	}
	delete(obj, "usage")
	data, _ := json.Marshal(obj)
	result, err := parseEnvelope(data)
	if err != nil || result.Usage.InputTokens != nil || result.Cost != nil {
		t.Fatal("missing usage/cost must remain unknown")
	}
}

func TestEnvelopeRejectsConsumedFieldAliases(t *testing.T) {
	clean := string(fixture(t, "clean"))
	for _, field := range []string{"type", "subtype", "is_error", "result", "session_id", "usage",
		"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "factory_credits", "thinking_tokens"} {
		t.Run(field, func(t *testing.T) {
			original := `"` + field + `"`
			alias := `"` + strings.ToUpper(field) + `"`
			for _, input := range []string{
				strings.Replace(clean, original, alias, 1),
				strings.Replace(clean, original+":", alias+`: null, `+original+":", 1),
			} {
				result, err := parseEnvelope([]byte(input))
				if err == nil || result.Report.Outcome != "" {
					t.Fatalf("field alias accepted: %+v, %v", result, err)
				}
			}
		})
	}
	contradictory := strings.Replace(clean, `"is_error": false`, `"is_error": true, "IS_ERROR": false`, 1)
	if result, err := parseEnvelope([]byte(contradictory)); err == nil || result.Report.Outcome != "" {
		t.Fatalf("failure became a clean review: %+v, %v", result, err)
	}
	metadata := strings.Replace(clean, `"duration_ms": 3814`, `"duration_ms": 3814, "DURATION_MS": 1`, 1)
	metadata = strings.Replace(metadata, `"ttft_ms": 3444`, `"ttft_ms": 3444, "TTFT_MS": 1`, 1)
	if result, err := parseEnvelope([]byte(metadata)); err != nil || result.Report.Outcome != "clean" {
		t.Fatalf("unrelated metadata rejected: %+v, %v", result, err)
	}
}

func TestOutputBoundedThroughCopy(t *testing.T) {
	// io.Copy may use ReaderFrom: it must not bypass the output cap.
	b := &boundedBuffer{limit: 8}
	if _, err := io.Copy(b, io.LimitReader(strings.NewReader(strings.Repeat("x", 100)), 100)); err != nil {
		t.Fatal(err)
	}
	if len(b.Bytes()) != 8 || !b.overflow {
		t.Fatal("output cap bypassed")
	}
	for _, stderrOverflow := range []bool{false, true} {
		r := request(t)
		d := Droid{run: func(_ context.Context, _ string, _ []string, _ string, out, stderr io.Writer) error {
			if stderrOverflow {
				_, _ = out.Write(fixture(t, "clean"))
				_, _ = io.WriteString(stderr, strings.Repeat("x", (64<<10)+1))
			} else {
				_, _ = io.WriteString(out, strings.Repeat("x", (2<<20)+1))
			}
			return nil
		}}
		_, err := d.Run(context.Background(), r)
		_ = assertKind(t, err, InvalidOutput)
	}
}

func TestRealSubprocessBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic executable")
	}
	dir := t.TempDir()
	recording := filepath.Join(dir, "recording.json")
	if err := os.WriteFile(recording, fixture(t, "permission-failure"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "synthetic droid")
	script := "#!/bin/sh\ncat '" + recording + "'\nexit 1\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r := request(t)
	result, err := (Droid{Binary: executable}).Run(context.Background(), r)
	runErr := assertKind(t, err, ProcessFailure)
	if runErr.ExitCode != 1 || result.SessionID != "synthetic-permission-failure-session" || result.Report.Outcome != "" {
		t.Fatalf("failure status or metadata lost: %+v, %v", result, err)
	}
	if strings.Contains(err.Error(), "permission") {
		t.Fatal("raw model output leaked into error")
	}
	// A valid report with a nonzero process exit must still fail.
	if err := os.WriteFile(recording, fixture(t, "clean"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = (Droid{Binary: executable}).Run(context.Background(), r)
	_ = assertKind(t, err, ProcessFailure)
	if result.Report.Outcome != "" {
		t.Fatal("nonzero exit exposed a clean report")
	}
	// Exercise successful parsing through the actual subprocess, not just a runner stub.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ncat '"+recording+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err = (Droid{Binary: executable}).Run(context.Background(), r)
	if err != nil || result.Report.Outcome != "clean" || result.SessionID != "synthetic-clean-session" {
		t.Fatalf("successful subprocess result lost: %+v, %v", result, err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, err = (Droid{Binary: executable}).Run(context.Background(), r)
	_ = assertKind(t, err, Timeout)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatal("deadline did not bound subprocess execution")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (Droid{Binary: executable}).Run(ctx, r)
	_ = assertKind(t, err, Canceled)
	_, err = (Droid{Binary: filepath.Join(dir, "missing")}).Run(context.Background(), request(t))
	_ = assertKind(t, err, ProcessFailure)
}

func TestRequestValidationAndFake(t *testing.T) {
	r := request(t)
	called := false
	fake := Fake{Respond: func(ctx context.Context, got Request) (Result, error) {
		called = true
		if got != r || ctx == nil {
			t.Fatal("fake changed request")
		}
		return parseEnvelope(fixture(t, "clean"))
	}}
	var a Agent = fake
	result, err := a.Run(context.Background(), r)
	if !called || err != nil || result.Report.Outcome != "clean" {
		t.Fatalf("fake response: %+v, %v", result, err)
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Prompt = "" }, func(r *Request) { r.Model = " " },
		func(r *Request) { r.Timeout = 0 }, func(r *Request) { r.WorkingDirectory = "relative" },
		func(r *Request) { r.ReasoningEffort = "impossible" },
	} {
		bad := r
		mutate(&bad)
		_, err := fake.Run(context.Background(), bad)
		_ = assertKind(t, err, InvalidRequest)
		_, err = (Droid{}).Run(context.Background(), bad)
		_ = assertKind(t, err, InvalidRequest)
	}
	want := errors.New("synthetic failure")
	fake.Respond = func(context.Context, Request) (Result, error) { return Result{}, want }
	if _, err := fake.Run(context.Background(), r); !errors.Is(err, want) {
		t.Fatal("fake did not propagate error")
	}
}
