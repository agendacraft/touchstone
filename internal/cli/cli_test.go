package cli

import (
	"bytes"
	"errors"
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
		{[]string{"review"}, "", true},
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
			if len(tc.args) == 1 && tc.args[0] == "review" && !errors.Is(err, ErrNotImplemented) {
				t.Fatal("review skeleton must fail explicitly")
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
