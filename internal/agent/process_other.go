//go:build !unix

package agent

import "os/exec"

// CommandContext kills the direct child on platforms without Unix process groups.
func prepareCommand(_ *exec.Cmd) {}
