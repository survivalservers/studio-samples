//go:build !windows

package main

import "time"

var clockBase = time.Now()

func nowFine() time.Duration { return time.Since(clockBase) }
