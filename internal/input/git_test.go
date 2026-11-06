package input

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agendacraft/touchstone/internal/patch"
	"github.com/agendacraft/touchstone/internal/process"
	"github.com/agendacraft/touchstone/internal/testutil"
)

type gitCommands struct {
	calls   []process.Request
	missing bool
}

func (g *gitCommands) Run(_ context.Context, request process.Request) (process.Result, error) {
	g.calls = append(g.calls, request)
	if g.missing {
		return process.Result{}, errors.New("missing executable")
	}
	if strings.Contains(strings.Join(request.Args, "\n"), "\ndiff\n") {
		data := []byte("--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-old\n+new\n")
		return process.Result{Output: data, Bytes: int64(len(data)), Started: true}, nil
	}
	return process.Result{Output: []byte("0123456789abcdef\n"), Bytes: 17, Started: true}, nil
}

func TestGitInjectableArgumentVectors(t *testing.T) {
	runner := &gitCommands{}
	const ref = "literal;not-a-shell"
	_, err := (Git{Runner: runner}).Diff(context.Background(), "/synthetic/root", ref, "HEAD", 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range runner.calls {
		if call.Dir != "/synthetic/root" || call.Args[0] != "git" || call.Env == nil {
			t.Fatalf("wrong execution boundary: %+v", call)
		}
		for _, arg := range call.Args {
			if arg == ref+"^{commit}" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("literal ref was not passed as one argument")
	}
	missing := &gitCommands{missing: true}
	if _, err := (Git{Runner: missing}).Root(context.Background(), "/synthetic/root"); err == nil || !strings.Contains(err.Error(), "git repository unavailable") {
		t.Fatalf("missing git error unclear: %v", err)
	}
}

func TestGitRealInputs(t *testing.T) {
	dir := testutil.Repo(t)
	for name, data := range map[string]string{
		"src/old name.go": strings.Repeat("unchanged rename line\n", 10),
		"src/delete.go":   "deleted\n",
		"src/modify.go":   "old\n",
		"binary.dat":      "old\x00binary",
		"ignored.txt":     "old\n",
	} {
		testutil.Write(t, dir, name, data)
	}
	base := testutil.Commit(t, dir, "")
	testutil.Git(t, dir, "checkout", "-b", "feature")
	testutil.Git(t, dir, "mv", "src/old name.go", "src/new name.go")
	testutil.Remove(t, dir, "src/delete.go")
	testutil.Write(t, dir, "src/modify.go", "new\n")
	testutil.Write(t, dir, "src/add.go", "added\n")
	testutil.Write(t, dir, "binary.dat", "new\x00binary")
	testutil.Write(t, dir, "ignored.txt", "new\n")
	head := testutil.Commit(t, dir, base)
	git := Git{}
	ctx := context.Background()
	nested := filepath.Join(dir, "src")
	root, err := git.Root(ctx, nested)
	realDir, _ := filepath.EvalSymlinks(dir)
	if err != nil || root != realDir {
		t.Fatalf("wrong repository root: %q %v", root, err)
	}
	for _, refs := range []struct{ base, head string }{
		{base, head}, {"main", "HEAD"}, {"", "HEAD"},
	} {
		result, err := git.Diff(ctx, dir, refs.base, refs.head, 1<<20, []string{"ignored.*"})
		if err != nil || result.TooLarge {
			t.Fatalf("git input: %+v %v", result, err)
		}
		files, err := patch.Split(result.Data)
		if err != nil {
			t.Fatal(err)
		}
		byPath := map[string]patch.File{}
		for _, file := range files {
			byPath[file.Path] = file
		}
		if len(byPath) != 4 || byPath["src/new name.go"].OldPath != "src/old name.go" ||
			!strings.Contains(string(byPath["src/delete.go"].Data), "+++ /dev/null") ||
			!strings.Contains(string(byPath["src/add.go"].Data), "--- /dev/null") ||
			!strings.Contains(string(byPath["src/modify.go"].Data), "+new") {
			t.Fatalf("lost rename, deletion, addition, or binary/ignore exclusion: %s", result.Data)
		}
		empty, err := git.Diff(ctx, dir, head, head, 1<<20, nil)
		if err != nil || len(empty.Data) != 0 {
			t.Fatalf("empty diff: %+v %v", empty, err)
		}
		huge, err := git.Diff(ctx, dir, refs.base, refs.head, 32, nil)
		full, fullErr := git.Diff(ctx, dir, refs.base, refs.head, 1<<20, nil)
		// The count includes binary summaries and ignored files before filtering.
		raw, rawErr := git.command(ctx, dir, 1<<20, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", "--find-renames", "--submodule=short", base, head, "--")
		if err != nil || fullErr != nil || rawErr != nil || !huge.TooLarge || len(huge.Data) != 0 || huge.Bytes != int64(len(raw.Output)) || huge.Bytes <= int64(len(full.Data)) {
			t.Fatalf("oversize input not counted/capped: %+v %v", huge, err)
		}
	}
	ignoredRename, err := git.Diff(ctx, dir, base, head, 1<<20, []string{"src/old*"})
	if err != nil || strings.Contains(string(ignoredRename.Data), "rename to") {
		t.Fatal("old rename path did not match ignore")
	}
}

func TestGitMergeBaseNotDefaultTip(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "example.txt", "common\n")
	base := testutil.Commit(t, dir, "")
	testutil.Git(t, dir, "checkout", "-b", "feature")
	testutil.Write(t, dir, "feature.txt", "feature\n")
	testutil.Commit(t, dir, base)
	testutil.Git(t, dir, "checkout", "main")
	testutil.Write(t, dir, "main-only.txt", "main\n")
	testutil.Commit(t, dir, base)
	testutil.Git(t, dir, "checkout", "feature")
	diff, err := (Git{}).Diff(context.Background(), dir, "", "HEAD", 1<<20, nil)
	if err != nil || !strings.Contains(string(diff.Data), "feature.txt") || strings.Contains(string(diff.Data), "main-only.txt") {
		t.Fatalf("default input was not merge-base: %s %v", diff.Data, err)
	}
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/trunk", base)
	testutil.Git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	diff, err = (Git{}).Diff(context.Background(), dir, "", "HEAD", 1<<20, nil)
	if err != nil || !strings.Contains(string(diff.Data), "feature.txt") {
		t.Fatalf("remote default not used: %v", err)
	}
}

func TestGitErrorsAndNoExternalDrivers(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "example.txt", "old\n")
	base := testutil.Commit(t, dir, "")
	testutil.Write(t, dir, "example.txt", "new\n")
	head := testutil.Commit(t, dir, base)
	for _, ref := range []string{"missing-ref", "--help", "HEAD^{tree}"} {
		_, err := (Git{}).Diff(context.Background(), dir, ref, head, 1024, nil)
		if err == nil || !strings.Contains(err.Error(), "unknown git ref") {
			t.Fatalf("unclear unknown ref: %v", err)
		}
	}
	t.Setenv("GIT_EXTERNAL_DIFF", "missing-must-not-run")
	diff, err := (Git{}).Diff(context.Background(), dir, base, head, 1024, nil)
	if err != nil || !strings.Contains(string(diff.Data), "+new") {
		t.Fatal("external diff inherited")
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-a-repo"))
	if _, err := (Git{}).Root(context.Background(), dir); err != nil {
		t.Fatal("GIT_DIR escaped cwd")
	}
	if _, err := (Git{}).Root(context.Background(), t.TempDir()); err == nil {
		t.Fatal("non-repo accepted")
	}
	// Executing the configured driver would create this marker.
	marker := filepath.Join(t.TempDir(), "driver-ran")
	testutil.Git(t, dir, "config", "diff.synthetic.command", "touch "+marker)
	testutil.Write(t, dir, ".gitattributes", "*.txt diff=synthetic\n")
	if _, err := (Git{}).Diff(context.Background(), dir, base, head, 4096, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external driver executed")
	}
}

func TestReadFileStdinAndLimit(t *testing.T) {
	const diff = "--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-old\n+new\n"
	file := filepath.Join(t.TempDir(), "input.patch")
	testutil.Write(t, filepath.Dir(file), filepath.Base(file), diff)
	for _, name := range []string{"-", file} {
		result, err := Read(context.Background(), name, strings.NewReader(diff), len(diff), nil)
		if err != nil || string(result.Data) != diff || result.TooLarge {
			t.Fatalf("file/stdin boundary: %+v %v", result, err)
		}
		result, err = Read(context.Background(), name, strings.NewReader(diff), len(diff)-1, nil)
		if err != nil || !result.TooLarge || result.Bytes != int64(len(diff)) {
			t.Fatalf("wrong exact count: %+v %v", result, err)
		}
	}
}
