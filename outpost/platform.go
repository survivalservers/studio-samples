package main

import (
	"errors"
	"time"

	gs "github.com/survivalservers/studio-go/sdk/go"
)

// Platform is the part of the SDK the game uses; local is the stand-in when no agent started it.
type Platform interface {
	Enabled() bool
	Ready() error
	Health() error
	Stop(reason string) error
	WaitAllocation(wait time.Duration) (*gs.Allocation, error)
	WaitShutdown(wait time.Duration) (*gs.Shutdown, error)
	PlayerConnect(id string) error
	PlayerDisconnect(id string) error
	SetLabel(name, value string) error
	Tick(p50ms, p99ms float64, hz int) error
	Close()
}

type sdk struct{ *gs.Client }

func (sdk) Enabled() bool { return true }

// openPlatform connects to the local API from GS_LOCAL_API_URL and GS_TOKEN, or runs as a plain local server without them.
func openPlatform() (Platform, error) {
	c, err := gs.FromEnv()
	var e *gs.Error
	if errors.As(err, &e) && e.Code == -1 { // GS_ERR_NO_AGENT
		return local{}, nil
	}
	if err != nil {
		return nil, err
	}
	return sdk{c}, nil
}

// openPlatformWith connects to a given local API (tests).
func openPlatformWith(url, token string) (Platform, error) {
	c, err := gs.Open(url, token)
	if err != nil {
		return nil, err
	}
	return sdk{c}, nil
}

// local answers like an agent that never allocates and never asks for a stop: a match on a developer's PC starts
// with the default settings at once.
type local struct{}

func (local) Enabled() bool                                        { return false }
func (local) Ready() error                                         { return nil }
func (local) Health() error                                        { return nil }
func (local) Stop(string) error                                    { return nil }
func (local) WaitAllocation(time.Duration) (*gs.Allocation, error) { return nil, nil }
func (local) WaitShutdown(w time.Duration) (*gs.Shutdown, error) {
	time.Sleep(w)
	return nil, nil
}
func (local) PlayerConnect(string) error       { return nil }
func (local) PlayerDisconnect(string) error    { return nil }
func (local) SetLabel(string, string) error    { return nil }
func (local) Tick(float64, float64, int) error { return nil }
func (local) Close()                           {}
