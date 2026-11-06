package findings

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/agendacraft/touchstone/internal/jsonutil"
)

// Verdict identifies one candidate using its layer-qualified ID.
type Verdict struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// ParseVerdicts requires exactly one verdict per expected ID. A malformed
// response is rejected in full, never salvaged or treated as partial coverage.
func ParseVerdicts(data []byte, ids []string) ([]Verdict, error) {
	entries, err := modelArray(data, 2*MaxFindings)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(ids) {
		return nil, errors.New("verdict coverage mismatch")
	}
	expected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || expected[id] {
			return nil, errors.New("invalid candidate identities")
		}
		expected[id] = true
	}
	result := make([]Verdict, 0, len(entries))
	for _, entry := range entries {
		if err := jsonutil.Object(entry, "id", "verdict", "reason"); err != nil {
			return nil, errors.New("invalid verdict fields")
		}
		var v Verdict
		if err := json.Unmarshal(entry, &v); err != nil {
			return nil, errors.New("invalid verdict field type")
		}
		if !expected[v.ID] {
			return nil, errors.New("unknown or duplicate verdict id")
		}
		delete(expected, v.ID)
		if v.Verdict != "confirmed" && v.Verdict != "rejected" && v.Verdict != "uncertain" {
			return nil, errors.New("invalid verdict")
		}
		if strings.TrimSpace(v.Reason) == "" {
			return nil, errors.New("missing verdict reason")
		}
		result = append(result, v)
	}
	return result, nil
}
