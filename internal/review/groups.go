package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/patch"
)

func EmptyReport() Report {
	return Report{SchemaVersion: 2, Outcome: "clean", Findings: []Finding{}, Rejected: []Finding{}, Layers: []LayerResult{},
		Verifier: LayerResult{Name: "verification", Status: "not_needed", Attempts: []Attempt{}}}
}

func IncompleteDiff(size int64) Report {
	report := EmptyReport()
	report.Outcome = "incomplete"
	report.Layers = append(report.Layers, LayerResult{Name: "input", Status: "incomplete", Error: fmt.Sprintf("diff too large, %d bytes", size), Attempts: []Attempt{}})
	return report
}

type pathGroup struct {
	diff   bytes.Buffer
	layers map[string]config.Layer
	paths  []string
}

func (r Runner) runGroups(ctx context.Context, diff []byte, cfg config.Config) (Report, error) {
	files, err := patch.Split(diff)
	if err != nil {
		return Report{}, err
	}
	var groups []*pathGroup
	byPolicy := map[string]*pathGroup{}
	for _, file := range files {
		if file.Binary {
			continue
		}
		layers, err := cfg.ForPath(file.Path)
		if err != nil {
			return Report{}, err
		}
		key, err := json.Marshal(layers)
		if err != nil {
			return Report{}, err
		}
		group := byPolicy[string(key)]
		if group == nil {
			group = &pathGroup{layers: layers}
			byPolicy[string(key)] = group
			groups = append(groups, group)
		}
		group.diff.Write(file.Data)
		group.paths = append(group.paths, file.Path)
	}
	report := EmptyReport()
	for i, group := range groups {
		result, err := r.runSingle(ctx, group.diff.Bytes(), cfg.WithLayers(group.layers))
		if err != nil {
			return Report{}, err
		}
		if len(groups) > 1 {
			markGroup(&result, fmt.Sprintf("g%d", i+1))
		}
		for i := range result.Layers {
			result.Layers[i].Paths = append([]string(nil), group.paths...)
		}
		report.Findings = append(report.Findings, result.Findings...)
		report.Rejected = append(report.Rejected, result.Rejected...)
		report.Layers = append(report.Layers, result.Layers...)
		if result.Outcome == "incomplete" {
			report.Outcome = "incomplete"
		}
		mergeVerifier(&report.Verifier, result.Verifier)
	}
	if report.Outcome != "incomplete" && len(report.Findings) > 0 {
		report.Outcome = "findings"
	}
	return report, nil
}

func markGroup(report *Report, group string) {
	for i := range report.Findings {
		report.Findings[i].Group = group
	}
	for i := range report.Rejected {
		report.Rejected[i].Group = group
	}
	for i := range report.Layers {
		report.Layers[i].Group = group
	}
}

func mergeVerifier(dst *LayerResult, src LayerResult) {
	dst.Attempts = append(dst.Attempts, src.Attempts...)
	order := map[string]int{"not_needed": 0, "disabled": 1, "complete": 2, "incomplete": 3}
	if order[src.Status] >= order[dst.Status] {
		dst.Status, dst.Error = src.Status, src.Error
	}
}
