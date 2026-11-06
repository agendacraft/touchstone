//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSIGTERMCleansUpReview(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	scratch := filepath.Join(dir, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho $$ > \"$TOUCHSTONE_TEST_DIR/$$\"\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "droid"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "review", "--diff", "-")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+scratch, "TOUCHSTONE_TEST_DIR="+dir)
	cmd.Stdin = strings.NewReader("--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-old\n+new\n")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()
	var pids []int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		pids = nil
		for _, entry := range entries {
			if pid, err := strconv.Atoi(entry.Name()); err == nil {
				pids = append(pids, pid)
			}
		}
		if len(pids) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		for _, pid := range pids {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
	if len(pids) != 2 {
		t.Fatalf("expected two active Droid processes, got %v", pids)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not exit after SIGTERM")
	}
	if cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("expected incomplete exit, got %v; stderr %q", cmd.ProcessState, stderr.String())
	}
	var report struct {
		Outcome string `json:"outcome"`
		Layers  []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Outcome != "incomplete" || len(report.Layers) != 2 {
		t.Fatalf("invalid cancellation report: %q, %v", out.String(), err)
	}
	for _, layer := range report.Layers {
		if layer.Status != "incomplete" || layer.Error != "canceled" {
			t.Fatalf("layer was not canceled: %+v", layer)
		}
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Errorf("Droid process %d still exists: %v", pid, err)
		}
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("scratch not removed: %v, %v", entries, err)
	}
}
