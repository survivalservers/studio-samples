//go:build unix

// Package procgroup lets a test stop everything a program it started has started: the program runs in a process
// group of its own, and Kill ends the whole group, so no game server or helper outlives its test.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Prepare puts the command in a new process group. Call before Start.
func Prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// If the test program itself dies (a time-out, Ctrl+C), cleanup never runs: where the system allows, the child
	// is told to die with it.
	dieWithParent(cmd.SysProcAttr)
}

// Kill ends the command's whole process group. Safe to call more than once, and before the command started.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
