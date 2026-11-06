// Package input obtains read-only repository diffs through the git binary.
package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/agendacraft/touchstone/internal/patch"
	"github.com/agendacraft/touchstone/internal/process"
)

type Git struct{ Runner process.Runner }

func (g Git) command(ctx context.Context, dir string, limit int, args ...string) (process.Result, error) {
	runner := g.Runner
	if runner == nil {
		runner = process.Local{}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	argv := append([]string{"git", "-c", "core.quotePath=true", "-c", "diff.orderFile=" + os.DevNull}, args...)
	return runner.Run(ctx, process.Request{Args: argv, Dir: dir, Env: gitEnv(), MaxBytes: limit})
}

func gitEnv() []string {
	// Do not inherit GIT_DIR, external diff drivers, config injection, or
	// replacement-object settings from the caller.
	var env []string
	for _, name := range []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "TMPDIR", "TEMP"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "LC_ALL=C")
}

func (g Git) Root(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, 64<<10, "rev-parse", "--show-toplevel")
	if err != nil || result.Bytes > 64<<10 {
		return "", errors.New("git repository unavailable; use --cwd inside a repository or --diff FILE|-")
	}
	return strings.TrimSuffix(string(result.Output), "\n"), nil
}

func (g Git) resolve(ctx context.Context, dir, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("unknown git ref %q", ref)
	}
	result, err := g.command(ctx, dir, 1024, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil || result.Bytes > 1024 {
		return "", fmt.Errorf("unknown git ref %q (expected a commit)", ref)
	}
	return strings.TrimSpace(string(result.Output)), nil
}

func (g Git) defaultBranch(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, 4096, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err == nil {
		return strings.TrimSpace(string(result.Output)), nil
	}
	// Offline synthetic/local repositories often have no remote HEAD.
	for _, ref := range []string{"refs/heads/main", "refs/heads/master"} {
		if _, err := g.resolve(ctx, dir, ref); err == nil {
			return ref, nil
		}
	}
	return "", errors.New("cannot determine default branch; set origin/HEAD or pass --base REF")
}

type Diff struct {
	Data     []byte
	Bytes    int64
	TooLarge bool
}

func (g Git) Diff(ctx context.Context, dir, base, head string, limit int, ignore []string) (Diff, error) {
	headID, err := g.resolve(ctx, dir, head)
	if err != nil {
		return Diff{}, err
	}
	if base == "" {
		ref, err := g.defaultBranch(ctx, dir)
		if err != nil {
			return Diff{}, err
		}
		baseID, err := g.resolve(ctx, dir, ref)
		if err != nil {
			return Diff{}, err
		}
		result, err := g.command(ctx, dir, 1024, "merge-base", baseID, headID)
		if err != nil {
			return Diff{}, errors.New("cannot find merge-base with default branch; pass --base REF")
		}
		base = strings.TrimSpace(string(result.Output))
	}
	baseID, err := g.resolve(ctx, dir, base)
	if err != nil {
		return Diff{}, err
	}
	result, err := g.command(ctx, dir, limit, "diff", "--no-ext-diff", "--no-textconv", "--no-color",
		"--no-relative", "--src-prefix=a/", "--dst-prefix=b/", "--find-renames", "--submodule=short", "--ignore-submodules=none", baseID, headID, "--")
	if err != nil {
		return Diff{}, errors.New("git diff failed (git required; refs must share a valid repository)")
	}
	if result.Bytes > int64(limit) {
		return Diff{Bytes: result.Bytes, TooLarge: true}, nil
	}
	return Filter(result.Output, ignore)
}

func Filter(data []byte, ignore []string) (Diff, error) {
	if patch.IsMailbox(data) {
		return Diff{Data: data, Bytes: int64(len(data))}, nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Diff{}, nil
	}
	files, err := patch.Split(data)
	if err != nil {
		return Diff{}, err
	}
	var filtered bytes.Buffer
	for _, file := range files {
		if file.Binary || ignored(file.Path, ignore) || ignored(file.OldPath, ignore) {
			continue
		}
		filtered.Write(file.Data)
	}
	return Diff{Data: filtered.Bytes(), Bytes: int64(filtered.Len())}, nil
}

func ignored(file string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, file); matched {
			return true
		}
	}
	return false
}
