package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/cli"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	out := cancelWriter{ctx, os.Stdout}
	stderr := cancelWriter{ctx, os.Stderr}
	err := cli.RunContext(ctx, os.Args[1:], os.Stdin, out, stderr, version, agent.Droid{})
	code := cli.ExitCode(err)
	if code == 3 {
		fmt.Fprintln(stderr, "touchstone:", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

type cancelWriter struct {
	ctx context.Context
	out io.Writer
}

func (w cancelWriter) Write(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	data := append([]byte(nil), p...)
	go func() {
		n, err := w.out.Write(data)
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-w.ctx.Done():
		select {
		case r := <-done:
			return r.n, r.err
		case <-time.After(100 * time.Millisecond):
			return 0, w.ctx.Err()
		}
	}
}
