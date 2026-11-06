package eval

import (
	"path/filepath"
	"testing"
)

func baselineSuite() Suite {
	// This independent score fixture describes 3 of 4 bugs found, one false
	// claim, and 2 of 3 true claims at the expected severity.
	m := Metrics{Bugs: 4, Found: 3, Claims: 4, Correct: 3, FalsePositives: 1, SeverityCorrect: 2}
	m.rates()
	s := Scores{Before: map[string]Metrics{}, After: map[string]Metrics{}}
	for _, scope := range scopes {
		s.Before[scope], s.After[scope] = m, m
	}
	return Suite{Version: 1, Mode: "recorded", Complete: true, Repeats: 1,
		Cases: []Result{{Case: "sample", Repeat: 1, Fingerprint: "fixed-ground-truth", Scores: s}}}
}

func TestBaselineRoundTripAndRegression(t *testing.T) {
	file := filepath.Join(t.TempDir(), "baseline.json")
	if err := SaveBaseline(file, baselineSuite(), 0.05); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadBaseline(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := Compare(baselineSuite(), baseline, -1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Suite)
	}{
		{"recall", func(s *Suite) {
			m := s.Cases[0].Scores.After["security"]
			m.Found--
			m.rates()
			s.Cases[0].Scores.After["security"] = m
		}},
		{"precision", func(s *Suite) {
			m := s.Cases[0].Scores.Before["correctness"]
			m.Correct--
			m.rates()
			s.Cases[0].Scores.Before["correctness"] = m
		}},
		{"false positives", func(s *Suite) {
			m := s.Cases[0].Scores.After["overall"]
			m.FalsePositives++
			m.rates()
			s.Cases[0].Scores.After["overall"] = m
		}},
		{"severity", func(s *Suite) {
			m := s.Cases[0].Scores.After["overall"]
			m.SeverityCorrect--
			m.rates()
			s.Cases[0].Scores.After["overall"] = m
		}},
		{"verifier", func(s *Suite) { s.Cases[0].Scores.Verifier.RejectedRealBugs++ }},
		{"fingerprint", func(s *Suite) { s.Cases[0].Fingerprint = "changed" }},
		{"case removed", func(s *Suite) { s.Cases = nil }},
		{"repeat changed", func(s *Suite) { s.Cases[0].Repeat++ }},
		{"incomplete", func(s *Suite) { s.Complete = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := baselineSuite()
			tc.mutate(&current)
			if err := Compare(current, baseline, -1); err == nil {
				t.Fatal("regression was accepted")
			}
		})
	}
	current := baselineSuite()
	m := current.Cases[0].Scores.After["overall"]
	m.SeverityCorrect--
	m.rates()
	current.Cases[0].Scores.After["overall"] = m
	if err := Compare(current, baseline, 1.0/3); err != nil {
		t.Fatalf("exact configured margin rejected: %v", err)
	}
	s := baselineSuite()
	s.Mode = "live"
	if err := SaveBaseline(file, s, 0); err == nil {
		t.Fatal("live baseline accepted")
	}
}

func TestKnownIncompleteBaselineChecksExactLayerStates(t *testing.T) {
	s := baselineSuite()
	s.Complete = false
	s.Cases[0].Report.Outcome = "incomplete"
	file := filepath.Join(t.TempDir(), "baseline.json")
	if err := SaveBaseline(file, s, 0); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBaseline(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := Compare(s, b, -1); err != nil {
		t.Fatalf("known incomplete state rejected: %v", err)
	}
	s.Cases[0].Report.Verifier.Error = "timeout"
	if err := Compare(s, b, -1); err == nil {
		t.Fatal("new verifier failure hidden by already-incomplete suite")
	}
}
