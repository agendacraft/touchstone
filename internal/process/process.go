// Package process runs argument vectors with bounded capture and no shell.
package process

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

type Request struct {
	Args      []string
	Dir       string
	Env       []string
	MaxBytes  int
	Combined  bool
	StopOnCap bool
}

type Result struct {
	Output   []byte
	Bytes    int64
	ExitCode int
	Started  bool
}

type Runner interface {
	Run(context.Context, Request) (Result, error)
}

type Local struct{}

func (Local) Run(ctx context.Context, r Request) (Result, error) {
	if len(r.Args) == 0 || r.Args[0] == "" || r.MaxBytes < 1 {
		return Result{}, errors.New("invalid command request")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Args[0], r.Args[1:]...)
	cmd.Dir, cmd.Env = r.Dir, r.Env
	cmd.WaitDelay = time.Second
	prepare(cmd)
	capture := &bounded{limit: r.MaxBytes}
	if r.StopOnCap {
		capture.overflow = cancel
	}
	cmd.Stdout = capture
	if r.Combined {
		cmd.Stderr = capture
	}
	// Stderr is discarded unless the caller explicitly requests combined
	// capture. Execution errors remain structured, never raw diagnostics.
	err := cmd.Run()
	result := Result{Output: capture.data, Bytes: capture.total, ExitCode: -1, Started: cmd.Process != nil}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	return result, err
}

type bounded struct {
	mu       sync.Mutex
	data     []byte
	total    int64
	limit    int
	overflow context.CancelFunc
}

func (b *bounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	keep := min(len(p), b.limit-len(b.data))
	b.data = append(b.data, p[:keep]...)
	if b.total > int64(b.limit) && b.overflow != nil {
		b.overflow()
	}
	return len(p), nil
}
