package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/findings"
)

func verifierID(f Finding) string { return f.Layer + ":" + f.ID }

func (r Runner) verify(ctx context.Context, dir, diffPath string, layer config.Layer, report *Report) {
	report.Verifier = LayerResult{Name: "verification", Status: "incomplete", Attempts: []Attempt{}}
	// Model IDs are layer-scoped. Qualify them only at the verifier boundary,
	// keeping each original ID and layer unchanged in the public report.
	type input struct {
		Layer string `json:"layer"`
		findings.Candidate
	}
	candidates := make([]input, len(report.Findings))
	ids := make([]string, len(candidates))
	for i, f := range report.Findings {
		ids[i] = verifierID(f)
		candidates[i] = input{Layer: f.Layer, Candidate: f.Candidate}
		candidates[i].ID = ids[i]
	}
	data, err := json.Marshal(candidates)
	if err != nil {
		report.Verifier.Error = "scratch_error"
		return
	}
	candidatesPath := filepath.Join(dir, "candidates.json")
	if err := os.WriteFile(candidatesPath, data, 0o600); err != nil {
		report.Verifier.Error = "scratch_error"
		return
	}
	specialty, err := prompts.ReadFile("prompts/verification.txt")
	if err != nil {
		report.Verifier.Error = "prompt_error"
		return
	}
	diffJSON, _ := json.Marshal(diffPath)
	candidatesJSON, _ := json.Marshal(candidatesPath)
	rules, _ := json.Marshal(layer.Rules)
	prompt := string(specialty) + "\nDiff file: " + string(diffJSON) +
		"\nCandidate findings file: " + string(candidatesJSON) + "\nAdditional review rules: " + string(rules)
	var verdicts []findings.Verdict
	report.Verifier = r.runSession(ctx, dir, "verification", layer, prompt, func(data []byte) error {
		var err error
		verdicts, err = findings.ParseVerdicts(data, ids)
		return err
	})
	if report.Verifier.Status != "complete" {
		return
	}
	byID := make(map[string]findings.Verdict, len(verdicts))
	for _, v := range verdicts {
		byID[v.ID] = v
	}
	kept := make([]Finding, 0, len(report.Findings))
	for _, f := range report.Findings {
		v := byID[verifierID(f)]
		f.Verification = Verification{Verdict: v.Verdict, Reason: v.Reason}
		switch v.Verdict {
		case "rejected":
			report.Rejected = append(report.Rejected, f)
			continue
		case "uncertain":
			f.Rank = "lower"
		}
		kept = append(kept, f)
	}
	report.Findings = kept
}
