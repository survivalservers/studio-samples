//go:build linux

package gs

import (
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"
)

// TestConnectHasATimeLimit: an agent that never answers the connection fails the call within the library's 3 second
// limit, instead of holding the game's thread for the system's own limit (minutes). The address is a listener whose
// queue is full, so the system drops each new connection's first packet.
func TestConnectHasATimeLimit(t *testing.T) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", sa.(*syscall.SockaddrInet4).Port)
	// The one connection the queue holds, never accepted: from here a new one cannot open.
	held, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		c.Close()
		t.Skip("this system opens a connection to a full queue, so it cannot show the limit")
	}

	g, err := Open("http://"+addr, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	t0 := time.Now()
	err = g.Ready()
	took := time.Since(t0)
	if e, ok := err.(*Error); !ok || e.Code != -2 {
		t.Fatalf("a connection that never opens: %v", err)
	}
	if took < 2*time.Second || took > 8*time.Second {
		t.Fatalf("the call failed after %s, want about 3 s", took)
	}
}
