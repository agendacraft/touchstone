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
	// S3's independent identity/location rules: equal normalized IDs OR
	// overlapping same-file/category locations, but only across layers.
	for _, tc := range []struct {
		name   string
		change func(*Finding)
		want   int
	}{
		{"same point", func(*Finding) {}, 1},
		{"overlap", func(f *Finding) { f.Line = 3; f.EndLine = 8 }, 1},
		{"adjacent is not overlap", func(f *Finding) { f.Line = 9 }, 2},
		{"different file", func(f *Finding) { f.File = "other.go" }, 2},
		{"different category", func(f *Finding) { f.Category = "security" }, 2},
		{"same layer", func(f *Finding) { f.Layer = "correctness" }, 2},
		{"normalized identity across locations", func(f *Finding) {
			f.ID = "DIVIDE__ZERO"
			f.File = "other.go"
			f.Line = 40
			f.Category = "security"
		}, 1},
		{"different identity and location", func(f *Finding) { f.Line = 40 }, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := claim("correctness", "divide.zero", 8), claim("security", "another", 8)
			tc.change(&b)
			got := Triage(Report{Findings: []Finding{a, b}}, config.Thresholds{MinSeverity: "info"})
			if len(got.Findings) != tc.want || got.Triage.Merged != 2-tc.want {
				t.Fatalf("wrong grouping: %+v", got)
			}
			if tc.want == 1 && len(got.Findings[0].Sources) != 2 {
				t.Fatal("a merged claim disappeared")
			}
		})
	}
}

func TestMergePreservesClaims(t *testing.T) {
	a, b := claim("correctness", "a", 8), claim("security", "b", 8)
	a.Severity, a.Confidence = "error", 0.6
	b.Confidence, b.Verification.Verdict = 0.99, "uncertain"
	got := Triage(Report{Findings: []Finding{b, a}}, config.Thresholds{MinSeverity: "error", MinConfidence: 0.9})
	if len(got.Findings) != 1 {
		t.Fatal("thresholds ran before severity/confidence merge")
	}
	f := got.Findings[0]
	if f.Severity != "error" || f.Confidence != 0.99 || f.Verification.Verdict != "confirmed" || f.Rank != "normal" ||
		!reflect.DeepEqual(f.Layers, []string{"correctness", "security"}) {
		t.Fatalf("wrong aggregate: %+v", f)
	}
	if len(f.Sources) != 2 || f.Sources[0].Confidence != 0.6 || f.Sources[1].Verification.Verdict != "uncertain" {
		t.Fatal("original severity, confidence, or verdict lost")
	}
	for _, source := range []Finding{a, b} {
		if !strings.Contains(f.Evidence, source.Evidence) || !strings.Contains(f.Verification.Reason, source.Verification.Reason) {
			t.Fatal("supporting evidence or verifier reason lost")
		}
	}
	if b.Verification.Verdict != "uncertain" || a.Confidence != 0.6 {
		t.Fatal("triage mutated its input claims")
	}
}

func TestDedupeTransitiveOverlap(t *testing.T) {
	a, b, c := claim("correctness", "a", 1), claim("security", "b", 3), claim("correctness", "c", 5)
	a.EndLine, b.EndLine = 3, 5
	got := deduplicate([]Finding{c, a, b})
	if len(got) != 1 || len(got[0].Sources) != 3 {
		t.Fatalf("range bridge failed: %+v", got)
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
