package freeport

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestOutside(t *testing.T) {
	for _, c := range []struct {
		lo, hi int
		want   []Span
	}{
		{32768, 60999, []Span{{Lowest, 32767}, {61000, 65535}}}, // Linux's default
		{40000, 65535, []Span{{Lowest, 39999}}},
		{1024, 65535, nil}, // everything is ephemeral: no room
		{1024, 50000, []Span{{50001, 65535}}},
		{0, 0, nil}, // unknown
	} {
		if got := Outside(c.lo, c.hi); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Outside(%d, %d) = %v, want %v", c.lo, c.hi, got, c.want)
		}
	}
	spans := []Span{{10000, 10002}, {20000, 20001}}
	var got []int
	for i := 0; i < room(spans); i++ {
		got = append(got, nth(spans, i))
	}
	if want := []int{10000, 10001, 10002, 20000, 20001}; !reflect.DeepEqual(got, want) {
		t.Errorf("nth over %v = %v, want %v", spans, got, want)
	}
}

// TestPickStaysOutTheKernelsRange: no program's bind(0) or outgoing connection can be handed a port Pick gave a test,
// because the kernel hands those out only inside its range.
func TestPickStaysOutTheKernelsRange(t *testing.T) {
	lo, hi := ephemeral()
	if room(Outside(lo, hi)) < minRoom {
		t.Skipf("this machine's ephemeral range %d-%d leaves no room outside it (Pick falls back to port 0)", lo, hi)
	}
	seen := map[string]bool{} // "tcp/40001"
	for i := 0; i < 40; i++ {
		proto := []string{"tcp", "udp"}[i%2]
		p, err := Pick(proto)
		if err != nil {
			t.Fatal(err)
		}
		if p < Lowest || (p >= lo && p <= hi) || p > 65535 {
			t.Fatalf("Pick(%s) = %d: inside the ephemeral range %d-%d or below %d", proto, p, lo, hi, Lowest)
		}
		if seen[proto+"/"+strconv.Itoa(p)] {
			t.Fatalf("Pick handed out %s port %d twice", proto, p)
		}
		seen[proto+"/"+strconv.Itoa(p)] = true
		// The port is free to the process the test hands it to.
		if !Free(proto, p) {
			t.Fatalf("Pick(%s) = %d, which something holds", proto, p)
		}
	}
	// And a bind of port 0 never lands there.
	for i := 0; i < 50; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if seen["tcp/"+strconv.Itoa(p)] {
			t.Fatalf("a bind of port 0 got %d, a port Pick gave out", p)
		}
	}
}

func TestPickFallsBackToPortZero(t *testing.T) {
	old := RangeFile
	t.Cleanup(func() { RangeFile = old })
	for name, content := range map[string]string{"all ephemeral": "1024\t65535\n", "unreadable": ""} {
		RangeFile = filepath.Join(t.TempDir(), "range")
		if content != "" {
			if err := os.WriteFile(RangeFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, proto := range []string{"tcp", "udp"} {
			if p, err := Pick(proto); err != nil || p <= 0 {
				t.Errorf("%s: Pick(%s) = %d, %v", name, proto, p, err)
			}
		}
	}
	if _, err := Pick("sctp"); err == nil {
		t.Error("Pick took an unknown protocol")
	}
}

func TestFreeSeesAHeldPort(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if Free("tcp", l.Addr().(*net.TCPAddr).Port) {
		t.Error("Free says a listening TCP port is free")
	}
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if Free("udp", c.LocalAddr().(*net.UDPAddr).Port) {
		t.Error("Free says a bound UDP port is free")
	}
	if !Free("udp", mustPick(t, "udp")) {
		t.Error("Free says a port Pick just gave out is held")
	}
}

func mustPick(t *testing.T, proto string) int {
	t.Helper()
	p, err := Pick(proto)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
