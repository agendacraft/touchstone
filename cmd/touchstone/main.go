package main

import (
	"fmt"
	"os"

	"github.com/agendacraft/touchstone/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr, version); err != nil {
		fmt.Fprintln(os.Stderr, "touchstone:", err)
		os.Exit(1)
	}
}
