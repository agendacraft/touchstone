package findings

import (
	"strconv"
	"strings"
	"testing"
)

const validCandidate = `{"id":"demo-1","severity":"warning","category":"logic","file":"src/example.go","line":7,"title":"Synthetic defect","evidence":"An invented reproduction.","confidence":0.8}`

// Oracle: the documented specialist array contract, not permissive decoding.
func TestCandidateContract(t *testing.T) {
	for _, input := range []string{"[]", "[" + validCandidate + "]", "Review:\n```json\n[" + validCandidate + "]\n```\nDone."} {
		result, err := ParseCandidates([]byte(input))
		if err != nil || result == nil {
			t.Fatalf("valid array rejected: %+v, %v", result, err)
		}
	}
	for _, confidence := range []string{"0", "1"} {
		if _, err := ParseCandidates([]byte("[" + strings.Replace(validCandidate, "0.8", confidence, 1) + "]")); err != nil {
			t.Fatalf("confidence boundary rejected: %v", err)
		}
	}
	for name, input := range map[string]string{
		"text": "No findings", "null": "null", "object": `{"findings":[]}`,
		"truncated": "[{", "null entry": "[null]", "two arrays": "[]\n" + "[" + validCandidate + "]",
		"invalid first": "[not JSON] followed by []", "trailing object": "[]{}",
		"unknown":           strings.Replace(validCandidate, `"id":`, `"extra":1,"id":`, 1),
		"duplicate key":     strings.Replace(validCandidate, `"line":7`, `"line":1,"line":7`, 1),
		"escaped duplicate": strings.Replace(validCandidate, `"line":7`, `"li\u006ee":1,"line":7`, 1),
		"alias":             strings.Replace(validCandidate, `"line"`, `"Line"`, 1),
		"missing":           strings.Replace(validCandidate, `,"confidence":0.8`, "", 1),
		"null field":        strings.Replace(validCandidate, `"confidence":0.8`, `"confidence":null`, 1),
		"fraction line":     strings.Replace(validCandidate, `"line":7`, `"line":1.5`, 1),
		"string line":       strings.Replace(validCandidate, `"line":7`, `"line":"7"`, 1),
		"zero line":         strings.Replace(validCandidate, `"line":7`, `"line":0`, 1),
		"unsafe path":       strings.Replace(validCandidate, "src/example.go", "../secret", 1),
		"empty title":       strings.Replace(validCandidate, "Synthetic defect", " ", 1),
		"empty category":    strings.Replace(validCandidate, "logic", "", 1),
		"empty evidence":    strings.Replace(validCandidate, "An invented reproduction.", "", 1),
		"bad id":            strings.Replace(validCandidate, "demo-1", "a,b", 1),
		"severity":          strings.Replace(validCandidate, "warning", "critical", 1),
		"low confidence":    strings.Replace(validCandidate, "0.8", "-0.01", 1),
		"high confidence":   strings.Replace(validCandidate, "0.8", "1.01", 1),
		"overflow":          strings.Replace(validCandidate, "0.8", "1e999", 1),
		"string confidence": strings.Replace(validCandidate, "0.8", `"0.8"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(input, "{") && name != "object" {
				input = "[" + input + "]"
			}
			if _, err := ParseCandidates([]byte(input)); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestCandidateLimits(t *testing.T) {
	if _, err := ParseCandidates([]byte("[" + validCandidate + "," + validCandidate + "]")); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
	entries := make([]string, 500)
	for i := range entries {
		entries[i] = strings.Replace(validCandidate, "demo-1", "demo-"+strconv.Itoa(i), 1)
	}
	atLimit := "[" + strings.Join(entries, ",") + "]"
	if result, err := ParseCandidates([]byte(atLimit)); err != nil || len(result) != 500 {
		t.Fatalf("500 findings boundary rejected: %v", err)
	}
	if _, err := ParseCandidates([]byte(atLimit[:len(atLimit)-1] + "," + validCandidate + "]")); err == nil {
		t.Fatal("501 findings accepted")
	}
	padded := "[]" + strings.Repeat(" ", (1<<20)-2)
	if _, err := ParseCandidates([]byte(padded)); err != nil {
		t.Fatal("1 MiB boundary rejected")
	}
	for _, data := range [][]byte{
		[]byte(padded + " "), {'[', ']', 0xff},
		[]byte(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)),
	} {
		if _, err := ParseCandidates(data); err == nil {
			t.Fatal("size, encoding, or depth limit bypassed")
		}
	}
}

func FuzzParseCandidates(f *testing.F) {
	f.Add("[]")
	f.Add("[" + validCandidate + "]")
	f.Add("```json\n[]\n```")
	f.Fuzz(func(_ *testing.T, input string) {
		_, _ = ParseCandidates([]byte(input))
	})
}
