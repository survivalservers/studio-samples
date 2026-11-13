//go:build !linux && !darwin && !windows

package main

import "errors"

// arena: memory of its own is only kept on Linux, macOS and Windows; elsewhere the game runs without it.
type arena struct {
	mem       []byte
	committed int
}

func newArena(int) (*arena, error)          { return nil, errors.New("not on this system") }
func (a *arena) commit(from, to int) error  { return nil }
func (a *arena) release(from, to int) error { return nil }
func (a *arena) bytes() []byte              { return a.mem }
func (a *arena) close()                     {}
