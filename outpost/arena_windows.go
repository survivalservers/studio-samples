//go:build windows

package main

import (
	"fmt"
	"unsafe"
)

// arena is memory the game holds outside Go's own heap, the way an engine keeps its world in big blocks of its own:
// address space for the most it may ever hold is reserved once, and parts are committed and written (so the machine
// counts them) or decommitted as the game needs more or less.
type arena struct {
	base      uintptr
	mem       []byte
	committed int
}

var (
	procVirtualAlloc = kernel32.NewProc("VirtualAlloc")
	procVirtualFree  = kernel32.NewProc("VirtualFree")
)

const (
	memCommit     = 0x1000
	memReserve    = 0x2000
	memDecommit   = 0x4000
	memRelease    = 0x8000
	pageReadWrite = 0x04
)

func newArena(maxBytes int) (*arena, error) {
	base, _, err := procVirtualAlloc.Call(0, uintptr(maxBytes), memReserve, pageReadWrite)
	if base == 0 {
		return nil, fmt.Errorf("reserve %d MiB: %v", maxBytes>>20, err)
	}
	// The address comes back as a number; read it as the pointer it is (memory Go's collector never moves or frees).
	p := *(*unsafe.Pointer)(unsafe.Pointer(&base))
	return &arena{base: base, mem: unsafe.Slice((*byte)(p), maxBytes)}, nil
}

func (a *arena) commit(from, to int) error {
	if r, _, err := procVirtualAlloc.Call(a.base+uintptr(from), uintptr(to-from), memCommit, pageReadWrite); r == 0 {
		return fmt.Errorf("commit %d MiB: %v", (to-from)>>20, err)
	}
	return nil
}

func (a *arena) release(from, to int) error {
	if r, _, err := procVirtualFree.Call(a.base+uintptr(from), uintptr(to-from), memDecommit); r == 0 {
		return fmt.Errorf("decommit: %v", err)
	}
	return nil
}

func (a *arena) bytes() []byte { return a.mem }

func (a *arena) close() { procVirtualFree.Call(a.base, 0, memRelease) }
