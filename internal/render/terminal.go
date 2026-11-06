package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/agendacraft/touchstone/internal/review"
)

type Terminal struct{ Color bool }

func (r Terminal) Render(out io.Writer, report review.Report) error {
	var text strings.Builder
	fmt.Fprintf(&text, "Touchstone: %s\n", report.Outcome)
	for _, banner := range banners(report) {
		fmt.Fprintln(&text, banner)
	}
	s := summary(report)
	fmt.Fprintf(&text, "%d shown | %d rejected | %d filtered (min severity: %s, confidence: %.2f)\n",
		s.Shown, s.Rejected, s.Filtered, s.MinSeverity, s.MinConfidence)
	writeLayerStatus(&text, report)
	for _, f := range report.Findings {
		fmt.Fprintln(&text)
		r.finding(&text, f)
	}
	if len(report.Rejected) > 0 {
		fmt.Fprintln(&text, "\nRejected (verifier audit):")
		for _, f := range report.Rejected {
			r.finding(&text, f)
		}
	}
	if len(report.Filtered) > 0 {
		fmt.Fprintln(&text, "\nFiltered (below thresholds):")
		for _, f := range report.Filtered {
			r.finding(&text, f)
		}
	}
	_, err := io.WriteString(out, text.String())
	return err
}

func writeLayerStatus(text *strings.Builder, report review.Report) {
	for _, layer := range report.Layers {
		fmt.Fprintf(text, "  %s: %s", plain(layer.Name), plain(layer.Status))
		if layer.Error != "" {
			fmt.Fprintf(text, " (%s)", plain(layer.Error))
		}
		fmt.Fprintln(text)
	}
	fmt.Fprintf(text, "  verifier: %s", plain(report.Verifier.Status))
	if report.Verifier.Error != "" {
		fmt.Fprintf(text, " (%s)", plain(report.Verifier.Error))
	}
	fmt.Fprintln(text)
}

func (r Terminal) finding(text *strings.Builder, f review.Finding) {
	marker := "[" + strings.ToUpper(f.Severity) + "]"
	if r.Color {
		color := map[string]string{"error": "31", "warning": "33", "info": "36"}[f.Severity]
		marker = "\x1b[" + color + "m" + marker + "\x1b[0m"
	}
	fmt.Fprintf(text, "  %s [%s] %s %s\n", marker, plain(f.DisplayID), plain(location(f)), plain(f.Title))
	fmt.Fprintf(text, "    confidence %.2f | %s | layer: %s\n", f.Confidence, plain(f.Verification.Verdict), plain(f.Layer))
	fmt.Fprintf(text, "    Evidence: %s\n", short(f.Evidence))
	if f.Verification.Reason != "" {
		fmt.Fprintf(text, "    Verifier: %s\n", short(f.Verification.Reason))
	}
	writeHints(text, f)
}

func writeHints(text *strings.Builder, f review.Finding) {
	if len(f.PossibleDuplicateOf) > 0 {
		fmt.Fprintf(text, "    Possible duplicate of: %s\n", plain(strings.Join(f.PossibleDuplicateOf, ", ")))
	}
}
