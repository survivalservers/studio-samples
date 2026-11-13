// The smallest game server with the Go layer; see ../../c/sample/sample.c for what it shows.
package main

import (
	"fmt"
	"os"
	"time"

	gs "github.com/survivalservers/studio-go/sdk/go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	g, err := gs.FromEnv()
	if err != nil {
		return err
	}
	defer g.Close()
	if err := g.Ready(); err != nil {
		return err
	}
	m, err := g.WaitAllocation(30 * time.Second)
	if err != nil || m == nil {
		return fmt.Errorf("no match: %v", err)
	}
	fmt.Printf("match %s map=%s\n", m.ExternalID, m.Env["MAP"])
	for _, step := range []func() error{
		func() error { return g.PlayerConnect("p1") }, func() error { return g.PlayerConnect("p2") },
		func() error { // a name a player chose, with a NUL in it: refused, and p1 and p2 still count
			if g.PlayerConnect("bad\x00name") == nil {
				return fmt.Errorf("a player ID with a control character was taken")
			}
			fmt.Println("refused a player ID with a control character")
			return nil
		},
		func() error { return g.PlayerDisconnect("p2") }, func() error { return g.SetLabel("mode", "ffa") },
		g.Health, func() error { return g.Tick(2.5, 6.25, 64) }, func() error { return g.Stop("match_over") },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	sd, err := g.WaitShutdown(30 * time.Second)
	if err != nil {
		return err
	}
	deadline, reason := "none", "none"
	if sd != nil {
		reason = sd.Reason
		if sd.Deadline.UnixMilli() > 0 {
			deadline = "set"
		}
	}
	fmt.Printf("shutdown reason=%s deadline=%s\n", reason, deadline)
	return nil
}
