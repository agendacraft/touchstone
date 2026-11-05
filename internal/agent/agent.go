// Package agent defines a single, independent review-layer invocation.
package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/agendacraft/touchstone/internal/findings"
)

type Request struct {
	Prompt           string
	Model            string
	ReasoningEffort  string
	WorkingDirectory string
	Timeout          time.Duration
}

func ValidEffort(s string) bool {
	switch s {
	case "none", "off", "minimal", "low", "medium", "high", "xhigh", "max", "dynamic":
		return true
	}
	return false
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.Prompt) == "" || strings.TrimSpace(r.Model) == "" ||
		!ValidEffort(r.ReasoningEffort) ||
		!filepath.IsAbs(r.WorkingDirectory) || r.Timeout <= 0 {
		return errors.New("invalid agent request")
	}
	return nil
}

type Usage struct {
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens *int64 `json:"cache_creation_input_tokens"`
	FactoryCredits      *int64 `json:"factory_credits"`
	ThinkingTokens      *int64 `json:"thinking_tokens"`
}

// Cost is nil when the provider does not report a currency amount.
type Cost struct {
	Amount   float64
	Currency string
}

type Result struct {
	Report    findings.Report
	Usage     Usage
	Cost      *Cost
	SessionID string
}

type Agent interface {
	Run(context.Context, Request) (Result, error)
}

type ErrorKind string

const (
	InvalidRequest ErrorKind = "invalid_request"
	ProcessFailure ErrorKind = "process_failure"
	Timeout        ErrorKind = "timeout"
	Canceled       ErrorKind = "canceled"
	InvalidOutput  ErrorKind = "invalid_output"
)

// RunError excludes raw subprocess output, which may contain sensitive data.
type RunError struct {
	Kind     ErrorKind
	ExitCode int // -1 when unavailable
	Cause    error
}

func (e *RunError) Error() string { return "agent run: " + string(e.Kind) }
func (e *RunError) Unwrap() error { return e.Cause }

// Fake runs a caller-supplied deterministic response without a model or account.
type Fake struct {
	Respond func(context.Context, Request) (Result, error)
}

func (f Fake) Run(ctx context.Context, r Request) (Result, error) {
	if err := r.Validate(); err != nil {
		return Result{}, &RunError{Kind: InvalidRequest, ExitCode: -1, Cause: err}
	}
	if f.Respond == nil {
		return Result{}, errors.New("fake agent has no response")
	}
	return f.Respond(ctx, r)
}
