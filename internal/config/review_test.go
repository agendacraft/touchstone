package config

import "testing"

func TestReviewLayersAreRepositoryWideSpecialists(t *testing.T) {
	cfg, err := Parse([]byte(`
[layers.correctness]
model = "repository-model"
rules = ["Repository rule."]
[layers.verification]
model = "reserved-verifier"
[[paths]]
pattern = "src/*.go"
[paths.layers.correctness]
model = "path-model"
`))
	if err != nil {
		t.Fatal(err)
	}
	layers := cfg.ReviewLayers()
	if len(layers) != 2 || layers["correctness"].Model != "repository-model" || layers["security"].Model != "glm-5.3-flash" {
		t.Fatalf("S1 must run exactly the repository-wide specialists: %+v", layers)
	}
	layers["correctness"].Rules[0] = "Mutated."
	if cfg.ReviewLayers()["correctness"].Rules[0] != "Repository rule." {
		t.Fatal("caller changed future review policy")
	}
}
