package main

import (
	"unsafe"
)

const pageStep = 2 << 20 // memory is taken and given back in 2 MiB steps

// growTo commits and writes pages until the arena holds want bytes or at most step more, and says how far it got.
// The pages get varied contents (the world's state, not zeros), so nothing can fold them away.
func (a *arena) growTo(want, step int) error {
	want = min(want, len(a.bytes()))
	for a.committed < want && step > 0 {
		n := min(pageStep, want-a.committed)
		if err := a.commit(a.committed, a.committed+n); err != nil {
			return err
		}
		w := unsafe.Slice((*uint64)(unsafe.Pointer(&a.bytes()[a.committed])), n/8)
		x := uint64(a.committed) | 1
		for i := range w {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			w[i] = x
		}
		a.committed += n
		step -= n
	}
	return nil
}

// shrinkTo gives pages back until the arena holds want bytes or at most step fewer.
func (a *arena) shrinkTo(want, step int) error {
	want = (want + pageStep - 1) / pageStep * pageStep
	if want >= a.committed {
		return nil
	}
	from := max(want, a.committed-step)
	from = from / pageStep * pageStep
	if err := a.release(from, a.committed); err != nil {
		return err
	}
	a.committed = from
	return nil
}

// words is the held part, as 64-bit words for the simulation to work through.
func (a *arena) words() []uint64 {
	if a.committed < 8 {
		return nil
	}
	return unsafe.Slice((*uint64)(unsafe.Pointer(&a.bytes()[0])), a.committed/8)
}
