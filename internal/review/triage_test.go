package review

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/findings"
)

func claim(layer, id string, line int) Finding {
	return Finding{
		Layer: layer, Candidate: findings.Candidate{
			ID: id, Severity: "warning", Category: "logic", File: "src/example.go", Line: line,
			Title: "Synthetic defect", Evidence: layer + " evidence", Confidence: 0.8,
		},
		Verification: Verification{Verdict: "confirmed", Reason: layer + " reason"}, Rank: "normal",
	}
}

func TestDuplicateHintsPreserveFindings(t *testing.T) {
	a, b := claim("correctness", "issue-1", 8), claim("security", "issue-1", 8)
	a.Title, b.Title = "Missing authentication check", "Missing authorization check"
	a.Severity, a.Confidence, a.Verification = "error", 0.4, Verification{Verdict: "uncertain", Reason: "uncertain error"}
	a.Rank = "lower"
	b.Confidence, b.Verification = 0.95, Verification{Verdict: "confirmed", Reason: "confirmed warning"}
	for _, rejected := range []bool{false, true} {
		report := Report{Findings: []Finding{b, a}}
		if rejected {
			report.Findings, report.Rejected = nil, report.Findings
		}
		got := Triage(report, config.Thresholds{MinSeverity: "info"})
		items := got.Findings
		if rejected {
			items = got.Rejected
		}
		if len(items) != 2 || got.Triage.Candidates != 2 ||
			items[0].Severity != "error" || items[0].Confidence != 0.4 || items[0].Verification.Reason != "uncertain error" ||
			items[0].Verification.Verdict != "uncertain" || items[0].Rank != "lower" ||
			items[1].Verification.Reason != "confirmed warning" || items[1].Verification.Verdict != "confirmed" ||
			len(items[0].PossibleDuplicateOf) != 1 || items[0].PossibleDuplicateOf[0] != items[1].DisplayID ||
			len(items[1].PossibleDuplicateOf) != 1 || items[1].PossibleDuplicateOf[0] != items[0].DisplayID {
			t.Fatalf("claims or hints changed: %+v", got)
		}
	}
	filtered := Triage(Report{Findings: []Finding{a, b}}, config.Thresholds{MinSeverity: "info", MinConfidence: 0.9})
	if len(filtered.Findings) != 1 || len(filtered.Filtered) != 1 || filtered.Findings[0].Layer != "security" ||
		filtered.Findings[0].PossibleDuplicateOf[0] != filtered.Filtered[0].DisplayID ||
		filtered.Filtered[0].PossibleDuplicateOf[0] != filtered.Findings[0].DisplayID {
		t.Fatalf("threshold changed claims or hints: %+v", filtered)
	}
	b.Title, b.File = "Divide by zero", "other.go"
	unrelated := Triage(Report{Findings: []Finding{a, b}}, config.Thresholds{MinSeverity: "info"})
	if len(unrelated.Findings) != 2 || len(unrelated.Findings[0].PossibleDuplicateOf) != 0 || len(unrelated.Findings[1].PossibleDuplicateOf) != 0 {
		t.Fatalf("unrelated layer-local IDs matched: %+v", unrelated)
	}
}

func TestDuplicateHintsAreDirect(t *testing.T) {
	a, b, c := claim("correctness", "a", 1), claim("security", "b", 1), claim("third", "c", 1)
	a.Title, b.Title, c.Title = "alpha beta", "alpha beta gamma", "beta gamma"
	got := Triage(Report{Findings: []Finding{a, b, c}}, config.Thresholds{MinSeverity: "info"})
	if len(got.Findings) != 3 || len(got.Findings[0].PossibleDuplicateOf) != 1 ||
		len(got.Findings[1].PossibleDuplicateOf) != 2 || len(got.Findings[2].PossibleDuplicateOf) != 1 {
		t.Fatalf("hints were made transitive: %+v", got)
	}
}

func TestRank(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Finding, *Finding)
	}{
		{"severity before confidence", func(a, b *Finding) { a.Severity = "error"; a.Confidence = 0.1; b.Confidence = 1 }},
		{"confidence before verdict", func(a, b *Finding) { a.Confidence = 0.9; a.Verification.Verdict = "uncertain" }},
		{"confirmed before uncertain", func(a, b *Finding) { b.Verification.Verdict = "uncertain" }},
		{"unverified before uncertain", func(a, b *Finding) { a.Verification.Verdict = "unverified"; b.Verification.Verdict = "uncertain" }},
		{"file", func(a, b *Finding) { a.File = "a.go"; b.File = "b.go" }},
		{"line", func(a, b *Finding) { a.Line = 2; b.Line = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := claim("correctness", "first", 8), claim("correctness", "second", 8)
			tc.change(&a, &b)
			got := Triage(Report{Findings: []Finding{b, a}}, config.Thresholds{MinSeverity: "info"}).Findings
			if len(got) != 2 || got[0].ID != "first" {
				t.Fatalf("wrong priority: %+v", got)
			}
		})
	}
}

func TestThresholdsAndRejectedAudit(t *testing.T) {
	a, b, rejected := claim("correctness", "a", 1), claim("correctness", "b", 2), claim("security", "a", 1)
	a.Confidence, b.Severity, b.Confidence = 0.8, "info", 0.79
	rejected.Verification = Verification{Verdict: "rejected", Reason: "Pre-existing behavior."}
	for _, tc := range []struct {
		name            string
		thresholds      config.Thresholds
		shown, filtered int
	}{
		{"inclusive confidence", config.Thresholds{MinSeverity: "info", MinConfidence: 0.8}, 1, 1},
		{"severity", config.Thresholds{MinSeverity: "warning"}, 1, 1},
		{"all filtered", config.Thresholds{MinSeverity: "error"}, 0, 2},
		{"none filtered", config.Thresholds{MinSeverity: "info"}, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Report{Outcome: "findings", Findings: []Finding{a, b}, Rejected: []Finding{rejected}}
			got := Triage(report, tc.thresholds)
			if len(got.Findings) != tc.shown || len(got.Filtered) != tc.filtered || got.Triage.Filtered != tc.filtered ||
				len(got.Rejected) != 1 || got.Rejected[0].Verification.Reason != "Pre-existing behavior." {
				t.Fatalf("wrong threshold/audit accounting: %+v", got)
			}
			if (got.Outcome == "clean") != (tc.shown == 0) {
				t.Fatal("outcome does not reflect post-filter findings")
			}
			report.Outcome = "incomplete"
			if Triage(report, tc.thresholds).Outcome != "incomplete" {
				t.Fatal("filter concealed incomplete review")
			}
		})
	}
}

func TestTriageDeterministic(t *testing.T) {
	input := []Finding{
		claim("correctness", "a", 1), claim("security", "a", 1),
		claim("correctness", "b", 4), claim("security", "c", 7),
		claim("correctness", "d", 12), claim("security", "d", 30),
	}
	random := rand.New(rand.NewPCG(7, 13))
	var expected []byte
	for range 80 {
		shuffled := slices.Clone(input)
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		report := Triage(Report{Findings: shuffled, Rejected: slices.Clone(shuffled)}, config.Thresholds{MinSeverity: "info"})
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if expected == nil {
			expected = data
		}
		if string(data) != string(expected) {
			t.Fatal("shuffled input changed output")
		}
	}
}
