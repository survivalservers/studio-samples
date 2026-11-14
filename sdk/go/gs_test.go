package gs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPlayersNewestLast: players reports from two threads take turns, so the agent's last report is the newest list
// even when an older report is slow to be taken.
func TestPlayersNewestLast(t *testing.T) {
	var mu sync.Mutex
	var taken []string // each players report, in the order the agent took it
	first := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v0/self/players" {
			slow := false
			once.Do(func() { slow = true; close(first) })
			if slow {
				time.Sleep(300 * time.Millisecond) // the second thread's change comes while this one is in flight
			}
			mu.Lock()
			taken = append(taken, string(b))
			mu.Unlock()
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	g, err := Open(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	done := make(chan error, 1)
	go func() { done <- g.PlayerConnect("a") }()
	<-first
	if err := g.PlayerConnect("b"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(taken) == 0 || taken[len(taken)-1] != `{"ids":["a","b"]}` {
		t.Fatalf("the reports in the order the agent took them: %q", taken)
	}
}

// TestPlayersPastTheAgentsLimits: the agent refuses a list of more than 1,000 IDs or a body over 64 KiB, so past
// either the client sends the count alone, and the list again once it fits.
func TestPlayersPastTheAgentsLimits(t *testing.T) {
	var mu sync.Mutex
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body struct {
			IDs   []string `json:"ids"`
			Count *int     `json:"count"`
		}
		if len(b) > 64<<10 || json.Unmarshal(b, &body) != nil || len(body.IDs) > 1000 {
			w.WriteHeader(422) // as the agent does
			io.WriteString(w, `{"code":"validation_failed","detail":"Too many player IDs."}`)
			return
		}
		mu.Lock()
		last = string(b)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer srv.Close()
	lastReport := func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
	ids := func(s string) int {
		var body struct{ IDs []string }
		if json.Unmarshal([]byte(s), &body) != nil || !strings.HasPrefix(s, `{"ids":[`) {
			return -1
		}
		return len(body.IDs)
	}

	g, err := Open(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for i := 0; i < 1000; i++ {
		if err := g.PlayerConnect(fmt.Sprintf("p%04d", i)); err != nil {
			t.Fatalf("player %d: %v", i, err)
		}
	}
	if n := ids(lastReport()); n != 1000 {
		t.Fatalf("1,000 players: %d IDs in %.80q", n, lastReport())
	}
	if err := g.PlayerConnect("p1000"); err != nil {
		t.Fatal(err)
	}
	if got := lastReport(); got != `{"count":1001}` {
		t.Fatalf("1,001 players: %.80q", got)
	}
	if err := g.PlayerDisconnect("p1000"); err != nil {
		t.Fatal(err)
	}
	if n := ids(lastReport()); n != 1000 {
		t.Fatalf("back to 1,000: %d IDs", n)
	}

	// 128-character IDs of 252 bytes: about 257 fill 64 KiB.
	g2, err := Open(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()
	long := func(i int) string { return strings.Repeat("é", 124) + fmt.Sprintf("%04d", i) }
	for i := 0; i < 300; i++ {
		if err := g2.PlayerConnect(long(i)); err != nil {
			t.Fatalf("long player %d: %v", i, err)
		}
	}
	if got := lastReport(); got != `{"count":300}` {
		t.Fatalf("300 long IDs: %.80q", got)
	}
	for i := 299; i >= 200; i-- {
		if err := g2.PlayerDisconnect(long(i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := lastReport(); ids(got) != 200 || len(got) > 64<<10 {
		t.Fatalf("200 long IDs: %d IDs, %d bytes", ids(got), len(got))
	}
}

// TestReplies: the C library reads the agent's replies as the agent sends them: a long allocation (chunked), JSON
// escapes, a refusal's reason, nothing yet, and a shutdown notice's deadline.
func TestReplies(t *testing.T) {
	big := strings.Repeat("x", 5000)
	var mu sync.Mutex // the handlers run on the server's goroutines
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(b))
		mu.Unlock()
		switch r.URL.Path {
		case "/v0/self/allocation":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"game_server_id":"gs_01","external_id":"métro \"7\"","env":{"BIG":"`)
			w.(http.Flusher).Flush() // chunked from here
			io.WriteString(w, big+`","EMOJI":"😀","TAB":"a\tb"},"labels":{"queue":"ranked"},"args":["-MaxPlayers=10","a b"],"expires_at":null}`)
		case "/v0/self/shutdown":
			if r.URL.Query().Get("wait") == "50ms" {
				w.WriteHeader(204)
				return
			}
			io.WriteString(w, `{"requested_at":"2026-10-19T12:00:00Z","deadline":"2026-10-19T12:00:30.250Z","reason":"api"}`)
		case "/v0/self/labels":
			w.WriteHeader(422)
			io.WriteString(w, `{"code":"validation_failed","detail":"A label name is not allowed."}`)
		default:
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	g, err := Open(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a, err := g.WaitAllocation(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.ExternalID != `métro "7"` || len(a.Env["BIG"]) != 5000 || a.Env["EMOJI"] != "😀" || a.Env["TAB"] != "a\tb" || a.Labels["queue"] != "ranked" ||
		a.GameServerID != "gs_01" || len(a.Args) != 2 || a.Args[0] != "-MaxPlayers=10" || a.Args[1] != "a b" {
		t.Fatalf("allocation %q %q %d %q %q %v %q", a.GameServerID, a.ExternalID, len(a.Env["BIG"]), a.Env["EMOJI"], a.Env["TAB"], a.Labels, a.Args)
	}
	if sd, err := g.WaitShutdown(50 * time.Millisecond); err != nil || sd != nil {
		t.Fatalf("no notice yet: %v %v", sd, err)
	}
	sd, err := g.WaitShutdown(time.Second)
	if err != nil || sd.Reason != "api" || sd.Deadline.UnixMilli() != time.Date(2026, 10, 19, 12, 0, 30, 250e6, time.UTC).UnixMilli() {
		t.Fatalf("notice %+v %v", sd, err)
	}
	err = g.SetLabel("Bad Name", "x")
	if e, ok := err.(*Error); !ok || e.Code != -3 || !strings.Contains(e.Message, "not allowed") {
		t.Fatalf("refusal %v", err)
	}
	if err := g.Tick(2.5, 6.25, 64); err != nil {
		t.Fatal(err)
	}
	g.PlayerConnect("p1")
	// A name a player chose with a control character in it never joins the list (the agent would refuse the whole
	// list), so the others keep counting.
	for _, bad := range []string{"bad\x00name", "bad\x01name", "tab\tname", "c1\u0085", "bad\xffutf8", "", strings.Repeat("é", 129)} {
		if e, ok := g.PlayerConnect(bad).(*Error); !ok || e.Code != -5 || !strings.Contains(e.Message, "player ID") {
			t.Fatalf("PlayerConnect(%q): %v", bad, e)
		}
	}
	if err := g.PlayerConnect(strings.Repeat("é", 128)); err != nil {
		t.Fatalf("128 characters: %v", err)
	}
	g.PlayerDisconnect(strings.Repeat("é", 128))
	g.PlayerConnect(`a "quoted" one`)
	g.PlayerDisconnect("p1")
	g.SetPlayerCount(3)
	g.Saved("Season 3 end")
	g.Saved("")
	g.Stop("done")
	want := []string{
		`PUT /v0/self/players {"ids":["p1"]}`, `PUT /v0/self/players {"ids":["p1","a \"quoted\" one"]}`,
		`PUT /v0/self/players {"ids":["a \"quoted\" one"]}`, `PUT /v0/self/players {"count":3}`,
		`POST /v0/self/tick {"p50_ms":2.500,"p99_ms":6.250,"hz":64}`, `POST /v0/self/stop {"reason":"done"}`,
		`POST /v0/self/saved {"keep":"Season 3 end"}`, `POST /v0/self/saved {}`,
	}
	mu.Lock()
	all := strings.Join(got, "\n")
	mu.Unlock()
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Fatalf("missing %s in:\n%s", w, all)
		}
	}
	if _, err := Open("http://127.0.0.1:1", "t"); err != nil {
		t.Fatal(err)
	}
	g2, _ := Open("http://127.0.0.1:1", "t")
	defer g2.Close()
	if err := g2.Ready(); err == nil || err.(*Error).Code != -2 {
		t.Fatalf("an agent that does not answer: %v", err)
	}
}

// TestOlderNamesChangeNothing: FromEnv reads the local API's address from GS_LOCAL_API_URL, or from its older name
// GS_AGENT_URL when the new one is not set, so a build gets the same calls under either (and GS_KIND world reads as
// persistent_server).
func TestOlderNamesChangeNothing(t *testing.T) {
	var calls [2][]string
	for i, names := range []map[string]string{
		{"GS_KIND": "persistent_server", "GS_LOCAL_API_URL": "", "GS_AGENT_URL": "-"},
		{"GS_KIND": "world", "GS_LOCAL_API_URL": "-", "GS_AGENT_URL": ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls[i] = append(calls[i], r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
			w.WriteHeader(204)
		}))
		t.Setenv("GS_TOKEN", "tok-1")
		for k, v := range names {
			// "" here is the address of this case's server; "-" leaves the variable empty.
			switch v {
			case "":
				v = srv.URL
			case "-":
				v = ""
			}
			t.Setenv(k, v)
		}
		g, err := FromEnv()
		if err != nil {
			t.Fatalf("%v: %v", names, err)
		}
		if err := g.Ready(); err != nil {
			t.Fatal(err)
		}
		if err := g.Health(); err != nil {
			t.Fatal(err)
		}
		g.Close()
		srv.Close()
	}
	if want := []string{"POST /v0/self/ready Bearer tok-1", "POST /v0/self/health Bearer tok-1"}; !slices.Equal(calls[0], want) ||
		!slices.Equal(calls[1], want) {
		t.Fatalf("calls: current %v, older %v", calls[0], calls[1])
	}
}

// TestBackground: the helper sends health on its own, calls onMatch once the match comes and onStop once the notice
// does (after which the match wait ends too), and StopBackground ends it promptly; a second start while it runs is
// refused.
func TestBackground(t *testing.T) {
	var mu sync.Mutex
	health, allocated, noticed := 0, false, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v0/self/health":
			health++
			w.WriteHeader(204)
		case "/v0/self/allocation":
			if health < 3 { // the match comes once health has been sent a few times
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				w.WriteHeader(204)
				return
			}
			allocated = true
			io.WriteString(w, `{"game_server_id":"gs_01","external_id":"m1","env":{"MAP":"dunes"},"labels":{},"args":[]}`)
		case "/v0/self/shutdown":
			if !allocated {
				w.WriteHeader(204)
				return
			}
			noticed = true
			io.WriteString(w, `{"requested_at":"2026-11-09T12:00:00Z","deadline":"2026-11-09T12:00:30Z","reason":"match_over"}`)
		default:
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	g, err := Open(srv.URL, "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	matched, stopped := make(chan *Allocation, 2), make(chan *Shutdown, 2)
	if err := g.StartBackground(10*time.Millisecond, func(a *Allocation) { matched <- a }, func(s *Shutdown) { stopped <- s }); err != nil {
		t.Fatal(err)
	}
	if err := g.StartBackground(time.Second, nil, nil); err == nil {
		t.Fatal("a second start was taken")
	}
	select {
	case a := <-matched:
		if a.ExternalID != "m1" || a.Env["MAP"] != "dunes" {
			t.Fatalf("match %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no match")
	}
	select {
	case s := <-stopped:
		if s.Reason != "match_over" || s.Deadline.UnixMilli() != time.Date(2026, 11, 9, 12, 0, 30, 0, time.UTC).UnixMilli() {
			t.Fatalf("notice %+v", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notice")
	}
	time.Sleep(50 * time.Millisecond)
	t0 := time.Now()
	g.StopBackground()
	if d := time.Since(t0); d > 6*time.Second {
		t.Fatalf("stop took %s", d)
	}
	mu.Lock()
	n := health
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n < 3 || health != n || !noticed || len(matched) != 0 || len(stopped) != 0 {
		t.Fatalf("health %d then %d, noticed %v, extra calls %d %d", n, health, noticed, len(matched), len(stopped))
	}
}
