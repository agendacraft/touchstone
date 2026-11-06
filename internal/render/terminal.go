package render

import (
	"fmt"
	"io"
	"slices"
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
	fmt.Fprintf(&text, "%d shown | %d merged | %d rejected | %d filtered (min severity: %s, confidence: %.2f)\n",
		s.Shown, s.Merged, s.Rejected, s.Filtered, s.MinSeverity, s.MinConfidence)
	writeLayerStatus(&text, report)
	files := []string{}
	for _, f := range report.Findings {
		files = append(files, f.File)
	}
	for _, file := range unique(files) {
		fmt.Fprintf(&text, "\n%s\n", plain(file))
		for _, f := range report.Findings {
			if f.File == file {
				r.finding(&text, f)
			}
		}
	}
	if len(report.Rejected) > 0 {
		fmt.Fprintln(&text, "\nRejected (verifier audit):")
		for _, f := range report.Rejected {
			fmt.Fprintf(&text, "  %s %s\n    Reason: %s\n", plain(location(f)), plain(f.Title), short(f.Verification.Reason))
			writeOtherSources(&text, f)
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
	fmt.Fprintf(text, "  %s %s %s\n", marker, plain(location(f)), plain(f.Title))
	fmt.Fprintf(text, "    confidence %.2f | %s | layers: %s\n", f.Confidence, plain(f.Verification.Verdict), plain(provenance(f)))
	fmt.Fprintf(text, "    Evidence: %s\n", short(f.Evidence))
	if f.Verification.Reason != "" {
		fmt.Fprintf(text, "    Verifier: %s\n", short(f.Verification.Reason))
	}
	writeOtherSources(text, f)
}

func writeOtherSources(text *strings.Builder, f review.Finding) {
	sources := []string{}
	for _, source := range f.Sources {
		if source.Layer == f.Layer && source.ID == f.ID {
			continue
		}
		sources = append(sources, fmt.Sprintf("%s:%s (%s, %.2f, %s)", source.Layer, source.ID, source.Severity, source.Confidence, source.Verification.Verdict))
	}
	if len(sources) > 0 {
		fmt.Fprintf(text, "    Also reported by: %s\n", plain(strings.Join(sources, ", ")))
	}
}

func unique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}
