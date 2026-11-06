package eval

import "github.com/agendacraft/touchstone/internal/review"

type Metrics struct {
	Bugs                   int      `json:"bugs"`
	Found                  int      `json:"found"`
	Claims                 int      `json:"claims"`
	Correct                int      `json:"correct"`
	FalsePositives         int      `json:"false_positives"`
	MustNotFlags           int      `json:"must_not_flags"`
	SeverityCorrect        int      `json:"severity_correct"`
	CleanCases             int      `json:"clean_cases"`
	CleanFalsePositives    int      `json:"clean_false_positives"`
	Recall                 *float64 `json:"recall"`
	Precision              *float64 `json:"precision"`
	SeverityAccuracy       *float64 `json:"severity_accuracy"`
	FalsePositivesPerClean *float64 `json:"false_positives_per_clean_case"`
}

type Verifier struct {
	Claims            int      `json:"claims"`
	Rejected          int      `json:"rejected"`
	RejectedCorrectly int      `json:"rejected_correctly"`
	RejectedRealBugs  int      `json:"rejected_real_bugs"`
	RejectionRate     *float64 `json:"rejection_rate"`
}

type Scores struct {
	Reviews         int                 `json:"reviews"`
	Incomplete      int                 `json:"incomplete"`
	IncompleteRate  *float64            `json:"incomplete_rate"`
	Before          map[string]Metrics  `json:"before_verifier"`
	After           map[string]Metrics  `json:"after_verifier"`
	Verifier        Verifier            `json:"verifier"`
	VerifierByLayer map[string]Verifier `json:"verifier_by_layer"`
}

var scopes = []string{"overall", "correctness", "security"}

func ratio(a, b int) *float64 {
	if b == 0 {
		return nil
	}
	value := float64(a) / float64(b)
	return &value
}

func (m *Metrics) rates() {
	m.Recall = ratio(m.Found, m.Bugs)
	m.Precision = ratio(m.Correct, m.Claims)
	m.SeverityAccuracy = ratio(m.SeverityCorrect, m.Correct)
	m.FalsePositivesPerClean = ratio(m.CleanFalsePositives, m.CleanCases)
}

func match(truth Truth, f review.Finding) int {
	return matchKind(truth, f, "must-find")
}

func matchKind(truth Truth, f review.Finding, kind string) int {
	for i, e := range truth.Findings {
		if e.Kind == kind && e.File == f.File && e.Start <= f.Line && f.Line <= e.End && categoryMatch(e, f.Category) {
			return i
		}
	}
	return -1
}

func measure(truth Truth, claims []review.Finding, scope string) Metrics {
	m := Metrics{}
	for _, e := range truth.Findings {
		if e.Kind == "must-find" {
			m.Bugs++
		}
	}
	if m.Bugs == 0 {
		m.CleanCases = 1
	}
	hits := map[int]bool{}
	for _, f := range claims {
		if scope != "overall" && f.Layer != scope {
			continue
		}
		m.Claims++
		if i := match(truth, f); i >= 0 {
			m.Correct++
			hits[i] = true
			if f.Severity == truth.Findings[i].Severity {
				m.SeverityCorrect++
			}
		} else {
			m.FalsePositives++
			if matchKind(truth, f, "must-not-flag") >= 0 {
				m.MustNotFlags++
			}
			if m.CleanCases == 1 {
				m.CleanFalsePositives++
			}
		}
	}
	m.Found = len(hits)
	m.rates()
	return m
}

func Score(truth Truth, report review.Report) Scores {
	retained := append(append([]review.Finding{}, report.Findings...), report.Filtered...)
	all := append(append([]review.Finding{}, retained...), report.Rejected...)
	s := Scores{Reviews: 1, Before: map[string]Metrics{}, After: map[string]Metrics{}, VerifierByLayer: map[string]Verifier{}}
	if report.Outcome == "incomplete" {
		s.Incomplete = 1
	}
	s.IncompleteRate = ratio(s.Incomplete, s.Reviews)
	for _, scope := range scopes {
		s.Before[scope] = measure(truth, all, scope)
		s.After[scope] = measure(truth, retained, scope)
		v := verifierScore(truth, all, report.Rejected, scope)
		if scope == "overall" {
			s.Verifier = v
		} else {
			s.VerifierByLayer[scope] = v
		}
	}
	return s
}

func verifierScore(truth Truth, all, rejected []review.Finding, scope string) Verifier {
	v := Verifier{}
	for _, f := range all {
		if scope == "overall" || f.Layer == scope {
			v.Claims++
		}
	}
	for _, f := range rejected {
		if scope != "overall" && f.Layer != scope {
			continue
		}
		v.Rejected++
		if match(truth, f) >= 0 {
			v.RejectedRealBugs++
		} else {
			v.RejectedCorrectly++
		}
	}
	v.RejectionRate = ratio(v.Rejected, v.Claims)
	return v
}

func addMetrics(a, b Metrics) Metrics {
	a.Bugs += b.Bugs
	a.Found += b.Found
	a.Claims += b.Claims
	a.Correct += b.Correct
	a.FalsePositives += b.FalsePositives
	a.MustNotFlags += b.MustNotFlags
	a.SeverityCorrect += b.SeverityCorrect
	a.CleanCases += b.CleanCases
	a.CleanFalsePositives += b.CleanFalsePositives
	a.rates()
	return a
}

func Aggregate(cases []Result) Scores {
	s := Scores{Before: map[string]Metrics{}, After: map[string]Metrics{}, VerifierByLayer: map[string]Verifier{}}
	for _, c := range cases {
		s.Reviews += c.Scores.Reviews
		s.Incomplete += c.Scores.Incomplete
		for _, scope := range scopes {
			s.Before[scope] = addMetrics(s.Before[scope], c.Scores.Before[scope])
			s.After[scope] = addMetrics(s.After[scope], c.Scores.After[scope])
			if scope != "overall" {
				s.VerifierByLayer[scope] = addVerifier(s.VerifierByLayer[scope], c.Scores.VerifierByLayer[scope])
			}
		}
		s.Verifier = addVerifier(s.Verifier, c.Scores.Verifier)
	}
	s.IncompleteRate = ratio(s.Incomplete, s.Reviews)
	return s
}

func addVerifier(a, b Verifier) Verifier {
	a.Claims += b.Claims
	a.Rejected += b.Rejected
	a.RejectedCorrectly += b.RejectedCorrectly
	a.RejectedRealBugs += b.RejectedRealBugs
	a.RejectionRate = ratio(a.Rejected, a.Claims)
	return a
}
