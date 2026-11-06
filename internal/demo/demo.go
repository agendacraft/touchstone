// Package demo bundles an isolated synthetic review input and recorded responses.
package demo

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/process"
)

//go:embed fixtures/*
var fixtures embed.FS

func DiffPath(dir string) string { return filepath.Join(dir, "buggy.patch") }

// Prepare creates a staged baseline and applies the bundled patch without a
// commit, identity configuration, network access, or changes to the caller's repo.
func Prepare(ctx context.Context, dir string, replay bool) error {
	for _, name := range []string{"ledger.go", "buggy.patch", "touchstone.toml"} {
		data, err := fixtures.ReadFile("fixtures/" + name)
		if err != nil {
			return err
		}
		if name == "touchstone.toml" && replay {
			for _, layer := range []string{"correctness", "security", "verification"} {
				data = fmt.Appendf(data, "\n[layers.%s]\nmodel = \"recorded-%s\"\n", layer, layer)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return errors.New("cannot write bundled demo files")
		}
	}
	for _, args := range [][]string{
		{"init", "--template=", "--initial-branch=demo"},
		{"add", "--", "ledger.go"},
		{"apply", "--", "buggy.patch"},
	} {
		if err := git(ctx, dir, args); err != nil {
			return err
		}
	}
	return nil
}

func git(parent context.Context, dir string, args []string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "LC_ALL=C"}
	for _, name := range []string{"PATH", "SystemRoot"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	_, err := (process.Local{}).Run(ctx, process.Request{
		Args: append([]string{"git"}, args...), Dir: dir, Env: env, MaxBytes: 64 << 10,
	})
	if err != nil {
		return errors.New("cannot prepare synthetic demo repository (Git required)")
	}
	return nil
}

// Replay replaces only the Agent boundary. Distinct synthetic model names
// route concurrent requests without depending on prompt text or call order.
func Replay() agent.Agent {
	return agent.Fake{Respond: func(ctx context.Context, r agent.Request) (agent.Result, error) {
		if err := ctx.Err(); err != nil {
			return agent.Result{}, err
		}
		var name string
		switch r.Model {
		case "recorded-correctness":
			name = "correctness"
		case "recorded-security":
			name = "security"
		case "recorded-verification":
			name = "verification"
		default:
			return agent.Result{}, errors.New("unknown demo recording")
		}
		data, err := fixtures.ReadFile("fixtures/" + name + ".txt")
		return agent.Result{Output: string(data)}, err
	}}
}
