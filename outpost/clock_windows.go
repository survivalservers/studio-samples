//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

// Update times are often under a millisecond, finer than Go's own Windows clock moves (0.5 ms or more), so the
// performance counter times them (the same clock as oss/bench).
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	qpc      = kernel32.NewProc("QueryPerformanceCounter")
	qpcFreq  int64
)

func init() {
	if r, _, err := kernel32.NewProc("QueryPerformanceFrequency").Call(uintptr(unsafe.Pointer(&qpcFreq))); r == 0 || qpcFreq <= 0 {
		panic("QueryPerformanceFrequency failed: " + err.Error())
	}
}

func nowFine() time.Duration {
	var c int64
	qpc.Call(uintptr(unsafe.Pointer(&c)))
	return time.Duration(c/qpcFreq)*time.Second + time.Duration(c%qpcFreq*int64(time.Second)/qpcFreq)
}
