// Runs quickstart.sh end to end against a small fake of the API: a Windows zip uploaded in parts and a Linux
// image, each through build, version, waiting and start. Needs bash, curl and jq, like the script.
package quickstart

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/procgroup"
)

type fakeAPI struct {
	t        *testing.T
	url      string
	mu       sync.Mutex
	appMade  bool
	declared struct {
		Bytes  int    `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	partSize  int
	parts     map[int][]byte
	buildGets int
	verGets   int
	version   map[string]any
	keys      map[string]bool
	log       []string
	// The start's answer before it succeeds: notReady 409 version_not_ready replies first; or, with refuse set, that
	// status and refuseBody every time.
	notReady   int
	refuse     int
	refuseBody string
	startKeys  map[string]bool
	abandoned  map[string]bool
}

func (f *fakeAPI) fail(w http.ResponseWriter, code int, msg string) {
	f.t.Errorf("fake API: %s", msg)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"code":"bad_request","detail":%q}`, msg)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	f.log = append(f.log, route)
	if strings.HasPrefix(r.URL.Path, "/upload/") { // presigned storage URLs: no API key
		var n int
		fmt.Sscanf(r.URL.Path, "/upload/%d", &n)
		if r.ContentLength <= 0 || len(r.TransferEncoding) > 0 {
			f.fail(w, 411, fmt.Sprintf("part %d sent without a length (storage links need one)", n))
			return
		}
		b, _ := io.ReadAll(r.Body)
		if len(b) == 0 || len(b) > f.partSize {
			f.fail(w, 400, fmt.Sprintf("part %d has %d bytes", n, len(b)))
			return
		}
		f.parts[n] = b
		sum := md5.Sum(b)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer gsk_live_EXAMPLE_not_a_real_key" {
		f.fail(w, 401, "no key on "+route)
		return
	}
	if r.Method == http.MethodPost {
		k := r.Header.Get("Idempotency-Key")
		// As the API does: a key whose start was refused is free again (the claim is dropped), any other key is
		// used once.
		if k == "" || (f.keys[k] && !f.abandoned[k]) {
			f.fail(w, 400, "missing or repeated Idempotency-Key on "+route)
			return
		}
		f.keys[k] = true
		delete(f.abandoned, k)
	}
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				f.fail(w, 400, "bad JSON on "+route)
				return
			}
			if route == "POST /v0/apps/plain-server/builds" {
				var src struct{ Source json.RawMessage }
				_ = json.Unmarshal(raw, &src)
				_ = json.Unmarshal(src.Source, &f.declared)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch route {
	case "GET /v0/apps/plain-server":
		if !f.appMade {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":"not_found"}`)
			return
		}
		fmt.Fprint(w, `{"slug":"plain-server"}`)
	case "POST /v0/apps":
		// The tier the open region sells on each system: a Linux app made standard cannot start there.
		wantTier := map[string]string{"linux": "high-frequency", "windows": "standard"}[fmt.Sprint(body["os"])]
		if body["slug"] != "plain-server" || wantTier == "" || body["tier"] != wantTier ||
			fmt.Sprint(body["default_placement"]) != "map[continents:[europe]]" {
			f.fail(w, 422, fmt.Sprintf("app body %v", body))
			return
		}
		f.appMade = true
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"app_1","slug":"plain-server"}`)
	case "POST /v0/apps/plain-server/builds":
		src, _ := body["source"].(map[string]any)
		if src["type"] == "image" {
			w.WriteHeader(202)
			fmt.Fprint(w, `{"id":"bld_img","status":"processing"}`)
			return
		}
		n := (f.declared.Bytes + f.partSize - 1) / f.partSize
		parts := []map[string]any{}
		for i := 1; i <= n; i++ {
			parts = append(parts, map[string]any{"number": i, "url": fmt.Sprintf("%s/upload/%d?sig=x", f.url, i)})
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_zip", "status": "awaiting_upload",
			"upload": map[string]any{"part_bytes": f.partSize, "parts": parts}})
	case "POST /v0/builds/bld_zip/complete":
		var whole []byte
		for i, p := range body["parts"].([]any) {
			part := p.(map[string]any)
			n := int(part["number"].(float64))
			sum := md5.Sum(f.parts[n])
			if n != i+1 || part["etag"] != `"`+hex.EncodeToString(sum[:])+`"` {
				f.fail(w, 422, fmt.Sprintf("part %d: etag %v", n, part["etag"]))
				return
			}
			whole = append(whole, f.parts[n]...)
		}
		sum := sha256.Sum256(whole)
		if len(whole) != f.declared.Bytes || hex.EncodeToString(sum[:]) != f.declared.Sha256 {
			f.fail(w, 422, fmt.Sprintf("reassembled %d bytes, checksum mismatch", len(whole)))
			return
		}
		w.WriteHeader(202)
		fmt.Fprint(w, `{"id":"bld_zip","status":"processing"}`)
	case "GET /v0/builds/bld_zip", "GET /v0/builds/bld_img":
		f.buildGets++
		status := "processing"
		if f.buildGets%2 == 0 {
			status = "ready"
		}
		fmt.Fprintf(w, `{"status":%q}`, status)
	case "POST /v0/apps/plain-server/versions":
		f.version = body
		w.WriteHeader(201)
		fmt.Fprint(w, `{"name":"1.0.0","regions":[{"region":"europe-west","status":"staging"}]}`)
	case "GET /v0/apps/plain-server/versions/1.0.0":
		f.verGets++
		status := "staging"
		if f.verGets%2 == 0 {
			status = "ready"
		}
		fmt.Fprintf(w, `{"name":"1.0.0","regions":[{"region":"europe-west","status":%q},{"region":"europe-central","status":"pending"}]}`, status)
	case "POST /v0/game-servers":
		if f.startKeys == nil {
			f.startKeys = map[string]bool{}
		}
		f.startKeys[r.Header.Get("Idempotency-Key")] = true
		if f.refuse != 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(f.refuse)
			fmt.Fprint(w, f.refuseBody)
			return
		}
		if f.notReady > 0 {
			f.notReady--
			if f.abandoned == nil {
				f.abandoned = map[string]bool{}
			}
			f.abandoned[r.Header.Get("Idempotency-Key")] = true
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(409)
			fmt.Fprint(w, `{"code":"version_not_ready","detail":"Version 1.0.0 is not staged yet in any region this start may use."}`)
			return
		}
		if body["app"] != "plain-server" || body["version"] != "1.0.0" ||
			fmt.Sprint(body["placement"]) != "map[continents:[europe]]" {
			f.fail(w, 422, fmt.Sprintf("start body %v", body))
			return
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"gs_1","region":"europe-west","continent":"europe","status":"running","address":{"ipv4":"203.0.113.9","ports":{"game":{"port":31042,"protocol":"udp"}}}}`)
	default:
		f.fail(w, 404, "unexpected "+route)
	}
}

func run(t *testing.T, api *fakeAPI, args ...string) string {
	t.Helper()
	out, err := runErr(t, api, args...)
	if err != nil {
		t.Fatalf("quickstart.sh %v: %v\n%s\ncalls: %v", args, err, out, api.log)
	}
	return out
}

func runErr(t *testing.T, api *fakeAPI, args ...string) (string, error) {
	t.Helper()
	for _, tool := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{"quickstart.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "API="+api.url, "API_KEY=gsk_live_EXAMPLE_not_a_real_key", "POLL_SECONDS=0", "PRACTICE=0")
	// The script and every curl it starts end with the test, on a timeout too.
	procgroup.Prepare(cmd)
	cmd.Cancel = func() error { procgroup.Kill(cmd); return nil }
	defer procgroup.Kill(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, partSize: 1 << 20, parts: map[int][]byte{}, keys: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func TestWindowsZipUploadedInParts(t *testing.T) {
	api := newFake(t)
	zip := filepath.Join(t.TempDir(), "Server.zip")
	data := make([]byte, 2<<20+12345) // three parts, the last one short
	_, _ = rand.Read(data)
	if err := os.WriteFile(zip, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, api, "windows", zip, "../plain/version-windows.json")

	if len(api.parts) != 3 || !bytes.Equal(append(append(api.parts[1], api.parts[2]...), api.parts[3]...), data) {
		t.Fatalf("parts did not add up to the zip (%d parts)", len(api.parts))
	}
	if api.version["build"] != "bld_zip" {
		t.Fatalf("version must name the uploaded build: %v", api.version["build"])
	}
	launch, _ := api.version["launch"].(map[string]any)
	if launch["executable"] != "PlainServer.exe" {
		t.Fatalf("version settings not sent as written: %v", launch)
	}
	if !strings.Contains(out, "Players connect to 203.0.113.9:31042") {
		t.Fatalf("output:\n%s", out)
	}
	if api.buildGets < 2 || api.verGets < 2 {
		t.Fatalf("must wait for the build (%d polls) and a ready region (%d polls)", api.buildGets, api.verGets)
	}
}

func TestLinuxImage(t *testing.T) {
	api := newFake(t)
	out := run(t, api, "linux", "registry.example.com/example/plain-server:1.0.0", "../plain/version-linux.json")
	if api.version["build"] != "bld_img" || !strings.Contains(out, "gs_1 in europe-west, running") {
		t.Fatalf("version %v, output:\n%s", api.version, out)
	}
	ports := api.version["launch"].(map[string]any)["ports"].([]any)
	if ports[0].(map[string]any)["container_port"] != float64(7777) {
		t.Fatalf("the image's port must be in the version: %v", ports)
	}
}

// A region can read ready a moment before placement has caught up (a start 0.76 s after "ready"
// answered 409 version_not_ready): the start is tried again, with the same key, until it goes through.
func TestAStartTheRegionIsNotReadyForIsTriedAgain(t *testing.T) {
	api := newFake(t)
	api.mu.Lock()
	api.notReady = 2 // under the lock: the handler reads it on its own goroutine
	api.mu.Unlock()
	out := run(t, api, "linux", "registry.example.com/example/plain-server:1.0.0", "../plain/version-linux.json")
	if strings.Count(out, "waiting for the image to reach the region") != 2 || !strings.Contains(out, "Players connect to 203.0.113.9:31042") {
		t.Fatalf("output:\n%s", out)
	}
	if len(api.startKeys) != 1 {
		t.Fatalf("every try must carry the same Idempotency-Key, so it can never start two: %d keys", len(api.startKeys))
	}
}

// Any other refusal stops the script with the API's own words (the error print once sent every JSON reply to
// /dev/null, so a 409 read as if it had no body), and an empty reply says so.
func TestARefusedStartSaysWhy(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{402, `{"code":"card_required","detail":"Add a card before the first start."}`, "  card_required: Add a card before the first start."},
		{409, ``, "  (no body)"},
		{502, `Bad Gateway`, "Bad Gateway"},
	} {
		api := newFake(t)
		api.mu.Lock() // the fake answers on its own goroutines, under this lock
		api.refuse, api.refuseBody = c.status, c.body
		api.mu.Unlock()
		out, err := runErr(t, api, "linux", "registry.example.com/example/plain-server:1.0.0", "../plain/version-linux.json")
		if err == nil || !strings.Contains(out, fmt.Sprintf("POST /v0/game-servers failed (%d):\n%s", c.status, c.want)) {
			t.Fatalf("%d: err %v, output:\n%s", c.status, err, out)
		}
		if strings.Contains(out, "waiting for the image") {
			t.Fatalf("%d: only version_not_ready is tried again:\n%s", c.status, out)
		}
	}
}

// The script reads the app, the build and the version back (GET calls, scope apps:read) as well as writing them, so
// the key it asks for must carry apps:read: scope checks are exact matches, and a key without it failed at the
// first wait.
func TestNoKeyNamesEveryScopeTheScriptUses(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	cmd := exec.Command("bash", "quickstart.sh", "linux", "registry.example.com/x:1", "version-linux.json")
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "API_KEY=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("quickstart.sh ran without API_KEY:\n%s", out)
	}
	for _, scope := range []string{"apps:read", "apps:write", "builds:write", "game_servers:write"} {
		if !strings.Contains(string(out), scope) {
			t.Errorf("the missing-key message does not name %s: %s", scope, out)
		}
	}
}
