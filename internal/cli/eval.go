package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/eval"
)

type evalOptions struct {
	cases, recordings, record, baseline, writeBaseline string
	repeats                                            int
	replay                                             bool
	margin                                             float64
}

func parseEvalOptions(args []string, stderr io.Writer) (evalOptions, error) {
	var o evalOptions
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&o.replay, "replay", false, "replay recorded responses without credentials (plainly labelled recorded)")
	flags.StringVar(&o.cases, "cases", "", "external case directory; default: bundled public cases")
	flags.StringVar(&o.recordings, "recordings", "", "external recording directory; default: alongside cases")
	flags.StringVar(&o.record, "record", "", "write live responses to a new external directory")
	flags.IntVar(&o.repeats, "repeats", 3, "independent runs per case, 1..20")
	flags.StringVar(&o.baseline, "baseline", "", "compare against baseline JSON; regression exits 1")
	flags.StringVar(&o.writeBaseline, "write-baseline", "", "write replay baseline including exact completion states")
	flags.Float64Var(&o.margin, "margin", -1, "absolute regression margin in 0..1 (default: baseline margin, or zero when writing)")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 || o.repeats < 1 || o.repeats > 20 ||
		math.IsNaN(o.margin) || math.IsInf(o.margin, 0) || (o.margin != -1 && (o.margin < 0 || o.margin > 1)) ||
		(o.replay && o.record != "") || (!o.replay && o.recordings != "") ||
		(o.writeBaseline != "" && (!o.replay || o.baseline != "")) {
		return o, errors.New("invalid eval arguments; run touchstone eval --help")
	}
	return o, nil
}

func runEval(ctx context.Context, args []string, out, stderr io.Writer, a agent.Agent) error {
	o, err := parseEvalOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	mode := "LIVE EVAL: fresh agent sessions; authentication required and charges may apply."
	if o.replay {
		mode = "RECORDED EVAL: captured agent responses, not live model judgment; no credentials needed."
	}
	if _, err := fmt.Fprintln(stderr, mode); err != nil {
		return err
	}
	source := eval.Bundled()
	if o.cases != "" {
		source = os.DirFS(o.cases)
	}
	var recorded fs.FS
	if o.recordings != "" {
		recorded = os.DirFS(o.recordings)
	}
	var baseline eval.Baseline
	if o.baseline != "" {
		baseline, err = eval.LoadBaseline(o.baseline)
		if err != nil {
			return fmt.Errorf("baseline: %w", err)
		}
	}
	suite, err := eval.Run(ctx, eval.Options{
		Cases: source, Recordings: recorded, RecordDir: o.record,
		Repeats: o.repeats, Replay: o.replay, Progress: stderr,
	}, a)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(suite); err != nil {
		return err
	}
	return finishEval(suite, baseline, o, stderr)
}

func finishEval(suite eval.Suite, baseline eval.Baseline, o evalOptions, stderr io.Writer) error {
	if o.writeBaseline != "" {
		m := o.margin
		if m == -1 {
			m = 0
		}
		if err := eval.SaveBaseline(o.writeBaseline, suite, m); err != nil {
			return err
		}
	}
	if o.baseline != "" {
		if err := eval.Compare(suite, baseline, o.margin); err != nil {
			if _, writeErr := fmt.Fprintf(stderr, "eval regression: %s\n", err); writeErr != nil {
				return writeErr
			}
			return reviewStatus(1)
		}
		if _, err := fmt.Fprintln(stderr, "eval baseline: matched (completion states checked; exit 2 still means incomplete)"); err != nil {
			return err
		}
	}
	if !suite.Complete {
		return reviewStatus(2)
	}
	return nil
}
