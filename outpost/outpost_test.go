package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gs "github.com/survivalservers/studio-go/sdk/go"
	"github.com/survivalservers/studio-samples/internal/fakeagent"
)

// client is one test player on UDP.
type client struct {
	t    *testing.T
	conn net.Conn
	seq  int
}

func dial(t *testing.T, addr net.Addr) *client {
	t.Helper()
	port := addr.(*net.UDPAddr).Port
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{t: t, conn: c}
}

func (c *client) send(format string, args ...any) {
	if _, err := fmt.Fprintf(c.conn, format, args...); err != nil {
		c.t.Fatal(err)
	}
}

// until reads datagrams until one starts with prefix, or fails after the wait.
func (c *client) until(prefix string, wait time.Duration) string {
	c.t.Helper()
	buf := make([]byte, 1500)
	end := time.Now().Add(wait)
	for time.Now().Before(end) {
		_ = c.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := c.conn.Read(buf)
		if err != nil {
			continue
		}
		if s := string(buf[:n]); strings.HasPrefix(s, prefix) {
			return s
		}
	}
	c.t.Fatalf("no %q within %s", prefix, wait)
	return ""
}

func (c *client) input(id, act string) {
	c.seq++
	c.send("in %s %d 0.5 -0.25 %s", id, c.seq, act)
}

type run struct {
	addr  chan net.Addr
	bound net.Addr
	err   chan error
	log   *safeBuf
}

type safeBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func start(t *testing.T, ctx context.Context, cfg Config, a *fakeagent.Agent, console io.Reader) *run {
	t.Helper()
	p, err := openPlatformWith(a.URL, a.Token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	r := &run{addr: make(chan net.Addr, 1), err: make(chan error, 1), log: &safeBuf{}}
	cfg.ListenPort = 0
	cfg.onListen = func(a net.Addr) { r.addr <- a }
	if cfg.TickHz == 0 {
		cfg.TickHz, cfg.SendHz = 30, 10
	}
	if cfg.Quiet == 0 {
		cfg.Quiet = 15 * time.Second
	}
	go func() { r.err <- Run(ctx, cfg, p, console, r.log) }()
	select {
	case r.bound = <-r.addr:
	case err := <-r.err:
		t.Fatalf("game ended before it listened: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("game did not listen")
	}
	return r
}

func (r *run) address() net.Addr { return r.bound }

func (r *run) wait(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case err := <-r.err:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(d):
		t.Fatalf("game still running after %s; log:\n%s", d, r.log.String())
	}
}

func hasPrefix(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// A match on a ready server: ready with no match, the allocation arrives, two players join, it goes live, plays out
// its length, tells them it is over and ends itself; players, labels and update times reach the local API.
func TestWarmMatchPlaysOutAndEndsItself(t *testing.T) {
	a := fakeagent.New("tok", map[string]any{
		"game_server_id": "gs_test", "external_id": "m-1",
		"env": map[string]string{"MAP": "canyon", "TEAM_SIZE": "1", "MATCH_SECONDS": "5"}, "labels": map[string]string{"queue": "casual"},
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := start(t, ctx, Config{ID: "gs_test", Kind: kindMatch, MaxPlayers: 24}, a, nil)

	// Before the allocation: no match to join yet.
	for !a.Has("GET allocation") {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	p1, p2 := dial(t, r.address()), dial(t, r.address())
	p1.send("hello alice")
	p1.until("welcome alice", 2*time.Second)
	p2.send("hello bob")
	p2.until("welcome bob", 2*time.Second)
	p3 := dial(t, r.address())
	p3.send("hello carol")
	p3.until("full", 2*time.Second) // one a side
	p1.send("ping 42")
	p1.until("pong 42", 2*time.Second)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p1.input("alice", "shoot")
		p2.input("bob", "shoot")
		time.Sleep(50 * time.Millisecond)
		if strings.Contains(r.log.String(), "match over") {
			break
		}
	}
	p1.until("over match_over", 2*time.Second)
	r.wait(t, 5*time.Second)

	calls := a.Calls()
	for _, want := range []string{"POST /v0/self/ready", "PUT players alice", "PUT players alice,bob", "PATCH labels map=canyon",
		"PATCH labels phase=live", "PATCH labels phase=ended", "POST stop match_over"} {
		if !a.Has(want) {
			t.Errorf("no %q in %v", want, calls)
		}
	}
	if !hasPrefix(calls, "POST tick hz=") {
		t.Errorf("no update times in %v", calls)
	}
	if !strings.Contains(r.log.String(), "allocated: match \"m-1\", map canyon, 1 a side") {
		t.Errorf("log: %s", r.log.String())
	}
}

// A world: a player gathers, the console's save writes their progress, a stop saves again, and the next run loads it
// so the returning player has their things; day, time of day and weather are labels.
func TestWorldKeepsProgressAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ID: "gs_w1", Kind: kindPersistent, Name: "EU Survival 1", SaveDir: filepath.Join(dir, "saves"), MaxPlayers: 24}

	a := fakeagent.New("tok", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	console, typeIn := io.Pipe()
	r := start(t, ctx, cfg, a, console)
	p := dial(t, r.address())
	p.send("hello dana")
	p.until("welcome dana", 2*time.Second)
	for i := 0; i < 20; i++ {
		p.input("dana", "gather")
		time.Sleep(40 * time.Millisecond)
	}
	p.until("st ", 2*time.Second)
	fmt.Fprintln(typeIn, "say Restart in 5 minutes")
	p.until("msg Restart in 5 minutes", 2*time.Second)
	fmt.Fprintln(typeIn, "save")
	waitLog(t, r, "World saved", 3*time.Second)
	s := readSave(t, cfg.SaveDir)
	got := s.Players["dana"]
	if got.Wood+got.Stone == 0 {
		t.Fatalf("dana's gathering was not saved: %+v", s)
	}
	for end := time.Now().Add(3 * time.Second); !hasPrefix(a.Calls(), "POST tick hz=") && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
	a.AskStop()
	r.wait(t, 5*time.Second)
	a.Close()
	for _, want := range []string{"PUT players dana", "PATCH labels day=1", "PATCH labels weather=clear"} {
		if !a.Has(want) {
			t.Errorf("no %q in %v", want, a.Calls())
		}
	}
	if !hasPrefix(a.Calls(), "PATCH labels time=") || !hasPrefix(a.Calls(), "POST tick hz=") {
		t.Errorf("no time of day or update times in %v", a.Calls())
	}

	// The next run of the same world, on a host from before the rename (GS_KIND=world): the same game.
	a2 := fakeagent.New("tok", nil)
	defer a2.Close()
	older := cfg
	older.Kind = "world"
	r2 := start(t, ctx, older, a2, nil)
	waitLog(t, r2, "loaded world EU Survival 1: day 1", 2*time.Second)
	p2 := dial(t, r2.address())
	p2.send("hello dana")
	p2.until("welcome dana", 2*time.Second)
	st := p2.until("st ", 2*time.Second)
	var tick int
	var x, y float64
	var hp, wood, stone int
	if _, err := fmt.Sscanf(st, "st %d %f %f %d %d %d", &tick, &x, &y, &hp, &wood, &stone); err != nil {
		t.Fatalf("state %q: %v", st, err)
	}
	if wood+stone < got.Wood+got.Stone {
		t.Errorf("dana came back with %d wood and %d stone, had %d and %d", wood, stone, got.Wood, got.Stone)
	}
	if s := readSave(t, cfg.SaveDir); s.Runs != 1 {
		t.Errorf("runs in the save before the second run's save: %d", s.Runs)
	}
	// End the second run before the folder goes: its stop saves again.
	a2.AskStop()
	r2.wait(t, 5*time.Second)
	if s := readSave(t, cfg.SaveDir); s.Runs != 2 {
		t.Errorf("runs after the second run: %d", s.Runs)
	}
}

// A player who goes quiet has left, and the match with nobody left ends as abandoned.
func TestQuietPlayerLeavesAndAnEmptyMatchEnds(t *testing.T) {
	a := fakeagent.New("tok", map[string]any{"game_server_id": "gs_q", "env": map[string]string{"TEAM_SIZE": "2"}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := start(t, ctx, Config{ID: "gs_q", Kind: kindMatch, Quiet: time.Second}, a, nil)
	for !a.Has("GET allocation") {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	p := dial(t, r.address())
	p.send("hello erin")
	p.until("welcome erin", 2*time.Second)
	r.wait(t, 5*time.Second)
	if !strings.Contains(r.log.String(), "erin left: timed out") || !a.Has("POST stop abandoned") || !a.Has("PUT players ") {
		t.Errorf("log %s\ncalls %v", r.log.String(), a.Calls())
	}
}

func waitLog(t *testing.T, r *run, s string, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if strings.Contains(r.log.String(), s) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %q in the log:\n%s", s, r.log.String())
}

func readSave(t *testing.T, dir string) Save {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "world.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Save
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The update's cost with a busy world and a full match, for sizing (go test -bench Step).
func BenchmarkStep(b *testing.B) {
	for _, tc := range []struct {
		kind    string
		players int
	}{{kindPersistent, 0}, {kindPersistent, 20}, {kindMatch, 8}} {
		b.Run(fmt.Sprintf("%s-%d", tc.kind, tc.players), func(b *testing.B) {
			g, _ := newGame(Config{Kind: tc.kind, TickHz: 30, SendHz: 10, MaxPlayers: 24, Quiet: time.Hour, SaveDir: b.TempDir()}, local{}, io.Discard)
			go func() {
				for f := range g.report {
					f()
				}
			}()
			if tc.kind == kindPersistent {
				g.save = &Save{Day: 1, Players: map[string]SavedPlayer{}}
			} else {
				g.allocate(&gs.Allocation{Env: map[string]string{"TEAM_SIZE": "4"}})
				g.phase = "live"
			}
			for i := 0; i < tc.players; i++ {
				id := fmt.Sprintf("p%d", i)
				g.players[id] = &player{id: id, hp: 100, last: time.Now(), pos: vec{float64(i * 37 % 400), float64(i * 53 % 400)}, in: vec{0.3, 0.7}}
			}
			for i := 0; i < 200; i++ { // let the creatures arrive
				g.step(1.0 / 30)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, pl := range g.players {
					pl.act = []string{"shoot", "gather", ""}[i%3]
				}
				g.step(1.0 / 30)
			}
		})
	}
}
