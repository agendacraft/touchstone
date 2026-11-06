package demo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyntheticRegression(t *testing.T) {
	// The oracle is the fixture's stated requirements: empty averages return
	// zero and access requires both authentication and ownership. Execute the
	// old and patched code, rather than treating model agreement as evidence.
	for _, patched := range []bool{false, true} {
		dir := t.TempDir()
		if err := Prepare(context.Background(), dir, false); err != nil {
			t.Fatal(err)
		}
		if !patched {
			data, err := fixtures.ReadFile("fixtures/ledger.go")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ledger.go"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		tests := `package ledger
import "testing"
func TestRequirements(t *testing.T) {
	defer func() { if recover() != nil { t.Error("empty average panicked") } }()
	for _, c := range []struct { signedIn bool; user, owner string; want bool } {
		{true, "owner", "owner", true},
		{true, "visitor", "owner", false},
		{false, "owner", "owner", false},
		{false, "visitor", "owner", false},
	} {
		if got := CanRead(c.signedIn, c.user, c.owner); got != c.want {
			t.Errorf("CanRead(%t, %q, %q) = %t", c.signedIn, c.user, c.owner, got)
		}
	}
	if Average([]int{1, 2}) != 1 { t.Error("truncation contract changed") }
	if Average(nil) != 0 { t.Error("empty average was not zero") }
}
`
		for name, data := range map[string]string{
			"go.mod":         "module synthetic.invalid/ledger\n\ngo 1.25.0\n",
			"ledger_test.go": tests,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		cmd := exec.CommandContext(ctx, "go", "test", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=")
		output, err := cmd.CombinedOutput()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatal("synthetic regression test timed out")
		}
		if (err != nil) != patched {
			t.Fatalf("patched=%t: requirements result = %v\n%s", patched, err, output)
		}
		if patched {
			for _, failure := range []string{
				`CanRead(true, "visitor", "owner") = true`,
				`CanRead(false, "owner", "owner") = true`,
				"empty average panicked",
			} {
				if !strings.Contains(string(output), failure) {
					t.Fatalf("planted regression not reproduced: %s\n%s", failure, output)
				}
			}
		}
	}
}
