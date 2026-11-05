// Package jsonutil provides bounded JSON decoding with duplicate-key rejection.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Check rejects trailing values, duplicate object keys, and excessive nesting.
func Check(data []byte, limit int) error {
	if len(data) > limit {
		return errors.New("JSON exceeds size limit")
	}
	if !utf8.Valid(data) {
		return errors.New("JSON must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := value(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func value(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("invalid or duplicate JSON key")
			}
			seen[s] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

// Object requires exactly the named, case-sensitive, non-null fields.
func Object(data []byte, fields ...string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return errors.New("expected JSON object")
	}
	if len(obj) != len(fields) {
		return errors.New("unexpected or missing JSON fields")
	}
	for _, field := range fields {
		v, ok := obj[field]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errors.New("missing or null JSON field: " + field)
		}
	}
	return nil
}
