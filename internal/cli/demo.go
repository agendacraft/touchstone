package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/demo"
)

func runDemo(ctx context.Context, args []string, out, stderr io.Writer, a agent.Agent) error {
	replay, err := parseDemoReplay(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	mode := "LIVE: calls Droid with the default models; authentication required and charges may apply."
	if replay {
		a = demo.Replay()
		mode = "RECORDED REPLAY: bundled agent responses, not live model judgment; no credentials needed."
	} else if err := demoAgentAvailable(a); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "touchstone-demo-")
	if err != nil {
		return errors.New("cannot create demo repository")
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := demo.Prepare(ctx, dir, replay); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Touchstone demo\n%s\nSynthetic repository: ledger.go (empty-average panic and authorization bypass).\nLocal check: Git whitespace validation, not a correctness test.\n\n", mode); err != nil {
		return err
	}
	var report bytes.Buffer
	result := runReview(ctx, []string{
		"--cwd", dir, "--diff", demo.DiffPath(dir), "--format", "terminal", "--run-checks",
	}, nil, &report, stderr, a)
	if _, err := report.WriteTo(out); err != nil {
		return err
	}
	if ExitCode(result) == 1 {
		return nil // Planted findings are the successful demo outcome.
	}
	return result
}

func parseDemoReplay(args []string, stderr io.Writer) (bool, error) {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	replay := fs.Bool("replay", false, "use bundled recorded agent responses; no credentials or model calls")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if fs.NArg() != 0 {
		return false, errors.New("demo takes no positional arguments")
	}
	return *replay, nil
}

func demoAgentAvailable(a agent.Agent) error {
	if a == nil {
		return errors.New("demo requires a model agent; use touchstone demo --replay without credentials")
	}
	if d, ok := a.(agent.Droid); ok {
		binary := d.Binary
		if binary == "" {
			binary = "droid"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return errors.New("demo requires the Droid CLI on PATH; install and authenticate it, or use touchstone demo --replay")
		}
	}
	return nil
}
