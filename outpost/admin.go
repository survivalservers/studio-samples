package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// command runs one console line, the admin commands a game server takes, each answered with one short line:
//
//	players                  who is on
//	kick <player> [reason]   sends them off (they may come back)
//	ban <player> [reason]    sends them off and keeps them out (a world keeps its bans in its save)
//	unban <player>
//	announce <text>          to every player (say <text> is the same)
//	xp <multiplier>          an XP event: 2 is double XP, 1 ends it
//	save                     a world saves now
//	status, help, stop
func (g *game) command(line string) (over bool) {
	line = strings.TrimSpace(line)
	word, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(word) {
	case "":
	case "say", "announce", "broadcast":
		if rest == "" {
			g.logf("Usage: announce <text>")
			break
		}
		for _, pl := range g.players {
			g.reply(pl.addr, "msg %s", rest)
		}
		g.logf("Announced to %s: %s", plural(len(g.players), "player"), rest)
	case "players", "list", "who":
		g.logf("%s", g.playerList())
	case "kick":
		name, why, _ := strings.Cut(rest, " ")
		if name == "" {
			g.logf("Usage: kick <player> [reason]")
			break
		}
		pl := g.players[name]
		if pl == nil {
			g.logf("%s is not on this server", name)
			break
		}
		// The answer first, so the lines a bulk action's command captures start with it.
		if why = strings.TrimSpace(why); why != "" {
			g.logf("Kicked %s (%s)", name, why)
		} else {
			g.logf("Kicked %s", name)
		}
		g.reply(pl.addr, "over kicked 0")
		g.leave(pl, "kicked")
	case "ban":
		name, why, _ := strings.Cut(rest, " ")
		if name == "" || len(name) > 64 {
			g.logf("Usage: ban <player> [reason]")
			break
		}
		why = strings.TrimSpace(why)
		if why == "" {
			why = "no reason given"
		}
		g.bans[name] = why
		pl := g.players[name]
		if pl == nil {
			g.logf("Banned %s (%s)", name, why)
		} else {
			g.logf("Banned %s (%s), kicked from this server", name, why) // the answer first, then the leave line
			g.reply(pl.addr, "over banned 0")
			g.leave(pl, "banned")
		}
		if g.cfg.Kind == kindPersistent {
			g.saveSoon()
		}
	case "unban":
		if _, ok := g.bans[rest]; !ok || rest == "" {
			g.logf("%s is not banned", strings.TrimSpace(rest+" "))
			break
		}
		delete(g.bans, rest)
		g.logf("Unbanned %s", rest)
		if g.cfg.Kind == kindPersistent {
			g.saveSoon()
		}
	case "xp":
		m, err := strconv.ParseFloat(strings.TrimSuffix(strings.ToLower(rest), "x"), 64)
		if err != nil || m < 0.5 || m > 10 {
			g.logf("Usage: xp <multiplier>, from 0.5 to 10 (2 is double XP, 1 turns it off)")
			break
		}
		was := g.xp
		g.xp = math.Round(m*100) / 100
		g.label("xp", xpLabel(g.xp))
		if g.xp == 1 {
			g.logf("%s off, XP back to normal", xpName(was))
		} else {
			g.logf("%s on", xpName(g.xp))
		}
		for _, pl := range g.players {
			if g.xp == 1 {
				g.reply(pl.addr, "msg %s is over", xpName(was))
			} else {
				g.reply(pl.addr, "msg %s is on", xpName(g.xp))
			}
		}
	case "save":
		if g.cfg.Kind != kindPersistent {
			g.logf("Nothing to save in a match")
			break
		}
		for _, pl := range g.players {
			g.keep(pl)
		}
		if err := g.saveWorld(); err != nil {
			g.logf("save failed: %v", err)
		}
	case "status":
		g.logf("status: %s", g.statusLine())
	case "help", "?":
		g.logf("Commands: players, kick <player>, ban <player> <reason>, unban <player>, announce <text>, xp <multiplier>, save, status, stop")
	case "stop", "quit":
		return true
	default:
		g.logf("Unknown command %q. Type help for the list.", word)
	}
	return false
}

// saveSoon makes the next autosave come within a few seconds (a ban or unban is kept even if the world stops soon).
func (g *game) saveSoon() {
	if soon := time.Now().Add(-g.load.SaveEvery + 5*time.Second); g.savedAt.After(soon) {
		g.savedAt = soon
	}
}

// playerList is everyone on, longest-playing first, with the speed check's count beside anyone it caught.
func (g *game) playerList() string {
	if len(g.players) == 0 {
		return "Nobody is playing"
	}
	ps := make([]*player, 0, len(g.players))
	for _, pl := range g.players {
		ps = append(ps, pl)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].since.Before(ps[j].since) })
	names := make([]string, len(ps))
	for i, pl := range ps {
		names[i] = pl.id
		if pl.flags > 0 {
			names[i] += fmt.Sprintf(" (%s)", plural(pl.flags, "speed flag"))
		}
	}
	return fmt.Sprintf("%d playing: %s", len(ps), strings.Join(names, ", "))
}

// speedCheck counts inputs that ask to move faster than a player can (a speed hack sends them); the server never
// moves anyone faster, and says so in its log at most once a minute a player.
func (g *game) speedCheck(pl *player, dx, dy float64) {
	v := math.Hypot(dx, dy)
	if v <= 1.5 {
		return
	}
	pl.flags++
	if time.Since(pl.flagAt) >= time.Minute {
		pl.flagAt = time.Now()
		g.logf("Speed check: %s moved %.1fx faster than allowed (%s)", pl.id, v, plural(pl.flags, "time"))
	}
}

func (g *game) statusLine() string {
	s := fmt.Sprintf("%s, %d creatures, %d shots, tick %d", plural(len(g.players), "player"), len(g.mobs), len(g.shots), g.tick)
	if g.arena != nil {
		s += ", " + size(int64(g.arena.committed)) + " in memory"
	}
	if g.xp != 1 {
		s += ", " + xpName(g.xp) + " on"
	}
	if n := len(g.bans); n > 0 {
		s += ", " + plural(n, "ban")
	}
	return s
}

func xpLabel(m float64) string { return strconv.FormatFloat(m, 'f', -1, 64) + "x" }

func xpName(m float64) string {
	switch m {
	case 2:
		return "Double XP"
	case 3:
		return "Triple XP"
	case 1:
		return "XP event"
	}
	return "XP x" + strconv.FormatFloat(m, 'f', -1, 64)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// holdMemory moves the memory the game holds toward what it needs now (load.go's model), taking at most step bytes
// more in one go; memory no longer needed goes back after two minutes, 16 MiB a second, as a real allocator returns
// it slowly.
func (g *game) holdMemory(step int) {
	a := g.arena
	if a == nil {
		return
	}
	want := g.load.BaseMB
	if g.cfg.Kind == kindPersistent && g.save != nil {
		want += min(g.save.Chunks*g.load.ChunkKB>>10, g.load.GrowMaxMB)
	}
	for _, pl := range g.players {
		want += pl.mb
	}
	want = min(want, g.load.capMB()) << 20
	switch {
	case want > a.committed:
		g.lowSince = time.Time{}
		if err := a.growTo(want, step); err != nil && !g.memErr {
			g.memErr = true
			g.logf("memory: %v (holding %s)", err, size(int64(a.committed)))
		}
	case a.committed-want > 64<<20:
		if g.lowSince.IsZero() {
			g.lowSince = time.Now()
		}
		if time.Since(g.lowSince) > 2*time.Minute && g.tick%int64(g.cfg.TickHz) == 0 {
			_ = a.shrinkTo(want, 16<<20)
		}
	default:
		g.lowSince = time.Time{}
	}
}
