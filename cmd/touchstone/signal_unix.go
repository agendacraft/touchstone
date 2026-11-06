//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

func ignoreBrokenPipe() {
	signal.Ignore(syscall.SIGPIPE)
}
