package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/agendacraft/touchstone/internal/config"
	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/agendacraft/touchstone/internal/process"
)

// CheckResult records local execution, not model verification. Output is
// bounded, untrusted tool output and must be escaped by human renderers.
type CheckResult struct {
	Name        string   `json:"name"`
	Args        []string `json:"args"`
	Local       bool     `json:"local"`
	Status      string   `json:"status"`
	Error       string   `json:"error,omitempty"`
	ExitCode    int      `json:"exit_code"`
	Output      string   `json:"output"`
	OutputBytes int64    `json:"output_bytes"`
}

type ChecksRunner struct{ Runner process.Runner }

func (r ChecksRunner) Run(ctx context.Context, root string, cfg config.Checks, enabled bool, report *Report) {
	if len(cfg.Commands) == 0 {
		return
	}
	layer := LayerResult{Name: "deterministic", Status: "disabled", Attempts: []Attempt{}}
	if enabled {
		layer.Status = "complete"
	}
	runner := r.Runner
	if runner == nil {
		runner = process.Local{}
	}
	env := []string{}
	for _, name := range cfg.EnvAllowlist {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	for i, check := range cfg.Commands {
		result := CheckResult{Name: check.Name, Args: append([]string(nil), check.Args...), Status: "disabled", ExitCode: -1}
		if enabled {
			result, finding := runCheck(ctx, runner, root, env, check, i)
			report.Checks = append(report.Checks, result)
			if finding != nil {
				report.Findings = append(report.Findings, *finding)
				if report.Outcome != "incomplete" {
					report.Outcome = "findings"
				}
			}
			if result.Status == "incomplete" {
				report.Outcome = "incomplete"
				layer.Status, layer.Error = "incomplete", "local_check_incomplete"
			}
		} else {
			report.Checks = append(report.Checks, result)
		}
	}
	report.Layers = append(report.Layers, layer)
}

func runCheck(parent context.Context, runner process.Runner, root string, env []string, check config.Check, index int) (CheckResult, *Finding) {
	ctx, cancel := context.WithTimeout(parent, check.Duration())
	defer cancel()
	result := CheckResult{Name: check.Name, Args: append([]string(nil), check.Args...), Status: "complete", ExitCode: -1}
	run, err := runner.Run(ctx, process.Request{Args: check.Args, Dir: root, Env: env, MaxBytes: check.OutputLimit(), Combined: true, StopOnCap: true})
	result.Local = run.Started
	result.ExitCode, result.Output, result.OutputBytes = run.ExitCode, strings.ToValidUTF8(string(run.Output), "?"), run.Bytes
	switch {
	case !run.Started:
		result.Status, result.Error = "incomplete", "command could not start"
	case ctx.Err() != nil:
		result.Status, result.Error = "incomplete", "check canceled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Error = "check timeout"
		}
	case run.Bytes > int64(check.OutputLimit()):
		result.Status, result.Error = "incomplete", "check output size cap exceeded"
	case err != nil && run.ExitCode <= 0:
		result.Status, result.Error = "incomplete", "command execution failed"
	case run.ExitCode != 0:
		result.Status = "failed"
	}
	if result.Status != "failed" {
		return result, nil
	}
	file, line := checkLocation(check.LocationRegex, result.Output)
	evidence := fmt.Sprintf("Local check %q exited with status %d.", check.Name, result.ExitCode)
	if strings.TrimSpace(result.Output) != "" {
		evidence += "\n" + result.Output
	}
	return result, &Finding{Layer: "deterministic", Candidate: findings.Candidate{
		ID: fmt.Sprintf("check-%d", index+1), Severity: "error", Category: "deterministic", File: file, Line: line,
		Title: "Local check failed: " + check.Name, Evidence: evidence, Confidence: 1,
	}, Rank: "normal", Verification: Verification{Verdict: "confirmed", Reason: "Observed local command failure; no model verification."}}
}

func checkLocation(pattern, output string) (string, int) {
	if pattern == "" {
		return "", 0
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", 0
	}
	fileIndex, lineIndex := re.SubexpIndex("file"), re.SubexpIndex("line")
	if fileIndex < 1 || lineIndex < 1 {
		return "", 0
	}
	for _, match := range re.FindAllStringSubmatch(output, -1) {
		file := match[fileIndex]
		line, err := strconv.Atoi(match[lineIndex])
		if findings.ValidPath(file) && line > 0 && err == nil {
			return file, line
		}
	}
	return "", 0
}
