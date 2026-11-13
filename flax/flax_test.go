// Runs the Flax sample's Linux build (the cooked game, headless: -headless -null -mute -std) against a fake local API.
// Needs the build (./build.sh with the Flax editor and .NET 10); skipped without it, and on systems other than Linux.
package flax

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/udprun"
)

const program = "Output/Linux/FlaxServer"

func start(t *testing.T, agent *fakeagent.Agent) *udprun.Run {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the test runs the Linux bundle")
	}
	exe, err := filepath.Abs(program)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skip("no build: run ./build.sh first (the Flax editor)")
	}
	return udprun.Start(t, exe, "gs_flax", agent, "-headless", "-null", "-mute", "-std")
}

func TestWarmMatchFromReadyToSelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_flax", "external_id": "m-9", "env": map[string]string{"MATCH_MODE": "duo"}})
	defer agent.Close()
	r := start(t, agent)
	r.WaitFor(t, `allocated: match "m-9"`)

	if got := r.Send(t, "join ana"); got != "welcome ana to gs_flax" {
		t.Fatalf("join: %q", got)
	}
	r.Send(t, "join bo")
	if got := r.Send(t, "ping"); got != "pong" {
		t.Fatalf("ping: %q", got)
	}
	r.Send(t, "leave ana")
	r.Send(t, "leave bo")
	r.Exit(t)

	calls := agent.Calls()
	if calls[0] != "POST /v0/self/ready" || calls[len(calls)-1] != "POST stop match_over" || calls[len(calls)-2] != "PUT players " {
		t.Fatalf("want ready first, the empty player list, then a self-stop; got %v", calls)
	}
	if !strings.Contains(r.Output(), "Server ready for players") {
		t.Fatalf("output:\n%s", r.Output())
	}
	if res := r.Result(t); res["reason"] != "match_over" || res["peak_players"] != float64(2) || res["match"] != "m-9" {
		t.Fatalf("result: %v", res)
	}
}

func TestStopAskedSavesAndLeavesWithoutASelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_flax"})
	defer agent.Close()
	r := start(t, agent)
	r.WaitFor(t, "allocated:")
	r.Send(t, "join cy")
	agent.AskStop()
	r.Exit(t)
	for _, c := range agent.Calls() {
		if strings.HasPrefix(c, "POST stop") {
			t.Fatalf("asked to stop, the server must not stop itself as well: %v", agent.Calls())
		}
	}
	if res := r.Result(t); res["reason"] != "stop_asked" {
		t.Fatalf("result: %v", res)
	}
}

func TestARefusedNameLeavesTheOthersCounted(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_flax"})
	defer agent.Close()
	r := start(t, agent)
	r.WaitFor(t, "Server ready for players")
	r.Send(t, "join ana")
	if got := r.Send(t, "join bad\x07name"); !strings.HasPrefix(got, "refused") {
		t.Fatalf("a control character in a name: %q", got)
	}
	time.Sleep(300 * time.Millisecond)
	if !agent.Has("PUT players ana") {
		t.Fatalf("ana was never counted: %v", agent.Calls())
	}
	for _, c := range agent.Calls() {
		if strings.Contains(c, "bad") {
			t.Fatalf("a refused name reached the player list: %v", agent.Calls())
		}
	}
}
