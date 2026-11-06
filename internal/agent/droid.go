package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agendacraft/touchstone/internal/findings"
	"github.com/agendacraft/touchstone/internal/jsonutil"
)

const maxEnvelope = 2 << 20

type commandRunner func(context.Context, string, []string, string, io.Writer, io.Writer) error

// Droid starts a fresh exec session for every Run. Binary defaults to "droid".
type Droid struct {
	Binary string
	run    commandRunner
}

func (d Droid) Run(ctx context.Context, r Request) (Result, error) {
	if err := r.Validate(); err != nil {
		return Result{}, &RunError{Kind: InvalidRequest, ExitCode: -1, Cause: err}
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "touchstone-run-")
	if err != nil {
		return Result{}, &RunError{Kind: ProcessFailure, ExitCode: -1, Cause: err}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	promptPath := filepath.Join(dir, "prompt.txt")
	settingsPath := filepath.Join(dir, "settings.json")
	settings := struct {
		HooksDisabled          bool `json:"hooksDisabled"`
		CloudSessionSync       bool `json:"cloudSessionSync"`
		SessionDefaultSettings struct {
			AutonomyLevel   string `json:"autonomyLevel"`
			InteractionMode string `json:"interactionMode"`
		} `json:"sessionDefaultSettings"`
	}{HooksDisabled: true}
	settings.SessionDefaultSettings.AutonomyLevel = "off"
	settings.SessionDefaultSettings.InteractionMode = "auto"
	data, err := json.Marshal(settings)
	if err == nil {
		err = os.WriteFile(settingsPath, data, 0o600)
	}
	if err == nil {
		err = os.WriteFile(promptPath, []byte(r.Prompt), 0o600)
	}
	if err != nil {
		return Result{}, &RunError{Kind: ProcessFailure, ExitCode: -1, Cause: err}
	}
	args := []string{"exec", "-o", "json", "-f", promptPath, "--cwd", r.WorkingDirectory,
		"-m", r.Model, "-r", r.ReasoningEffort, "--settings", settingsPath, "--disable-builtin-skills"}
	args = append(args, "--only-tools", "Read,Grep,Glob,LS")
	binary := d.Binary
	if binary == "" {
		binary = "droid"
	}
	run := d.run
	if run == nil {
		run = runCommand
	}
	out, stderr := &boundedBuffer{limit: maxEnvelope}, &boundedBuffer{limit: 64 << 10}
	runErr := run(ctx, binary, args, r.WorkingDirectory, out, stderr)
	result, parseErr := parseEnvelopeOutput(out.Bytes(), r.RawOutput)
	if ctx.Err() != nil {
		kind := Canceled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = Timeout
		}
		return Result{}, &RunError{Kind: kind, ExitCode: -1, Cause: ctx.Err()}
	}
	if runErr != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			code = exit.ExitCode()
		}
		// Preserve structured metadata when available, but never return a report.
		result.Report = findings.Report{}
		result.Output = ""
		return result, &RunError{Kind: ProcessFailure, ExitCode: code}
	}
	if out.overflow || stderr.overflow || parseErr != nil {
		result.Report = findings.Report{}
		result.Output = ""
		return result, &RunError{Kind: InvalidOutput, ExitCode: 0}
	}
	return result, nil
}

func runCommand(ctx context.Context, binary string, args []string, cwd string, out, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = cwd, out, stderr
	cmd.WaitDelay = time.Second
	prepareCommand(cmd)
	return cmd.Run()
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func rejectFieldAliases(data []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return errors.New("expected JSON object")
	}
	for key := range object {
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				return errors.New("invalid JSON field alias")
			}
		}
	}
	return nil
}

func parseEnvelope(data []byte) (Result, error) {
	return parseEnvelopeOutput(data, false)
}

func parseEnvelopeOutput(data []byte, raw bool) (Result, error) {
	var result Result
	if err := jsonutil.Check(data, maxEnvelope); err != nil {
		return result, err
	}
	if err := rejectFieldAliases(data, "type", "subtype", "is_error", "result", "session_id", "usage"); err != nil {
		return result, err
	}
	var envelope struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		IsError   *bool           `json:"is_error"`
		Text      *string         `json:"result"`
		SessionID string          `json:"session_id"`
		Usage     json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return result, errors.New("invalid Droid envelope")
	}
	if envelope.Type != "result" || envelope.IsError == nil || envelope.Text == nil ||
		strings.TrimSpace(envelope.SessionID) == "" {
		return result, errors.New("missing Droid envelope fields")
	}
	result.SessionID = envelope.SessionID
	if len(envelope.Usage) > 0 && string(envelope.Usage) != "null" {
		if err := rejectFieldAliases(envelope.Usage, "input_tokens", "output_tokens", "cache_read_input_tokens",
			"cache_creation_input_tokens", "factory_credits", "thinking_tokens"); err != nil {
			return Result{}, errors.New("invalid Droid usage")
		}
		if err := json.Unmarshal(envelope.Usage, &result.Usage); err != nil {
			return Result{}, errors.New("invalid Droid usage")
		}
		for _, value := range []*int64{result.Usage.InputTokens, result.Usage.OutputTokens,
			result.Usage.CacheReadTokens, result.Usage.CacheCreationTokens, result.Usage.FactoryCredits, result.Usage.ThinkingTokens} {
			if value != nil && *value < 0 {
				return Result{}, errors.New("negative Droid usage")
			}
		}
	}
	if *envelope.IsError || envelope.Subtype != "success" {
		return result, errors.New("Droid reported failure")
	}
	if raw {
		result.Output = *envelope.Text
		return result, nil
	}
	report, err := findings.Parse([]byte(*envelope.Text))
	if err != nil {
		return result, err
	}
	result.Report = report
	return result, nil
}
