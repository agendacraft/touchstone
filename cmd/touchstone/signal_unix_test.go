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
		case _ = <-done:
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
	case _ = <-done:
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

func TestSignalUnblocksCLIIO(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	script := "#!/bin/sh\necho started > \"$TOUCHSTONE_TEST_MARKER\"\nprintf '%s' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"session_id\":\"test\",\"result\":\"[]\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "droid"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		for _, mode := range []string{"stdin", "fifo-open", "fifo-read", "stdout", "stderr", "review-output"} {
			t.Run(signal.String()+"/"+mode, func(t *testing.T) {
				fifo := filepath.Join(t.TempDir(), "diff")
				args := []string{"review", "--diff", "-"}
				if strings.HasPrefix(mode, "fifo-") {
					if err := syscall.Mkfifo(fifo, 0o600); err != nil {
						t.Fatal(err)
					}
					args[2] = fifo
				}
				if mode == "stdout" {
					args = []string{"version"}
				}
				if mode == "stderr" {
					args = []string{"unknown"}
				}
				cmd := exec.Command(binary, args...)
				cmd.Dir = dir
				marker := filepath.Join(t.TempDir(), "started")
				scratch := t.TempDir()
				cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+scratch, "TOUCHSTONE_TEST_MARKER="+marker)
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = input.Close() }()
				if mode == "stdout" || mode == "stderr" || mode == "review-output" {
					read, write, err := os.Pipe()
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = read.Close() }()
					defer func() { _ = write.Close() }()
					writeFD := int(write.Fd())
					if err := syscall.SetNonblock(writeFD, true); err != nil {
						t.Fatal(err)
					}
					buf := make([]byte, 4096)
					for {
						_, err := syscall.Write(writeFD, buf)
						if err == syscall.EAGAIN {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					if err := syscall.SetNonblock(writeFD, false); err != nil {
						t.Fatal(err)
					}
					if mode == "stdout" || mode == "review-output" {
						cmd.Stdout = write
					} else {
						cmd.Stderr = write
					}
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait(); close(done) }()
				defer func() {
					_ = cmd.Process.Kill()
					select {
					case _ = <-done:
					case <-time.After(5 * time.Second):
					}
				}()
				if mode == "review-output" {
					if _, err := input.Write([]byte("--- a/a\n+++ b/a\n")); err != nil {
						t.Fatal(err)
					}
					if err := input.Close(); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(5 * time.Second)
					for {
						_, err := os.Stat(marker)
						entries, readErr := os.ReadDir(scratch)
						if err == nil && readErr == nil && len(entries) == 0 {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("review did not finish before output blocked")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				if mode == "fifo-read" {
					deadline := time.Now().Add(5 * time.Second)
					var fd int
					for {
						fd, err = syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
						if err == nil || time.Now().After(deadline) {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					if err != nil {
						t.Fatalf("open FIFO writer: %v", err)
					}
					defer func() { _ = syscall.Close(fd) }()
				}
				time.Sleep(200 * time.Millisecond)
				select {
				case err := <-done:
					t.Fatalf("CLI exited before signal: %v", err)
				default:
				}
				if err := cmd.Process.Signal(signal); err != nil {
					t.Fatal(err)
				}
				select {
				case _ = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("CLI remained blocked after signal")
				}
				if cmd.ProcessState.ExitCode() != 3 {
					t.Fatalf("expected command error exit, got %v", cmd.ProcessState)
				}
			})
		}
	}
}

func TestClosedOutputPipesExitWithError(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	script := "#!/bin/sh\nprintf '%s' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"session_id\":\"test\",\"result\":\"[]\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "droid"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		args   []string
		stderr bool
	}{
		{"review output", []string{"review", "--diff", "-"}, false},
		{"version output", []string{"version"}, false},
		{"flag help", []string{"review", "--help"}, true},
		{"flag error", []string{"review", "--bad"}, true},
		{"diagnostic", []string{"unknown"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := read.Close(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = write.Close() }()
			cmd := exec.Command(binary, tc.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd.Stdin = strings.NewReader("--- a/a\n+++ b/a\n")
			if tc.stderr {
				cmd.Stderr = write
			} else {
				cmd.Stdout = write
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case _ = <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				_ = <-done
				t.Fatal("CLI did not exit")
			}
			if cmd.ProcessState.ExitCode() != 3 {
				t.Fatalf("expected exit 3, got %v", cmd.ProcessState)
			}
		})
	}
}

func TestSignalUnblocksConfigFIFO(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "touchstone")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		for _, command := range []string{"review", "validate"} {
			for _, mode := range []string{"open", "read"} {
				t.Run(sig.String()+"/"+command+"/"+mode, func(t *testing.T) {
					repo := t.TempDir()
					fifo := filepath.Join(repo, "touchstone.toml")
					if err := syscall.Mkfifo(fifo, 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"review", "--diff", "-", "--cwd", repo}
					if command == "validate" {
						args = []string{"config", "validate", "--cwd", repo}
					}
					cmd := exec.Command(binary, args...)
					cmd.Dir = repo
					cmd.Stdin = strings.NewReader("synthetic diff")
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- cmd.Wait(); close(done) }()
					defer func() {
						_ = cmd.Process.Kill()
						select {
						case _ = <-done:
						case <-time.After(5 * time.Second):
						}
					}()
					if mode == "read" {
						deadline := time.Now().Add(5 * time.Second)
						var fd int
						var err error
						for {
							fd, err = syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
							if err == nil || time.Now().After(deadline) {
								break
							}
							time.Sleep(10 * time.Millisecond)
						}
						if err != nil {
							t.Fatalf("open config FIFO writer: %v", err)
						}
						defer func() { _ = syscall.Close(fd) }()
					}
					time.Sleep(200 * time.Millisecond)
					select {
					case err := <-done:
						t.Fatalf("CLI exited before signal: %v", err)
					default:
					}
					if err := cmd.Process.Signal(sig); err != nil {
						t.Fatal(err)
					}
					select {
					case _ = <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("CLI remained blocked loading configuration")
					}
					if cmd.ProcessState.ExitCode() != 3 {
						t.Fatalf("expected exit 3, got %v", cmd.ProcessState)
					}
				})
			}
		}
	}
}
