// Package udprun runs a sample's built game server for the samples' tests: in a process group of its own that the
// test ends whatever happens, with the fake local API, a free UDP game port and an artifact folder, and it reads its
// output, answers on the game port and result file. The Godot tests have their own (godotrun), which adds Godot's
// own arguments.
package udprun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/freeport"
	"github.com/survivalservers/studio-samples/internal/procgroup"
)

// Run is one running game server.
type Run struct {
	Port      int    // the UDP game port (GS_PORT_GAME)
	Artifacts string // GS_ARTIFACT_DIR
	cmd       *exec.Cmd
	mu        sync.Mutex
	out       strings.Builder
	done      chan struct{}
	err       error
}

// FreeUDPPort returns a UDP port nothing listens on just now, for the game server to bind once it starts: outside the
// kernel's ephemeral range, so no program's bind(0) or outgoing connection takes it in between (internal/freeport).
func FreeUDPPort(t *testing.T) int {
	t.Helper()
	p, err := freeport.Pick("udp")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Start runs exe (in its own folder) with args, the fake local API's address and token, a free UDP game port and an
// artifact folder; it and everything it started end with the test.
func Start(t *testing.T, exe, gsID string, agent *fakeagent.Agent, args ...string) *Run {
	t.Helper()
	r := &Run{Port: FreeUDPPort(t), Artifacts: t.TempDir(), done: make(chan struct{})}
	r.cmd = exec.Command(exe, args...)
	r.cmd.Dir = filepath.Dir(exe)
	r.cmd.Env = append(os.Environ(), "GS_ID="+gsID, "GS_LOCAL_API_URL="+agent.URL, "GS_TOKEN="+agent.Token,
		fmt.Sprintf("GS_PORT_GAME=%d", r.Port), "GS_PUBLIC_IP=127.0.0.1", "GS_ARTIFACT_DIR="+r.Artifacts)
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

// Output is everything it printed so far.
func (r *Run) Output() string { r.mu.Lock(); defer r.mu.Unlock(); return r.out.String() }

// WaitFor waits until it has printed text.
func (r *Run) WaitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(r.Output(), text) {
		select {
		case <-r.done:
			t.Fatalf("the server exited (%v) without printing %q:\n%s", r.err, text, r.Output())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output:\n%s", text, r.Output())
		}
	}
}

// Send sends one line to the game port and returns the answer.
func (r *Run) Send(t *testing.T, line string) string {
	t.Helper()
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", r.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, r.Output())
	}
	return string(buf[:n])
}

// Exit waits for it to end with code 0.
func (r *Run) Exit(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatalf("the server exited with %v:\n%s", r.err, r.Output())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the server did not end:\n%s", r.Output())
	}
}

// Result reads match-result.json from the artifact folder.
func (r *Run) Result(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Artifacts, "match-result.json"))
	if err != nil {
		t.Fatalf("result file: %v\n%s", err, r.Output())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
