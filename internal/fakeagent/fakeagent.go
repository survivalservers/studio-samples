// Package fakeagent is a stand-in for the local API, for testing the samples: it checks the token, records
// every call in order, hands out an allocation on the second long-poll, and answers the shutdown long-poll when
// the test asks. The open-source local test agent replaces it for real use.
package fakeagent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// Agent is a running fake local API.
type Agent struct {
	URL   string
	Token string

	mu    sync.Mutex
	calls []string
	alloc any
	polls int
	stop  chan struct{}
	once  sync.Once
	srv   *httptest.Server
}

// New starts a fake local API. alloc is the allocation it hands out (nil: never allocated).
func New(token string, alloc any) *Agent {
	a := &Agent{Token: token, alloc: alloc, stop: make(chan struct{})}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	a.URL = a.srv.URL
	return a
}

// Close stops the server.
func (a *Agent) Close() { a.AskStop(); a.srv.Close() }

// AskStop makes the shutdown long-poll answer, as when a stop is asked for through the API.
func (a *Agent) AskStop() { a.once.Do(func() { close(a.stop) }) }

// Calls returns the calls so far, in order, as "POST /v0/self/ready", "PUT players a,b", `POST stop {...}`.
func (a *Agent) Calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

// Polls is how many times the allocation long-poll was called.
func (a *Agent) Polls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.polls
}

// Has reports whether a call was made.
func (a *Agent) Has(call string) bool {
	for _, c := range a.Calls() {
		if c == call {
			return true
		}
	}
	return false
}

func (a *Agent) record(call string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call)
}

func (a *Agent) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+a.Token {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /v0/self/ready", "POST /v0/self/health":
		a.record(r.Method + " " + r.URL.Path)
	case "GET /v0/self/allocation":
		if r.URL.Query().Get("wait") == "" {
			http.Error(w, "wait missing", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		a.polls++
		first := a.polls == 1
		a.mu.Unlock()
		if first || a.alloc == nil {
			select { // a real long-poll holds the call; keep it short here
			case <-r.Context().Done():
			case <-time.After(100 * time.Millisecond):
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		a.record("GET allocation")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.alloc)
	case "GET /v0/self/shutdown":
		select {
		case <-a.stop:
			a.record("GET shutdown")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"deadline": %q}`, time.Now().Add(30*time.Second).UTC().Format(time.RFC3339))
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		}
	case "PUT /v0/self/players":
		var body struct {
			IDs   []string `json:"ids"`
			Count *int     `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Count != nil {
			a.record(fmt.Sprintf("PUT count %d", *body.Count))
			break
		}
		// The agent's rule: an ID is 1 to 128 characters with no control characters, or the list is refused whole.
		for i, id := range body.IDs {
			if bad := idProblem(id); bad != "" {
				a.record(fmt.Sprintf("PUT players refused ids[%d]", i))
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				json.NewEncoder(w).Encode(map[string]any{"code": "validation_failed", "detail": "A player ID is not allowed.",
					"errors": []map[string]string{{"field": fmt.Sprintf("ids[%d]", i), "message": "it " + bad}}})
				return
			}
		}
		a.record("PUT players " + strings.Join(body.IDs, ","))
	case "PATCH /v0/self/labels":
		var body map[string]*string
		_ = json.NewDecoder(r.Body).Decode(&body)
		var parts []string
		for k, v := range body {
			if v == nil {
				parts = append(parts, k+"=(removed)")
			} else {
				parts = append(parts, k+"="+*v)
			}
		}
		sort.Strings(parts)
		a.record("PATCH labels " + strings.Join(parts, ","))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"labels": {}}`)
	case "POST /v0/self/tick":
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		a.record(fmt.Sprintf("POST tick hz=%v", body["hz"]))
		w.WriteHeader(http.StatusNoContent)
	case "POST /v0/self/stop":
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		a.record(fmt.Sprintf("POST stop %v", body["reason"]))
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func idProblem(id string) string {
	n := 0
	for _, r := range id {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return "has a control character"
		}
		n++
	}
	if n == 0 || n > 128 {
		return "is empty or longer than 128 characters"
	}
	return ""
}
