package patch

import (
	"bytes"
	"strings"
	"testing"
)

func TestSplitPreservesFileBoundaries(t *testing.T) {
	for _, gitHeaders := range []bool{false, true} {
		first := "--- a/first.go\n+++ b/first.go\n@@ -1,2 +1,2 @@\n--- header-looking content\n+++ header-looking content\n same\n"
		second := "--- a/second.go\n+++ b/second.go\n@@ -1 +1 @@\n-old\n+new"
		if gitHeaders {
			first = "diff --git a/first.go b/first.go\n" + first
			second = "diff --git a/second.go b/second.go\n" + second
		}
		files, err := Split([]byte(first + second))
		if err != nil || len(files) != 2 || files[0].Path != "first.go" || files[1].Path != "second.go" ||
			string(files[0].Data) != first || string(files[1].Data) != second {
			t.Fatalf("patch bytes/boundaries lost: %+v %v", files, err)
		}
	}
}

func TestSpecialPatchPaths(t *testing.T) {
	for _, tc := range []struct {
		data, old, path string
		binary          bool
	}{
		{"diff --git a/name b/part b/name b/part\nold mode 100644\nnew mode 100755\n", "name b/part", "name b/part", false},
		{"diff --git \"a/caf\\303\\251.go\" \"b/caf\\303\\251.go\"\nold mode 100644\nnew mode 100755\n", "café.go", "café.go", false},
		{"diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n", "old.go", "new.go", false},
		{"diff --git a/blob.bin b/blob.bin\nBinary files a/blob.bin and b/blob.bin differ\n", "blob.bin", "blob.bin", true},
		{"--- a/deleted.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n", "deleted.go", "deleted.go", false},
		{"--- /dev/null\n+++ b/added.go\n@@ -0,0 +1 @@\n+new\n", "", "added.go", false},
	} {
		files, err := Split([]byte(tc.data))
		if err != nil || len(files) != 1 || files[0].Path != tc.path || files[0].OldPath != tc.old || files[0].Binary != tc.binary ||
			!bytes.Equal(files[0].Data, []byte(tc.data)) {
			t.Fatalf("path/bytes mismatch: %+v %v", files, err)
		}
	}
	for _, data := range []string{
		"--- a/../escape\n+++ b/../escape\n",
		"diff --git \"a/line\\nfile\" \"b/line\\nfile\"\n",
		"diff --git a/src/file b/src/file\n--- a/src/file\n+++ b/../escape\n",
	} {
		if _, err := Split([]byte(data)); err == nil {
			t.Fatalf("unsafe path accepted: %s", strings.TrimSpace(data))
		}
	}
}

func TestCRLFPatchPreservesBytes(t *testing.T) {
	const data = "--- a/example.go\r\n+++ b/example.go\r\n@@ -1 +1 @@\r\n-old\r\n+new\r\n"
	files, err := Split([]byte(data))
	if err != nil || len(files) != 1 || files[0].Path != "example.go" || string(files[0].Data) != data {
		t.Fatalf("CRLF input changed: %+v %v", files, err)
	}
}
