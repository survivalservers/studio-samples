// Package freeport picks a port for a test that hands it to a game server process, which binds it later.
//
// A port picked by binding port 0 comes from the kernel's ephemeral range, the pool every program's bind(0) and
// outgoing connection draws from, so between the pick and the game server's bind anything on the machine can be handed
// it, and the game server then fails to start. A test cannot hold such a port for another process. On Linux, Pick
// takes a random free port outside that range instead (/proc/sys/net/ipv4/ip_local_port_range; 32768-60999 by
// default), where the kernel hands out nothing by itself, and checks no socket holds it on any address. Where the range
// leaves too little room (a machine set to 1024-65535) or cannot be read, it falls back to port 0.
//
// Ports handed out in this process are not handed out again, so two tests running side by side never get the same one.
package freeport

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Lowest is where the ports Pick chooses from start: below it are the well-known and registered ports services listen on.
const Lowest = 10000

// minRoom is the fewest ports outside the ephemeral range worth choosing from; with fewer Pick uses port 0.
const minRoom = 1000

// RangeFile is where Linux keeps its ephemeral range.
var RangeFile = "/proc/sys/net/ipv4/ip_local_port_range"

var (
	mu    sync.Mutex
	given = map[string]bool{} // "udp/40001": already handed out in this process
)

// Pick returns a port nothing holds just now for proto ("tcp" or "udp"), outside the kernel's ephemeral range where
// there is room.
func Pick(proto string) (int, error) {
	if proto != "tcp" && proto != "udp" {
		return 0, fmt.Errorf("freeport: unknown protocol %q", proto)
	}
	if spans := Outside(ephemeral()); room(spans) >= minRoom {
		for try := 0; try < 200; try++ {
			port := nth(spans, rand.IntN(room(spans)))
			if take(proto, port) && Free(proto, port) {
				return port, nil
			}
		}
	}
	return zero(proto)
}

// Span is a run of ports, both ends included.
type Span struct{ Lo, Hi int }

// Outside is the ports from Lowest to 65535 that are not in the ephemeral range lo-hi; nil when the range is unknown
// (lo is 0).
func Outside(lo, hi int) []Span {
	if lo <= 0 || hi < lo {
		return nil
	}
	var out []Span
	if lo-1 >= Lowest {
		out = append(out, Span{Lowest, lo - 1})
	}
	if hi+1 <= 65535 {
		out = append(out, Span{max(hi+1, Lowest), 65535})
	}
	return out
}

func room(spans []Span) int {
	n := 0
	for _, s := range spans {
		n += s.Hi - s.Lo + 1
	}
	return n
}

// nth is the i-th port of the spans, counted from the first.
func nth(spans []Span, i int) int {
	for _, s := range spans {
		if i <= s.Hi-s.Lo {
			return s.Lo + i
		}
		i -= s.Hi - s.Lo + 1
	}
	return spans[len(spans)-1].Hi
}

// ephemeral reads the kernel's ephemeral range; 0, 0 when it cannot (not Linux, or no /proc).
func ephemeral() (int, int) {
	b, err := os.ReadFile(RangeFile)
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, 0
	}
	lo, err1 := strconv.Atoi(f[0])
	hi, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return lo, hi
}

// take marks a port as handed out in this process; false when it already was.
func take(proto string, port int) bool {
	mu.Lock()
	defer mu.Unlock()
	k := proto + "/" + strconv.Itoa(port)
	if given[k] {
		return false
	}
	given[k] = true
	return true
}

// Free reports whether no socket holds the port: a bind on every IPv4 address conflicts with one on any of them.
func Free(proto string, port int) bool {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	if proto == "udp" {
		c, err := net.ListenPacket("udp4", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// zero is the old way: whatever port the kernel hands a bind of port 0, let go at once.
func zero(proto string) (int, error) {
	if proto == "udp" {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port, nil
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
