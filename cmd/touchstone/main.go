package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/cli"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := cli.RunContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version, agent.Droid{})
	code := cli.ExitCode(err)
	if code == 3 {
		fmt.Fprintln(os.Stderr, "touchstone:", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}
