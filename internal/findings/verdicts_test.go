package findings

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const verdict = `{"id":"correctness:demo-1","verdict":"confirmed","reason":"The removed guard allows 10/0 at line 2."}`

// The documented verifier protocol is the oracle: exact fields, one verdict
// per candidate, and no partial salvage when coverage or syntax is malformed.
func TestParseVerdicts(t *testing.T) {
	for _, wrapper := range []string{"%s", " \r\n%s\t", "```json\n%s\n```", "```\r\n%s\r\n```"} {
		for _, value := range []string{"confirmed", "rejected", "uncertain"} {
			data := fmt.Sprintf(wrapper, "["+strings.Replace(verdict, "confirmed", value, 1)+"]")
			got, err := ParseVerdicts([]byte(data), []string{"correctness:demo-1"})
			if err != nil || len(got) != 1 || got[0].Verdict != value || got[0].Reason == "" {
				t.Fatalf("valid verdict rejected: %+v, %v", got, err)
			}
		}
	}
	got, err := ParseVerdicts([]byte("[]"), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty verdict array: %+v, %v", got, err)
	}
	for name, data := range map[string]string{
		"missing ID":        "[]",
		"unknown ID":        "[" + strings.Replace(verdict, "correctness:demo-1", "security:demo-1", 1) + "]",
		"duplicate ID":      "[" + verdict + "," + verdict + "]",
		"missing field":     `[{"id":"correctness:demo-1","verdict":"confirmed"}]`,
		"null field":        "[" + strings.Replace(verdict, `"confirmed"`, `null`, 1) + "]",
		"unknown field":     "[" + strings.Replace(verdict, `"verdict":`, `"extra":true,"verdict":`, 1) + "]",
		"case alias":        "[" + strings.Replace(verdict, `"verdict":`, `"Verdict":`, 1) + "]",
		"duplicate key":     "[" + strings.Replace(verdict, `"verdict":`, `"verdict":"rejected","verdict":`, 1) + "]",
		"escaped key":       "[" + strings.Replace(verdict, `"verdict":`, `"ver\u0064ict":"rejected","verdict":`, 1) + "]",
		"wrong type":        "[" + strings.Replace(verdict, `"confirmed"`, `true`, 1) + "]",
		"invalid verdict":   "[" + strings.Replace(verdict, "confirmed", "verified", 1) + "]",
		"empty reason":      `[{"id":"correctness:demo-1","verdict":"confirmed","reason":" \t"}]`,
		"prose":             "No issues: [" + verdict + "]",
		"trailing JSON":     "[" + verdict + "][]",
		"object":            verdict,
		"null":              "null",
		"invalid UTF8":      "[\"" + string([]byte{0xff}) + "\"]",
		"unsupported fence": "```JSON\n[" + verdict + "]\n```",
		"multiple fences":   "```\n[" + verdict + "]\n```\n```\n[]\n```",
		"oversize":          strings.Repeat(" ", MaxBytes+1),
		"deep nesting":      strings.Repeat("[", 66) + strings.Repeat("]", 66),
	} {
		t.Run(name, func(t *testing.T) {
			ids := []string{"correctness:demo-1"}
			if name == "duplicate ID" {
				ids = append(ids, "security:demo-1")
			}
			if result, err := ParseVerdicts([]byte(data), ids); err == nil || result != nil {
				t.Fatalf("malformed verdicts salvaged: %+v, %v", result, err)
			}
		})
	}
}

func TestVerdictLimitsAndCoverage(t *testing.T) {
	ids := make([]string, 1000)
	entries := make([]Verdict, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("correctness:demo-%d", i)
		entries[i] = Verdict{ID: ids[i], Verdict: "uncertain", Reason: "The diff omits caller context."}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseVerdicts(data, ids); err != nil || len(got) != 1000 {
		t.Fatalf("maximum count rejected: %d, %v", len(got), err)
	}
	// Exactly 1 MiB is accepted; one additional byte rejects the entire output.
	padded := append(append([]byte{}, data...), []byte(strings.Repeat(" ", MaxBytes-len(data)))...)
	if _, err := ParseVerdicts(padded, ids); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseVerdicts(append(padded, ' '), ids); err == nil || got != nil {
		t.Fatal("oversize response accepted")
	}
	entries = append(entries, Verdict{ID: "security:extra", Verdict: "rejected", Reason: "Not changed."})
	ids = append(ids, "security:extra")
	data, _ = json.Marshal(entries)
	if got, err := ParseVerdicts(data, ids); err == nil || got != nil {
		t.Fatal("1001 verdicts accepted")
	}
	for _, bad := range [][]string{{""}, {"correctness:demo-1", "correctness:demo-1"}} {
		data := "[" + verdict + "]"
		if len(bad) == 2 {
			data = "[" + verdict + "," + verdict + "]"
		}
		if _, err := ParseVerdicts([]byte(data), bad); err == nil {
			t.Fatal("invalid candidate identities accepted")
		}
	}
}
