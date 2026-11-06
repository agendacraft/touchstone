package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/review"
)

type Options struct {
	Cases      fs.FS
	Recordings fs.FS
	RecordDir  string
	Repeats    int
	Replay     bool
	Progress   io.Writer
}

type Result struct {
	Case           string        `json:"case"`
	Repeat         int           `json:"repeat"`
	Holdout        bool          `json:"holdout"`
	Fingerprint    string        `json:"case_sha256"`
	Scores         Scores        `json:"scores"`
	LatencyMS      *int64        `json:"latency_ms"`
	Cost           *agent.Cost   `json:"cost"`
	FactoryCredits *int64        `json:"factory_credits"`
	Report         review.Report `json:"report"`
}

type Suite struct {
	Version    int               `json:"version"`
	Mode       string            `json:"mode"`
	Complete   bool              `json:"complete"`
	Repeats    int               `json:"repeats"`
	Cases      []Result          `json:"cases"`
	Scores     Scores            `json:"scores"`
	Partitions map[string]Scores `json:"partitions"`
	Spread     map[string]Spread `json:"spread"`
}

func Run(ctx context.Context, o Options, a agent.Agent) (Suite, error) {
	suite := Suite{Version: 1, Mode: "live", Complete: true, Repeats: o.Repeats, Cases: []Result{}}
	if o.Repeats < 1 || o.Repeats > 20 || (!o.Replay && a == nil) || (o.Replay && o.RecordDir != "") {
		return suite, errors.New("invalid eval repetitions, agent, or recording options")
	}
	cases, err := Load(o.Cases)
	if err != nil {
		return suite, err
	}
	if o.Replay {
		suite.Mode = "recorded"
		if o.Recordings == nil {
			o.Recordings = o.Cases
		}
	}
	if o.RecordDir != "" {
		if err := os.Mkdir(o.RecordDir, 0o700); err != nil {
			return suite, fmt.Errorf("record directory must be new: %w", err)
		}
	}
	for repeat := 1; repeat <= o.Repeats; repeat++ {
		for _, c := range cases {
			if err := ctx.Err(); err != nil {
				return suite, err
			}
			result, err := runCase(ctx, c, repeat, o, a)
			if err != nil {
				return suite, fmt.Errorf("case %s repeat %d: %w", c.Name, repeat, err)
			}
			suite.Cases = append(suite.Cases, result)
			if result.Report.Outcome == "incomplete" {
				suite.Complete = false
			}
			if o.Progress != nil {
				if _, err := fmt.Fprintf(o.Progress, "eval %s repeat %d: %s\n", c.Name, repeat, result.Report.Outcome); err != nil {
					return suite, err
				}
			}
		}
	}
	suite.summarize()
	return suite, nil
}

func (s *Suite) summarize() {
	s.Scores = Aggregate(s.Cases)
	s.Partitions = map[string]Scores{}
	for _, partition := range []string{"development", "holdout"} {
		var selected []Result
		for _, result := range s.Cases {
			if result.Holdout == (partition == "holdout") {
				selected = append(selected, result)
			}
		}
		s.Partitions[partition] = Aggregate(selected)
	}
	s.Spread = spreads(*s)
}

func runCase(ctx context.Context, c Case, repeat int, o Options, a agent.Agent) (Result, error) {
	file := fmt.Sprintf("%s/recording-%d.json", c.Name, repeat)
	s, err := newSession(c, o.Recordings, file, a, o.Replay)
	if err != nil {
		return Result{}, err
	}
	start := time.Now()
	report, err := (review.Runner{Agent: s}).Run(ctx, c.Diff, s.config())
	if err != nil {
		return Result{}, err
	}
	elapsed := time.Since(start).Milliseconds()
	if err := s.finish(); err != nil {
		return Result{}, err
	}
	report = review.Triage(report, config.Defaults().Output())
	result := Result{Case: c.Name, Repeat: repeat, Holdout: c.Truth.Holdout,
		Fingerprint: c.Fingerprint, Scores: Score(c.Truth, report), Report: report,
		Cost: reviewCost(report), FactoryCredits: reviewCredits(report)}
	if !o.Replay {
		result.LatencyMS = &elapsed
	}
	if o.RecordDir != "" {
		dir := filepath.Join(o.RecordDir, c.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return result, err
		}
		if err := writeJSON(filepath.Join(o.RecordDir, filepath.FromSlash(file)), s.recording); err != nil {
			return result, err
		}
	}
	return result, nil
}

func reviewCost(report review.Report) *agent.Cost {
	total := &agent.Cost{}
	count := 0
	for _, layer := range append(append([]review.LayerResult{}, report.Layers...), report.Verifier) {
		for _, attempt := range layer.Attempts {
			if attempt.Cost == nil || (count > 0 && attempt.Cost.Currency != total.Currency) {
				return nil
			}
			count++
			total.Currency = attempt.Cost.Currency
			total.Amount += attempt.Cost.Amount
		}
	}
	if count == 0 {
		return nil
	}
	return total
}

func reviewCredits(report review.Report) *int64 {
	var total int64
	count := 0
	for _, layer := range append(append([]review.LayerResult{}, report.Layers...), report.Verifier) {
		for _, attempt := range layer.Attempts {
			if attempt.Usage.FactoryCredits == nil {
				return nil
			}
			total += *attempt.Usage.FactoryCredits
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return &total
}
