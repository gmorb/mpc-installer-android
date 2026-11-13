package main

// Community signals from the catalog's service (open-mpc's Worker, docs/COMMUNITY_SIGNALS.md upstream): how many people report a
// plugin working, how many like it, and, per plugin, on which devices and OS versions. Read-only and anonymous: liking and
// reporting need a sign-in on the catalog site, so the app links there. The counts are an extra: when the service cannot be reached
// the page shows none and works as before.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var signalsBase = "https://openmpc-signals.open-mpc.workers.dev"

// Signal is one plugin's counts: people reporting it working (all versions, and per version), on an MPC Sample, and likes.
type Signal struct {
	Working  int            `json:"working"`
	Likes    int            `json:"likes"`
	Sample   int            `json:"sample,omitempty"`
	Versions map[string]int `json:"versions"`
}

type signalsCache struct {
	mu     sync.Mutex
	all    map[string]Signal
	at     time.Time
	err    error
	plugin map[string]cachedDetail
}

type cachedDetail struct {
	body []byte
	at   time.Time
}

var signalsClient = &http.Client{Timeout: 10 * time.Second}

func getJSON(u string, limit int64) ([]byte, error) {
	resp, err := signalsClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// counts returns every plugin's counts, read at most every five minutes (the service caches for as long).
func (c *signalsCache) counts() (map[string]Signal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.all != nil && time.Since(c.at) < 5*time.Minute || c.err != nil && time.Since(c.at) < time.Minute {
		return c.all, c.err
	}
	b, err := getJSON(signalsBase+"/api/counts", 4<<20)
	var out struct {
		Counts map[string]Signal `json:"counts"`
	}
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	c.at, c.err = time.Now(), err
	if err == nil {
		c.all = out.Counts
		if c.all == nil {
			c.all = map[string]Signal{}
		}
	}
	return c.all, c.err
}

var pluginIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// detail is one plugin's breakdown (devices and OS versions people reported on, the latest reports), passed through as the
// service sends it, cached five minutes.
func (c *signalsCache) detail(id string) ([]byte, error) {
	if !pluginIDRe.MatchString(id) {
		return nil, fmt.Errorf("bad plugin id")
	}
	c.mu.Lock()
	if d, ok := c.plugin[id]; ok && time.Since(d.at) < 5*time.Minute {
		c.mu.Unlock()
		return d.body, nil
	}
	c.mu.Unlock()
	b, err := getJSON(signalsBase+"/api/plugin?id="+id, 1<<20)
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.plugin == nil {
		c.plugin = map[string]cachedDetail{}
	}
	c.plugin[id] = cachedDetail{b, time.Now()}
	c.mu.Unlock()
	return b, nil
}

// signals: GET /api/signals gives every plugin's counts ({"ok": false} when the service cannot be reached);
// GET /api/signals?id=<plugin> gives one plugin's breakdown.
func (a *App) signals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "GET only")
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		b, err := a.sig.detail(id)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
		return
	}
	c, err := a.sig.counts()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "counts": map[string]Signal{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "counts": c})
}
