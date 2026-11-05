// Package cli implements the foundation commands without running reviews.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/agendacraft/touchstone/internal/config"
)

var ErrNotImplemented = errors.New("review is not implemented; Touchstone is not usable yet")

const usage = `Touchstone (early development, not usable yet)
Usage:
  touchstone review
  touchstone version
  touchstone config validate [--cwd DIRECTORY]
`

func Run(args []string, out, stderr io.Writer, version string) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		_, err := io.WriteString(out, usage)
		return err
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return errors.New("version takes no arguments")
		}
		_, err := fmt.Fprintf(out, "touchstone %s\n", version)
		return err
	case "review":
		if len(args) != 1 {
			return errors.New("review takes no arguments in this scaffold")
		}
		return ErrNotImplemented
	case "config":
		if len(args) < 2 || args[1] != "validate" {
			return errors.New("expected: touchstone config validate [--cwd DIRECTORY]")
		}
		fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		cwd := fs.String("cwd", "", "repository directory (defaults to the current directory)")
		if err := fs.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("config validate takes no positional arguments")
		}
		if *cwd == "" {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			*cwd = dir
		}
		info, err := os.Stat(*cwd)
		if err != nil || !info.IsDir() {
			return errors.New("cwd must be an existing directory")
		}
		if _, err := config.Load(*cwd); err != nil {
			return fmt.Errorf("configuration invalid: %w", err)
		}
		_, err = fmt.Fprintln(out, "configuration valid (built-in defaults plus optional touchstone.toml)")
		return err
	default:
		return errors.New("unknown command; run touchstone --help")
	}
}
