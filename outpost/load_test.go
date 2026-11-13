package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
)

// The defaults are a real game's; OUTPOST_LOAD=light is the small version; GS_MEMORY_MB caps what it holds.
func TestLoadSettings(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	c, err := fromEnv(env("GS_KIND", "match", "GS_MEMORY_MB", "4096", "GS_CPU", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Load; !l.On || l.BaseMB != 1536 || l.PlayerMB != [2]int{30, 60} || l.OutKbps != 200 || l.capMB() != 4096*80/100-64 {
		t.Fatalf("match defaults: %+v cap %d", l, l.capMB())
	}
	c, err = fromEnv(env("GS_KIND", "persistent_server", "OUTPOST_PLAYER_MB", "40-50", "OUTPOST_CPU_IDLE_PCT", "5.5", "OUTPOST_SAVE_EVERY", "15m"))
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Load; l.BaseMB != 2600 || l.GrowMaxMB != 1536 || l.PlayerMB != [2]int{40, 50} || l.IdlePct != 5.5 ||
		l.SaveEvery != 15*time.Minute || l.SaveBaseMB != 40 || l.capMB() < 1<<30 {
		t.Fatalf("world settings: %+v", l)
	}
	// A host from before the rename sends GS_KIND=world for a persistent server: the same settings.
	older, err := fromEnv(env("GS_KIND", "world", "OUTPOST_PLAYER_MB", "40-50", "OUTPOST_CPU_IDLE_PCT", "5.5", "OUTPOST_SAVE_EVERY", "15m"))
	if err != nil || older.Kind != kindPersistent || older.Load != c.Load {
		t.Fatalf("GS_KIND=world: kind %q, %+v, %v", older.Kind, older.Load, err)
	}
	c, err = fromEnv(env("OUTPOST_LOAD", "light"))
	if err != nil || c.Load.On || c.Load.BaseMB != 0 || c.Load.OutKbps != 0 {
		t.Fatalf("light: %+v %v", c.Load, err)
	}
	for _, bad := range [][]string{{"OUTPOST_LOAD", "heavy"}, {"OUTPOST_BASE_MB", "-1"}, {"OUTPOST_PLAYER_MB", "60-30"},
		{"OUTPOST_CPU_PLAYER_PCT", "x"}, {"OUTPOST_SAVE_EVERY", "1s"}} {
		if _, err := fromEnv(env(bad...)); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}
	// A size under one CPU holds the loop to its share of it.
	if m := (Load{CPUs: 0.5}).cpuMax(); m != 45 {
		t.Fatalf("half a CPU: at most %v%%", m)
	}
}

// With the load on, the server really holds the memory (the machine counts it), its update loop takes about the
// CPU the model says, each player gets state at the model's rate after the join burst and is asked to send its own
// share, and the world's save is a region file of the model's size that later saves rewrite only in part.
func TestLoadIsReal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc for what the system counts")
	}
	dir := t.TempDir()
	l := Load{On: true, BaseMB: 96, PlayerMB: [2]int{16, 16}, MemoryPct: 80, IdlePct: 20, PlayerPct: 10,
		OutKbps: 400, InKbps: 80, BurstKB: 64, SaveBaseMB: 2, SaveChunkKB: 16, ChunkKB: 256, GrowMaxMB: 64, SaveEvery: time.Hour}
	cfg := Config{ID: "gs_load", Kind: kindPersistent, Name: "Load World", SaveDir: filepath.Join(dir, "saves"), MaxPlayers: 24, Load: l, TickHz: 20, SendHz: 10}
	rss0 := rssMB(t)
	cpu0 := cpuSeconds(t)
	a := fakeagent.New("tok", nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	console, typeIn := io.Pipe()
	r := start(t, ctx, cfg, a, console)
	waitLog(t, r, "Loaded world Load World", 5*time.Second)

	p1, p2 := dial(t, r.address()), dial(t, r.address())
	p1.send("hello rook")
	if w := p1.until("welcome rook", 2*time.Second); !strings.HasSuffix(w, " up=10000") {
		t.Fatalf("welcome %q: no ask for 80 kb/s up", w)
	}
	p2.send("hello wren")
	p2.until("welcome wren", 2*time.Second)

	// The join burst first (the world around them), then state at about 400 kb/s = 50 KB a second.
	burst := 0
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		p1.input("rook", "gather")
		p2.input("wren", "build")
		if n, s := p1.read(50 * time.Millisecond); strings.HasPrefix(s, "map ") {
			burst += n
		}
	}
	if burst < 60<<10 {
		t.Fatalf("join burst %d bytes, want about 64 KB", burst)
	}
	got := 0
	from := time.Now()
	for time.Since(from) < 3*time.Second {
		p1.input("rook", "gather")
		p2.input("wren", "gather")
		n, _ := p1.read(50 * time.Millisecond)
		got += n
	}
	if kbps := float64(got) * 8 / 1000 / time.Since(from).Seconds(); kbps < 250 || kbps > 650 {
		t.Fatalf("state to one player: %.0f kb/s, want about 400", kbps)
	}

	// Memory: 96 MB of map plus 16 for each of the two players, written so the system counts it.
	if grew := rssMB(t) - rss0; grew < 110 {
		t.Fatalf("resident memory grew %d MB, want at least 110", grew)
	}
	// CPU: 20% empty plus 10% for each player is 40% of one CPU; the whole test process ran at least most of that.
	used := cpuSeconds(t) - cpu0
	if wall := time.Since(from).Seconds() + 2; used < 0.15*wall {
		t.Fatalf("CPU %.2f s over %.1f s, want about 40%%", used, wall)
	}

	// The save: world.json and world.regions of 2 MB terrain plus the explored chunks, 16 KB a record.
	fmt.Fprintln(typeIn, "save")
	waitLog(t, r, "World saved, 2 MB", 5*time.Second)
	path := filepath.Join(cfg.SaveDir, regionsFile)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	s := readSave(t, cfg.SaveDir)
	if want := int64(regionHeaderSize) + int64(128+s.Chunks)*16<<10; st.Size() != want {
		t.Fatalf("regions %d bytes, want %d (%d chunks explored)", st.Size(), want, s.Chunks)
	}
	before := revisions(t, path)
	for i := 0; i < 10; i++ {
		p1.input("rook", "gather")
		time.Sleep(60 * time.Millisecond)
	}
	fmt.Fprintln(typeIn, "save")
	waitLogCount(t, r, "World saved", 2, 5*time.Second)
	after := revisions(t, path)
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
		}
	}
	if changed == 0 || changed > 8 {
		t.Fatalf("the second save rewrote %d of %d records; want only the few rook gathered on", changed, len(before))
	}
	a.AskStop()
	r.wait(t, 10*time.Second)
}

// The state traffic follows OUTPOST_NET_OUT_KBPS whatever the update and snapshot rates (a match's 30 and 20 send a
// snapshot every update: 1.2.0 sent each player half as much again as its setting).
func TestStateRateFollowsTheSetting(t *testing.T) {
	l := Load{On: true, OutKbps: 240, IdlePct: 1, SaveEvery: time.Hour}
	cfg := Config{ID: "gs_rate", Kind: kindPersistent, Name: "Rate", SaveDir: t.TempDir(), MaxPlayers: 8, Load: l, TickHz: 30, SendHz: 20}
	a := fakeagent.New("tok", nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r := start(t, ctx, cfg, a, nil)
	p := dial(t, r.address())
	p.send("hello kite")
	p.until("welcome kite", 2*time.Second)
	got := 0
	from := time.Now()
	for time.Since(from) < 3*time.Second {
		p.input("kite", "")
		n, _ := p.read(50 * time.Millisecond)
		got += n
	}
	if kbps := float64(got) * 8 / 1000 / time.Since(from).Seconds(); kbps < 170 || kbps > 310 {
		t.Fatalf("state to one player: %.0f kb/s, want about 240", kbps)
	}
	a.AskStop()
	r.wait(t, 5*time.Second)
}

// The admin commands: players, kick, ban (and a banned player is kept out), unban, announce, xp, save.
func TestAdminCommands(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ID: "gs_adm", Kind: kindPersistent, Name: "EU PvP", SaveDir: filepath.Join(dir, "saves"), MaxPlayers: 24}
	a := fakeagent.New("tok", nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	console, typeIn := io.Pipe()
	r := start(t, ctx, cfg, a, console)
	vex, owl := dial(t, r.address()), dial(t, r.address())
	vex.send("hello Vex_77")
	vex.until("welcome Vex_77", 2*time.Second)
	owl.send("hello NightOwl")
	owl.until("welcome NightOwl", 2*time.Second)
	vex.send("in Vex_77 1 3.00 0.00") // a speed hack's input
	waitLog(t, r, "Speed check: Vex_77 moved 3.0x faster than allowed (1 time)", 2*time.Second)

	say := func(line, want string) {
		t.Helper()
		fmt.Fprintln(typeIn, line)
		waitLog(t, r, want, 2*time.Second)
	}
	say("players", "2 playing: Vex_77 (1 speed flag), NightOwl")
	say("announce Restart in 5 minutes", "Announced to 2 players: Restart in 5 minutes")
	owl.until("msg Restart in 5 minutes", 2*time.Second)
	say("xp 2", "Double XP on")
	owl.until("msg Double XP is on", 2*time.Second)
	say("kick nobody", "nobody is not on this server")
	say("ban Vex_77 cheating", "Banned Vex_77 (cheating), kicked from this server\nVex_77 left: banned")
	vex.until("over banned", 2*time.Second)
	vex.send("hello Vex_77")
	vex.until("banned cheating", 2*time.Second)
	say("ban Ghost99 griefing", "Banned Ghost99 (griefing)")
	say("players", "1 playing: NightOwl")
	say("kick NightOwl afk", "Kicked NightOwl (afk)")
	owl.until("over kicked", 2*time.Second)
	say("save", "World saved, ")
	if s := readSave(t, cfg.SaveDir); s.Bans["Vex_77"] != "cheating" || s.XP != 2 {
		t.Fatalf("the save keeps bans and the XP event: %+v %v", s.Bans, s.XP)
	}
	say("unban Vex_77", "Unbanned Vex_77")
	vex.send("hello Vex_77")
	vex.until("welcome Vex_77", 2*time.Second)
	say("xp 1", "Double XP off, XP back to normal")
	say("help", "Commands: players, kick <player>, ban <player> <reason>")
	for end := time.Now().Add(2 * time.Second); !a.Has("PATCH labels xp=1x") && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond) // labels go out off the game loop
	}
	if !a.Has("PATCH labels xp=2x") || !a.Has("PATCH labels xp=1x") {
		t.Errorf("no xp label in %v", a.Calls())
	}
	// The next run keeps the ban it was saved with.
	a.AskStop()
	r.wait(t, 5*time.Second)
	a2 := fakeagent.New("tok", nil)
	defer a2.Close()
	r2 := start(t, ctx, cfg, a2, nil)
	g := dial(t, r2.address())
	g.send("hello Ghost99")
	g.until("banned griefing", 2*time.Second)
	a2.AskStop()
	r2.wait(t, 5*time.Second)
}

// Region records: a whole file the first time, then only the changed and new records.
func TestRegionsWriteInPlace(t *testing.T) {
	dir := t.TempDir()
	g, _ := newGame(Config{Kind: kindPersistent, Name: "R", SaveDir: dir, Load: Load{On: true, SaveBaseMB: 1, SaveChunkKB: 8}}, local{}, io.Discard)
	g.save = &Save{Chunks: 3}
	if _, err := g.writeRegions(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, regionsFile)
	r0 := revisions(t, path)
	if len(r0) != 128+3 {
		t.Fatalf("%d records, want 131", len(r0))
	}
	g.touchAt(vec{10, 10})
	g.save.Chunks = 5
	if _, err := g.writeRegions(); err != nil {
		t.Fatal(err)
	}
	r1 := revisions(t, path)
	changed := 0
	for i := range r0 {
		if r0[i] != r1[i] {
			changed++
		}
	}
	if len(r1) != 133 || changed != 1 {
		t.Fatalf("%d records with %d of the old ones changed, want 133 and 1", len(r1), changed)
	}
	// A file from another world (or damaged) is written whole again.
	g.seed++
	if _, err := g.writeRegions(); err != nil {
		t.Fatal(err)
	}
	r2 := revisions(t, path)
	for i := range r1 {
		if r1[i] == r2[i] {
			t.Fatalf("record %d kept from a file that was not this world's", i)
		}
	}
}

func revisions(t *testing.T, path string) []uint64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:8]) != regionMagic {
		t.Fatalf("no region header in %s", path)
	}
	rec := int(binary.LittleEndian.Uint32(b[12:]))
	var out []uint64
	for p := regionHeaderSize; p+rec <= len(b); p += rec {
		out = append(out, binary.LittleEndian.Uint64(b[p+8:]))
	}
	return out
}

func (c *client) read(wait time.Duration) (int, string) {
	buf := make([]byte, 2048)
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	n, err := c.conn.Read(buf)
	if err != nil {
		return 0, ""
	}
	return n, string(buf[:n])
}

func waitLogCount(t *testing.T, r *run, s string, n int, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if strings.Count(r.log.String(), s) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d %q in the log:\n%s", n, s, r.log.String())
}

func rssMB(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var kb int
			fmt.Sscan(strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:")), &kb)
			return kb >> 10
		}
	}
	t.Fatal("no VmRSS")
	return 0
}

// cpuSeconds is this process's CPU time so far, from /proc (user and system, in clock ticks of 1/100 s).
func cpuSeconds(t *testing.T) float64 {
	t.Helper()
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:]))
	var u, s float64
	fmt.Sscan(f[11], &u)
	fmt.Sscan(f[12], &s)
	return (u + s) / 100
}

// tickRecorder is a local platform that keeps what Tick was sent (from the game's report goroutine, so under a lock).
type tickRecorder struct {
	local
	mu sync.Mutex
	hz []int
}

func (r *tickRecorder) Tick(_, _ float64, hz int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hz = append(r.hz, hz)
	return nil
}

func (r *tickRecorder) rates() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.hz...)
}

// The rate sent with the update times is the game's own (20 for a world, 30 for a match), also in a second that
// managed fewer updates, so the platform's frame-time budget (1000/rate) holds still.
func TestTickReportsTheGamesOwnRate(t *testing.T) {
	for _, tc := range []struct {
		kind string
		hz   int
	}{{kindPersistent, 20}, {kindMatch, 30}} {
		rec := &tickRecorder{}
		g, err := newGame(Config{Kind: tc.kind, Name: "R", TickHz: tc.hz, SendHz: 10, SaveDir: t.TempDir()}, rec, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for f := range g.report {
				f()
			}
		}()
		full := make([]time.Duration, tc.hz)
		short := make([]time.Duration, tc.hz/3) // a stalled second: a third of its updates
		g.reportTicks(full, time.Second)
		g.reportTicks(short, time.Second)
		close(g.report)
		for end := time.Now().Add(2 * time.Second); len(rec.rates()) < 2 && time.Now().Before(end); {
			time.Sleep(10 * time.Millisecond)
		}
		if hz := rec.rates(); len(hz) != 2 || hz[0] != tc.hz || hz[1] != tc.hz {
			t.Fatalf("%s: rates sent %v, want %d both times", tc.kind, hz, tc.hz)
		}
	}
}
