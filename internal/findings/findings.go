// Package findings defines and validates the untrusted model-output contract.
package findings

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/agendacraft/touchstone/internal/jsonutil"
)

const MaxBytes = 1 << 20
const MaxFindings = 500

type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Outcome       string    `json:"outcome"`
	Findings      []Finding `json:"findings"`
}

type Finding struct {
	ID          string `json:"id"`
	Layer       string `json:"layer"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Parse accepts only the versioned JSON contract, never prose or repaired JSON.
func Parse(data []byte) (Report, error) {
	var report Report
	if err := jsonutil.Check(data, MaxBytes); err != nil {
		return report, fmt.Errorf("findings JSON: %w", err)
	}
	if err := jsonutil.Object(data, "schema_version", "outcome", "findings"); err != nil {
		return report, err
	}
	var raw struct {
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return report, errors.New("findings must be an array")
	}
	if len(raw.Findings) > MaxFindings {
		return report, errors.New("too many findings")
	}
	for _, f := range raw.Findings {
		if err := jsonutil.Object(f, "id", "layer", "severity", "file", "line", "title", "description", "evidence"); err != nil {
			return report, err
		}
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, errors.New("invalid findings field type")
	}
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r Report) Validate() error {
	if r.SchemaVersion != 1 || r.Findings == nil || len(r.Findings) > MaxFindings {
		return errors.New("invalid findings version or array")
	}
	switch r.Outcome {
	case "clean":
		if len(r.Findings) != 0 {
			return errors.New("clean report contains findings")
		}
	case "findings":
		if len(r.Findings) == 0 {
			return errors.New("findings outcome requires findings")
		}
	case "incomplete":
	default:
		return errors.New("invalid findings outcome")
	}
	seen := make(map[string]bool)
	for i, f := range r.Findings {
		if !idPattern.MatchString(f.ID) || seen[f.ID] {
			return fmt.Errorf("finding %d: invalid or duplicate id", i)
		}
		seen[f.ID] = true
		if f.Layer != "correctness" && f.Layer != "security" {
			return fmt.Errorf("finding %d: invalid layer", i)
		}
		if f.Severity != "error" && f.Severity != "warning" && f.Severity != "info" {
			return fmt.Errorf("finding %d: invalid severity", i)
		}
		if !ValidPath(f.File) || f.Line < 1 {
			return fmt.Errorf("finding %d: invalid location", i)
		}
		if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Description) == "" || strings.TrimSpace(f.Evidence) == "" {
			return fmt.Errorf("finding %d: missing explanation or evidence", i)
		}
	}
	return nil
}

// ValidPath accepts normalized repository-relative slash paths, not host paths.
func ValidPath(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.HasPrefix(s, "/") &&
		!strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\:\x00\r\n") && path.Clean(s) == s
}
