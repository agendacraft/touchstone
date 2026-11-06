package review

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
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

func TestDedupe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Finding)
		want   int
	}{
		{"duplicate", func(f *Finding) { f.Title = "The synthetic defect!" }, 1},
		{"different line", func(f *Finding) { f.Line = 9 }, 2},
		{"same id different file", func(f *Finding) { f.ID = "issue-1"; f.File = "other.go" }, 2},
		{"unrelated titles same line", func(f *Finding) { f.Title = "Authorization bypass" }, 2},
		{"different category", func(f *Finding) { f.Category = "security" }, 1},
		{"same layer", func(f *Finding) { f.Layer = "correctness" }, 2},
		{"reworded", func(f *Finding) { f.Title = "Synthetic defect causes failure" }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := claim("correctness", "issue-1", 8), claim("security", "another", 8)
			tc.change(&b)
			for _, rejected := range []bool{false, true} {
				report := Report{Findings: []Finding{a, b}}
				if rejected {
					report.Findings, report.Rejected = nil, report.Findings
				}
				got := Triage(report, config.Thresholds{MinSeverity: "info"})
				items := got.Findings
				if rejected {
					items = got.Rejected
				}
				if len(items) != tc.want || got.Triage.Merged != 2-tc.want {
					t.Fatalf("wrong grouping: %+v", got)
				}
			}
		})
	}
}

func TestMergePreservesClaims(t *testing.T) {
	a, b := claim("correctness", "a", 8), claim("security", "b", 8)
	a.Severity, a.Confidence, a.Verification.Verdict = "error", 0.4, "uncertain"
	b.Confidence, b.Verification.Verdict = 0.95, "confirmed"
	report := Report{Findings: []Finding{b, a}}
	got := Triage(report, config.Thresholds{MinSeverity: "info"})
	if len(got.Findings) != 1 {
		t.Fatal("duplicate was not merged")
	}
	f := got.Findings[0]
	if f.Severity != "error" || f.Confidence != 0.4 || f.Verification.Verdict != "uncertain" || f.Rank != "lower" ||
		!reflect.DeepEqual(f.Layers, []string{"correctness", "security"}) {
		t.Fatalf("wrong aggregate: %+v", f)
	}
	if len(f.Sources) != 2 || f.Sources[0].Confidence != 0.4 || f.Sources[1].Confidence != 0.95 || f.Sources[1].Verification.Verdict != "confirmed" {
		t.Fatal("original assessments lost")
	}
	for _, source := range []Finding{a, b} {
		if !strings.Contains(f.Evidence, source.Evidence) || !strings.Contains(f.Verification.Reason, source.Verification.Reason) {
			t.Fatal("supporting evidence or verifier reason lost")
		}
	}
	filtered := Triage(report, config.Thresholds{MinSeverity: "info", MinConfidence: 0.9})
	if len(filtered.Findings) != 0 || len(filtered.Filtered) != 1 || filtered.Filtered[0].Confidence != 0.4 {
		t.Fatalf("threshold did not use representative assessment: %+v", filtered)
	}
}

func TestDedupeGroupMembership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layers [3]string
		titles [3]string
	}{
		{"same layer", [3]string{"correctness", "security", "security"}, [3]string{"request bypass", "request authentication bypass", "request authorization bypass"}},
		{"all pairs must match", [3]string{"correctness", "security", "third"}, [3]string{"alpha beta gamma", "alpha beta", "beta gamma"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := make([]Finding, 3)
			for i := range claims {
				claims[i] = claim(tc.layers[i], string(rune('a'+i)), 1)
				claims[i].Title = tc.titles[i]
			}
			for _, rejected := range []bool{false, true} {
				report := Report{Findings: claims}
				if rejected {
					report.Findings, report.Rejected = nil, claims
				}
				got := Triage(report, config.Thresholds{MinSeverity: "info"})
				items := got.Findings
				if rejected {
					items = got.Rejected
				}
				if len(items) != 2 || got.Triage.Merged != 1 {
					t.Fatalf("distinct claims combined: %+v", got)
				}
			}
		})
	}
}

func TestRepresentativeVerdictTieBreak(t *testing.T) {
	a, b := claim("correctness", "a", 1), claim("security", "b", 1)
	a.Verification.Verdict = "uncertain"
	got := Triage(Report{Findings: []Finding{a, b}}, config.Thresholds{MinSeverity: "info"})
	if len(got.Findings) != 1 || got.Findings[0].Layer != "security" || got.Findings[0].Verification.Verdict != "confirmed" {
		t.Fatalf("confirmed assessment did not win tie: %+v", got)
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
			got := deduplicate([]Finding{b, a})
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
				len(got.Rejected) != 1 || got.Rejected[0].Verification.Reason != "Pre-existing behavior." || got.Triage.Merged != 0 {
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
