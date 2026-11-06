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

// Exercise the built command's actual process exits, with a synthetic executable
// at the external Droid boundary. No account, credentials, or model is used.
func TestProcessExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic Droid executable")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	build := exec.Command("go", "build", "-o", binary, ".")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	build.Dir = cwd
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	script := "#!/bin/sh\ncat \"$TOUCHSTONE_TEST_ENVELOPE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "droid"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(dir, "envelope.json")
	for _, tc := range []struct {
		name, output, outcome string
		code                  int
		args                  []string
	}{
		{"clean", "[]", "clean", 0, []string{"review", "--diff", "-"}},
		{"json fenced clean", "```json\n[]\n```", "clean", 0, []string{"review", "--diff", "-"}},
		{"bare fenced clean", "```\n[]\n```", "clean", 0, []string{"review", "--diff", "-"}},
		{"fence outer whitespace", " \t\r\n```json\r\n[]\r\n```\r\n ", "clean", 0, []string{"review", "--diff", "-"}},
		{"prose before", "Review: []", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"prose after", "[] Done.", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"prose clean", "No findings: []", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"two arrays", "[]\n[]", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"two fences", "```json\n[]\n```\n```\n[]\n```", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"fence inner prose", "```json\nNo findings: []\n```", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"fence outer prose", "```json\n[]\n```\nDone.", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"findings", `[{"id":"synthetic-1","severity":"warning","category":"logic","file":"example.go","line":1,"title":"Zero divisor","evidence":"The synthetic change divides by zero.","confidence":1}]`, "findings", 1, []string{"review", "--diff", "-"}},
		{"incomplete", "malformed", "incomplete", 2, []string{"review", "--diff", "-"}},
		{"command error", "", "", 3, []string{"review"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": "synthetic-session", "result": tc.output})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(envelope, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, tc.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TOUCHSTONE_TEST_ENVELOPE="+envelope)
			cmd.Stdin = strings.NewReader("--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-return 0\n+return 1/0\n")
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			_ = cmd.Run()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.code {
				t.Fatalf("wrong process exit: %v, stderr %q", cmd.ProcessState, stderr.String())
			}
			if tc.code == 3 {
				if out.Len() != 0 || stderr.Len() == 0 {
					t.Fatal("command error must be diagnostic only")
				}
				return
			}
			var report struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Outcome != tc.outcome || stderr.Len() != 0 {
				t.Fatalf("bad process report: %q, stderr %q, %v", out.String(), stderr.String(), err)
			}
		})
	}
}
