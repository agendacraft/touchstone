package findings

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

const synthetic = `{"schema_version":1,"outcome":"findings","findings":[{"id":"demo-1","layer":"correctness","severity":"error","file":"src/example.go","line":7,"title":"Synthetic defect","description":"An invented failure.","evidence":"Invented reproduction."}]}`

// The version-1 wire contract is the oracle, not the JSON decoder's defaults.
func TestParseContract(t *testing.T) {
	report, err := Parse([]byte(synthetic))
	if err != nil || len(report.Findings) != 1 || report.Findings[0].Line != 7 {
		t.Fatalf("valid synthetic report: %+v, %v", report, err)
	}
	for _, outcome := range []string{"clean", "incomplete"} {
		data := `{"schema_version":1,"outcome":"` + outcome + `","findings":[]}`
		r, err := Parse([]byte(data))
		if err != nil || r.Outcome != outcome || r.Findings == nil {
			t.Fatalf("valid %s report: %+v, %v", outcome, r, err)
		}
	}
	cases := map[string]string{
		"prose":             "No bugs found.",
		"fences":            "```json\n" + synthetic + "\n```",
		"truncated":         synthetic[:len(synthetic)-1],
		"trailing":          synthetic + `{}`,
		"unknown root":      strings.Replace(synthetic, `"schema_version":1`, `"extra":true,"schema_version":1`, 1),
		"duplicate root":    strings.Replace(synthetic, `"schema_version":1`, `"schema_version":2,"schema_version":1`, 1),
		"escaped duplicate": strings.Replace(synthetic, `"schema_version":1`, `"schema_\u0076ersion":1,"schema_version":1`, 1),
		"wrong case":        strings.Replace(synthetic, `"schema_version"`, `"Schema_version"`, 1),
		"missing version":   strings.Replace(synthetic, `"schema_version":1,`, "", 1),
		"null version":      strings.Replace(synthetic, `"schema_version":1`, `"schema_version":null`, 1),
		"wrong version":     strings.Replace(synthetic, `"schema_version":1`, `"schema_version":2`, 1),
		"unknown nested":    strings.Replace(synthetic, `"id":"demo-1"`, `"extra":true,"id":"demo-1"`, 1),
		"duplicate nested":  strings.Replace(synthetic, `"line":7`, `"line":1,"line":7`, 1),
		"missing evidence":  strings.Replace(synthetic, `,"evidence":"Invented reproduction."`, "", 1),
		"null evidence":     strings.Replace(synthetic, `"evidence":"Invented reproduction."`, `"evidence":null`, 1),
		"string line":       strings.Replace(synthetic, `"line":7`, `"line":"7"`, 1),
		"fraction line":     strings.Replace(synthetic, `"line":7`, `"line":7.5`, 1),
		"zero line":         strings.Replace(synthetic, `"line":7`, `"line":0`, 1),
		"unknown severity":  strings.Replace(synthetic, `"error"`, `"critical"`, 1),
		"unknown layer":     strings.Replace(synthetic, `"correctness"`, `"style"`, 1),
		"unknown outcome":   strings.Replace(synthetic, `"outcome":"findings"`, `"outcome":"success"`, 1),
		"empty id":          strings.Replace(synthetic, `"demo-1"`, `""`, 1),
		"comma id":          strings.Replace(synthetic, `"demo-1"`, `"a,b"`, 1),
		"empty title":       strings.Replace(synthetic, `"Synthetic defect"`, `" "`, 1),
		"clean with issues": strings.Replace(synthetic, `"outcome":"findings"`, `"outcome":"clean"`, 1),
		"null report":       "null",
		"null finding":      `{"schema_version":1,"outcome":"findings","findings":[null]}`,
		"null findings":     `{"schema_version":1,"outcome":"clean","findings":null}`,
		"missing findings":  `{"schema_version":1,"outcome":"clean"}`,
		"no issues":         `{"schema_version":1,"outcome":"findings","findings":[]}`,
		"invalid UTF8":      string([]byte{'"', 0xff, '"'}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if r, err := Parse([]byte(data)); err == nil {
				t.Fatalf("accepted malformed output: %+v", r)
			}
		})
	}
}

func TestLocations(t *testing.T) {
	for _, file := range []string{"", ".", "..", "../secret", "/tmp/example", "a/../b.go", "a//b.go", `C:\example.go`, "C:/example.go", "a\nb", "a\x00b"} {
		t.Run(file, func(t *testing.T) {
			var r Report
			if err := json.Unmarshal([]byte(synthetic), &r); err != nil {
				t.Fatal(err)
			}
			r.Findings[0].File = file
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(data); err == nil {
				t.Fatal("accepted unsafe location")
			}
		})
	}
}

func TestLimitsAndUniqueIDs(t *testing.T) {
	var r Report
	if err := json.Unmarshal([]byte(synthetic), &r); err != nil {
		t.Fatal(err)
	}
	r.Findings = append(r.Findings, r.Findings[0])
	data, _ := json.Marshal(r)
	if _, err := Parse(data); err == nil {
		t.Fatal("duplicate finding IDs accepted")
	}
	// These are protocol limits, not inferred implementation values.
	if _, err := Parse([]byte(strings.Repeat(" ", (1<<20)+1))); err == nil {
		t.Fatal("over-1-MiB report accepted")
	}
	example := r.Findings[0]
	r.Findings = make([]Finding, 500)
	for i := range r.Findings {
		r.Findings[i] = example
		r.Findings[i].ID = "synthetic-" + strconv.Itoa(i)
	}
	data, _ = json.Marshal(r)
	if parsed, err := Parse(data); err != nil || len(parsed.Findings) != 500 {
		t.Fatalf("valid 500-finding boundary rejected: %v", err)
	}
	r.Findings = append(r.Findings, example)
	data, _ = json.Marshal(r)
	if _, err := Parse(data); err == nil {
		t.Fatal("501 findings accepted")
	}
	if _, err := Parse([]byte(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66))); err == nil {
		t.Fatal("excessive nesting accepted")
	}
	clean := `{"schema_version":1,"outcome":"clean","findings":[]}`
	padded := clean + strings.Repeat(" ", (1<<20)-len(clean))
	if _, err := Parse([]byte(padded)); err != nil {
		t.Fatalf("valid 1-MiB boundary rejected: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(synthetic)
	f.Add(`{"schema_version":1,"outcome":"clean","findings":[]}`)
	f.Add(`{"findings":null}`)
	// Arbitrary model bytes must never panic, regardless of acceptance.
	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = Parse([]byte(data))
	})
}
