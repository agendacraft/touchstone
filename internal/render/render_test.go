package render

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/agendacraft/touchstone/internal/review"
)

var updateGolden = flag.Bool("update", false, "update the renderer's public-output golden files")

func syntheticReport(unverified bool) review.Report {
	makeFinding := func(id, severity, file string, line int, confidence float64, verdict string) review.Finding {
		return review.Finding{
			Layer: "correctness", Candidate: findings.Candidate{
				ID: id, Severity: severity, Category: "logic", File: file, Line: line,
				Title: "Synthetic " + id, Evidence: "The synthetic change removes a required guard.", Confidence: confidence,
			},
			Verification: review.Verification{Verdict: verdict, Reason: "The diff supports this verdict."}, Rank: "normal",
		}
	}
	report := review.Report{
		SchemaVersion: 2, Outcome: "incomplete",
		Findings: []review.Finding{
			makeFinding("zero-divisor", "error", "src/calc.go", 8, 0.95, "confirmed"),
			makeFinding("partial-read", "warning", "src/read.go", 14, 0.8, "uncertain"),
			makeFinding("small-risk", "info", "src/read.go", 20, 0.3, "confirmed"),
		},
		Rejected: []review.Finding{makeFinding("old-behavior", "error", "src/calc.go", 3, 0.7, "rejected")},
		Layers: []review.LayerResult{
			{Name: "correctness", Status: "complete", Attempts: []review.Attempt{}},
			{Name: "security", Status: "incomplete", Error: "timeout", Attempts: []review.Attempt{}},
		},
		Verifier: review.LayerResult{Name: "verification", Status: "complete", Attempts: []review.Attempt{}},
	}
	report.Rejected[0].Verification.Reason = "This behavior existed before the synthetic diff."
	if unverified {
		report.Verifier.Status, report.Verifier.Error = "incomplete", "invalid_output"
		report.Rejected = []review.Finding{}
		for i := range report.Findings {
			report.Findings[i].Verification = review.Verification{Verdict: "unverified"}
		}
	}
	return report
}

func TestGoldenReports(t *testing.T) {
	// The synthetic contract has two visible findings, one filtered claim,
	// an incomplete layer, and either a reasoned rejection or failed verifier.
	// These are public rendered bytes, not snapshots of implementation source.
	for _, state := range []string{"incomplete", "unverified", "hints"} {
		input := syntheticReport(state == "unverified")
		if state == "hints" {
			input.Outcome = "findings"
			input.Layers[1].Status, input.Layers[1].Error = "complete", ""
			input.Findings[0].Verification = review.Verification{Verdict: "uncertain", Reason: "The first source remains uncertain."}
			second := input.Findings[0]
			second.Layer, second.ID, second.Severity, second.Confidence = "security", "zero_divisor", "warning", 0.99
			second.Evidence = "A second source identifies the removed guard."
			second.Verification = review.Verification{Verdict: "confirmed", Reason: "The second source is confirmed."}
			input.Findings = append(input.Findings, second)
		}
		report := review.Triage(input, config.Thresholds{MinSeverity: "warning", MinConfidence: 0.7})
		for _, format := range []struct {
			name     string
			renderer Renderer
		}{
			{"terminal", Terminal{}}, {"terminal-color", Terminal{Color: true}}, {"json", JSON{}}, {"markdown", Markdown{}},
		} {
			t.Run(state+"/"+format.name, func(t *testing.T) {
				var out bytes.Buffer
				if err := format.renderer.Render(&out, report); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join("testdata", state+"."+format.name+".golden")
				if *updateGolden {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out.Bytes(), want) {
					t.Fatalf("public %s output changed:\n%s", format.name, out.String())
				}
			})
		}
	}
}

type terminalBuffer struct {
	bytes.Buffer
	tty bool
}

func (b *terminalBuffer) IsTerminal() bool { return b.tty }

func TestSelectAndNOColor(t *testing.T) {
	original, existed := os.LookupEnv("NO_COLOR")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("NO_COLOR", original)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})
	report := review.Triage(syntheticReport(false), config.Thresholds{MinSeverity: "info"})
	for _, tc := range []struct {
		name, format              string
		tty, noColor, color, json bool
	}{
		{"tty default", "", true, false, true, false},
		{"pipe default", "", false, false, false, true},
		{"explicit terminal on pipe", "terminal", false, false, false, false},
		{"tty NO_COLOR", "", true, true, false, false},
		{"explicit tty NO_COLOR", "terminal", true, true, false, false},
		{"json on tty", "json", true, false, false, true},
		{"markdown on tty", "markdown", true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noColor {
				t.Setenv("NO_COLOR", "")
			} else if err := os.Unsetenv("NO_COLOR"); err != nil {
				t.Fatal(err)
			}
			out := &terminalBuffer{tty: tc.tty}
			renderer, err := Select(tc.format, out)
			if err != nil {
				t.Fatal(err)
			}
			if err := renderer.Render(out, report); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "\x1b[") != tc.color || strings.HasPrefix(out.String(), "{") != tc.json {
				t.Fatalf("wrong format/color: %q", out.String())
			}
		})
	}
	if _, err := Select("xml", io.Discard); err == nil {
		t.Fatal("unknown format accepted")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	if IsTerminal(write) {
		t.Fatal("pipe mistaken for a TTY")
	}
}

func TestRenderedDeterminism(t *testing.T) {
	random := rand.New(rand.NewPCG(2, 5))
	for _, renderer := range []Renderer{Terminal{}, JSON{}, Markdown{}} {
		var expected string
		for range 30 {
			report := syntheticReport(false)
			// A second layer reports the same defect with different evidence.
			duplicate := report.Findings[0]
			duplicate.Layer, duplicate.ID, duplicate.Evidence = "security", "second-view", "A second synthetic source."
			report.Findings = append(report.Findings, duplicate)
			random.Shuffle(len(report.Findings), func(i, j int) { report.Findings[i], report.Findings[j] = report.Findings[j], report.Findings[i] })
			report = review.Triage(report, config.Thresholds{MinSeverity: "warning", MinConfidence: 0.7})
			var out bytes.Buffer
			if err := renderer.Render(&out, report); err != nil {
				t.Fatal(err)
			}
			if expected == "" {
				expected = out.String()
			}
			if out.String() != expected {
				t.Fatal("shuffled claims changed rendered output")
			}
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic output error") }

func TestRenderFailuresAndUntrustedText(t *testing.T) {
	report := review.Triage(syntheticReport(false), config.Thresholds{MinSeverity: "info"})
	for _, renderer := range []Renderer{Terminal{}, JSON{}, Markdown{}} {
		if err := renderer.Render(failingWriter{}, report); err == nil {
			t.Fatal("output error concealed")
		}
	}
	report.Findings = slices.Clone(report.Findings)
	report.Findings[0].Title = "\x1b[31m<script>@everyone</script> [click](https://example.invalid)\n# heading"
	for _, renderer := range []Renderer{Terminal{}, Markdown{}} {
		var out bytes.Buffer
		if err := renderer.Render(&out, report); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Fatal("model supplied terminal control sequence")
		}
		if _, markdown := renderer.(Markdown); markdown && (strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "@everyone") || strings.Contains(out.String(), "[click](")) {
			t.Fatal("model text became active PR markup")
		}
	}
}

func TestTerminalShortensEvidence(t *testing.T) {
	report := review.Triage(syntheticReport(false), config.Thresholds{MinSeverity: "info"})
	report.Findings[0].Evidence = strings.Repeat("synthetic evidence ", 30)
	var out bytes.Buffer
	if err := (Terminal{}).Render(&out, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "...") {
		t.Fatal("terminal lost bounded evidence")
	}
}

func TestRenderersPreserveRankOrder(t *testing.T) {
	info := review.Finding{Layer: "correctness", Candidate: findings.Candidate{ID: "low", Severity: "info", File: "a.go", Line: 1, Title: "Low priority", Confidence: 0.9}, Verification: review.Verification{Verdict: "confirmed"}}
	high := review.Finding{Layer: "security", Candidate: findings.Candidate{ID: "high", Severity: "error", File: "z.go", Line: 1, Title: "High priority", Confidence: 0.8}, Verification: review.Verification{Verdict: "confirmed"}}
	report := review.Triage(review.Report{Findings: []review.Finding{info, high}}, config.Thresholds{MinSeverity: "info"})
	for _, renderer := range []Renderer{Terminal{}, JSON{}, Markdown{}} {
		var out bytes.Buffer
		if err := renderer.Render(&out, report); err != nil {
			t.Fatal(err)
		}
		highPos, lowPos := strings.Index(out.String(), "High priority"), strings.Index(out.String(), "Low priority")
		if highPos < 0 || lowPos < 0 || highPos >= lowPos {
			t.Fatalf("wrong rank order: %s", out.String())
		}
	}
}

func TestMarkdownDisplaysIDsInEverySection(t *testing.T) {
	report := review.Triage(syntheticReport(false), config.Thresholds{MinSeverity: "warning"})
	var out bytes.Buffer
	if err := (Markdown{}).Render(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, section := range [][]review.Finding{report.Findings, report.Rejected, report.Filtered} {
		for _, f := range section {
			if !strings.Contains(out.String(), "**"+f.DisplayID+" — "+f.File) {
				t.Fatalf("missing display ID %s: %s", f.DisplayID, out.String())
			}
		}
	}
}
