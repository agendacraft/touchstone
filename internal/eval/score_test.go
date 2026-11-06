package eval

import (
	"math"
	"testing"

	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/agendacraft/touchstone/internal/review"
)

func claim(layer, file, category, severity string, line int) review.Finding {
	return review.Finding{Layer: layer, Candidate: findings.Candidate{
		ID: "claim", File: file, Category: category, Severity: severity, Line: line,
		Title: "A claim", Evidence: "Supporting explanation", Confidence: 1,
	}}
}

func assertRatio(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-12 {
		t.Fatalf("ratio = %v, want %g", got, want)
	}
}

func TestScoreWorkedExample(t *testing.T) {
	// Independent oracle: two annotated bugs, two retained copies of the
	// first, one uncertain second bug, two retained false claims, and two
	// rejected claims (one true, one false). No claim is merged or removed.
	truth := Truth{Findings: []Expected{
		{File: "a.go", Start: 4, End: 5, Category: "logic", Kind: "must-find", Severity: "error"},
		{File: "a.go", Start: 10, End: 10, Category: "auth", Kind: "must-find", Severity: "warning"},
		{File: "a.go", Start: 20, End: 20, Category: "logic", Kind: "must-not-flag"},
	}}
	report := review.Report{Findings: []review.Finding{
		claim("correctness", "a.go", "logic", "error", 4),
		claim("security", "a.go", "logic", "warning", 5),
		claim("security", "a.go", "logic", "error", 20),
		claim("correctness", "a.go", "logic", "error", 99),
		claim("correctness", "a.go", "auth", "info", 10),
	}, Rejected: []review.Finding{
		claim("correctness", "a.go", "auth", "warning", 10),
		claim("security", "a.go", "logic", "error", 30),
	}}
	report.Findings[4].Verification.Verdict = "uncertain"
	got := Score(truth, report)
	before, after := got.Before["overall"], got.After["overall"]
	if before.Bugs != 2 || before.Found != 2 || before.Claims != 7 || before.Correct != 4 || before.FalsePositives != 3 {
		t.Fatalf("before: %+v", before)
	}
	if after.Bugs != 2 || after.Found != 2 || after.Claims != 5 || after.Correct != 3 || after.FalsePositives != 2 {
		t.Fatalf("after: %+v", after)
	}
	if after.MustNotFlags != 1 || got.VerifierByLayer["correctness"].RejectedRealBugs != 1 ||
		got.VerifierByLayer["security"].RejectedCorrectly != 1 {
		t.Fatal("forbidden flag or source-layer verifier impact was lost")
	}
	assertRatio(t, before.Precision, 4.0/7)
	assertRatio(t, after.Precision, 3.0/5)
	assertRatio(t, before.Recall, 1)
	assertRatio(t, after.SeverityAccuracy, 1.0/3)
	assertRatio(t, before.SeverityAccuracy, 1.0/2)
	if got.Verifier.Rejected != 2 || got.Verifier.RejectedCorrectly != 1 || got.Verifier.RejectedRealBugs != 1 {
		t.Fatalf("verifier: %+v", got.Verifier)
	}
	assertRatio(t, got.Verifier.RejectionRate, 2.0/7)
	if got.After["correctness"].Claims != 3 || got.After["correctness"].Correct != 2 ||
		got.After["security"].Claims != 2 || got.After["security"].Correct != 1 {
		t.Fatalf("layer scores: %+v", got.After)
	}
	clean := Score(Truth{}, report)
	if clean.After["overall"].CleanCases != 1 || clean.After["overall"].CleanFalsePositives != 5 {
		t.Fatalf("clean: %+v", clean)
	}
	assertRatio(t, clean.After["overall"].Precision, 0)
	assertRatio(t, clean.After["overall"].FalsePositivesPerClean, 5)
	if clean.After["overall"].Recall != nil || clean.After["overall"].SeverityAccuracy != nil {
		t.Fatal("undefined metrics must be null, not perfect scores")
	}
	total := Aggregate([]Result{{Scores: got}, {Scores: clean}})
	if total.After["overall"].Bugs != 2 || total.After["overall"].FalsePositives != 7 {
		t.Fatalf("aggregate: %+v", total)
	}
	assertRatio(t, total.After["overall"].Precision, 3.0/10)
	report.Outcome = "incomplete"
	failed := Score(truth, report)
	assertRatio(t, failed.IncompleteRate, 1)
	total = Aggregate([]Result{{Scores: got}, {Scores: clean}, {Scores: failed}})
	if total.Reviews != 3 || total.Incomplete != 1 {
		t.Fatalf("reliability counts: %+v", total)
	}
	assertRatio(t, total.IncompleteRate, 1.0/3)
}

func TestMatchingBoundaries(t *testing.T) {
	truth := Truth{Findings: []Expected{{File: "dir/a.go", Start: 4, End: 6,
		Category: "logic", Aliases: []string{"bounds"}, Kind: "must-find", Severity: "error"}}}
	for _, tc := range []struct {
		file, category string
		line           int
		want           bool
	}{
		{"dir/a.go", "logic", 4, true},
		{"dir/a.go", " BOUNDS ", 6, true},
		{"dir/a.go", "logic", 3, false},
		{"dir/a.go", "logic", 7, false},
		{"other/a.go", "logic", 5, false},
		{"dir/a.go", "logical", 5, false},
		{"dir/a.go", "security", 5, false},
	} {
		f := claim("correctness", tc.file, tc.category, "error", tc.line)
		if got := match(truth, f) >= 0; got != tc.want {
			t.Errorf("%+v matched = %v", tc, got)
		}
	}
}

func TestFilteredClaimsStillCount(t *testing.T) {
	// Eval never lets display thresholds hide false positives.
	s := Score(Truth{}, review.Report{Filtered: []review.Finding{claim("security", "a.go", "logic", "info", 2)}})
	if s.After["overall"].FalsePositives != 1 {
		t.Fatal("filtered claim disappeared")
	}
}

func TestSpreadKeepsHoldoutSeparate(t *testing.T) {
	bug := Truth{Findings: []Expected{{File: "a.go", Start: 1, End: 1, Category: "logic", Kind: "must-find", Severity: "error"}}}
	found := Score(bug, review.Report{Findings: []review.Finding{claim("correctness", "a.go", "logic", "error", 1)}})
	missed := Score(bug, review.Report{})
	s := Suite{Repeats: 3, Cases: []Result{
		{Repeat: 1, Scores: found}, {Repeat: 2, Scores: missed}, {Repeat: 3, Scores: found},
		{Repeat: 1, Holdout: true, Scores: missed}, {Repeat: 2, Holdout: true, Scores: missed}, {Repeat: 3, Holdout: true, Scores: missed},
	}}
	spread := spreads(s)
	dev := spread["development/after/overall/recall"]
	holdout := spread["holdout/after/overall/recall"]
	if dev.Min != 0 || dev.Max != 1 || dev.N != 3 || math.Abs(dev.Mean-2.0/3) > 1e-12 ||
		holdout.Min != 0 || holdout.Max != 0 || holdout.N != 3 {
		t.Fatalf("spread: %+v", spread)
	}
}
