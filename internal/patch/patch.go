// Package patch splits unified git patches without interpreting hunk contents.
package patch

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	"github.com/agendacraft/touchstone/internal/findings"
)

type File struct {
	Path    string
	OldPath string
	Data    []byte
	Binary  bool
}

func Split(data []byte) ([]File, error) {
	var files []File
	var block bytes.Buffer
	var hunk hunkState
	sawNewHeader := false
	finish := func() error {
		if len(bytes.TrimSpace(block.Bytes())) == 0 {
			return nil
		}
		file, err := parse(block.Bytes())
		if err != nil {
			return err
		}
		files = append(files, file)
		block = bytes.Buffer{}
		return nil
	}
	for line := range bytes.Lines(data) {
		gitHeader := bytes.HasPrefix(line, []byte("diff --git "))
		plainHeader := !hunk.active() && sawNewHeader && bytes.HasPrefix(line, []byte("--- "))
		if gitHeader || plainHeader {
			if err := finish(); err != nil {
				return nil, err
			}
			hunk, sawNewHeader = hunkState{}, false
		}
		if !hunk.active() && bytes.HasPrefix(line, []byte("+++ ")) {
			sawNewHeader = true
		}
		block.Write(line)
		hunk.consume(line)
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return files, nil
}

type hunkState struct{ old, new int }

func (h hunkState) active() bool { return h.old > 0 || h.new > 0 }

func (h *hunkState) consume(line []byte) {
	if bytes.HasPrefix(line, []byte("@@ ")) {
		fields := strings.Fields(string(line))
		if len(fields) >= 4 {
			h.old, h.new = rangeCount(fields[1]), rangeCount(fields[2])
		}
		return
	}
	if !h.active() {
		return
	}
	switch {
	case bytes.HasPrefix(line, []byte("-")):
		h.old--
	case bytes.HasPrefix(line, []byte("+")):
		h.new--
	case bytes.HasPrefix(line, []byte(" ")):
		h.old--
		h.new--
	}
}

func rangeCount(field string) int {
	_, count, found := strings.Cut(field, ",")
	if !found {
		return 1
	}
	n, _ := strconv.Atoi(count)
	return n
}

func parse(data []byte) (File, error) {
	f := File{Data: data}
	for raw := range bytes.Lines(data) {
		line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if strings.HasPrefix(line, "@@") {
			break
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			f.OldPath, f.Path = headerPaths(strings.TrimPrefix(line, "diff --git "))
		case strings.HasPrefix(line, "--- "):
			f.OldPath = stripPrefix(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ "):
			name := stripPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if name != "" {
				f.Path = name
			} else {
				f.Path = f.OldPath
			}
		case strings.HasPrefix(line, "rename from "):
			f.OldPath = unquote(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			f.Path = unquote(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			f.Binary = true
		}
	}
	if !findings.ValidPath(f.Path) || (f.OldPath != "" && !findings.ValidPath(f.OldPath)) {
		return File{}, errors.New("cannot resolve normalized repository-relative paths in diff")
	}
	return f, nil
}

func unquote(s string) string {
	if strings.HasPrefix(s, `"`) {
		value, err := strconv.Unquote(s)
		if err != nil {
			return ""
		}
		return value
	}
	return s
}

func stripPrefix(s, prefix string) string {
	// Non-git unified patches may append a tab-delimited timestamp.
	s, _, _ = strings.Cut(s, "\t")
	s = unquote(s)
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

func headerPaths(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		// Git quotes each name independently when it contains control bytes.
		quoted, err := strconv.QuotedPrefix(s)
		if err == nil {
			return strings.TrimPrefix(unquote(quoted), "a/"), strings.TrimPrefix(unquote(strings.TrimSpace(strings.TrimPrefix(s, quoted))), "b/")
		}
	}
	// Unquoted spaces are legal in git headers. For unchanged names, compare
	// both halves rather than assuming the first space separates the names.
	for before, after, found := strings.Cut(s, " b/"); found; {
		old := strings.TrimPrefix(before, "a/")
		if old == after {
			return old, after
		}
		nextBefore, nextAfter, nextFound := strings.Cut(after, " b/")
		before += " b/" + nextBefore
		after, found = nextAfter, nextFound
	}
	old, name, _ := strings.Cut(s, " b/")
	return strings.TrimPrefix(old, "a/"), name
}
