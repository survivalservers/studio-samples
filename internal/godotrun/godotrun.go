// Package godotrun runs a Godot project headless for the samples' tests: it finds Godot, copies the project to a
// temporary folder (Godot writes its cache next to the project), starts it in a process group of its own that the
// test ends whatever happens, and reads its output, its UDP game port and its result file.
package godotrun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/freeport"
	"github.com/survivalservers/studio-samples/internal/procgroup"
)

// Program is the Godot 4 program: GODOT, or godot or godot4 on the PATH. The test is skipped without one.
func Program(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("GODOT"); p != "" {
		return p
	}
	for _, name := range []string{"godot", "godot4"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("Godot not found: set GODOT to run this test")
	return ""
}

// Copy copies the named files and folders under src into a new temporary project folder and returns it.
func Copy(t *testing.T, src string, names ...string) string {
	t.Helper()
	dst := t.TempDir()
	for _, name := range names {
		root := filepath.Join(src, name)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
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

type output struct {
	mu sync.Mutex
	b  strings.Builder
}

func (o *output) add(line string) { o.mu.Lock(); o.b.WriteString(line + "\n"); o.mu.Unlock() }
func (o *output) String() string  { o.mu.Lock(); defer o.mu.Unlock(); return o.b.String() }

// Run is one running Godot.
type Run struct {
	Port      int    // the UDP game port (GS_PORT_GAME)
	Artifacts string // GS_ARTIFACT_DIR
	cmd       *exec.Cmd
	out       *output
	done      chan struct{} // closed when Godot has exited
	err       error
}

// Start runs args (after the program; "--headless" is added) with env added to the test's own environment, and a
// free game port and an artifact folder in GS_PORT_GAME and GS_ARTIFACT_DIR. Godot and everything it started are
// ended when the test ends, passed or failed.
func Start(t *testing.T, program string, env []string, args ...string) *Run {
	t.Helper()
	r := &Run{Port: FreeUDPPort(t), Artifacts: t.TempDir(), out: &output{}, done: make(chan struct{})}
	r.cmd = exec.Command(program, append([]string{"--headless"}, args...)...)
	r.cmd.Env = append(append(os.Environ(), env...),
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
			r.out.add(s.Text())
		}
		_, _ = io.Copy(io.Discard, pipe)
		r.err = r.cmd.Wait()
		close(r.done)
	}()
	return r
}

// Output is everything Godot printed so far.
func (r *Run) Output() string { return r.out.String() }

// WaitFor waits until Godot has printed text.
func (r *Run) WaitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(r.out.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output:\n%s", text, r.out.String())
		}
		select {
		case <-r.done:
			if !strings.Contains(r.out.String(), text) {
				t.Fatalf("Godot exited (%v) without printing %q:\n%s", r.err, text, r.out.String())
			}
		case <-time.After(50 * time.Millisecond):
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
		t.Fatalf("%s: %v\n%s", line, err, r.out.String())
	}
	return string(buf[:n])
}

// Exit waits for Godot to end with code 0.
func (r *Run) Exit(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatalf("Godot exited with %v:\n%s", r.err, r.out.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("Godot did not end:\n%s", r.out.String())
	}
}

// Result reads match-result.json from the artifact folder.
func (r *Run) Result(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Artifacts, "match-result.json"))
	if err != nil {
		t.Fatalf("result file: %v\n%s", err, r.out.String())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
