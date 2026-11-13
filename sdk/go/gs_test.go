package gs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestReplies: the C library reads the agent's replies as the agent sends them: a long allocation (chunked), JSON
// escapes, a refusal's reason, nothing yet, and a shutdown notice's deadline.
func TestReplies(t *testing.T) {
	big := strings.Repeat("x", 5000)
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(b))
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
	all := strings.Join(got, "\n")
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
