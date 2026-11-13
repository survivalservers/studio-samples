// Command plain-server is a sample game server that needs no SDK. It shows everything a game server can do on the
// platform, in about 300 lines you can copy into any engine:
//
//   - find its port and match settings in GS_* environment variables and the allocation file;
//   - say it is ready on the local API (or just print a line the version's ready check looks for);
//   - wait for a match if it was pre-started as a ready server;
//   - report its players;
//   - end the match by stopping itself, and save on the way out when asked to stop;
//   - leave a result file to be collected after it exits.
//
// The "game" is a tiny UDP protocol: "join <name>", "leave <name>", "ping". The match ends when everyone who
// joined has left, or after -match-length. With no GS_* variables it runs as a plain local server on port 7777.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Config is what the game server learns from its environment.
type Config struct {
	ID             string // GS_ID
	Kind           string // GS_KIND: match or persistent_server
	Region         string // GS_REGION: the region it runs in, such as europe-west
	Continent      string // GS_CONTINENT: that region's continent, such as europe
	PublicIP       string // GS_PUBLIC_IP: the address players use
	PublicPort     int    // GS_PORT_GAME: the port players use
	ListenPort     int    // the port to bind: GS_PORT_GAME_INTERNAL in a Linux image, else GS_PORT_GAME
	AllocationFile string // GS_ALLOCATION_FILE
	ArtifactDir    string // GS_ARTIFACT_DIR: files here are collected when the game server exits
	API            *LocalAPI
	MatchLength    time.Duration
}

// localAPIURL is the local API's address: GS_LOCAL_API_URL, or its older name GS_AGENT_URL.
func localAPIURL(getenv func(string) string) string {
	if u := getenv("GS_LOCAL_API_URL"); u != "" {
		return u
	}
	return getenv("GS_AGENT_URL")
}

// FromEnv reads the GS_* variables. Missing ones fall back to values that work on a developer's PC.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		ID:             getenv("GS_ID"),
		Kind:           getenv("GS_KIND"),
		Region:         getenv("GS_REGION"),
		Continent:      getenv("GS_CONTINENT"),
		PublicIP:       getenv("GS_PUBLIC_IP"),
		AllocationFile: getenv("GS_ALLOCATION_FILE"),
		ArtifactDir:    getenv("GS_ARTIFACT_DIR"),
		API:            &LocalAPI{URL: localAPIURL(getenv), Token: getenv("GS_TOKEN")},
	}
	if c.ID == "" {
		c.ID = "local"
	}
	// "world" is the older name for a persistent server: read it the same.
	if c.Kind == "world" {
		c.Kind = "persistent_server"
	}
	port := func(name string) (int, error) {
		v := getenv(name)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 65535 {
			return 0, fmt.Errorf("%s=%q is not a port", name, v)
		}
		return n, nil
	}
	var err error
	if c.PublicPort, err = port("GS_PORT_GAME"); err != nil {
		return c, err
	}
	internal, err := port("GS_PORT_GAME_INTERNAL")
	if err != nil {
		return c, err
	}
	switch {
	case internal != 0: // Linux image: we map the public port onto the port the image listens on
		c.ListenPort = internal
	case c.PublicPort != 0: // Windows build: bind the public port itself
		c.ListenPort = c.PublicPort
	default:
		c.ListenPort = 7777
	}
	return c, nil
}

func main() {
	matchLength := flag.Duration("match-length", 0, "end the match after this long (0: when everyone has left)")
	logFile := flag.String("log-file", "", "write the log to this file instead of the console, as Unreal writes Saved/Logs "+
		"(name it in the version's log_paths to have it kept)")
	climb := flag.Int("memory-climb", 0, "use this many more MB of memory each second, to try a version's memory size "+
		"(its warning before the size, its end past it)")
	climbTo := flag.Int("memory-climb-to", 0, "with -memory-climb: stop climbing at this many MB and hold them (0: never stop)")
	flag.Parse()

	cfg, err := FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg.MatchLength = *matchLength

	// Ctrl+Break and Ctrl+C on Windows, SIGTERM and SIGINT on Linux: the platform's stop signal. Save and exit.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var log io.Writer = os.Stdout
	if *logFile != "" {
		if err := os.MkdirAll(filepath.Dir(*logFile), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer f.Close()
		log = f
	}

	if *climb > 0 {
		go climbMemory(ctx, *climb, *climbTo, log)
	}
	if err := Run(ctx, cfg, log); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// held keeps what climbMemory took in use.
var held [][]byte

// climbMemory takes mbPerSecond more memory each second, every page written so the system counts it, until it holds
// upTo MB (never, with 0). Past the version's memory size the system stops the game server.
func climbMemory(ctx context.Context, mbPerSecond, upTo int, log io.Writer) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for total := 0; upTo == 0 || total < upTo; total += mbPerSecond {
		b := make([]byte, mbPerSecond<<20)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
		held = append(held, b)
		fmt.Fprintf(log, "memory: holding %d MB\n", total+mbPerSecond)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Run serves one match and returns when it is over.
func Run(ctx context.Context, cfg Config, log io.Writer) error {
	s, err := Listen(cfg, log)
	if err != nil {
		return err
	}
	return s.Serve(ctx)
}
