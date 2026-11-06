package findings

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/agendacraft/touchstone/internal/jsonutil"
)

// Candidate is the specialist model's output, before any independent verifier.
type Candidate struct {
	ID         string  `json:"id"`
	Severity   string  `json:"severity"`
	Category   string  `json:"category"`
	File       string  `json:"file"`
	Line       int     `json:"line"`
	Title      string  `json:"title"`
	Evidence   string  `json:"evidence"`
	Confidence float64 `json:"confidence"`
}

// ParseCandidates accepts one JSON array, optionally in one json or bare fence.
func ParseCandidates(data []byte) ([]Candidate, error) {
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return nil, errors.New("invalid model output size or encoding")
	}
	raw := bytes.Trim(data, " \t\r\n")
	if bytes.HasPrefix(raw, []byte("```")) {
		newline := bytes.IndexByte(raw, '\n')
		if newline < 0 {
			return nil, errors.New("invalid findings fence")
		}
		header := bytes.TrimSuffix(raw[:newline], []byte("\r"))
		if !bytes.Equal(header, []byte("```")) && !bytes.Equal(header, []byte("```json")) {
			return nil, errors.New("invalid findings fence")
		}
		body := raw[newline+1:]
		if !bytes.HasSuffix(body, []byte("\n```")) {
			return nil, errors.New("invalid findings fence")
		}
		raw = bytes.Trim(body[:len(body)-4], " \t\r\n")
	}
	if err := jsonutil.Check(raw, MaxBytes); err != nil {
		return nil, errors.New("invalid findings JSON")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil || len(entries) > MaxFindings {
		return nil, errors.New("invalid findings array")
	}
	result := make([]Candidate, 0, len(entries))
	seen := make(map[string]bool)
	for _, entry := range entries {
		if err := jsonutil.Object(entry, "id", "severity", "category", "file", "line", "title", "evidence", "confidence"); err != nil {
			return nil, errors.New("invalid finding fields")
		}
		var f Candidate
		if err := json.Unmarshal(entry, &f); err != nil {
			return nil, errors.New("invalid finding field type")
		}
		if !idPattern.MatchString(f.ID) || seen[f.ID] {
			return nil, errors.New("invalid or duplicate finding id")
		}
		seen[f.ID] = true
		if f.Severity != "error" && f.Severity != "warning" && f.Severity != "info" {
			return nil, errors.New("invalid finding severity")
		}
		if !ValidPath(f.File) || f.Line < 1 {
			return nil, errors.New("invalid finding location")
		}
		if strings.TrimSpace(f.Category) == "" || strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Evidence) == "" {
			return nil, errors.New("missing finding explanation")
		}
		if math.IsNaN(f.Confidence) || math.IsInf(f.Confidence, 0) || f.Confidence < 0 || f.Confidence > 1 {
			return nil, errors.New("invalid finding confidence")
		}
		result = append(result, f)
	}
	return result, nil
}
