package eval

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func singleCase(t *testing.T) fstest.MapFS {
	t.Helper()
	source := Bundled()
	files := fstest.MapFS{}
	for _, name := range []string{"diff.patch", "expected.json"} {
		data, err := fs.ReadFile(source, "01-index-boundary/"+name)
		if err != nil {
			t.Fatal(err)
		}
		files["one/"+name] = &fstest.MapFile{Data: data}
	}
	return files
}

func TestLoadBundledCases(t *testing.T) {
	cases, err := Load(Bundled())
	if err != nil {
		t.Fatal(err)
	}
	clean, holdout, public := 0, 0, 0
	difficulty := map[string]bool{}
	for _, c := range cases {
		difficulty[c.Truth.Difficulty] = true
		if measure(c.Truth, nil, "overall").Bugs == 0 {
			clean++
		}
		if c.Truth.Holdout {
			holdout++
		}
		if strings.HasPrefix(c.Name, "13-public") || strings.HasPrefix(c.Name, "14-public") {
			public++
			fix, err := fs.ReadFile(Bundled(), c.Name+"/fix.patch")
			if err != nil || len(fix) == 0 {
				t.Fatalf("missing public answer patch: %v", err)
			}
		}
	}
	if len(cases) < 12 || clean < 5 || holdout < 4 || public < 2 || len(difficulty) != 3 {
		t.Fatalf("corpus: %d cases, %d clean, %d holdout, %d public, difficulties %v", len(cases), clean, holdout, public, difficulty)
	}
}

func TestLoadRejectsInvalidTruth(t *testing.T) {
	for _, mutate := range []func(*Truth){
		func(v *Truth) { v.Version = 2 },
		func(v *Truth) { v.Source = "" },
		func(v *Truth) { v.Difficulty = "unknown" },
		func(v *Truth) { v.Findings[0].Start = 0 },
		func(v *Truth) { v.Findings[0].End = 1 },
		func(v *Truth) { v.Findings[0].File = "../a.go" },
		func(v *Truth) { v.Findings[0].File = "unrelated.go" },
		func(v *Truth) { v.Findings[0].Kind = "maybe" },
		func(v *Truth) { v.Findings[0].Severity = "high" },
		func(v *Truth) { v.Findings[0].Aliases = []string{""} },
		func(v *Truth) { v.Findings = append(v.Findings, v.Findings[0]) },
	} {
		source := singleCase(t)
		var truth Truth
		if err := json.Unmarshal(source["one/expected.json"].Data, &truth); err != nil {
			t.Fatal(err)
		}
		mutate(&truth)
		data, err := json.Marshal(truth)
		if err != nil {
			t.Fatal(err)
		}
		source["one/expected.json"].Data = data
		if _, err := Load(source); err == nil {
			t.Fatalf("accepted invalid truth: %+v", truth)
		}
	}
}

func TestLoadFailureAndFingerprint(t *testing.T) {
	source := singleCase(t)
	first, err := Load(source)
	if err != nil {
		t.Fatal(err)
	}
	source["one/expected.json"].Data = append(source["one/expected.json"].Data, '\n')
	second, err := Load(source)
	if err != nil || first[0].Fingerprint == second[0].Fingerprint {
		t.Fatal("expectation drift was not fingerprinted")
	}
	source["one/expected.json"].Data = append(source["one/expected.json"].Data, []byte("{}")...)
	if _, err := Load(source); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	delete(source, "one/expected.json")
	if _, err := Load(source); err == nil {
		t.Fatal("missing truth accepted")
	}
	if _, err := Load(fstest.MapFS{}); err == nil {
		t.Fatal("empty suite accepted")
	}
}
