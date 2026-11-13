// Runs the O3DE sample's Linux server (LabServer.HeadlessServerLauncher with the null renderer) against a fake local
// API. Needs the build (./build.sh with the O3DE SDK) and the system libraries the sample's Dockerfile installs;
// skipped without them, and on systems other than Linux.
package o3de

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/udprun"
)

const program = "dist/linux/bin/LabServer.HeadlessServerLauncher"

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
		t.Skip("no build: run ./build.sh first (the O3DE SDK)")
	}
	// The engine links X11, XCB and xkbcommon libraries though it opens no window; without them it loads none of its
	// modules and the game never starts.
	for _, lib := range []string{exe, filepath.Join(filepath.Dir(exe), "libLabServer.so")} {
		out, _ := exec.Command("ldd", lib).CombinedOutput()
		if missing := notFound(string(out)); len(missing) > 0 {
			t.Skipf("this machine lacks libraries the build needs (%s): install the packages in the sample's Dockerfile",
				strings.Join(missing, ", "))
		}
	}
	return udprun.Start(t, exe, "gs_o3de", agent, "--project-path=../project", "-rhi=null", "-bg_ConnectToAssetProcessor=0")
}

func TestWarmMatchFromReadyToSelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_o3de", "external_id": "m-9", "env": map[string]string{"MATCH_MODE": "duo"}})
	defer agent.Close()
	r := start(t, agent)
	r.WaitFor(t, `allocated: match "m-9"`)

	if got := r.Send(t, "join ana"); got != "welcome ana to gs_o3de" {
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
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_o3de"})
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
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_o3de"})
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

// notFound lists the libraries ldd's output says it cannot find.
func notFound(ldd string) []string {
	var missing []string
	for _, line := range strings.Split(ldd, "\n") {
		if name, _, ok := strings.Cut(strings.TrimSpace(line), " => not found"); ok {
			missing = append(missing, name)
		}
	}
	return missing
}
