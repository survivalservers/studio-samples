// Command outpost is a small game server on the Go SDK (../sdk/go) with real work in every update, in the two
// shapes studios host most:
//
//   - a short match (GS_KIND=match): pre-started as a ready server or started fresh, it takes its map and team size
//     from the match settings, plays one round with the players the matchmaker sent, ends itself and exits;
//   - a survival world, run as a persistent server (GS_KIND=persistent_server): always on, it loads its save, keeps
//     each player's progress, saves on a timer, on the console line "save" and on the way out, and carries on
//     happily when the platform pauses it.
//
// Both report what the platform shows: ready, players (connect and disconnect, one by one), update times every
// second (the tick graph) and labels (the match's phase and map; the world's day, time of day, weather and an XP
// event).
//
// It costs what a real game of its shape costs (load.go; every number is an OUTPOST_* setting): memory it really
// holds (about 1.5 GB for a match, 2.6 GB for a world plus what its players explored, 30-60 MB a player, never more
// than 80% of its size), an update loop whose work grows with its players (about 6-8% of one CPU empty and 3% more a
// player), about 200 kb/s of state to each player in a match (160 in a world) with the map sent when they join, and a
// world save of tens to a few hundred MB (world.regions beside world.json). OUTPOST_LOAD=light runs it small.
//
// The console takes the admin commands a game has (admin.go): players, kick, ban, unban, announce (or say), xp,
// save, status, help and stop, each answered with one short line.
//
// The game is a tiny UDP protocol, one line per datagram (the player simulator in ops/testcell/demo speaks it):
//
//	hello <player>                    -> welcome <player> <tick> [up=<bytes a second to send>] | full | banned <why>
//	                                     | over <reason>
//	in <player> <seq> <dx> <dy> [act] (movement and an action: shoot, gather, build; anything after it is the
//	                                  client's repeats and is ignored) -> st <tick> ... (state, sent to every player at
//	                                  the snapshot rate, with "ent" packets after it and "map" packets after a join)
//	bye <player>                      -> bye <player>
//	ping <token>                      -> pong <token>
//	                                  <- msg <text> (the console's announce: a warning to every player)
//	                                  <- over kicked|banned <score> (the console sent them off)
//
// A player who sends nothing for 15 seconds has left. With no GS_* variables it runs as a local server on port 7777.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Config is what the game server learns from its environment and flags.
type Config struct {
	ID         string // GS_ID
	Kind       string // GS_KIND: match or persistent_server
	PublicIP   string // GS_PUBLIC_IP
	PublicPort int    // GS_PORT_GAME
	ListenPort int    // GS_PORT_GAME_INTERNAL in a Linux image, else GS_PORT_GAME, else 7777
	SaveDir    string // -save-dir: where a world keeps world.json
	Name       string // WORLD_NAME, for a world's save and log
	MaxPlayers int    // MAX_PLAYERS (world; a match takes two teams of TEAM_SIZE)
	TickHz     int    // updates a second
	SendHz     int    // state snapshots a second to each player
	Quiet      time.Duration
	Load       Load // OUTPOST_* (load.go); none in tests unless they set it

	onListen func(net.Addr) // tests: the bound game address
}

// The two values of GS_KIND. A persistent server runs this game as a survival world.
const (
	kindMatch      = "match"
	kindPersistent = "persistent_server"
)

// normalKind reads GS_KIND from any host: hosts from before the rename call a persistent server "world".
func normalKind(kind string) string {
	if kind == "world" {
		return kindPersistent
	}
	return kind
}

func fromEnv(getenv func(string) string) (Config, error) {
	c := Config{ID: getenv("GS_ID"), Kind: normalKind(getenv("GS_KIND")), PublicIP: getenv("GS_PUBLIC_IP"),
		Name: getenv("WORLD_NAME"), MaxPlayers: 24, Quiet: 15 * time.Second}
	if c.ID == "" {
		c.ID = "local"
	}
	if c.Kind == "" {
		c.Kind = kindMatch
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	num := func(name string, lo, hi int) (int, error) {
		v := getenv(name)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("%s=%q is not a number from %d to %d", name, v, lo, hi)
		}
		return n, nil
	}
	var err error
	if c.PublicPort, err = num("GS_PORT_GAME", 0, 65535); err != nil {
		return c, err
	}
	internal, err := num("GS_PORT_GAME_INTERNAL", 0, 65535)
	if err != nil {
		return c, err
	}
	switch {
	case internal != 0:
		c.ListenPort = internal
	case c.PublicPort != 0:
		c.ListenPort = c.PublicPort
	default:
		c.ListenPort = 7777
	}
	if n, err := num("MAX_PLAYERS", 1, 200); err != nil {
		return c, err
	} else if n > 0 {
		c.MaxPlayers = n
	}
	if c.Load, err = loadFromEnv(c.Kind, getenv); err != nil {
		return c, err
	}
	return c, nil
}

func main() {
	saveDir := flag.String("save-dir", "saves", "where a world keeps its save (name it in the persistent server's save_paths)")
	tickHz := flag.Int("tick-hz", 0, "updates a second (default 30 for a match, 20 for a world)")
	sendHz := flag.Int("send-hz", 0, "state snapshots a second to each player (default 20 for a match, 10 for a world)")
	flag.Parse()

	cfg, err := fromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg.SaveDir, cfg.TickHz, cfg.SendHz = *saveDir, *tickHz, *sendHz
	if cfg.TickHz == 0 {
		cfg.TickHz = map[bool]int{true: 30, false: 20}[cfg.Kind == kindMatch]
	}
	if cfg.SendHz == 0 {
		cfg.SendHz = map[bool]int{true: 20, false: 10}[cfg.Kind == kindMatch]
	}

	// Ctrl+Break and Ctrl+C on Windows, SIGTERM and SIGINT on Linux: the platform's stop. A world saves first.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p, err := openPlatform()
	if err != nil {
		fmt.Fprintln(os.Stderr, "local API:", err)
		os.Exit(1)
	}
	defer p.Close()
	if err := Run(ctx, cfg, p, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Run serves one match or one persistent server's run and returns when it is over.
func Run(ctx context.Context, cfg Config, p Platform, console io.Reader, log io.Writer) error {
	g, err := newGame(cfg, p, log)
	if err != nil {
		return err
	}
	return g.serve(ctx, console)
}
