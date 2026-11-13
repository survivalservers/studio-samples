package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Allocation is the match data a game server receives when a start picks it: the same JSON comes from the
// allocation file, the allocation long-poll and the engine add-ons.
type Allocation struct {
	GameServerID string            `json:"game_server_id"`
	AllocatedAt  time.Time         `json:"allocated_at"`
	ExternalID   string            `json:"external_id,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Args         []string          `json:"args,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
}

// StopNotice arrives on the shutdown long-poll when a stop has been asked for; the game server should save and
// exit before Deadline.
type StopNotice struct {
	Deadline time.Time `json:"deadline"`
}

// LocalAPI talks to the local API on the machine ($GS_LOCAL_API_URL, with $GS_TOKEN). Every call is plain
// HTTP and JSON, so any engine's own HTTP client can make the same calls. A LocalAPI with no URL does nothing,
// which is how the server runs on a developer's PC without the platform.
type LocalAPI struct {
	URL    string
	Token  string
	Client *http.Client
}

// Enabled reports whether there is a local API to talk to.
func (a *LocalAPI) Enabled() bool { return a != nil && a.URL != "" }

func (a *LocalAPI) do(ctx context.Context, method, path string, body, out any) (int, error) {
	if !a.Enabled() {
		return 0, nil
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.URL+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return res.StatusCode, &callError{fmt.Sprintf("%s %s: %d %s", method, path, res.StatusCode, bytes.TrimSpace(msg)), res.StatusCode, msg}
	}
	if out != nil && res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return res.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

// Ready says the game server can take players (for a ready server: that it can be picked for a match).
// Needed only when the version's ready check is local_api; a log line, a bound port or a delay work too.
func (a *LocalAPI) Ready(ctx context.Context) error {
	_, err := a.do(ctx, http.MethodPost, "/v0/self/ready", nil, nil)
	return err
}

// Health is the optional heartbeat, for versions that set health_timeout.
func (a *LocalAPI) Health(ctx context.Context) error {
	_, err := a.do(ctx, http.MethodPost, "/v0/self/health", nil, nil)
	return err
}

// WaitAllocation long-polls once for up to wait. It returns nil and no error when the wait ran out first.
func (a *LocalAPI) WaitAllocation(ctx context.Context, wait time.Duration) (*Allocation, error) {
	var alloc Allocation
	code, err := a.do(ctx, http.MethodGet, "/v0/self/allocation?wait="+url.QueryEscape(wait.String()), nil, &alloc)
	if err != nil || code != http.StatusOK {
		return nil, err
	}
	return &alloc, nil
}

// WaitStop long-polls once for up to wait. It returns nil and no error when no stop was asked for in that time.
func (a *LocalAPI) WaitStop(ctx context.Context, wait time.Duration) (*StopNotice, error) {
	var notice StopNotice
	code, err := a.do(ctx, http.MethodGet, "/v0/self/shutdown?wait="+url.QueryEscape(wait.String()), nil, &notice)
	if err != nil || code != http.StatusOK {
		return nil, err
	}
	return &notice, nil
}

// callError is a call the local API refused, with its reply.
type callError struct {
	msg    string
	status int
	body   []byte
}

func (e *callError) Error() string { return e.msg }

// PlayerRefused is a player list refused for one ID in it: a player ID is 1 to 128 characters with no control
// characters, and players choose their own names. Leave that player out and report the rest.
type PlayerRefused struct {
	ID     string
	Reason string
}

func (e *PlayerRefused) Error() string {
	return fmt.Sprintf("the platform refused player ID %q (%s); the others are reported without it", e.ID, e.Reason)
}

// Players reports who is connected, so studios can find game servers by player count. A list refused for one of
// its IDs comes back as a *PlayerRefused naming it (the refusal's field says which: "ids[2]").
func (a *LocalAPI) Players(ctx context.Context, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	_, err := a.do(ctx, http.MethodPut, "/v0/self/players", map[string]any{"ids": ids}, nil)
	var ce *callError
	if errors.As(err, &ce) && ce.status == http.StatusUnprocessableEntity {
		var p struct {
			Errors []struct{ Field, Message string } `json:"errors"`
		}
		if json.Unmarshal(ce.body, &p) == nil {
			for _, fe := range p.Errors {
				var i int
				if _, err := fmt.Sscanf(fe.Field, "ids[%d]", &i); err == nil && i >= 0 && i < len(ids) {
					return &PlayerRefused{ID: ids[i], Reason: fe.Message}
				}
			}
		}
	}
	return err
}

// Stop ends this game server on purpose, the same as exiting with code 0.
func (a *LocalAPI) Stop(ctx context.Context, reason string) error {
	_, err := a.do(ctx, http.MethodPost, "/v0/self/stop", map[string]string{"reason": reason}, nil)
	return err
}
