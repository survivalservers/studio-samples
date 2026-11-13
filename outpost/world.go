package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Save is a world's file: its clock, what was built, and every player who has ever played, so a returning player
// finds their things where they left them.
type Save struct {
	Name       string                 `json:"name"`
	Day        int                    `json:"day"`
	Minute     float64                `json:"minute"` // in-game minutes since the world began
	Weather    string                 `json:"weather"`
	Structures int                    `json:"structures"`
	Chunks     int                    `json:"chunks"` // explored beyond the starting terrain (each a record in world.regions)
	Runs       int                    `json:"runs"`
	Players    map[string]SavedPlayer `json:"players"`
	Bans       map[string]string      `json:"bans,omitempty"` // player to the reason
	XP         float64                `json:"xp,omitempty"`   // an XP event's multiplier while it is on
	SavedAt    time.Time              `json:"saved_at"`
}

// SavedPlayer is one player's progress.
type SavedPlayer struct {
	X, Y        float64
	Wood, Stone int
	Level       int
	Seen        time.Time
}

// One in-game day is 24 real minutes.
const minutesPerSecond = 1.0

func (g *game) savePath() string { return filepath.Join(g.cfg.SaveDir, "world.json") }

func (g *game) loadWorld() error {
	g.save = &Save{Name: g.cfg.Name, Day: 1, Minute: 6 * 60, Weather: "clear", Players: map[string]SavedPlayer{}} // day 1, dawn
	b, err := os.ReadFile(g.savePath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		g.logf("new world %s", g.cfg.Name)
	case err != nil:
		return fmt.Errorf("read save: %w", err)
	default:
		if err := json.Unmarshal(b, g.save); err != nil {
			return fmt.Errorf("save %s is damaged: %w", g.savePath(), err)
		}
		if g.save.Players == nil {
			g.save.Players = map[string]SavedPlayer{}
		}
		if g.save.Chunks == 0 && g.load.On {
			// A save from before worlds kept their explored chunks: as much explored as its days and building say
			// (a chunk for each in-game day and for every 40 structures).
			g.save.Chunks = min(g.save.Structures/40+g.save.Day, g.maxChunks())
		}
		g.logf("loaded world %s: day %d, %d structures, %d chunks explored, %d players known, saved %s", g.save.Name, g.save.Day,
			g.save.Structures, g.save.Chunks, len(g.save.Players), g.save.SavedAt.UTC().Format(time.RFC3339))
	}
	if g.save.Bans == nil {
		g.save.Bans = map[string]string{}
	}
	g.bans = g.save.Bans
	if g.save.XP > 0 {
		g.xp = g.save.XP
		g.label("xp", xpLabel(g.xp))
	}
	g.save.Runs++
	g.worldMins = g.save.Minute
	g.savedAt = time.Now()
	g.label("day", strconv.Itoa(g.save.Day))
	g.label("time", timeOfDay(g.worldMins))
	g.label("weather", g.save.Weather)
	return nil
}

// saveWorld writes the whole save to a new file and then puts it in place, so a crash mid-write never leaves half a
// save.
func (g *game) saveWorld() error {
	g.save.Minute = g.worldMins
	g.save.SavedAt = time.Now().UTC()
	g.save.XP = 0
	if g.xp != 1 {
		g.save.XP = g.xp
	}
	b, err := json.MarshalIndent(g.save, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(g.cfg.SaveDir, 0o755); err != nil {
		return err
	}
	var regions int64
	if g.load.On && g.load.SaveChunkKB > 0 {
		if regions, err = g.writeRegions(); err != nil {
			return fmt.Errorf("regions: %w", err)
		}
	}
	tmp := g.savePath() + ".new"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, g.savePath()); err != nil {
		return err
	}
	g.savedAt = time.Now()
	g.logf("World saved, %s (day %d, %d players known, %d structures)", size(int64(len(b)+1)+regions), g.save.Day,
		len(g.save.Players), g.save.Structures)
	return nil
}

// keep copies a player's progress into the save.
func (g *game) keep(pl *player) {
	g.save.Players[pl.id] = SavedPlayer{X: pl.pos.x, Y: pl.pos.y, Wood: pl.wood, Stone: pl.stone, Level: pl.level, Seen: time.Now().UTC()}
}

// worldClock moves the world's time and weather on, and saves every five minutes.
func (g *game) worldClock(dt float64) {
	g.worldMins += dt * minutesPerSecond
	day := 1 + int(g.worldMins/1440)
	if day != g.save.Day {
		g.save.Day = day
		g.save.Weather = []string{"clear", "clear", "rain", "fog", "storm", "clear"}[g.rnd.Intn(6)]
		g.label("day", strconv.Itoa(day))
		g.label("weather", g.save.Weather)
	}
	g.label("time", timeOfDay(g.worldMins))
	if time.Since(g.savedAt) >= g.load.SaveEvery {
		for _, pl := range g.players {
			g.keep(pl)
		}
		if err := g.saveWorld(); err != nil {
			g.logf("save failed: %v", err)
			g.savedAt = time.Now()
		}
	}
}

func timeOfDay(mins float64) string {
	h := math.Mod(mins, 1440) / 60
	switch {
	case h < 5:
		return "night"
	case h < 8:
		return "dawn"
	case h < 18:
		return "day"
	case h < 21:
		return "dusk"
	}
	return "night"
}

// spawn keeps the creature count near what the players draw: a few dozen on an empty world, 30 more for each player,
// more at night.
func (g *game) spawn() {
	want := 40 + 30*len(g.players)
	if g.labels["time"] == "night" {
		want += want / 2
	}
	if want > maxCreature {
		want = maxCreature
	}
	for len(g.mobs) < want {
		g.mobs = append(g.mobs, mob{pos: vec{g.rnd.Float64() * g.size, g.rnd.Float64() * g.size}, hp: 20})
	}
	if len(g.mobs) > want && g.tick%int64(g.cfg.TickHz) == 0 {
		g.mobs = g.mobs[:len(g.mobs)-1] // wander off slowly
	}
}

// gather gives wood next to a tree (the rocks double as trees here) and stone otherwise, more during an XP event.
func (g *game) gather(pl *player) {
	n := int(g.xp)
	if g.rnd.Float64() < g.xp-float64(n) {
		n++
	}
	g.touchAt(pl.pos)
	for _, k := range g.rocks {
		if near(k, pl.pos, 12) {
			pl.wood += n
			return
		}
	}
	pl.stone += n
}

// maxChunks is how far a world's exploring can grow its memory.
func (g *game) maxChunks() int {
	if g.load.ChunkKB <= 0 {
		return 0
	}
	return g.load.GrowMaxMB << 10 / g.load.ChunkKB
}

// explore: each player now and then reaches a chunk nobody had been to, which the world keeps from then on.
func (g *game) explore(dt float64) {
	if !g.load.On || g.save.Chunks >= g.maxChunks() {
		return
	}
	for range g.players {
		if g.rnd.Float64() < g.load.ChunksPerHour/3600*dt {
			g.save.Chunks++
		}
	}
}
