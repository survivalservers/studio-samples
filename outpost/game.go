package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	gs "github.com/survivalservers/studio-go/sdk/go"
)

const (
	arenaSize   = 400.0
	worldSize   = 1600.0
	obstacles   = 256 // rocks and walls every moving thing is kept out of
	directions  = 64  // headings an AI tries each update
	maxCreature = 900
)

type packet struct {
	data []byte
	from net.Addr
}

type vec struct{ x, y float64 }

type player struct {
	id      string
	addr    net.Addr
	pos     vec
	in      vec // last movement input
	act     string
	seq     int
	hp      int
	score   int
	last    time.Time
	since   time.Time
	history [32]vec // positions for checking shots against where targets were
	hpos    int
	// world progress, kept in the save
	wood, stone, level int
	// what the server holds and sends for them
	mb     int // memory for this player's view of the world, in MiB
	burst  int // bytes of map or world still to send since they joined
	flags  int // inputs that moved faster than anyone can (the speed check)
	flagAt time.Time
}

type mob struct {
	pos, vel vec
	hp       int
}

type shot struct {
	from     string
	pos, vel vec
	ttl      int
}

type game struct {
	cfg  Config
	p    Platform
	log  io.Writer
	conn net.PacketConn
	rnd  *rand.Rand

	players map[string]*player
	byAddr  map[string]string
	rocks   []vec
	mobs    []mob
	shots   []shot
	tick    int64
	size    float64
	report  chan func() // SDK calls, in order, off the game loop

	// load (load.go): what a real game of this shape costs
	load     Load
	meter    *meter
	arena    *arena
	cursor   int       // where the simulation is in the arena
	snapHz   int       // state snapshots a second each player really gets
	lowSince time.Time // since when the game has held more memory than it needs
	memErr   bool
	seed     uint64       // the world's own, for its region records
	revision uint64       // region records written so far
	dirty    map[int]bool // region records changed since the last save
	bans     map[string]string
	xp       float64

	// match
	alloc      *gs.Allocation
	phase      string // waiting (no match yet), lobby, live, ended
	mapName    string
	teamSize   int
	matchLen   time.Duration
	allocAt    time.Time
	liveAt     time.Time
	firstJoin  time.Time
	everJoined bool
	peak       int

	// world
	save      *Save
	savedAt   time.Time
	worldMins float64 // in-game minutes since the world began
	labels    map[string]string
}

func newGame(cfg Config, p Platform, log io.Writer) (*game, error) {
	cfg.Kind = normalKind(cfg.Kind)
	if cfg.Kind != kindMatch && cfg.Kind != kindPersistent {
		return nil, fmt.Errorf("GS_KIND=%q: match or persistent_server (or world, its older name)", cfg.Kind)
	}
	g := &game{cfg: cfg, p: p, log: log, rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
		players: map[string]*player{}, byAddr: map[string]string{}, report: make(chan func(), 4096), labels: map[string]string{},
		load: cfg.Load, dirty: map[int]bool{}, bans: map[string]string{}, xp: 1}
	g.size = arenaSize
	if cfg.Kind == kindPersistent {
		g.size = worldSize
	}
	if g.load.SaveEvery <= 0 {
		g.load.SaveEvery = 5 * time.Minute
	}
	h := fnv.New64a()
	h.Write([]byte(cfg.Name))
	g.seed = h.Sum64()
	g.meter = newMeter(g.load, g.rnd)
	if g.load.On {
		most := min(g.load.capMB(), g.load.BaseMB+g.load.GrowMaxMB+64*g.load.PlayerMB[1]+256)
		if most > 0 {
			a, err := newArena(most << 20)
			if err != nil {
				fmt.Fprintf(log, "memory: %v; running without it\n", err)
			} else {
				g.arena = a
			}
		}
	}
	// The same rocks every run: a world's players come back to the same map.
	r := rand.New(rand.NewSource(7))
	for i := 0; i < obstacles; i++ {
		g.rocks = append(g.rocks, vec{r.Float64() * g.size, r.Float64() * g.size})
	}
	return g, nil
}

func (g *game) logf(format string, args ...any) { fmt.Fprintf(g.log, format+"\n", args...) }

func (g *game) serve(ctx context.Context, console io.Reader) error {
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", g.cfg.ListenPort))
	if err != nil {
		return fmt.Errorf("bind game port: %w", err)
	}
	g.conn = conn
	defer conn.Close()
	if g.cfg.onListen != nil {
		g.cfg.onListen(conn.LocalAddr())
	}

	done := make(chan struct{})
	go g.reporter(done)
	defer func() { close(g.report); <-done }()

	if g.cfg.Kind == kindPersistent {
		if err := g.loadWorld(); err != nil {
			return err
		}
	} else {
		g.phase = "waiting"
		// A fresh start has its match before it starts: read it now so it is set up before the first player.
		if g.p.Enabled() {
			if a, err := g.p.WaitAllocation(0); err == nil && a != nil {
				g.allocate(a)
			}
		} else {
			g.allocate(&gs.Allocation{ExternalID: "local", Env: map[string]string{}})
		}
	}

	if g.arena != nil {
		defer g.arena.close()
		t0 := time.Now()
		g.holdMemory(1 << 62)
		what := "the arena maps"
		if g.cfg.Kind == kindPersistent {
			what = "world " + g.cfg.Name
		}
		g.logf("Loaded %s: %s in memory (%.1f s)", what, size(int64(g.arena.committed)), time.Since(t0).Seconds())
	}
	g.logf("listening on udp %s; players connect to %s:%d", conn.LocalAddr(), g.cfg.PublicIP, g.cfg.PublicPort)
	g.logf("Server ready for players")
	if err := g.p.Ready(); err != nil {
		return fmt.Errorf("say ready: %w", err)
	}

	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	packets := make(chan packet, 1024)
	allocated := make(chan *gs.Allocation, 1)
	shutdown := make(chan *gs.Shutdown, 1)
	lines := make(chan string, 16)
	go g.read(loopCtx, packets)
	go waitFor(loopCtx, func() (*gs.Shutdown, error) { return g.p.WaitShutdown(30 * time.Second) }, shutdown)
	if g.cfg.Kind == kindMatch && g.alloc == nil {
		go waitFor(loopCtx, func() (*gs.Allocation, error) { return g.p.WaitAllocation(30 * time.Second) }, allocated)
	}
	go g.health(loopCtx)
	if console != nil {
		go readLines(console, lines)
	}

	period := time.Second / time.Duration(g.cfg.TickHz)
	sendEvery := g.cfg.TickHz / g.cfg.SendHz
	if sendEvery < 1 {
		sendEvery = 1
	}
	g.snapHz = g.cfg.TickHz / sendEvery // a match's 30 updates and 20 snapshots a second send one every update
	next := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	var work []time.Duration
	second := time.Now()
	lastStep := time.Now()
	statusAt := time.Now()

	for {
		select {
		case <-ctx.Done():
			return g.finish("stop_signal", false)
		case n := <-shutdown:
			g.logf("stop asked (%s); leaving before %s", n.Reason, n.Deadline.UTC().Format(time.RFC3339))
			return g.finish("stop_asked", false)
		case a := <-allocated:
			g.allocate(a)
		case line := <-lines:
			if over := g.command(line); over {
				return g.finish("console", false)
			}
		case pk := <-packets:
			g.handle(pk)
		case <-timer.C:
			start := nowFine()
			dt := time.Since(lastStep).Seconds()
			lastStep = time.Now()
			if dt > 0.1 { // after a freeze or a stall: no catch-up burst
				dt = 0.1
			}
			g.step(dt)
			if g.tick%int64(sendEvery) == 0 {
				g.send()
			}
			if g.load.On {
				now := time.Now()
				g.meter.everySecond(now)
				g.holdMemory(24 << 20)
				g.workUntil(start + time.Duration(float64(period)*g.meter.cpuTarget(len(g.players), now)/100))
			}
			work = append(work, nowFine()-start)
			if reason, over := g.checkEnd(); over {
				return g.finish(reason, true)
			}
			if time.Since(second) >= time.Second {
				g.reportTicks(work, time.Since(second))
				work = work[:0]
				second = time.Now()
			}
			if time.Since(statusAt) >= time.Minute {
				statusAt = time.Now()
				g.logf("status: %s", g.statusLine())
			}
			next = next.Add(period)
			if d := time.Until(next); d < -time.Second { // frozen or far behind: start the schedule again
				next = time.Now()
			}
			timer.Reset(time.Until(next))
		}
	}
}

// read hands every datagram to the game loop.
func (g *game) read(ctx context.Context, out chan<- packet) {
	buf := make([]byte, 1500)
	for ctx.Err() == nil {
		_ = g.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := g.conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		select {
		case out <- packet{append([]byte(nil), buf[:n]...), from}:
		default: // the loop is behind: drop, as a real server's socket buffer would
		}
	}
}

func (g *game) reply(to net.Addr, format string, args ...any) {
	_, _ = g.conn.WriteTo([]byte(fmt.Sprintf(format, args...)), to)
}

func (g *game) handle(pk packet) {
	f := strings.Fields(string(pk.data))
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "ping":
		g.reply(pk.from, "pong %s", strings.Join(f[1:], " "))
	case "hello":
		if len(f) < 2 || len(f[1]) > 64 {
			return
		}
		if why, ok := g.bans[f[1]]; ok {
			g.reply(pk.from, "banned %s", why)
			return
		}
		g.join(f[1], pk.from)
	case "in":
		if len(f) < 5 {
			return
		}
		pl := g.players[f[1]]
		if pl == nil || pl.addr.String() != pk.from.String() {
			return
		}
		seq, _ := strconv.Atoi(f[2])
		dx, _ := strconv.ParseFloat(f[3], 64)
		dy, _ := strconv.ParseFloat(f[4], 64)
		if seq <= pl.seq {
			return // late or repeated
		}
		pl.seq, pl.last = seq, time.Now()
		g.speedCheck(pl, dx, dy)
		pl.in = vec{clamp(dx, -1, 1), clamp(dy, -1, 1)}
		if len(f) > 5 {
			pl.act = f[5]
		}
	case "bye":
		if len(f) < 2 {
			return
		}
		if pl := g.players[f[1]]; pl != nil && pl.addr.String() == pk.from.String() {
			g.leave(pl, "bye")
			g.reply(pk.from, "bye %s", f[1])
		}
	}
}

func (g *game) join(id string, from net.Addr) {
	if pl := g.players[id]; pl != nil { // a resend, or the player came back from a new address
		delete(g.byAddr, pl.addr.String())
		pl.addr, pl.last = from, time.Now()
		g.byAddr[from.String()] = id
		g.welcome(from, id)
		return
	}
	switch {
	case g.cfg.Kind == kindMatch && (g.phase == "waiting" || g.phase == "ended"):
		g.reply(from, "over %s", map[bool]string{true: "no_match_yet", false: "ended"}[g.phase == "waiting"])
		return
	case len(g.players) >= g.maxPlayers():
		g.reply(from, "full")
		return
	}
	pl := &player{id: id, addr: from, hp: 100, last: time.Now(), since: time.Now(),
		pos: vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}}
	if g.cfg.Kind == kindPersistent {
		if s, ok := g.save.Players[id]; ok {
			pl.pos, pl.wood, pl.stone, pl.level = vec{s.X, s.Y}, s.Wood, s.Stone, s.Level
		}
	}
	if g.load.On {
		lo, hi := g.load.PlayerMB[0], g.load.PlayerMB[1]
		pl.mb = lo + g.rnd.Intn(hi-lo+1)
		pl.burst = g.load.BurstKB << 10
	}
	g.players[id] = pl
	g.byAddr[from.String()] = id
	if !g.everJoined {
		g.everJoined, g.firstJoin = true, time.Now()
	}
	if len(g.players) > g.peak {
		g.peak = len(g.players)
	}
	g.async(func() { _ = g.p.PlayerConnect(id) })
	g.logf("%s joined (%d playing)", id, len(g.players))
	g.welcome(from, id)
}

// welcome lets a player in; with the load on it also says how much each client sends a second (inputs with the
// last few repeated, acknowledgements, voice), so both directions carry a real game's traffic.
func (g *game) welcome(to net.Addr, id string) {
	if g.load.On && g.load.InKbps > 0 {
		g.reply(to, "welcome %s %d up=%d", id, g.tick, g.load.InKbps*1000/8)
		return
	}
	g.reply(to, "welcome %s %d", id, g.tick)
}

func (g *game) leave(pl *player, why string) {
	if g.cfg.Kind == kindPersistent {
		g.keep(pl)
	}
	delete(g.players, pl.id)
	delete(g.byAddr, pl.addr.String())
	id := pl.id
	g.async(func() { _ = g.p.PlayerDisconnect(id) })
	g.logf("%s left: %s (%d playing)", pl.id, why, len(g.players))
}

func (g *game) maxPlayers() int {
	if g.cfg.Kind == kindMatch {
		return 2 * g.teamSize
	}
	return g.cfg.MaxPlayers
}

// allocate sets up the match the matchmaker asked for.
func (g *game) allocate(a *gs.Allocation) {
	g.alloc, g.allocAt, g.phase = a, time.Now(), "lobby"
	g.mapName = pick(a.Env["MAP"], "Substation")
	g.teamSize = atoiOr(a.Env["TEAM_SIZE"], 4, 1, 16)
	g.matchLen = time.Duration(atoiOr(a.Env["MATCH_SECONDS"], 480, 5, 7200)) * time.Second
	names := make([]string, 0, len(a.Env))
	for k := range a.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	g.logf("allocated: match %q, map %s, %d a side, %s, settings %v", a.ExternalID, g.mapName, g.teamSize, g.matchLen, names)
	g.label("map", g.mapName)
	g.label("phase", "lobby")
	// Drones the players fight: the arena's own AI, 24 for each player slot.
	g.mobs = g.mobs[:0]
	for i := 0; i < 24*2*g.teamSize; i++ {
		g.mobs = append(g.mobs, mob{pos: vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}, hp: 30})
	}
}

// step is one update: movement, collisions, the AI and shots. It is the server's real work.
func (g *game) step(dt float64) {
	g.tick++
	now := time.Now()
	for _, pl := range g.players {
		if now.Sub(pl.last) > g.cfg.Quiet {
			g.leave(pl, "timed out")
			continue
		}
		pl.pos = g.collide(vec{pl.pos.x + pl.in.x*40*dt, pl.pos.y + pl.in.y*40*dt}, 3)
		pl.history[pl.hpos%len(pl.history)] = pl.pos
		pl.hpos++
		switch pl.act {
		case "shoot":
			if g.phase == "live" {
				d := norm(pl.in)
				g.shots = append(g.shots, shot{from: pl.id, pos: pl.pos, vel: vec{d.x * 200, d.y * 200}, ttl: g.cfg.TickHz})
			}
		case "gather":
			if g.cfg.Kind == kindPersistent {
				g.gather(pl)
			}
		case "build":
			if g.cfg.Kind == kindPersistent && pl.wood >= 10 {
				pl.wood -= 10
				g.touchAt(pl.pos)
				g.save.Structures++
				if g.save.Structures%25 == 0 {
					pl.level++
				}
			}
		}
		pl.act = ""
	}
	if g.cfg.Kind == kindPersistent {
		g.worldClock(dt)
		g.explore(dt)
		g.spawn()
	}
	g.think(dt)
	g.fly()
}

// collide pushes p out of every rock it overlaps (r: its radius) and keeps it on the map.
func (g *game) collide(p vec, r float64) vec {
	for _, k := range g.rocks {
		dx, dy := p.x-k.x, p.y-k.y
		d2 := dx*dx + dy*dy
		if d2 < (r+6)*(r+6) && d2 > 1e-9 {
			d := math.Sqrt(d2)
			p.x, p.y = k.x+dx/d*(r+6), k.y+dy/d*(r+6)
		}
	}
	return vec{clamp(p.x, 0, g.size), clamp(p.y, 0, g.size)}
}

// think moves every creature or drone: towards the nearest player it can see, along the clear heading that gets it
// closest, else wandering.
func (g *game) think(dt float64) {
	for i := range g.mobs {
		m := &g.mobs[i]
		var target *player
		best := 400.0 * 400.0 // creatures see this far
		for _, pl := range g.players {
			dx, dy := pl.pos.x-m.pos.x, pl.pos.y-m.pos.y
			if d2 := dx*dx + dy*dy; d2 < best {
				best, target = d2, pl
			}
		}
		if target == nil {
			if g.rnd.Intn(20) == 0 {
				a := g.rnd.Float64() * 2 * math.Pi
				m.vel = vec{math.Cos(a) * 8, math.Sin(a) * 8}
			}
			m.pos = g.collide(vec{m.pos.x + m.vel.x*dt, m.pos.y + m.vel.y*dt}, 2)
			continue
		}
		// Try each heading; keep the clear one that ends closest to the target.
		bestH, bestD := vec{}, math.Inf(1)
		for h := 0; h < directions; h++ {
			a := float64(h) * 2 * math.Pi / directions
			c := vec{m.pos.x + math.Cos(a)*24*dt, m.pos.y + math.Sin(a)*24*dt}
			blocked := false
			for _, k := range g.rocks {
				if dx, dy := c.x-k.x, c.y-k.y; dx*dx+dy*dy < 64 {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			if dx, dy := target.pos.x-c.x, target.pos.y-c.y; dx*dx+dy*dy < bestD {
				bestD, bestH = dx*dx+dy*dy, c
			}
		}
		if !math.IsInf(bestD, 1) {
			m.pos = bestH
		}
		if best < 9 && g.tick%int64(g.cfg.TickHz) == 0 {
			target.hp -= 2
			if target.hp <= 0 {
				target.hp = 100
				target.pos = vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}
			}
		}
	}
}

// fly moves shots and checks each against where every other player was when it was fired (a simple rewind).
func (g *game) fly() {
	kept := g.shots[:0]
	for _, s := range g.shots {
		s.pos = vec{s.pos.x + s.vel.x/float64(g.cfg.TickHz), s.pos.y + s.vel.y/float64(g.cfg.TickHz)}
		s.ttl--
		hit := false
		for _, pl := range g.players {
			if pl.id == s.from {
				continue
			}
			for _, h := range pl.history {
				if dx, dy := h.x-s.pos.x, h.y-s.pos.y; dx*dx+dy*dy < 4 {
					hit = true
					break
				}
			}
			if hit {
				pl.hp -= 25
				if pl.hp <= 0 {
					pl.hp = 100
					pl.pos = vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}
					if sh := g.players[s.from]; sh != nil {
						sh.score++
					}
				}
				break
			}
		}
		for i := range g.mobs {
			if hit {
				break
			}
			if dx, dy := g.mobs[i].pos.x-s.pos.x, g.mobs[i].pos.y-s.pos.y; dx*dx+dy*dy < 4 {
				hit = true
				if sh := g.players[s.from]; sh != nil {
					sh.score++
				}
				g.mobs[i].pos = vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}
			}
		}
		if !hit && s.ttl > 0 {
			kept = append(kept, s)
		}
	}
	g.shots = kept
}

// send gives each player the state around them: themselves and up to 16 nearby players and creatures.
func (g *game) send() {
	var b strings.Builder
	for _, pl := range g.players {
		b.Reset()
		if g.cfg.Kind == kindPersistent {
			fmt.Fprintf(&b, "st %d %.1f %.1f %d %d %d %d %d %s", g.tick, pl.pos.x, pl.pos.y, pl.hp, pl.wood, pl.stone, pl.level, g.save.Day, g.labels["time"])
		} else {
			fmt.Fprintf(&b, "st %d %.1f %.1f %d %d %s", g.tick, pl.pos.x, pl.pos.y, pl.hp, pl.score, g.phase)
		}
		n := 0
		for _, o := range g.players {
			if o != pl && n < 8 && near(o.pos, pl.pos, 150) {
				fmt.Fprintf(&b, " p%.0f,%.0f", o.pos.x, o.pos.y)
				n++
			}
		}
		for i := range g.mobs {
			if n >= 16 {
				break
			}
			if near(g.mobs[i].pos, pl.pos, 100) {
				fmt.Fprintf(&b, " m%.0f,%.0f", g.mobs[i].pos.x, g.mobs[i].pos.y)
				n++
			}
		}
		g.sendState(pl, b.String())
	}
}

// sendState sends one player's snapshot, filled out to the state traffic a real game sends (the meter's share),
// in packets of at most maxDatagram: the state first, the rest as "ent" packets, and while they are new the map or
// world around them as "map" packets.
func (g *game) sendState(pl *player, st string) {
	if !g.load.On {
		_, _ = g.conn.WriteTo([]byte(st), pl.addr)
		return
	}
	want := g.meter.outBytes(max(g.snapHz, 1), time.Now())
	first := min(max(want-len(st)-3, 0), maxDatagram-len(st)-3)
	if first > 0 {
		st += " d=" + fill(g.rnd, first)
	}
	_, _ = g.conn.WriteTo([]byte(st), pl.addr)
	left := want - len(st)
	for left > 64 {
		head := fmt.Sprintf("ent %d ", g.tick)
		n := min(left, maxDatagram) - len(head)
		_, _ = g.conn.WriteTo([]byte(head+fill(g.rnd, n)), pl.addr)
		left -= len(head) + n
	}
	if pl.burst > 0 {
		for sent := 0; pl.burst > 0 && sent < (768<<10)/max(g.snapHz, 1); {
			head := fmt.Sprintf("map %d ", pl.burst)
			n := min(pl.burst, maxDatagram-len(head))
			_, _ = g.conn.WriteTo([]byte(head+fill(g.rnd, n)), pl.addr)
			pl.burst -= n
			sent += len(head) + n
		}
	}
}

// checkEnd runs a match's life: lobby until both teams are in (or 45 s after the first player), live for its length,
// then over. It gives up on a match nobody joined within two minutes.
func (g *game) checkEnd() (string, bool) {
	if g.cfg.Kind != kindMatch || g.alloc == nil {
		return "", false
	}
	switch g.phase {
	case "lobby":
		if !g.everJoined && time.Since(g.allocAt) > 2*time.Minute {
			return "no_players", true
		}
		if g.everJoined && (len(g.players) >= 2*g.teamSize || (time.Since(g.firstJoin) > 45*time.Second && len(g.players) >= 2)) {
			g.phase, g.liveAt = "live", time.Now()
			g.label("phase", "live")
			g.logf("match live: %d players on %s", len(g.players), g.mapName)
		}
		if g.everJoined && len(g.players) == 0 {
			return "abandoned", true
		}
	case "live":
		if len(g.players) == 0 {
			return "abandoned", true
		}
		if time.Since(g.liveAt) >= g.matchLen {
			return "match_over", true
		}
	}
	return "", false
}

// finish ends a match or a persistent server's run: the players hear it, a world saves, the result is logged, and a
// match that ended by itself tells the platform (the same as exiting with 0).
func (g *game) finish(reason string, selfStop bool) error {
	if g.cfg.Kind == kindMatch {
		g.phase = "ended"
		g.label("phase", "ended")
		best, bestID := -1, ""
		for _, pl := range g.players {
			g.reply(pl.addr, "over %s %d", reason, pl.score)
			if pl.score > best {
				best, bestID = pl.score, pl.id
			}
		}
		g.logf("match over: %s after %s, %d players at most, top %s (%d)%s", reason, time.Since(g.allocAt).Round(time.Second), g.peak, bestID, best,
			map[bool]string{true: ", " + xpLabel(g.xp) + " XP", false: ""}[g.xp != 1])
	} else {
		for _, pl := range g.players {
			g.reply(pl.addr, "over %s 0", reason)
			g.keep(pl)
		}
		if err := g.saveWorld(); err != nil {
			g.logf("save failed: %v", err)
		}
		g.logf("world %s stopping: %s, day %d, %d players known", g.cfg.Name, reason, g.save.Day, len(g.save.Players))
	}
	for id := range g.players {
		id := id
		g.async(func() { _ = g.p.PlayerDisconnect(id) })
	}
	if selfStop {
		r := reason
		g.async(func() {
			if err := g.p.Stop(r); err != nil {
				g.logf("stop: %v (exiting with 0 counts as a stop too)", err)
			}
		})
	}
	return nil
}

// reportTicks sends the last second's update times (the middle and the slowest 1%) and the rate the game runs at,
// as a real game reports its tick rate: the platform judges frame times against 1000/rate (50 ms for a world at 20 a
// second, 33 ms for a match at 30). A second with fewer updates (a stall, a resume from a pause) still reports the rate
// it aims for; before 1.2.3 it sent the count it managed, so a slow second moved the budget too.
func (g *game) reportTicks(work []time.Duration, over time.Duration) {
	if len(work) == 0 {
		return
	}
	s := append([]time.Duration(nil), work...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	p50 := ms(s[len(s)/2])
	p99 := ms(s[int(math.Ceil(float64(len(s))*0.99))-1])
	hz := g.cfg.TickHz
	if hz <= 0 {
		hz = int(math.Round(float64(len(work)) / over.Seconds()))
	}
	select {
	case g.report <- func() { _ = g.p.Tick(p50, p99, hz) }:
	default: // a slow agent never holds the game up; the next second goes instead
	}
}

func (g *game) label(name, value string) {
	if g.labels[name] == value {
		return
	}
	g.labels[name] = value
	g.async(func() { _ = g.p.SetLabel(name, value) })
}

// async queues an SDK call; the reporter makes them one at a time, in order.
func (g *game) async(f func()) {
	select {
	case g.report <- f:
	default:
		g.logf("Platform connection busy: one report skipped")
	}
}

func (g *game) reporter(done chan<- struct{}) {
	defer close(done)
	for f := range g.report {
		f()
	}
}

func (g *game) health(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = g.p.Health()
		}
	}
}

// waitFor repeats a long-poll until it returns something, then hands it over. Errors wait a second.
func waitFor[T any](ctx context.Context, once func() (*T, error), out chan<- *T) {
	for ctx.Err() == nil {
		v, err := once()
		if v != nil {
			out <- v
			return
		}
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func readLines(r io.Reader, out chan<- string) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		out <- s.Text()
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func near(a, b vec, r float64) bool   { dx, dy := a.x-b.x, a.y-b.y; return dx*dx+dy*dy < r*r }
func ms(d time.Duration) float64      { return float64(d.Microseconds()) / 1000 }
func norm(v vec) vec {
	l := math.Hypot(v.x, v.y)
	if l < 1e-9 {
		return vec{1, 0}
	}
	return vec{v.x / l, v.y / l}
}
func pick(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
func atoiOr(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return def
	}
	return n
}
