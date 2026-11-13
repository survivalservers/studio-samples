// Runs the Godot sample headless against a fake local API. Needs Godot 4: set GODOT to its program, or
// have godot or godot4 on the PATH; skipped otherwise.
package godot

import (
	"strings"
	"testing"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/godotrun"
)

func start(t *testing.T, agent *fakeagent.Agent) *godotrun.Run {
	t.Helper()
	program := godotrun.Program(t)
	project := godotrun.Copy(t, ".", "project.godot", "server.tscn", "server.gd")
	return godotrun.Start(t, program, []string{"GS_ID=gs_godot", "GS_LOCAL_API_URL=" + agent.URL, "GS_TOKEN=" + agent.Token},
		"--path", project)
}

func TestWarmMatchFromReadyToSelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_godot", "external_id": "m-9", "env": map[string]string{"MATCH_MODE": "duo"}})
	defer agent.Close()
	r := start(t, agent)
	r.WaitFor(t, `allocated: match "m-9"`)

	if got := r.Send(t, "join ana"); got != "welcome ana to gs_godot" {
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
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_godot"})
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
