package review

import (
	"cmp"
	"slices"
	"strings"

	"github.com/agendacraft/touchstone/internal/config"
)

// Summary counts claims before merging and retained defects after filtering.
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
	kept := deduplicate(report.Findings)
	report.Rejected = deduplicate(report.Rejected)
	summary.Merged = summary.Candidates - len(kept) - len(report.Rejected)
	report.Findings = []Finding{}
	report.Filtered = []Finding{}
	for _, f := range kept {
		if severity(f.Severity) < severity(thresholds.MinSeverity) || f.Confidence < thresholds.MinConfidence {
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

func deduplicate(input []Finding) []Finding {
	pending := slices.Clone(input)
	slices.SortFunc(pending, compareFindings)
	result := make([]Finding, 0, len(pending))
	for len(pending) > 0 {
		first, rest := pending[0], pending[1:]
		group := []Finding{first}
		pending = rest
		// Grow a connected component, including claims that bridge ranges.
		for {
			unmatched := make([]Finding, 0, len(pending))
			matched := false
			for _, candidate := range pending {
				if slices.ContainsFunc(group, func(member Finding) bool { return sameDefect(member, candidate) }) {
					group = append(group, candidate)
					matched = true
				} else {
					unmatched = append(unmatched, candidate)
				}
			}
			pending = unmatched
			if !matched {
				break
			}
		}
		result = append(result, merge(group))
	}
	slices.SortFunc(result, compareFindings)
	return result
}

func sameDefect(a, b Finding) bool {
	if a.Layer == b.Layer {
		return false
	}
	return normalizeID(a.ID) == normalizeID(b.ID) ||
		(a.File == b.File && a.Category == b.Category && a.Line <= lastLine(b) && b.Line <= lastLine(a))
}

func normalizeID(id string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(id), func(c rune) bool {
		return c == '.' || c == '_' || c == '-'
	}), "-")
}

func lastLine(f Finding) int { return max(f.Line, f.EndLine) }

func merge(group []Finding) Finding {
	slices.SortFunc(group, compareFindings)
	result := group[0]
	result.Layers, result.Sources = []string{}, []Source{}
	evidence, reasons := []string{}, []string{}
	for _, f := range group {
		result.Confidence = max(result.Confidence, f.Confidence)
		result.Layers = append(result.Layers, f.Layer)
		result.Sources = append(result.Sources, Source{
			Layer: f.Layer, Candidate: f.Candidate, Verification: f.Verification, EndLine: f.EndLine,
		})
		evidence = append(evidence, f.Evidence)
		if f.Verification.Reason != "" {
			reasons = append(reasons, f.Verification.Reason)
		}
		if verdict(f.Verification.Verdict) > verdict(result.Verification.Verdict) {
			result.Verification.Verdict = f.Verification.Verdict
		}
	}
	result.Layers = uniqueSorted(result.Layers)
	result.Evidence = strings.Join(uniqueSorted(evidence), "\n\n")
	result.Verification.Reason = strings.Join(uniqueSorted(reasons), "\n\n")
	result.Rank = "normal"
	if result.Verification.Verdict == "uncertain" {
		result.Rank = "lower"
	}
	return result
}

func uniqueSorted(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
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
		cmp.Compare(a.EndLine, b.EndLine),
	)
}
