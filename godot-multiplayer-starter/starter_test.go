// Runs the starter headless: a real client joins the real server over ENet on this computer, moves, and leaves, and
// the server tells a fake local API about each step. Needs Godot 4: set GODOT to its program, or have godot or godot4
// on the PATH; skipped otherwise. No window is opened.
package starter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/godotrun"
)

var projectFiles = []string{"project.godot", "main.tscn", "main.gd", "addons"}

func server(t *testing.T, env ...string) *godotrun.Run {
	t.Helper()
	return godotrun.Start(t, godotrun.Program(t), env, "--path", godotrun.Copy(t, ".", projectFiles...))
}

func client(t *testing.T, port int) *godotrun.Run {
	t.Helper()
	return godotrun.Start(t, godotrun.Program(t), nil, "--path", godotrun.Copy(t, ".", projectFiles...), "--",
		fmt.Sprintf("--connect=127.0.0.1:%d", port), "--test-join")
}

func TestAPlayerJoinsMovesAndTheMatchEnds(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_starter"})
	defer agent.Close()
	s := server(t, "GS_ID=gs_starter", "GS_KIND=match", "GS_LOCAL_API_URL="+agent.URL, "GS_TOKEN="+agent.Token)
	s.WaitFor(t, "Server ready for players")

	c := client(t, s.Port)
	c.WaitFor(t, "joined and moved")
	c.Exit(t)

	s.WaitFor(t, "match over")
	s.Exit(t)
	calls := agent.Calls()
	if calls[0] != "POST /v0/self/ready" || calls[len(calls)-1] != "POST stop match_over" {
		t.Fatalf("want ready first and a self-stop last, got %v", calls)
	}
	joined := false
	for _, call := range calls {
		joined = joined || (strings.HasPrefix(call, "PUT players") && strings.TrimSpace(strings.TrimPrefix(call, "PUT players")) != "")
	}
	if !joined {
		t.Fatalf("the player was never reported: %v", calls)
	}
}

func TestARunByHandStaysUpWhenEveryoneLeaves(t *testing.T) {
	s := server(t)
	s.WaitFor(t, "Server ready for players")
	c := client(t, s.Port)
	c.WaitFor(t, "joined and moved")
	c.Exit(t)
	s.WaitFor(t, "left")
	if strings.Contains(s.Output(), "match over") {
		t.Fatalf("a server run by hand ended by itself:\n%s", s.Output())
	}
}

func TestAPersistentServerStaysUpWhenEveryoneLeaves(t *testing.T) {
	for _, kind := range []string{"persistent_server", "world"} {
		t.Run(kind, func(t *testing.T) {
			agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_starter"})
			defer agent.Close()
			urlVar := "GS_LOCAL_API_URL"
			if kind == "world" { // the older names together: world, and the local API's address in GS_AGENT_URL
				urlVar = "GS_AGENT_URL"
			}
			s := server(t, "GS_ID=gs_starter", "GS_KIND="+kind, urlVar+"="+agent.URL, "GS_TOKEN="+agent.Token)
			s.WaitFor(t, "Server ready for players")
			c := client(t, s.Port)
			c.WaitFor(t, "joined and moved")
			c.Exit(t)
			s.WaitFor(t, "left")
			if strings.Contains(s.Output(), "match over") {
				t.Fatalf("%s ended by itself:\n%s", kind, s.Output())
			}
		})
	}
}

func TestAStopNoticeEndsItWithoutASelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_starter"})
	defer agent.Close()
	s := server(t, "GS_ID=gs_starter", "GS_KIND=match", "GS_LOCAL_API_URL="+agent.URL, "GS_TOKEN="+agent.Token)
	s.WaitFor(t, "Server ready for players")
	agent.AskStop()
	s.WaitFor(t, "asked to stop")
	s.Exit(t)
	for _, call := range agent.Calls() {
		if strings.HasPrefix(call, "POST stop") {
			t.Fatalf("asked to stop, it must not stop itself as well: %v", agent.Calls())
		}
	}
}

// The add-on here is a copy so the starter stands alone once it is its own repository: it must not fall behind.
func TestTheAddOnMatchesTheOneInTheSamples(t *testing.T) {
	err := filepath.WalkDir("../godot-addon/addons", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("../godot-addon", path)
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(rel)
		if err != nil {
			return err
		}
		if string(want) != string(got) {
			t.Errorf("%s differs from ../godot-addon/%s", rel, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVersionFilesParse(t *testing.T) {
	for _, name := range []string{"version-linux.json", "version-windows.json", ".mcp.json"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
