package render

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/agendacraft/touchstone/internal/review"
)

type Markdown struct{}

func (Markdown) Render(out io.Writer, report review.Report) error {
	var text strings.Builder
	fmt.Fprintf(&text, "## Touchstone review: %s\n", markdown(report.Outcome))
	for _, notice := range report.Notices {
		fmt.Fprintf(&text, "\n%s\n", markdown(notice))
	}
	for _, banner := range banners(report) {
		fmt.Fprintf(&text, "\n> **%s**\n", markdown(banner))
	}
	s := summary(report)
	fmt.Fprintf(&text, "\n| Shown | Rejected | Filtered |\n| --- | --- | --- |\n| %d | %d | %d |\n",
		s.Shown, s.Rejected, s.Filtered)
	fmt.Fprintf(&text, "\nThresholds: severity ≥ %s; confidence ≥ %s.\n", markdown(s.MinSeverity), confidence(s.MinConfidence))
	fmt.Fprintln(&text, "\n| Layer | Status | Error |\n| --- | --- | --- |")
	for _, layer := range report.Layers {
		markdownLayer(&text, layer)
	}
	markdownLayer(&text, report.Verifier)
	for _, check := range report.Checks {
		fmt.Fprintf(&text, "\nLocal check **%s**: %s; executed locally: %t; argv: %s\n\n    %s\n", markdown(check.Name), markdown(check.Status), check.Local, markdown(check.Error), safeArgv(check.Args))
	}
	for _, severity := range []string{"error", "warning", "info"} {
		findings := []review.Finding{}
		for _, f := range report.Findings {
			if f.Severity == severity {
				findings = append(findings, f)
			}
		}
		if len(findings) == 0 {
			continue
		}
		fmt.Fprintf(&text, "\n### %s\n", strings.ToUpper(severity))
		for _, f := range findings {
			markdownFinding(&text, f)
		}
	}
	markdownAudit(&text, "Rejected findings (verifier reasons)", report.Rejected)
	markdownAudit(&text, "Filtered findings (below thresholds)", report.Filtered)
	_, err := io.WriteString(out, text.String())
	return err
}

func markdownLayer(text *strings.Builder, layer review.LayerResult) {
	name := layer.Name
	if layer.Group != "" {
		name += " [" + layer.Group + "]"
	}
	fmt.Fprintf(text, "| %s | %s | %s |\n", markdown(name), markdown(layer.Status), markdown(layer.Error))
}

func markdownFinding(text *strings.Builder, f review.Finding) {
	fmt.Fprintf(text, "\n- **%s — %s**: %s\n", markdown(f.DisplayID), markdown(location(f)), markdown(f.Title))
	fmt.Fprintf(text, "  - Severity: %s; confidence: %s; verdict: %s; layer: %s.\n",
		markdown(f.Severity), confidence(f.Confidence), markdown(f.Verification.Verdict), markdown(f.Layer))
	fmt.Fprintf(text, "  - Evidence: %s\n", markdown(f.Evidence))
	if f.Verification.Reason != "" {
		fmt.Fprintf(text, "  - Verifier: %s\n", markdown(f.Verification.Reason))
	}
	if len(f.PossibleDuplicateOf) > 0 {
		fmt.Fprintf(text, "  - Possible duplicate of: %s.\n", markdown(strings.Join(f.PossibleDuplicateOf, ", ")))
	}
}

func markdownAudit(text *strings.Builder, title string, findings []review.Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(text, "\n<details>\n<summary>%s (%d)</summary>\n", title, len(findings))
	for _, f := range findings {
		markdownFinding(text, f)
	}
	fmt.Fprintln(text, "\n</details>")
}

// Escape model text before placing it inside PR-comment markup.
func markdown(value string) string {
	value = plain(value)
	return markdownEscaped(value)
}

func markdownEscaped(value string) string {
	value = html.EscapeString(value)
	replacer := strings.NewReplacer(
		"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]",
		"|", "\\|", "~", "\\~", "@", "&#64;",
	)
	return replacer.Replace(value)
}
