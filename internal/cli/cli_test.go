package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
		fail bool
	}{
		{nil, "early development", false},
		{[]string{"--help"}, "touchstone config validate", false},
		{[]string{"version"}, "touchstone test-version\n", false},
		{[]string{"review", "--cwd", dir}, "", true},
		{[]string{"config", "validate", "--cwd", dir}, "configuration valid", false},
		{[]string{"config", "validate", "--help"}, "", false},
		{[]string{"config", "validate", "--cwd", filepath.Join(dir, "missing")}, "", true},
		{[]string{"version", "extra"}, "", true},
		{[]string{"review", "extra"}, "", true},
		{[]string{"config"}, "", true},
		{[]string{"config", "validate", "extra"}, "", true},
		{[]string{"config", "validate", "--unknown"}, "", true},
		{[]string{"unknown"}, "", true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out, stderr bytes.Buffer
			err := Run(tc.args, &out, &stderr, "test-version")
			if (err != nil) != tc.fail || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("out %q, err %v", out.String(), err)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "touchstone.toml"), []byte("typo=true"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := Run([]string{"config", "validate", "--cwd", dir}, &out, &stderr, "dev"); err == nil || out.Len() != 0 {
		t.Fatal("invalid configuration reported success")
	}
}

func TestConfigValidateDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	args := []string{"config", "validate", "--cwd", dir}
	var out, stderr bytes.Buffer
	if err := Run(args, &out, &stderr, "dev"); err != nil || !strings.Contains(out.String(), "configuration valid") {
		t.Fatalf("absent config should use defaults: %q, %v", out.String(), err)
	}
	if err := os.Symlink("missing.toml", filepath.Join(dir, "touchstone.toml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out.Reset()
	if err := Run(args, &out, &stderr, "dev"); err == nil || out.Len() != 0 {
		t.Fatalf("dangling config reported success: %q, %v", out.String(), err)
	}
}
