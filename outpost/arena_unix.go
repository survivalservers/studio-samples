//go:build linux || darwin

package main

import "syscall"

// arena is memory the game holds outside Go's own heap, the way an engine keeps its world in big blocks of its own:
// address space for the most it may ever hold is set aside once, and pages are written (so the machine counts them)
// or handed back as the game needs more or less.
type arena struct {
	mem       []byte
	committed int
}

func newArena(maxBytes int) (*arena, error) {
	mem, err := syscall.Mmap(-1, 0, maxBytes, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON|syscall.MAP_NORESERVE)
	if err != nil {
		return nil, err
	}
	return &arena{mem: mem}, nil
}

// commit makes [from, to) usable; written pages are what the machine counts.
func (a *arena) commit(from, to int) error { return nil }

// release hands [from, to) back to the system.
func (a *arena) release(from, to int) error {
	return syscall.Madvise(a.mem[from:to], syscall.MADV_DONTNEED)
}

func (a *arena) bytes() []byte { return a.mem }

func (a *arena) close() { _ = syscall.Munmap(a.mem) }
