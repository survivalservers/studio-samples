package main

import (
	"bytes"
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

	"github.com/survivalservers/studio-samples/internal/fakeagent"
)

// player sends one command to the game server and returns the reply.
func player(t *testing.T, addr net.Addr, cmd string) string {
	t.Helper()
	port := addr.(*net.UDPAddr).Port
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(cmd)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	return string(buf[:n])
}

type started struct {
	srv  *Server
	log  *syncBuffer
	done chan error
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func start(t *testing.T, ctx context.Context, cfg Config) started {
	t.Helper()
	log := &syncBuffer{}
	s, err := Listen(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	return started{s, log, done}
}

func wait(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the game server did not end")
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func newAgent(t *testing.T, alloc *Allocation) *fakeagent.Agent {
	var a any
	if alloc != nil {
		a = alloc
	}
	agent := fakeagent.New("tok", a)
	t.Cleanup(agent.Close)
	return agent
}

func TestWarmMatchFromReadyToSelfStop(t *testing.T) {
	agent := newAgent(t, &Allocation{GameServerID: "gs_1", ExternalID: "88231", Env: map[string]string{"MATCH_MODE": "ranked"}})
	artifacts := t.TempDir()
	cfg := Config{ID: "gs_1", ListenPort: 0, ArtifactDir: artifacts, API: &LocalAPI{URL: agent.URL, Token: "tok"}}
	g := start(t, context.Background(), cfg)

	// Players only arrive once a match has picked this game server.
	eventually(t, "the allocation", func() bool { return strings.Contains(g.log.String(), "allocated:") })
	if got := player(t, g.srv.Addr(), "join ana"); got != "welcome ana to gs_1" {
		t.Fatalf("join: %q", got)
	}
	player(t, g.srv.Addr(), "join bo")
	if got := player(t, g.srv.Addr(), "ping"); got != "pong" {
		t.Fatalf("ping: %q", got)
	}
	player(t, g.srv.Addr(), "leave ana")
	player(t, g.srv.Addr(), "leave bo")
	wait(t, g.done)

	calls := agent.Calls()
	if calls[0] != "POST /v0/self/ready" {
		t.Fatalf("ready must come first, got %v", calls)
	}
	last := calls[len(calls)-1]
	if last != "POST stop match_over" {
		t.Fatalf("the match must end with a self-stop, got %v", calls)
	}
	if !contains(calls, "PUT players ") || calls[len(calls)-2] != "PUT players " {
		t.Fatalf("the empty player list must be reported before the stop, got %v", calls)
	}
	if !strings.Contains(g.log.String(), "Server ready for players") || !strings.Contains(g.log.String(), `match "88231"`) {
		t.Fatalf("log:\n%s", g.log.String())
	}
	b, err := os.ReadFile(filepath.Join(artifacts, "match-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if result["reason"] != "match_over" || result["peak_players"] != float64(2) || result["match"] != "88231" {
		t.Fatalf("result: %s", b)
	}
}

func TestTheAllocationCallWinsOverTheFileWhichServesOnlyWithoutALocalAPI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "allocation.json")
	if err := os.WriteFile(file, []byte(`{"game_server_id":"gs_2","external_id":"m-file","labels":{"queue":"duo"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// With a local API: the call (the only source of secret values), not the file.
	agent := newAgent(t, &Allocation{GameServerID: "gs_2", ExternalID: "m-call", Env: map[string]string{"TOKEN": "secret"}})
	g := start(t, context.Background(), Config{ID: "gs_2", AllocationFile: file, API: &LocalAPI{URL: agent.URL, Token: "tok"}})
	eventually(t, "the allocation", func() bool { return strings.Contains(g.log.String(), "allocated:") })
	player(t, g.srv.Addr(), "join cy")
	player(t, g.srv.Addr(), "leave cy")
	wait(t, g.done)
	if !strings.Contains(g.log.String(), `match "m-call"`) || strings.Contains(g.log.String(), "m-file") {
		t.Fatalf("log:\n%s", g.log.String())
	}

	// Without one: the file.
	g = start(t, context.Background(), Config{ID: "gs_2", AllocationFile: file, MatchLength: 200 * time.Millisecond, API: &LocalAPI{}})
	wait(t, g.done)
	if !strings.Contains(g.log.String(), `match "m-file"`) {
		t.Fatalf("log:\n%s", g.log.String())
	}
}

func TestStopAskedEndsTheMatchWithoutASelfStop(t *testing.T) {
	agent := newAgent(t, &Allocation{GameServerID: "gs_3"})
	artifacts := t.TempDir()
	g := start(t, context.Background(), Config{ID: "gs_3", ArtifactDir: artifacts, API: &LocalAPI{URL: agent.URL, Token: "tok"}})
	player(t, g.srv.Addr(), "join di")
	agent.AskStop()
	wait(t, g.done)
	for _, c := range agent.Calls() {
		if strings.HasPrefix(c, "POST stop") {
			t.Fatalf("asked to stop, the server must not stop itself as well: %v", agent.Calls())
		}
	}
	b, _ := os.ReadFile(filepath.Join(artifacts, "match-result.json"))
	if !strings.Contains(string(b), `"reason": "stop_asked"`) {
		t.Fatalf("result: %s", b)
	}
}

func TestStopSignalAndMatchLength(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := start(t, ctx, Config{ID: "local", API: &LocalAPI{}})
	if got := player(t, g.srv.Addr(), "ping"); got != "pong" {
		t.Fatalf("with no local API the game still runs; ping: %q", got)
	}
	cancel()
	wait(t, g.done)
	if !strings.Contains(g.log.String(), "match over: stop_signal") {
		t.Fatalf("log:\n%s", g.log.String())
	}

	agent := newAgent(t, &Allocation{GameServerID: "gs_4"})
	g = start(t, context.Background(), Config{ID: "gs_4", MatchLength: 300 * time.Millisecond, API: &LocalAPI{URL: agent.URL, Token: "tok"}})
	wait(t, g.done)
	if last := agent.Calls()[len(agent.Calls())-1]; last != "POST stop match_over" {
		t.Fatalf("match length must end in a self-stop: %v", agent.Calls())
	}
}

func TestAFailedReadyCallIsAnError(t *testing.T) {
	agent := newAgent(t, nil)
	s, err := Listen(Config{API: &LocalAPI{URL: agent.URL, Token: "wrong"}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want a 401 error, got %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := FromEnv(env(nil))
	if err != nil || c.ListenPort != 7777 || c.ID != "local" || c.API.Enabled() {
		t.Fatalf("local defaults: %+v %v", c, err)
	}
	c, _ = FromEnv(env(map[string]string{"GS_PORT_GAME": "31042", "GS_LOCAL_API_URL": "http://127.0.0.1:7600", "GS_TOKEN": "t"}))
	if c.ListenPort != 31042 || c.PublicPort != 31042 || !c.API.Enabled() {
		t.Fatalf("Windows: bind the public port: %+v", c)
	}
	c, _ = FromEnv(env(map[string]string{"GS_PORT_GAME": "31042", "GS_PORT_GAME_INTERNAL": "7777"}))
	if c.ListenPort != 7777 || c.PublicPort != 31042 {
		t.Fatalf("Linux image: bind the container port: %+v", c)
	}
	if _, err := FromEnv(env(map[string]string{"GS_PORT_GAME": "seven"})); err == nil {
		t.Fatal("a bad port must be an error")
	}

	// The older names (GS_KIND=world, the local API's address in GS_AGENT_URL) describe the same game server.
	older, _ := FromEnv(env(map[string]string{"GS_KIND": "world", "GS_REGION": "us-central", "GS_CONTINENT": "north-america",
		"GS_AGENT_URL": "http://127.0.0.1:7600", "GS_TOKEN": "t"}))
	current, _ := FromEnv(env(map[string]string{"GS_KIND": "persistent_server", "GS_REGION": "us-central", "GS_CONTINENT": "north-america",
		"GS_LOCAL_API_URL": "http://127.0.0.1:7600", "GS_TOKEN": "t"}))
	for _, c := range []Config{older, current} {
		if c.Kind != "persistent_server" || c.Region != "us-central" || c.Continent != "north-america" || c.API.URL != "http://127.0.0.1:7600" {
			t.Fatalf("kind %q, region %q, continent %q, local API %q", c.Kind, c.Region, c.Continent, c.API.URL)
		}
	}
}

// TestARefusedPlayerNameLeavesTheOthersCounted: a player who joins with a control character in their name (a NUL
// here) makes the platform refuse the list holding it (422, its place named); the game leaves that player out and
// the others are reported without it, and stay counted as more join.
func TestARefusedPlayerNameLeavesTheOthersCounted(t *testing.T) {
	agent := newAgent(t, &Allocation{GameServerID: "gs_9"})
	g := start(t, context.Background(), Config{ID: "gs_9", ArtifactDir: t.TempDir(), API: &LocalAPI{URL: agent.URL, Token: "tok"}})
	eventually(t, "the allocation", func() bool { return strings.Contains(g.log.String(), "allocated:") })
	player(t, g.srv.Addr(), "join ana")
	eventually(t, "ana reported", func() bool { return contains(agent.Calls(), "PUT players ana") })
	player(t, g.srv.Addr(), "join bad\x00name")
	eventually(t, "the refusal", func() bool { return contains(agent.Calls(), "PUT players refused ids[1]") })
	eventually(t, "ana reported again without it", func() bool {
		c := agent.Calls()
		return len(c) > 0 && c[len(c)-1] == "PUT players ana"
	})
	player(t, g.srv.Addr(), "join bo")
	eventually(t, "ana and bo", func() bool { return contains(agent.Calls(), "PUT players ana,bo") })
	if !strings.Contains(g.log.String(), `refused player ID "bad\x00name"`) {
		t.Fatalf("log:\n%s", g.log.String())
	}
	agent.AskStop()
	wait(t, g.done)
}
