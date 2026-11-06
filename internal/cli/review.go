package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/input"
	"github.com/agendacraft/touchstone/internal/render"
	"github.com/agendacraft/touchstone/internal/review"
)

type reviewOptions struct {
	diff, base, head, cwd, format, minSeverity string
	concurrency, maxDiff                       int
	noVerify, runChecks                        bool
	minConfidence                              float64
	ignore                                     stringList
	seen                                       map[string]bool
}

func parseReviewOptions(args []string, stderr io.Writer) (reviewOptions, error) {
	var o reviewOptions
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.diff, "diff", "", "diff file, or - for stdin (exclusive with --base/--head)")
	fs.StringVar(&o.base, "base", "", "base commit (default: merge-base with default branch)")
	fs.StringVar(&o.head, "head", "HEAD", "head commit for git input")
	fs.StringVar(&o.cwd, "cwd", ".", "repository directory (or config directory for --diff outside Git)")
	fs.StringVar(&o.format, "format", "", "terminal, json, or markdown (default: terminal on a TTY, json when piped)")
	fs.StringVar(&o.minSeverity, "min-severity", "", "minimum severity: info, warning, or error (overrides config)")
	fs.Float64Var(&o.minConfidence, "min-confidence", 0, "minimum confidence from 0 to 1 (overrides config)")
	fs.IntVar(&o.maxDiff, "max-diff-bytes", 0, "maximum diff bytes (default: config or 16777216; maximum 268435456)")
	fs.IntVar(&o.concurrency, "concurrency", 2, "maximum parallel specialist layers")
	fs.BoolVar(&o.runChecks, "run-checks", false, "trust and execute explicitly configured local check commands")
	fs.BoolVar(&o.noVerify, "no-verify", false, "disable independent verification (findings remain unverified)")
	fs.Var(&o.ignore, "ignore", "exclude repository-relative path glob (repeatable; additive to config)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.seen = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.seen[f.Name] = true })
	if o.seen["diff"] && (o.seen["base"] || o.seen["head"]) {
		return o, errors.New("--diff and git inputs (--base/--head) are mutually exclusive")
	}
	if (o.seen["diff"] && o.diff == "") || (o.seen["base"] && o.base == "") || o.head == "" {
		return o, errors.New("input flags must not be empty")
	}
	if fs.NArg() != 0 || o.concurrency < 1 {
		return o, errors.New("review requires positive --concurrency and no positional arguments")
	}
	return o, nil
}

func runReview(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer, a agent.Agent) error {
	options, err := parseReviewOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	renderer, err := render.Select(options.format, out)
	if err != nil {
		return err
	}
	dir, inGit, err := options.directory(ctx)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(ctx, dir)
	if err != nil {
		return fmt.Errorf("configuration invalid: %w", err)
	}
	thresholds, policy, checksEnabled, err := options.policy(cfg, inGit)
	if err != nil {
		return err
	}
	selected, err := options.input(ctx, dir, in, policy)
	if err != nil {
		return err
	}
	report, err := options.review(ctx, a, dir, selected, cfg, policy.MaxDiffBytes, checksEnabled)
	if err != nil {
		return err
	}
	report = review.Triage(report, thresholds)
	if err := renderer.Render(out, report); err != nil {
		return err
	}
	return outcomeStatus(report.Outcome)
}

func (o reviewOptions) directory(ctx context.Context) (string, bool, error) {
	dir, err := filepath.Abs(o.cwd)
	if err != nil {
		return "", false, errors.New("cannot resolve cwd")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false, errors.New("cwd must be an existing directory")
	}
	root, err := (input.Git{}).Root(ctx, dir)
	if err == nil {
		return root, true, nil
	}
	if !o.seen["diff"] {
		return "", false, err
	}
	return dir, false, nil
}

func (o reviewOptions) policy(cfg config.Config, inGit bool) (config.Thresholds, config.Input, bool, error) {
	thresholds, policy, checks := cfg.Output(), cfg.Input(), cfg.Checks()
	if o.seen["min-severity"] {
		thresholds.MinSeverity = o.minSeverity
	}
	if o.seen["min-confidence"] {
		thresholds.MinConfidence = o.minConfidence
	}
	if o.seen["max-diff-bytes"] {
		policy.MaxDiffBytes = o.maxDiff
	}
	policy.Ignore = append(policy.Ignore, o.ignore...)
	if o.seen["run-checks"] {
		checks.Enabled = o.runChecks
	}
	if err := thresholds.Validate(); err != nil {
		return thresholds, policy, false, err
	}
	if err := policy.Validate(); err != nil {
		return thresholds, policy, false, err
	}
	if checks.Enabled && len(checks.Commands) > 0 && !inGit {
		return thresholds, policy, false, errors.New("local checks require a git repository root; select it with --cwd")
	}
	return thresholds, policy, checks.Enabled, nil
}

func (o reviewOptions) input(ctx context.Context, dir string, in io.Reader, policy config.Input) (input.Diff, error) {
	if o.seen["diff"] {
		return input.Read(ctx, o.diff, in, policy.MaxDiffBytes, policy.Ignore)
	}
	return (input.Git{}).Diff(ctx, dir, o.base, o.head, policy.MaxDiffBytes, policy.Ignore)
}

func (o reviewOptions) review(ctx context.Context, a agent.Agent, dir string, selected input.Diff, cfg config.Config, limit int, checks bool) (review.Report, error) {
	if selected.TooLarge {
		return review.IncompleteDiff(selected.Bytes), nil
	}
	runner := review.Runner{Agent: a, Concurrency: o.concurrency, DisableVerifier: o.noVerify, MaxDiffBytes: limit}
	report, err := runner.Run(ctx, selected.Data, cfg)
	if err != nil {
		return report, err
	}
	if len(selected.Data) > 0 {
		(review.ChecksRunner{}).Run(ctx, dir, cfg.Checks(), checks, &report)
	}
	return report, nil
}

func outcomeStatus(outcome string) error {
	switch outcome {
	case "findings":
		return reviewStatus(1)
	case "incomplete":
		return reviewStatus(2)
	default:
		return nil
	}
}
