//go:build windows

package procgroup

import "os/exec"

// Prepare does nothing on Windows; Kill ends the process itself.
func Prepare(cmd *exec.Cmd) {}

// Kill ends the process.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
