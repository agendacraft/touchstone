package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agendacraft/touchstone/internal/testutil"
)

func TestSyntheticGroundTruth(t *testing.T) {
	// Execute the API contracts, not a text search for the seeded defect.
	// Every original must pass. The seven buggy changes must fail their
	// specific test, while every clean change must still pass.
	oracles := map[string]string{
		"01-index-boundary":        `defer func() { if recover() != nil { t.Fatal("index at length panics") } }(); if _, ok := At([]int{1}, 1); ok { t.Fatal("invalid index accepted") }`,
		"02-map-zero-value":        `if _, ok := Lookup(map[string]int{"valid": 0}, "missing"); ok { t.Fatal("missing token authenticated") }; if id, ok := Lookup(map[string]int{"valid": 0}, "valid"); !ok || id != 0 { t.Fatal("valid zero ID rejected") }`,
		"03-slice-alias":           `xs := []int{1, 2}; out := WithMarker(xs[:1]); if xs[1] != 2 || len(out) != 2 || out[1] != 7 { t.Fatal("backing array mutated") }`,
		"04-expiry-boundary":       `now := time.Unix(100, 0); if Valid(now, now) { t.Fatal("expired token accepted") }; if !Valid(now, now.Add(time.Second)) { t.Fatal("unexpired token rejected") }`,
		"05-path-prefix":           `if Inside("/safe", "/safe-other/x") { t.Fatal("sibling path accepted") }; if !Inside("/safe", "/safe/x") { t.Fatal("child rejected") }`,
		"06-owner-check":           `if CanEdit(true, "other", "owner") || CanEdit(false, "owner", "owner") { t.Fatal("unauthorized edit") }; if !CanEdit(true, "owner", "owner") { t.Fatal("owner rejected") }`,
		"07-comparator-overflow":   `max := int(^uint(0) >> 1); if Less(max, -1) || !Less(-1, max) { t.Fatal("extreme ints misordered") }`,
		"08-clean-empty-guard":     `if Mean(nil) != 0 || Mean([]int{1, 3}) != 2 { t.Fatal("mean contract violated") }`,
		"09-clean-defensive-copy":  `xs := []int{1, 2}; out := WithMarker(xs[:1]); out[0] = 99; if xs[0] != 1 || xs[1] != 2 || out[1] != 7 { t.Fatal("copy contract violated") }`,
		"10-clean-prefix-boundary": `if Inside("/safe", "/safe-other/x") || !Inside("/safe", "/safe/x") || !Inside("/safe", "/safe") { t.Fatal("path boundary changed") }`,
		"11-clean-constant-time":   `if !EqualMAC([]byte{1, 2}, []byte{1, 2}) || EqualMAC([]byte{1}, []byte{1, 2}) || EqualMAC([]byte{1}, []byte{2}) { t.Fatal("MAC equality changed") }`,
		"12-clean-preexisting":     `if Average([]int{1, 2}) != 1 || Average([]int{0}) != 0 || Average([]int{1000, 1000}) != 1000 { t.Fatal("documented truncation changed") }`,
	}
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "go.mod", "module synthetic.invalid/eval\n\ngo 1.25.0\n")
	cases, err := Load(Bundled())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		body, ok := oracles[c.Name]
		if !ok {
			continue
		}
		// These fixtures intentionally contain full-file hunks. Materialize
		// the original side; Git, not this helper, applies the change.
		var original strings.Builder
		inHunk := false
		for _, line := range strings.Split(string(c.Diff), "\n") {
			if strings.HasPrefix(line, "@@ ") {
				inHunk = true
				continue
			}
			if inHunk && len(line) > 0 && (line[0] == ' ' || line[0] == '-') {
				original.WriteString(line[1:] + "\n")
			}
		}
		testutil.Write(t, dir, c.Name+"/sample.go", original.String())
		imports := "import \"testing\"\n"
		if c.Name == "04-expiry-boundary" {
			imports = "import (\"testing\"; \"time\")\n"
		}
		testutil.Write(t, dir, c.Name+"/sample_test.go", "package sample\n"+imports+"func TestGroundTruth(t *testing.T) { "+body+" }\n")
		testutil.Write(t, dir, c.Name+"/diff.patch", string(c.Diff))
	}
	runCorpus(t, dir, false, oracles)
	for name := range oracles {
		testutil.Git(t, dir, "apply", "--directory="+name, "--", name+"/diff.patch")
	}
	runCorpus(t, dir, true, oracles)
}

func runCorpus(t *testing.T, dir string, changed bool, oracles map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if !changed && err != nil {
		t.Fatalf("original corpus failed: %v\n%s", err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	seen := map[string]bool{}
	for {
		var event struct {
			Action  string
			Package string
			Test    string
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("go test output: %v\n%s", err, output)
		}
		if event.Test != "TestGroundTruth" || (event.Action != "pass" && event.Action != "fail") {
			continue
		}
		name := strings.TrimPrefix(event.Package, "synthetic.invalid/eval/")
		want := "pass"
		if changed && name < "08-" {
			want = "fail"
		}
		if event.Action != want {
			t.Errorf("%s changed=%v: got %s, want %s", name, changed, event.Action, want)
		}
		seen[name] = true
	}
	if len(seen) != len(oracles) {
		t.Fatalf("did not execute every ground truth test: %v\n%s", seen, output)
	}
}
