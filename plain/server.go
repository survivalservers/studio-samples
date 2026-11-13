package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Server is one match: a UDP socket, the players in it, and the loops that talk to the local API.
type Server struct {
	cfg  Config
	log  io.Writer
	conn net.PacketConn

	players  map[string]bool
	peak     int
	joined   bool
	alloc    *Allocation
	started  time.Time
	reported chan []string // player lists for the reporter, newest wins
	refused  chan string   // a player the platform refused (a name with a control character): left out
	done     chan struct{} // closed when the reporter has sent the last list
}

// Listen binds the game port. Binding first, then saying ready, means a player can never be sent to a port
// nobody is listening on.
func Listen(cfg Config, log io.Writer) (*Server, error) {
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", cfg.ListenPort))
	if err != nil {
		return nil, fmt.Errorf("bind game port: %w", err)
	}
	return &Server{cfg: cfg, log: log, conn: conn, players: map[string]bool{}}, nil
}

// Addr is the bound game address (useful when the port was 0).
func (s *Server) Addr() net.Addr { return s.conn.LocalAddr() }

func (s *Server) logf(format string, args ...any) { fmt.Fprintf(s.log, format+"\n", args...) }

// Serve runs the match until it ends. It returns nil for every planned ending (match over, stop asked, stop
// signal), so the process exits with code 0, which the platform counts as a normal stop.
func (s *Server) Serve(ctx context.Context) error {
	defer s.conn.Close()
	s.started = time.Now()
	api := s.cfg.API

	// Match data comes from the allocation call when there is a local API: it answers at once on a fresh start
	// and is the only place secret settings come from on a ready server. The allocation file has the same data without
	// the secret values, for programs that make no HTTP calls.
	if !api.Enabled() {
		if alloc, err := readAllocationFile(s.cfg.AllocationFile); err != nil {
			s.logf("allocation file: %v", err)
		} else if alloc != nil {
			s.setAllocation(alloc)
		}
	}

	// A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up
	// before the first player arrives. A ready server (pre-started) has none yet and waits for it after ready.
	haveMatch := false
	if api.Enabled() {
		if a, err := api.WaitAllocation(ctx, 0); err == nil && a != nil {
			s.setAllocation(a)
			haveMatch = true
		}
	}

	s.logf("listening on udp %s; players connect to %s:%d", s.Addr(), s.cfg.PublicIP, s.cfg.PublicPort)
	// A version can use this exact line as its ready check ({"type": "log", "pattern": "Server ready for players"})
	// instead of the call below.
	s.logf("Server ready for players")
	if err := api.Ready(ctx); err != nil {
		return fmt.Errorf("say ready: %w", err)
	}

	loopCtx, stopLoops := context.WithCancel(ctx)
	defer stopLoops()
	allocated := make(chan *Allocation, 1)
	stopAsked := make(chan *StopNotice, 1)
	if api.Enabled() {
		if !haveMatch {
			go poll(loopCtx, func(c context.Context) (*Allocation, error) { return api.WaitAllocation(c, 30*time.Second) }, allocated)
		}
		go poll(loopCtx, func(c context.Context) (*StopNotice, error) { return api.WaitStop(c, 30*time.Second) }, stopAsked)
		go heartbeat(loopCtx, api, 10*time.Second)
	}
	s.reported = make(chan []string, 16)
	s.refused = make(chan string, 16)
	s.done = make(chan struct{})
	go s.reporter(api)

	var matchOver <-chan time.Time
	if s.cfg.MatchLength > 0 {
		matchOver = time.After(s.cfg.MatchLength)
	}

	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return s.finish("stop_signal", false)
		case a := <-allocated:
			s.setAllocation(a)
		case n := <-stopAsked:
			s.logf("stop asked; leaving before %s", n.Deadline.Format(time.RFC3339))
			return s.finish("stop_asked", false)
		case <-matchOver:
			return s.finish("match_over", true)
		case id := <-s.refused:
			// The platform takes no list with this player in it: the others still count without it.
			if s.players[id] {
				delete(s.players, id)
				s.reported <- s.playerList()
			}
			continue
		default:
		}

		_ = s.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return fmt.Errorf("read: %w", err)
		}
		reply, changed := s.handle(strings.TrimSpace(string(buf[:n])))
		if reply != "" {
			_, _ = s.conn.WriteTo([]byte(reply), from)
		}
		if changed {
			s.reported <- s.playerList()
			if s.joined && len(s.players) == 0 {
				return s.finish("match_over", true)
			}
		}
	}
}

// handle runs one command from a player and says whether the player list changed.
func (s *Server) handle(line string) (reply string, changed bool) {
	cmd, name, _ := strings.Cut(line, " ")
	name = strings.TrimSpace(name)
	switch {
	case cmd == "ping":
		return "pong", false
	case cmd == "join" && name != "":
		if s.players[name] {
			return "already in " + s.cfg.ID, false
		}
		s.players[name] = true
		s.joined = true
		if len(s.players) > s.peak {
			s.peak = len(s.players)
		}
		s.logf("%s joined (%d playing)", name, len(s.players))
		return "welcome " + name + " to " + s.cfg.ID, true
	case cmd == "leave" && name != "":
		if !s.players[name] {
			return "not in " + s.cfg.ID, false
		}
		delete(s.players, name)
		s.logf("%s left (%d playing)", name, len(s.players))
		return "bye " + name, true
	default:
		return "unknown command; try join <name>, leave <name> or ping", false
	}
}

func (s *Server) playerList() []string {
	ids := make([]string, 0, len(s.players))
	for id := range s.players {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) setAllocation(a *Allocation) {
	s.alloc = a
	// Setting names only: values can be secret, and the console is a log kept for 90 days.
	names := make([]string, 0, len(a.Env))
	for k := range a.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	s.logf("allocated: match %q, labels %v, settings %v", a.ExternalID, a.Labels, names)
}

// reporter sends player lists to the local API one at a time, skipping to the newest when it falls
// behind, so a slow call never holds up the game loop.
func (s *Server) reporter(api *LocalAPI) {
	defer close(s.done)
	for ids := range s.reported {
		for more := true; more; {
			select {
			case newer, ok := <-s.reported:
				if !ok {
					more = false
					break
				}
				ids = newer
			default:
				more = false
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := api.Players(ctx, ids); err != nil {
			s.logf("report players: %v", err)
			var r *PlayerRefused
			if errors.As(err, &r) {
				select {
				case s.refused <- r.ID:
				default:
				}
			}
		}
		cancel()
	}
}

// finish ends the match: the last player list goes out, the result file is written, and if the game server
// decided to end (rather than being asked to stop) it tells the platform, which is the same as exiting with 0.
func (s *Server) finish(reason string, selfStop bool) error {
	close(s.reported)
	<-s.done
	s.logf("match over: %s after %s, %d players at most", reason, time.Since(s.started).Round(time.Second), s.peak)
	if err := s.writeResult(reason); err != nil {
		s.logf("result file: %v", err)
	}
	if selfStop {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.cfg.API.Stop(ctx, reason); err != nil {
			s.logf("stop: %v (exiting with 0 counts as a stop too)", err)
		}
	}
	return nil
}

// writeResult leaves match-result.json in GS_ARTIFACT_DIR, which the platform collects after exit.
func (s *Server) writeResult(reason string) error {
	if s.cfg.ArtifactDir == "" {
		return nil
	}
	result := map[string]any{
		"game_server_id": s.cfg.ID,
		"reason":         reason,
		"peak_players":   s.peak,
		"seconds":        int(time.Since(s.started).Seconds()),
	}
	if s.alloc != nil {
		result["match"] = s.alloc.ExternalID
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cfg.ArtifactDir, "match-result.json"), append(b, '\n'), 0o644)
}

// readAllocationFile returns nil and no error when there is no file (yet). Secret values are never in the file.
func readAllocationFile(path string) (*Allocation, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a Allocation
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &a, nil
}

// poll repeats one long-poll until it returns a value, then hands it over. Errors wait a second and retry.
func poll[T any](ctx context.Context, once func(context.Context) (*T, error), out chan<- *T) {
	for ctx.Err() == nil {
		v, err := once(ctx)
		if v != nil {
			out <- v
			return
		}
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

// heartbeat sends the optional health call. Only needed when the version sets health_timeout.
func heartbeat(ctx context.Context, api *LocalAPI, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = api.Health(ctx)
		}
	}
}
