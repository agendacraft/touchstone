package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func CheckCommand(t *testing.T, mode string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, "-test.run=^TestCheckProcess$", "--", mode}
}

// CheckProcess is called only by TestCheckProcess in packages that exercise
// the real command boundary. Its output is synthetic public test data.
func CheckProcess() {
	if os.Getenv("TOUCHSTONE_CHECK_HELPER") != "1" {
		return
	}
	_, mode, _ := strings.Cut(strings.Join(os.Args, "\x00"), "\x00--\x00")
	switch mode {
	case "pass":
		dir, _ := os.Getwd()
		fmt.Printf("cwd=%s secret=%s\n", dir, os.Getenv("TOUCHSTONE_CHECK_SECRET"))
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "src/example.go:7: synthetic failure")
		os.Exit(4)
	case "timeout":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "output":
		fmt.Print(strings.Repeat("x", 8192))
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "literal":
		fmt.Print(strings.Join(os.Args, "\n"))
		os.Exit(1)
	}
	os.Exit(5)
}
