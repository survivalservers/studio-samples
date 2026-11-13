//go:build unix && !linux

package procgroup

import "syscall"

func dieWithParent(attr *syscall.SysProcAttr) {}
