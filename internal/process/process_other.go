//go:build !unix

package process

import "os/exec"

func prepare(_ *exec.Cmd) {}
