package jsonutil

import (
	"strings"
	"testing"
)

func TestJSONSyntaxAndKeys(t *testing.T) {
	for _, data := range []string{`{}`, `[]`, `{"nested":[{"a":1},{"a":2}]}`, `{"a":null}`, `"string"`, `1`, `true`} {
		if err := Check([]byte(data), 1024); err != nil {
			t.Fatalf("valid JSON rejected: %s: %v", data, err)
		}
	}
	for _, data := range []string{``, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"nested":{"a":1,"a":2}}`,
		`[1,]`, `[`, `{"a":`, `{1:true}`, `[] {}`, `{"a":1]`, `}`, strings.Repeat(" ", 1025)} {
		if err := Check([]byte(data), 1024); err == nil {
			t.Fatalf("invalid JSON accepted: %s", data)
		}
	}
}

func TestExactObjectShape(t *testing.T) {
	if err := Object([]byte(`{"a":1,"b":[]}`), "a", "b"); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`null`, `[]`, `{}`, `{"a":1}`, `{"A":1,"b":[]}`, `{"a":null,"b":[]}`, `{"a":1,"b":[],"c":1}`} {
		if err := Object([]byte(data), "a", "b"); err == nil {
			t.Fatalf("non-exact object accepted: %s", data)
		}
	}
}
