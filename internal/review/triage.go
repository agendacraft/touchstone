package review

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/agendacraft/touchstone/internal/config"
)

// Summary counts claims before and after filtering.
type Summary struct {
	Candidates    int     `json:"candidates"`
	Merged        int     `json:"merged"`
	Shown         int     `json:"shown"`
	Rejected      int     `json:"rejected"`
	Filtered      int     `json:"filtered"`
	MinSeverity   string  `json:"min_severity"`
	MinConfidence float64 `json:"min_confidence"`
}

// Triage runs after verification. Rejected claims cannot erase or corroborate
// retained claims; each section keeps its own full source audit trail.
func Triage(report Report, thresholds config.Thresholds) Report {
	summary := Summary{
		Candidates:  len(report.Findings) + len(report.Rejected),
		MinSeverity: thresholds.MinSeverity, MinConfidence: thresholds.MinConfidence,
	}
	type item struct {
		finding  Finding
		rejected bool
	}
	items := make([]item, 0, summary.Candidates)
	for _, f := range report.Findings {
		items = append(items, item{finding: f})
	}
	for _, f := range report.Rejected {
		items = append(items, item{finding: f, rejected: true})
	}
	slices.SortFunc(items, func(a, b item) int {
		return cmp.Or(compareFindings(a.finding, b.finding), cmp.Compare(boolOrder(a.rejected), boolOrder(b.rejected)))
	})
	for i := range items {
		items[i].finding.DisplayID = fmt.Sprintf("f%d", i+1)
		items[i].finding.PossibleDuplicateOf = nil
		items[i].finding.Rank = "normal"
		if items[i].finding.Verification.Verdict == "uncertain" {
			items[i].finding.Rank = "lower"
		}
	}
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if sameDefect(items[i].finding, items[j].finding) {
				items[i].finding.PossibleDuplicateOf = append(items[i].finding.PossibleDuplicateOf, items[j].finding.DisplayID)
				items[j].finding.PossibleDuplicateOf = append(items[j].finding.PossibleDuplicateOf, items[i].finding.DisplayID)
			}
		}
	}
	report.Findings, report.Rejected, report.Filtered = []Finding{}, []Finding{}, []Finding{}
	for _, item := range items {
		f := item.finding
		if item.rejected {
			report.Rejected = append(report.Rejected, f)
		} else if severity(f.Severity) < severity(thresholds.MinSeverity) || f.Confidence < thresholds.MinConfidence {
			report.Filtered = append(report.Filtered, f)
		} else {
			report.Findings = append(report.Findings, f)
		}
	}
	summary.Shown, summary.Rejected, summary.Filtered = len(report.Findings), len(report.Rejected), len(report.Filtered)
	report.Triage = &summary
	if report.Outcome != "incomplete" {
		report.Outcome = "clean"
		if len(report.Findings) > 0 {
			report.Outcome = "findings"
		}
	}
	return report
}

func sameDefect(a, b Finding) bool {
	if a.Layer == b.Layer || a.File != b.File || a.Line != b.Line {
		return false
	}
	aTokens, bTokens := titleTokens(a.Title), titleTokens(b.Title)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return false
	}
	intersection := 0
	for token := range aTokens {
		if bTokens[token] {
			intersection++
		}
	}
	return 2*intersection >= len(aTokens)+len(bTokens)-intersection
}

func titleTokens(title string) map[string]bool {
	stopwords := map[string]bool{"a": true, "an": true, "and": true, "as": true, "at": true, "by": true, "for": true, "in": true, "is": true, "of": true, "on": true, "or": true, "the": true, "to": true, "with": true}
	tokens := make(map[string]bool)
	for _, token := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !stopwords[token] {
			tokens[token] = true
		}
	}
	return tokens
}

func severity(value string) int {
	return map[string]int{"error": 3, "warning": 2, "info": 1}[value]
}

func verdict(value string) int {
	return map[string]int{"confirmed": 3, "unverified": 2, "uncertain": 1}[value]
}

func compareFindings(a, b Finding) int {
	return cmp.Or(
		cmp.Compare(severity(b.Severity), severity(a.Severity)),
		cmp.Compare(b.Confidence, a.Confidence),
		cmp.Compare(verdict(b.Verification.Verdict), verdict(a.Verification.Verdict)),
		cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Layer, b.Layer), cmp.Compare(a.ID, b.ID),
		cmp.Compare(a.Category, b.Category), cmp.Compare(a.Title, b.Title),
		cmp.Compare(a.Evidence, b.Evidence), cmp.Compare(a.Verification.Reason, b.Verification.Reason),
	)
}

func boolOrder(value bool) int {
	if value {
		return 1
	}
	return 0
}
