package eval

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"

	"github.com/agendacraft/touchstone/internal/review"
)

type BaselineCase struct {
	Fingerprint string     `json:"case_sha256"`
	Scores      Scores     `json:"scores"`
	Completion  Completion `json:"completion"`
}

type LayerState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

type Completion struct {
	Incomplete bool         `json:"incomplete"`
	Layers     []LayerState `json:"layers"`
	Verifier   LayerState   `json:"verifier"`
}

type Baseline struct {
	Version  int                     `json:"version"`
	Margin   float64                 `json:"margin"`
	Complete bool                    `json:"complete"`
	Cases    map[string]BaselineCase `json:"cases"`
}

func resultKey(c Result) string { return fmt.Sprintf("%s/%d", c.Case, c.Repeat) }

func completion(report review.Report) Completion {
	state := Completion{Incomplete: report.Outcome == "incomplete", Layers: []LayerState{},
		Verifier: LayerState{report.Verifier.Name, report.Verifier.Status, report.Verifier.Error}}
	for _, layer := range report.Layers {
		state.Layers = append(state.Layers, LayerState{layer.Name, layer.Status, layer.Error})
	}
	return state
}

func validMargin(m float64) bool { return !math.IsNaN(m) && !math.IsInf(m, 0) && m >= 0 && m <= 1 }

func SaveBaseline(file string, suite Suite, margin float64) error {
	if suite.Mode != "recorded" || len(suite.Cases) == 0 || !validMargin(margin) {
		return errors.New("baseline requires recorded replay and margin in 0..1")
	}
	b := Baseline{Version: 1, Margin: margin, Complete: suite.Complete, Cases: map[string]BaselineCase{}}
	for _, c := range suite.Cases {
		b.Cases[resultKey(c)] = BaselineCase{Fingerprint: c.Fingerprint, Scores: c.Scores, Completion: completion(c.Report)}
	}
	return writeJSON(file, b)
}

func LoadBaseline(file string) (Baseline, error) {
	var b Baseline
	data, err := os.ReadFile(file)
	if err != nil {
		return b, err
	}
	if err := decode(data, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || !validMargin(b.Margin) || len(b.Cases) == 0 {
		return b, errors.New("invalid baseline")
	}
	for _, c := range b.Cases {
		if c.Fingerprint == "" || len(c.Scores.Before) != len(scopes) || len(c.Scores.After) != len(scopes) {
			return b, errors.New("invalid baseline case")
		}
		for _, scope := range scopes {
			if _, ok := c.Scores.Before[scope]; !ok {
				return b, errors.New("missing baseline scope")
			}
			if _, ok := c.Scores.After[scope]; !ok {
				return b, errors.New("missing baseline scope")
			}
		}
	}
	return b, nil
}

func Compare(suite Suite, baseline Baseline, margin float64) error {
	if margin == -1 {
		margin = baseline.Margin
	}
	if !validMargin(margin) {
		return errors.New("margin must be in 0..1")
	}
	if suite.Complete != baseline.Complete || len(suite.Cases) != len(baseline.Cases) {
		return errors.New("completion or case/repetition set drift")
	}
	for _, c := range suite.Cases {
		b, ok := baseline.Cases[resultKey(c)]
		if !ok || b.Fingerprint != c.Fingerprint {
			return fmt.Errorf("%s: case or expectation drift", resultKey(c))
		}
		if !reflect.DeepEqual(completion(c.Report), b.Completion) {
			return fmt.Errorf("%s: layer completion drift", resultKey(c))
		}
		for phase, current := range map[string]map[string]Metrics{"before": c.Scores.Before, "after": c.Scores.After} {
			previous := b.Scores.Before
			if phase == "after" {
				previous = b.Scores.After
			}
			for _, scope := range scopes {
				if metricRegression(current[scope], previous[scope], margin) {
					return fmt.Errorf("%s %s/%s: score regression beyond margin %g", resultKey(c), phase, scope, margin)
				}
			}
		}
		if float64(c.Scores.Verifier.RejectedRealBugs-b.Scores.Verifier.RejectedRealBugs) > margin {
			return fmt.Errorf("%s: verifier rejects more real bug claims", resultKey(c))
		}
		for _, scope := range []string{"correctness", "security"} {
			if float64(c.Scores.VerifierByLayer[scope].RejectedRealBugs-b.Scores.VerifierByLayer[scope].RejectedRealBugs) > margin {
				return fmt.Errorf("%s %s: verifier rejects more real bug claims", resultKey(c), scope)
			}
		}
	}
	return nil
}

func metricRegression(current, previous Metrics, margin float64) bool {
	if current.Bugs != previous.Bugs || current.CleanCases != previous.CleanCases ||
		float64(current.FalsePositives-previous.FalsePositives) > margin ||
		float64(current.MustNotFlags-previous.MustNotFlags) > margin {
		return true
	}
	old := metricValues(previous)
	for name, now := range metricValues(current) {
		before := old[name]
		if now == nil || before == nil {
			continue
		}
		delta := *before - *now
		if name == "false_positives_per_clean_case" {
			delta = -delta
		}
		if delta > margin+1e-12 {
			return true
		}
	}
	return false
}
