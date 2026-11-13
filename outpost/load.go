package main

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// Load is how heavy the server is, so the platform measures what a real game of this shape costs: memory it really
// holds (a map loaded at start, more for each player and as a world is explored), an update loop whose work grows
// with the players, state traffic to each player at a real game's rate with a burst when someone joins, and a world
// save file of a real size. Every number is a setting (OUTPOST_* variables, below); OUTPOST_LOAD=light, or a Config
// with no Load (the tests), is the small version: no extra memory, work, traffic or save file.
type Load struct {
	On bool

	// Memory, in MiB. A match holds BaseMB from the start; a world holds BaseMB plus ChunkKB for each explored chunk
	// (up to GrowMaxMB) plus PlayerMB for each player online. Never more than MemoryPct of the size (GS_MEMORY_MB).
	BaseMB, GrowMaxMB, ChunkKB int
	PlayerMB                   [2]int // each player takes a random amount in this range
	LimitMB, MemoryPct         int

	// CPU, in percent of one CPU: IdlePct with nobody on, PlayerPct more for each player, wandering by JitterPct of
	// the total. Never more than 90% of one CPU, or of the size when it is under one CPU (GS_CPU).
	IdlePct, PlayerPct, JitterPct float64
	CPUs                          float64

	// Network, in kilobits a second for each player (the panel's unit): the server's state out, and what it asks each
	// client to send (it says so in its welcome). A joining player is sent BurstKB first (the map, or the world
	// around them); every so often an event doubles the state traffic for a few seconds.
	OutKbps, InKbps, BurstKB int

	// A world's save: world.json plus world.regions, SaveBaseMB of terrain and SaveChunkKB for each explored chunk.
	// ChunksPerHour is how many new chunks a player explores in an hour of play. Saves every SaveEvery.
	SaveBaseMB, SaveChunkKB int
	ChunksPerHour           float64
	SaveEvery               time.Duration
}

// loadFromEnv reads the OUTPOST_* settings for a kind of server, from the defaults below.
func loadFromEnv(kind string, getenv func(string) string) (Load, error) {
	l := Load{On: true, MemoryPct: 80, JitterPct: 20, ChunkKB: 256, PlayerMB: [2]int{30, 60}, InKbps: 80,
		SaveBaseMB: 40, SaveChunkKB: 48, ChunksPerHour: 3, SaveEvery: 5 * time.Minute}
	if kind == kindMatch {
		l.BaseMB, l.IdlePct, l.PlayerPct, l.OutKbps, l.BurstKB = 1536, 6, 3, 200, 400
	} else {
		l.BaseMB, l.GrowMaxMB, l.IdlePct, l.PlayerPct, l.OutKbps, l.BurstKB = 2600, 1536, 8, 3, 160, 2048
	}
	switch v := strings.ToLower(getenv("OUTPOST_LOAD")); v {
	case "", "game":
	case "light", "off":
		l = Load{SaveEvery: 5 * time.Minute}
	default:
		return l, fmt.Errorf("OUTPOST_LOAD=%q: game (the default) or light", v)
	}
	ints := []struct {
		name   string
		to     *int
		lo, hi int
	}{
		{"OUTPOST_BASE_MB", &l.BaseMB, 0, 1 << 20}, {"OUTPOST_GROW_MAX_MB", &l.GrowMaxMB, 0, 1 << 20},
		{"OUTPOST_CHUNK_KB", &l.ChunkKB, 1, 1 << 20}, {"OUTPOST_MEMORY_PCT", &l.MemoryPct, 10, 95},
		{"OUTPOST_NET_OUT_KBPS", &l.OutKbps, 0, 100000}, {"OUTPOST_NET_IN_KBPS", &l.InKbps, 0, 100000},
		{"OUTPOST_JOIN_BURST_KB", &l.BurstKB, 0, 1 << 20},
		{"OUTPOST_SAVE_BASE_MB", &l.SaveBaseMB, 0, 1 << 16}, {"OUTPOST_SAVE_CHUNK_KB", &l.SaveChunkKB, 1, 1 << 14},
		{"GS_MEMORY_MB", &l.LimitMB, 0, 1 << 30},
	}
	for _, s := range ints {
		if v := getenv(s.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < s.lo || n > s.hi {
				return l, fmt.Errorf("%s=%q is not a number from %d to %d", s.name, v, s.lo, s.hi)
			}
			*s.to = n
		}
	}
	floats := []struct {
		name   string
		to     *float64
		lo, hi float64
	}{
		{"OUTPOST_CPU_IDLE_PCT", &l.IdlePct, 0, 90}, {"OUTPOST_CPU_PLAYER_PCT", &l.PlayerPct, 0, 90},
		{"OUTPOST_CPU_JITTER_PCT", &l.JitterPct, 0, 80}, {"OUTPOST_CHUNKS_PER_HOUR", &l.ChunksPerHour, 0, 10000},
		{"GS_CPU", &l.CPUs, 0, 1024},
	}
	for _, s := range floats {
		if v := getenv(s.name); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < s.lo || f > s.hi {
				return l, fmt.Errorf("%s=%q is not a number from %g to %g", s.name, v, s.lo, s.hi)
			}
			*s.to = f
		}
	}
	if v := getenv("OUTPOST_PLAYER_MB"); v != "" {
		lo, hi, ok := strings.Cut(v, "-")
		if !ok {
			hi = lo
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || a < 0 || b < a || b > 1<<16 {
			return l, fmt.Errorf("OUTPOST_PLAYER_MB=%q: a number of MiB or a range like 30-60", v)
		}
		l.PlayerMB = [2]int{a, b}
	}
	if v := getenv("OUTPOST_SAVE_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 10*time.Second || d > 24*time.Hour {
			return l, fmt.Errorf("OUTPOST_SAVE_EVERY=%q: a duration from 10s to 24h", v)
		}
		l.SaveEvery = d
	}
	return l, nil
}

// capMB is the most memory the game lets itself hold: MemoryPct of its size, with room for the program itself.
func (l Load) capMB() int {
	if l.LimitMB <= 0 {
		return math.MaxInt32
	}
	return max(l.LimitMB*l.MemoryPct/100-64, 0)
}

// cpuMax is the most of one CPU the update loop may take.
func (l Load) cpuMax() float64 {
	m := 90.0
	if l.CPUs > 0 && l.CPUs < 1 {
		m = 90 * l.CPUs
	}
	return m
}

// meter is the load's state over a run: the wandering factor, events, and the work done.
type meter struct {
	l        Load
	rnd      *rand.Rand
	wander   float64   // multiplies the CPU target, around 1
	eventEnd time.Time // a busy moment (a fight, a raid) until then
	nextAt   time.Time // when the next one starts
	second   time.Time
}

func newMeter(l Load, rnd *rand.Rand) *meter {
	now := time.Now()
	return &meter{l: l, rnd: rnd, wander: 1, second: now, nextAt: now.Add(time.Duration(60+rnd.Intn(120)) * time.Second)}
}

// everySecond moves the wandering factor and starts or ends events.
func (m *meter) everySecond(now time.Time) {
	if now.Sub(m.second) < time.Second {
		return
	}
	m.second = now
	j := m.l.JitterPct / 100
	m.wander = math.Max(1-j, math.Min(1+j, m.wander+m.rnd.NormFloat64()*j/4))
	if now.After(m.nextAt) {
		m.eventEnd = now.Add(time.Duration(3+m.rnd.Intn(5)) * time.Second)
		m.nextAt = now.Add(time.Duration(60+m.rnd.Intn(150)) * time.Second)
	}
}

func (m *meter) event(now time.Time) bool { return now.Before(m.eventEnd) }

// cpuTarget is the share of one CPU the update loop should take now, in percent.
func (m *meter) cpuTarget(players int, now time.Time) float64 {
	t := (m.l.IdlePct + m.l.PlayerPct*float64(players)) * m.wander
	if m.event(now) {
		t *= 1.35
	}
	t *= 1 + m.rnd.NormFloat64()*0.05 // a little from one update to the next
	return math.Max(0, math.Min(m.l.cpuMax(), t))
}

// outBytes is how many bytes of state one player gets in one snapshot.
func (m *meter) outBytes(sendHz int, now time.Time) int {
	if m.l.OutKbps <= 0 {
		return 0
	}
	b := float64(m.l.OutKbps) * 1000 / 8 / float64(sendHz) * m.wander * (0.85 + 0.3*m.rnd.Float64())
	if m.event(now) {
		b *= 2
	}
	return int(b)
}

// filler is text that stands in for the parts of a snapshot this sample does not model one by one (other entities'
// positions, animation and effects): base-64 of random bytes, the same shape and size a real delta would have.
var filler = func() string {
	r := rand.New(rand.NewSource(11))
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, 64<<10)
	for i := range b {
		b[i] = alphabet[r.Intn(64)]
	}
	return string(b)
}()

// fill returns n bytes of filler starting somewhere new each time.
func fill(rnd *rand.Rand, n int) string {
	if n <= 0 {
		return ""
	}
	if n > len(filler) {
		n = len(filler)
	}
	at := rnd.Intn(len(filler) - n + 1)
	return filler[at : at+n]
}

// maxDatagram keeps every packet under a usual path's MTU.
const maxDatagram = 1200

// workUntil runs the world's own simulation over the memory the game holds (each update moves through its chunks,
// mixing each word with its neighbours, the way terrain, fluids and growth are stepped) until the deadline. It is the
// part of an update's cost that grows with the players; the arena is nil in the light version.
func (g *game) workUntil(deadline time.Duration) {
	a := g.arena
	if a == nil {
		return
	}
	words := a.words()
	if len(words) < 1024 {
		return
	}
	for nowFine() < deadline {
		if g.cursor+512 >= len(words) {
			g.cursor = 0
		}
		w := words[g.cursor : g.cursor+512]
		prev := w[0]
		for i := 1; i < len(w); i++ {
			v := w[i]
			v ^= prev >> 7
			v *= 0x9E3779B97F4A7C15
			v ^= v >> 29
			w[i] = v
			prev = v
		}
		g.cursor += 512
	}
}
