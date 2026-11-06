// Package cli implements Touchstone commands and their exit-status contract.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/review"
)

const usage = `Touchstone (early development)
Usage:
  touchstone review --diff FILE|- [--cwd DIRECTORY] [--concurrency N]
  touchstone version
  touchstone config validate [--cwd DIRECTORY]
`

func Run(args []string, out, stderr io.Writer, version string) error {
	return RunContext(context.Background(), args, os.Stdin, out, stderr, version, agent.Droid{})
}

// RunContext permits cancellation and deterministic agents without global state.
func RunContext(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer, version string, a agent.Agent) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		_, err := io.WriteString(out, usage)
		return err
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return errors.New("version takes no arguments")
		}
		_, err := fmt.Fprintf(out, "touchstone %s\n", version)
		return err
	case "review":
		return runReview(ctx, args[1:], in, out, stderr, a)
	case "config":
		if len(args) < 2 || args[1] != "validate" {
			return errors.New("expected: touchstone config validate [--cwd DIRECTORY]")
		}
		fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		cwd := fs.String("cwd", "", "repository directory (defaults to the current directory)")
		if err := fs.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("config validate takes no positional arguments")
		}
		if *cwd == "" {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			*cwd = dir
		}
		info, err := os.Stat(*cwd)
		if err != nil || !info.IsDir() {
			return errors.New("cwd must be an existing directory")
		}
		if _, err := config.Load(*cwd); err != nil {
			return fmt.Errorf("configuration invalid: %w", err)
		}
		_, err = fmt.Fprintln(out, "configuration valid (built-in defaults plus optional touchstone.toml)")
		return err
	default:
		return errors.New("unknown command; run touchstone --help")
	}
}

// ExitCode distinguishes clean (0), findings (1), incomplete (2), and command
// or setup errors (3). A result status is not a diagnostic to print on stderr.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var status reviewStatus
	if errors.As(err, &status) {
		return int(status)
	}
	return 3
}

type reviewStatus int

func (s reviewStatus) Error() string {
	if s == 1 {
		return "review has findings"
	}
	return "review incomplete"
}

func runReview(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer, a agent.Agent) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	diff := fs.String("diff", "", "diff file, or - for stdin (required)")
	cwd := fs.String("cwd", ".", "directory containing optional touchstone.toml")
	concurrency := fs.Int("concurrency", 2, "maximum parallel specialist layers")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *diff == "" || *concurrency < 1 {
		return errors.New("review requires --diff FILE|- and positive --concurrency, with no positional arguments")
	}
	dir, err := filepath.Abs(*cwd)
	if err != nil {
		return errors.New("cannot resolve cwd")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("cwd must be an existing directory")
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("configuration invalid: %w", err)
	}
	if *diff != "-" {
		file, err := os.Open(*diff)
		if err != nil {
			return errors.New("cannot open diff file")
		}
		defer func() { _ = file.Close() }()
		in = file
	}
	data, err := io.ReadAll(io.LimitReader(in, review.MaxDiffBytes+1))
	if err != nil {
		return errors.New("cannot read diff")
	}
	report, err := (review.Runner{Agent: a, Concurrency: *concurrency}).Run(ctx, data, cfg)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	switch report.Outcome {
	case "findings":
		return reviewStatus(1)
	case "incomplete":
		return reviewStatus(2)
	default:
		return nil
	}
}
