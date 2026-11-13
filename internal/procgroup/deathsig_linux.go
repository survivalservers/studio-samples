//go:build linux

package procgroup

import "syscall"

func dieWithParent(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }
