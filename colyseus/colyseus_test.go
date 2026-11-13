// Runs the Colyseus sample against a fake local API, with the sample's own scripted client as the players. Needs Node
// 22 or newer and the build (npm ci && npm run build); skipped without them.
package colyseus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/freeport"
	"github.com/survivalservers/studio-samples/internal/procgroup"
)

type run struct {
	dir       string // a copy of the build, away from node_modules, as a host runs it
	port      int
	artifacts string
	cmd       *exec.Cmd
	mu        sync.Mutex
	out       strings.Builder
	done      chan struct{}
	err       error
}

func node(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not found")
	}
	if _, err := os.Stat("dist/server.cjs"); err != nil {
		t.Skip("no build: run npm ci && npm run build first")
	}
	return p
}

// freeTCPPort is the port the Colyseus server binds once it starts: outside the kernel's ephemeral range, so no
// program's bind(0) or outgoing connection takes it in between (internal/freeport).
func freeTCPPort(t *testing.T) int {
	t.Helper()
	p, err := freeport.Pick("tcp")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func start(t *testing.T, agent *fakeagent.Agent) *run {
	t.Helper()
	r := &run{dir: t.TempDir(), port: freeTCPPort(t), artifacts: t.TempDir(), done: make(chan struct{})}
	nodePath := node(t)
	// Run the bundles from a folder of their own: next to node_modules a package left out of the bundle would still
	// be found, and the build would fail only on a machine (it did once, with Express).
	for _, f := range []string{"server.cjs", "client.cjs"} {
		b, err := os.ReadFile(filepath.Join("dist", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.cmd = exec.Command(nodePath, "server.cjs")
	r.cmd.Dir = r.dir
	r.cmd.Env = append(os.Environ(), "GS_ID=gs_colyseus", "GS_LOCAL_API_URL="+agent.URL, "GS_TOKEN="+agent.Token,
		fmt.Sprintf("GS_PORT_GAME=%d", r.port), "GS_PUBLIC_IP=127.0.0.1", "GS_ARTIFACT_DIR="+r.artifacts)
	pipe, err := r.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	r.cmd.Stderr = r.cmd.Stdout
	procgroup.Prepare(r.cmd)
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		procgroup.Kill(r.cmd)
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
		}
	})
	go func() {
		s := bufio.NewScanner(pipe)
		for s.Scan() {
			r.mu.Lock()
			r.out.WriteString(s.Text() + "\n")
			r.mu.Unlock()
		}
		r.err = r.cmd.Wait()
		close(r.done)
	}()
	return r
}

func (r *run) output() string { r.mu.Lock(); defer r.mu.Unlock(); return r.out.String() }

func (r *run) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(r.output(), text) {
		select {
		case <-r.done:
			t.Fatalf("the server exited (%v) without printing %q:\n%s", r.err, text, r.output())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output:\n%s", text, r.output())
		}
	}
}

func (r *run) exit(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatalf("the server exited with %v:\n%s", r.err, r.output())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the server did not end:\n%s", r.output())
	}
}

func (r *run) result(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.artifacts, "match-result.json"))
	if err != nil {
		t.Fatalf("result file: %v\n%s", err, r.output())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

type clientResult struct {
	OK      bool `json:"ok"`
	Players []struct {
		Name    string   `json:"name"`
		Welcome string   `json:"welcome"`
		PongMs  *float64 `json:"pong_ms"`
		Error   string   `json:"error"`
		Closed  *int     `json:"closed"`
	} `json:"players"`
}

// client runs the scripted client and returns what it printed. Safe from any goroutine.
func (r *run) client(nodePath string, args ...string) (clientResult, error) {
	cmd := exec.Command(nodePath, append([]string{"client.cjs", fmt.Sprintf("ws://127.0.0.1:%d", r.port)}, args...)...)
	cmd.Dir = r.dir
	procgroup.Prepare(cmd)
	defer procgroup.Kill(cmd)
	out, _ := cmd.Output()
	var res clientResult
	if err := json.Unmarshal(out, &res); err != nil {
		return res, fmt.Errorf("client said %q: %v", out, err)
	}
	return res, nil
}

func (r *run) players(t *testing.T, args ...string) clientResult {
	t.Helper()
	res, err := r.client(node(t), args...)
	if err != nil {
		t.Fatalf("%v\nserver:\n%s", err, r.output())
	}
	return res
}

func TestMatchFromReadyToSelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_colyseus", "external_id": "m-9", "env": map[string]string{"MATCH_MODE": "duo"}})
	defer agent.Close()
	r := start(t, agent)
	r.waitFor(t, `allocated: match "m-9"`)

	res := r.players(t, "--players", "ana,bo", "--hold", "1.5") // over a second, so a tick report goes out
	if !res.OK || len(res.Players) != 2 || res.Players[0].Welcome != "welcome ana to gs_colyseus" || res.Players[1].PongMs == nil {
		t.Fatalf("client: %+v\nserver:\n%s", res, r.output())
	}
	r.exit(t)

	calls := agent.Calls()
	if calls[0] != "POST /v0/self/ready" || calls[len(calls)-1] != "POST stop match_over" || calls[len(calls)-2] != "PUT players " {
		t.Fatalf("want ready first, the empty player list, then a self-stop; got %v", calls)
	}
	if !agent.Has("PUT players ana,bo") {
		t.Fatalf("both players never reported together: %v", calls)
	}
	if !agent.Has("POST tick hz=20") {
		t.Fatalf("no tick times: %v", calls)
	}
	if !strings.Contains(r.output(), "Server ready for players") {
		t.Fatalf("output:\n%s", r.output())
	}
	if res := r.result(t); res["reason"] != "match_over" || res["peak_players"] != float64(2) || res["match"] != "m-9" {
		t.Fatalf("result: %v", res)
	}
}

func TestStopAskedSavesAndLeavesWithoutASelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_colyseus"})
	defer agent.Close()
	r := start(t, agent)
	r.waitFor(t, "allocated:")
	nodePath := node(t)
	type reply struct {
		res clientResult
		err error
	}
	done := make(chan reply, 1)
	go func() { res, err := r.client(nodePath, "--players", "cy", "--stay", "20"); done <- reply{res, err} }()
	r.waitFor(t, "cy joined")
	agent.AskStop()
	r.exit(t)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	res := got.res
	if !res.OK || res.Players[0].Closed == nil {
		t.Fatalf("the player was not let go when the server stopped: %+v", res)
	}
	for _, c := range agent.Calls() {
		if strings.HasPrefix(c, "POST stop") {
			t.Fatalf("asked to stop, the server must not stop itself as well: %v", agent.Calls())
		}
	}
	if res := r.result(t); res["reason"] != "stop_asked" || res["peak_players"] != float64(1) {
		t.Fatalf("result: %v", res)
	}
}

func TestSigtermSavesAndExitsZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sends Ctrl+Break, checked on the Windows lab machine")
	}
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_colyseus"})
	defer agent.Close()
	r := start(t, agent)
	r.waitFor(t, "Server ready for players")
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	r.exit(t)
	if res := r.result(t); res["reason"] != "stop_signal" {
		t.Fatalf("result: %v", res)
	}
}

func TestARefusedNameLeavesTheOthersCounted(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_colyseus"})
	defer agent.Close()
	r := start(t, agent)
	r.waitFor(t, "Server ready for players")
	res := r.players(t, "--players", "ana,bad\x07name", "--hold", "1")
	if res.OK || res.Players[0].Error != "" || !strings.Contains(res.Players[1].Error, "refused") {
		t.Fatalf("want ana in and the control character refused: %+v", res)
	}
	if !agent.Has("PUT players ana") {
		t.Fatalf("ana was never counted: %v", agent.Calls())
	}
	for _, c := range agent.Calls() {
		if strings.Contains(c, "bad") {
			t.Fatalf("a refused name reached the player list: %v", agent.Calls())
		}
	}
	r.exit(t) // ana left, so the match ended
}
