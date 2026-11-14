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
	if os.Getenv("GS_SAMPLE_BACKGROUND") != "" {
		return background(g)
	}
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

// background: with GS_SAMPLE_BACKGROUND set, the library's background helper sends health and calls the two functions
// once each.
func background(g *gs.Client) error {
	matched, stopped := make(chan struct{}), make(chan *gs.Shutdown, 1)
	err := g.StartBackground(200*time.Millisecond, func(m *gs.Allocation) {
		fmt.Printf("match %s map=%s\n", m.ExternalID, m.Env["MAP"])
		close(matched)
	}, func(sd *gs.Shutdown) { stopped <- sd })
	if err != nil {
		return err
	}
	if err := g.Ready(); err != nil {
		return err
	}
	select {
	case <-matched:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("no match")
	}
	if err := g.PlayerConnect("p1"); err != nil {
		return err
	}
	if err := g.SetLabel("mode", "ffa"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond) // the helper sends health meanwhile
	if err := g.Stop("match_over"); err != nil {
		return err
	}
	reason, deadline := "none", "none"
	select {
	case sd := <-stopped:
		reason = sd.Reason
		if sd.Deadline.UnixMilli() > 0 {
			deadline = "set"
		}
	case <-time.After(30 * time.Second):
	}
	fmt.Printf("shutdown reason=%s deadline=%s\n", reason, deadline)
	g.StopBackground()
	return nil
}
