// Package gs is a thin Go layer over the gs_sdk C library (cgo): the game server's side of the local API. A pure-Go
// game can as well call the local API over HTTP itself; this is for engines that already link the C library.
// Apache-2.0.
package gs

// #cgo CFLAGS: -I${SRCDIR}/../c/include
// #cgo linux LDFLAGS: -lpthread
// #cgo windows LDFLAGS: -lws2_32
// #include <stdlib.h>
// #include "gs_sdk.h"
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Error is a failed call: its code and the agent's or the library's reason.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("gs %d: %s", e.Code, e.Message) }

// Allocation is the game server's match: its IDs, settings, labels and command-line arguments.
type Allocation struct {
	GameServerID string
	ExternalID   string
	Env          map[string]string
	Labels       map[string]string
	Args         []string
}

// Shutdown is a shutdown notice: save and exit before Deadline.
type Shutdown struct {
	Deadline time.Time
	Reason   string
}

// Client is the game server's side of the local API. It may be used from several goroutines.
type Client struct {
	c  *C.gs_client
	mu sync.Mutex
	bg *background // StartBackground's, while it runs
}

// FromEnv connects as the game server the machine started (GS_LOCAL_API_URL, GS_TOKEN).
func FromEnv() (*Client, error) {
	var c *C.gs_client
	if r := C.gs_open(&c); r != C.GS_OK {
		msg := "gs_open failed"
		if r == C.GS_ERR_NO_AGENT {
			msg = "no local API here (GS_LOCAL_API_URL and GS_TOKEN are not set)"
		}
		return nil, &Error{int(r), msg}
	}
	return &Client{c: c}, nil
}

// Open connects with a given address and token.
func Open(agentURL, token string) (*Client, error) {
	u, t := C.CString(agentURL), C.CString(token)
	defer C.free(unsafe.Pointer(u))
	defer C.free(unsafe.Pointer(t))
	var c *C.gs_client
	if r := C.gs_open_with(u, t, &c); r != C.GS_OK {
		return nil, &Error{int(r), "gs_open_with failed"}
	}
	return &Client{c: c}, nil
}

// Close lets go of the client, ending its background helper first.
func (g *Client) Close() {
	g.StopBackground()
	if g.c != nil {
		C.gs_close(g.c)
		g.c = nil
	}
}

func (g *Client) check(r C.gs_result) error {
	if r == C.GS_OK {
		return nil
	}
	return &Error{int(r), C.GoString(C.gs_last_error(g.c))}
}

func (g *Client) Ready() error  { return g.check(C.gs_ready(g.c)) }
func (g *Client) Health() error { return g.check(C.gs_health(g.c)) }

// Stop ends the game server itself; a shutdown notice follows.
func (g *Client) Stop(reason string) error {
	r := C.CString(reason)
	defer C.free(unsafe.Pointer(r))
	return g.check(C.gs_stop(g.c, r))
}

// Saved says a persistent server just finished a save: it is backed up at once, and with a name, kept under that name.
// An agent older than this call is not an error: the folder watcher backs saves up without it.
func (g *Client) Saved(keep string) error {
	var k *C.char
	if keep != "" {
		k = C.CString(keep)
		defer C.free(unsafe.Pointer(k))
	}
	r := C.gs_saved(g.c, k)
	if r == C.GS_ERR_UNSUPPORTED {
		return nil
	}
	return g.check(r)
}

// WaitAllocation waits up to wait for the match; nil when there is none yet.
func (g *Client) WaitAllocation(wait time.Duration) (*Allocation, error) {
	var a *C.gs_allocation
	r := C.gs_wait_allocation(g.c, C.int(wait.Milliseconds()), &a)
	if r == C.GS_ERR_NONE_YET {
		return nil, nil
	}
	if err := g.check(r); err != nil {
		return nil, err
	}
	defer C.gs_allocation_free(a)
	out := &Allocation{GameServerID: C.GoString(C.gs_allocation_game_server_id(a)), ExternalID: C.GoString(C.gs_allocation_external_id(a)),
		Env: map[string]string{}, Labels: map[string]string{}}
	for i := C.size_t(0); ; i++ {
		s := C.gs_allocation_arg(a, i)
		if s == nil {
			break
		}
		out.Args = append(out.Args, C.GoString(s))
	}
	for i := C.size_t(0); ; i++ {
		n := C.gs_allocation_env_name(a, i)
		if n == nil {
			break
		}
		out.Env[C.GoString(n)] = C.GoString(C.gs_allocation_env_value(a, i))
	}
	for i := C.size_t(0); ; i++ {
		n := C.gs_allocation_label_name(a, i)
		if n == nil {
			break
		}
		out.Labels[C.GoString(n)] = C.GoString(C.gs_allocation_label_value(a, i))
	}
	return out, nil
}

// WaitShutdown waits up to wait for a shutdown notice; nil when there is none.
func (g *Client) WaitShutdown(wait time.Duration) (*Shutdown, error) {
	var deadline C.int64_t
	reason := (*C.char)(C.malloc(128))
	defer C.free(unsafe.Pointer(reason))
	r := C.gs_wait_shutdown(g.c, C.int(wait.Milliseconds()), &deadline, reason, 128)
	if r == C.GS_ERR_NONE_YET {
		return nil, nil
	}
	if err := g.check(r); err != nil {
		return nil, err
	}
	return &Shutdown{Deadline: time.UnixMilli(int64(deadline)), Reason: C.GoString(reason)}, nil
}

// PlayerConnect adds a player to the list the agent sees. An ID is 1 to 128 characters with no control characters; any
// other (often a name a player chose) is refused (code -5) and never joins the list.
func (g *Client) PlayerConnect(id string) error {
	if strings.IndexByte(id, 0) >= 0 { // a C string would end at the NUL and report another player
		return &Error{-5, "the player ID has a control character (1 to 128 characters, no control characters)"}
	}
	s := C.CString(id)
	defer C.free(unsafe.Pointer(s))
	return g.check(C.gs_player_connect(g.c, s))
}

func (g *Client) PlayerDisconnect(id string) error {
	s := C.CString(id)
	defer C.free(unsafe.Pointer(s))
	return g.check(C.gs_player_disconnect(g.c, s))
}

func (g *Client) SetPlayerCount(n int) error { return g.check(C.gs_set_player_count(g.c, C.int(n))) }

func (g *Client) SetLabel(name, value string) error {
	n, v := C.CString(name), C.CString(value)
	defer C.free(unsafe.Pointer(n))
	defer C.free(unsafe.Pointer(v))
	return g.check(C.gs_set_label(g.c, n, v))
}

func (g *Client) RemoveLabel(name string) error {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	return g.check(C.gs_set_label(g.c, n, nil))
}

// Tick reports one second of update times and the update rate, when known (0 otherwise).
func (g *Client) Tick(p50ms, p99ms float64, hz int) error {
	return g.check(C.gs_tick(g.c, C.double(p50ms), C.double(p99ms), C.int(hz)))
}

type background struct {
	stop  chan struct{} // closed by StopBackground
	ended chan struct{} // closed once the stop notice came: the match wait answers at once from then on
	wg    sync.WaitGroup
}

// pause waits d, ending early once the helper is stopped; false when it was.
func (b *background) pause(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-b.stop:
		return false
	case <-t.C:
		return true
	}
}

func (b *background) stopped() bool {
	select {
	case <-b.stop:
		return true
	default:
		return false
	}
}

// StartBackground sends health every healthEvery (zero: none) and calls onMatch and onStop once each, from goroutines
// of its own, when the match arrives and when the stop notice comes (nil: not waited for), as the C library's
// gs_background_start does. StopBackground or Close ends it within about 5 seconds; never call either from inside a
// callback. Start it once per client.
func (g *Client) StartBackground(healthEvery time.Duration, onMatch func(*Allocation), onStop func(*Shutdown)) error {
	if healthEvery < 0 || (healthEvery == 0 && onMatch == nil && onStop == nil) {
		return &Error{-5, "the background helper needs health, onMatch or onStop"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bg != nil {
		return &Error{-5, "the background helper is already running"}
	}
	b := &background{stop: make(chan struct{}), ended: make(chan struct{})}
	g.bg = b
	if healthEvery > 0 {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for {
				g.Health()
				if !b.pause(healthEvery) {
					return
				}
			}
		}()
	}
	if onMatch != nil {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for !b.stopped() {
				a, err := g.WaitAllocation(5 * time.Second)
				if a != nil {
					onMatch(a)
					return
				}
				select {
				case <-b.ended:
					return
				default:
				}
				if err != nil && !b.pause(time.Second) { // the agent is away for a moment: ask again
					return
				}
			}
		}()
	}
	if onStop != nil {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for !b.stopped() {
				sd, err := g.WaitShutdown(5 * time.Second)
				if sd != nil {
					close(b.ended)
					onStop(sd)
					return
				}
				if err != nil && !b.pause(time.Second) {
					return
				}
			}
		}()
	}
	return nil
}

// StopBackground ends StartBackground's helper and waits for it (up to about 5 seconds); never from a callback.
func (g *Client) StopBackground() {
	g.mu.Lock()
	b := g.bg
	g.bg = nil
	g.mu.Unlock()
	if b != nil {
		close(b.stop)
		b.wg.Wait()
	}
}
