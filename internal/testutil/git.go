// Package testutil supplies synthetic repositories without relying on the
// developer's Git identity or changing their configuration.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitInput(t, dir, "", args...)
}

func gitInput(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func Repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary required")
	}
	dir := t.TempDir()
	Git(t, dir, "init", "--initial-branch=main")
	return dir
}

func Write(t *testing.T, dir, name, content string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func Commit(t *testing.T, dir, parent string) string {
	t.Helper()
	Git(t, dir, "add", "--all")
	tree := Git(t, dir, "write-tree")
	body := "tree " + tree + "\n"
	if parent != "" {
		body += "parent " + parent + "\n"
	}
	body += "author Synthetic Fixture <fixture@example.invalid> 1700000000 +0000\ncommitter Synthetic Fixture <fixture@example.invalid> 1700000000 +0000\n\nSynthetic change\n"
	id := gitInput(t, dir, body, "hash-object", "-t", "commit", "-w", "--stdin")
	Git(t, dir, "update-ref", "HEAD", id)
	return id
}

func Remove(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(fmt.Errorf("remove fixture: %w", err))
	}
}
