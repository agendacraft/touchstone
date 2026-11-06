package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifierProcessExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic Droid executable")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	script := "#!/bin/sh\nif [ -f candidates.json ]; then\ncat \"$TOUCHSTONE_VERDICT_ENVELOPE\"\nelse\ncat \"$TOUCHSTONE_CANDIDATE_ENVELOPE\"\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "droid"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidatesPath := filepath.Join(dir, "candidates-envelope.json")
	verdictPath := filepath.Join(dir, "verdict-envelope.json")
	writeEnvelope := func(file, output string) {
		t.Helper()
		data, err := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "is_error": false,
			"session_id": "synthetic-session", "result": output,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeEnvelope(candidatesPath, `[{"id":"one","severity":"error","category":"logic","file":"example.go","line":1,"title":"Zero divisor","evidence":"The changed expression divides by zero.","confidence":1}]`)
	for _, tc := range []struct {
		verdict, outcome string
		code             int
	}{
		{"confirmed", "findings", 1},
		{"rejected", "clean", 0},
		{"uncertain", "findings", 1},
		{"invalid", "incomplete", 2},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			writeEnvelope(verdictPath, `[
				{"id":"correctness:one","verdict":"`+tc.verdict+`","reason":"The changed expression divides by zero."},
				{"id":"security:one","verdict":"`+tc.verdict+`","reason":"The changed expression divides by zero."}
			]`)
			cmd := exec.Command(binary, "review", "--diff", "-")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TOUCHSTONE_VERDICT_ENVELOPE="+verdictPath, "TOUCHSTONE_CANDIDATE_ENVELOPE="+candidatesPath)
			cmd.Stdin = strings.NewReader("--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-return 0\n+return 1/0\n")
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			_ = cmd.Run()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.code || stderr.Len() != 0 {
				t.Fatalf("wrong verifier process exit: %v, stderr %q", cmd.ProcessState, stderr.String())
			}
			var report struct {
				Outcome  string `json:"outcome"`
				Verifier struct {
					Status string `json:"status"`
				} `json:"verifier"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Outcome != tc.outcome {
				t.Fatalf("bad verifier process report: %s, %v", out.String(), err)
			}
			if (report.Verifier.Status == "complete") != (tc.code != 2) {
				t.Fatal("subprocess verifier completion was misreported")
			}
		})
	}
}
