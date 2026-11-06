// Package render presents the same triaged report in human and machine formats.
package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/agendacraft/touchstone/internal/review"
	"golang.org/x/term"
)

type Renderer interface {
	Render(io.Writer, review.Report) error
}

func IsTerminal(out io.Writer) bool {
	if terminal, ok := out.(interface{ IsTerminal() bool }); ok {
		return terminal.IsTerminal()
	}
	if file, ok := out.(interface{ Fd() uintptr }); ok {
		return term.IsTerminal(int(file.Fd()))
	}
	return false
}

// Select resolves automatic format and color from the actual output writer.
func Select(format string, out io.Writer) (Renderer, error) {
	tty := IsTerminal(out)
	if format == "" {
		format = "json"
		if tty {
			format = "terminal"
		}
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	switch format {
	case "terminal":
		return Terminal{Color: tty && !noColor}, nil
	case "json":
		return JSON{}, nil
	case "markdown":
		return Markdown{}, nil
	default:
		return nil, errors.New("format must be terminal, json, or markdown")
	}
}

type JSON struct{}

func (JSON) Render(out io.Writer, report review.Report) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func summary(report review.Report) review.Summary {
	if report.Triage != nil {
		return *report.Triage
	}
	return review.Summary{Shown: len(report.Findings), Rejected: len(report.Rejected)}
}

func banners(report review.Report) []string {
	result := []string{}
	if report.Outcome == "incomplete" {
		result = append(result, "INCOMPLETE: a specialist layer or required verifier failed; this is not a clean review.")
	}
	if report.Verifier.Status == "disabled" || report.Verifier.Status == "incomplete" ||
		slices.ContainsFunc(append(slices.Clone(report.Findings), report.Filtered...), unverified) {
		result = append(result, "UNVERIFIED: independent verification was disabled or unavailable; findings are not fully verified.")
	}
	return result
}

func unverified(f review.Finding) bool {
	return f.Verification.Verdict == "unverified" ||
		slices.ContainsFunc(f.Sources, func(s review.Source) bool { return s.Verification.Verdict == "unverified" })
}

func location(f review.Finding) string {
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

func provenance(f review.Finding) string {
	layers := f.Layers
	if len(layers) == 0 {
		layers = []string{f.Layer}
	}
	return strings.Join(layers, ", ")
}

func plain(value string) string {
	value = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return ' '
		}
		return c
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func short(value string) string {
	runes := []rune(plain(value))
	if len(runes) > 180 {
		return string(runes[:177]) + "..."
	}
	return string(runes)
}
