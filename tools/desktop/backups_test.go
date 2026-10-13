package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// makeBackups writes n backups next to the fake device's MPC.settings, oldest first (day 1 .. day n), plus files that must never be touched.
func makeBackups(t *testing.T, fd *fakeDevice, n int) (dir string) {
	t.Helper()
	dir = filepath.Join(fd.dir, "Settings", "MPC")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		f := filepath.Join(dir, fmt.Sprintf("MPC.settings.bak-acid-202601%02d-%06d", i, i))
		os.WriteFile(f, []byte(strings.Repeat("x", 2048)), 0o644)
		ts := base.Add(time.Duration(i) * 24 * time.Hour)
		os.Chtimes(f, ts, ts)
	}
	os.WriteFile(filepath.Join(dir, "MPC.settings.keep-me"), []byte("not a backup"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.bak-1"), []byte("not ours"), 0o644)
	return dir
}

func backupNames(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "MPC.settings.bak-") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func TestBackupsAreCountedAndPrunedNewestKept(t *testing.T) {
	fd := newFakeDevice(t)
	dir := makeBackups(t, fd, 12)
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	bi, err := d.Backups()
	if err != nil || bi.Count != 12 || bi.Newest != "2026-01-12" || bi.Oldest != "2026-01-01" || bi.TotalKB != 24 {
		t.Fatalf("backup info: %+v %v", bi, err)
	}
	for _, bad := range []int{0, -1, maxKeep + 1} {
		if _, err := d.PruneBackups(bad, nil); err == nil {
			t.Errorf("keep=%d must be refused", bad)
		}
	}
	if len(backupNames(dir)) != 12 {
		t.Fatal("a refused prune must delete nothing")
	}
	if n, err := d.PruneBackups(20, nil); err != nil || n != 0 {
		t.Errorf("keeping more than there are deletes nothing: %d %v", n, err)
	}
	n, err := d.PruneBackups(5, nil)
	if err != nil || n != 7 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	var want []string
	for i := 8; i <= 12; i++ {
		want = append(want, fmt.Sprintf("MPC.settings.bak-acid-202601%02d-%06d", i, i))
	}
	if got := backupNames(dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the five NEWEST must remain:\n got %v\nwant %v", got, want)
	}
	for _, f := range []string{"MPC.settings", "MPC.settings.keep-me", "other.bak-1"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s must never be touched: %v", f, err)
		}
	}
	if len(fd.calls()) != 0 {
		t.Error("MPC must not be touched")
	}
	if n, _ := d.PruneBackups(1, nil); n != 4 || len(backupNames(dir)) != 1 || !strings.HasSuffix(backupNames(dir)[0], "-000012") {
		t.Errorf("keeping 1 leaves exactly the newest: %v", backupNames(dir))
	}
}

func TestServerBackupEndpoints(t *testing.T) {
	h := newHarness(t)
	dir := makeBackups(t, h.fd, 8)
	if resp, _ := h.do("GET", "/api/backups", nil, nil); resp.StatusCode != 400 {
		t.Errorf("not connected: %d", resp.StatusCode)
	}
	if code, _ := h.post("/api/prune", map[string]any{"keep": 3, "confirm": true}); code != 400 {
		t.Errorf("not connected: %d", code)
	}
	if code, _ := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "secret"}); code != 200 {
		t.Fatal("connect")
	}
	_, body := h.do("GET", "/api/backups", nil, nil)
	if !strings.Contains(string(body), `"count":8`) || !strings.Contains(string(body), `"keepDefault":10`) {
		t.Errorf("backups: %s", body)
	}
	if code, _ := h.post("/api/prune", map[string]any{"keep": 3}); code != 400 {
		t.Errorf("needs confirmation: %d", code)
	}
	if code, _ := h.post("/api/prune", map[string]any{"keep": 0, "confirm": true}); code != 400 {
		t.Errorf("keep 0: %d", code)
	}
	if len(backupNames(dir)) != 8 {
		t.Fatal("nothing deleted yet")
	}
	code, m := h.post("/api/prune", map[string]any{"keep": 3, "confirm": true})
	if code != 200 || m["deleted"].(float64) != 5 || len(backupNames(dir)) != 3 {
		t.Fatalf("prune: %d %v %v", code, m, backupNames(dir))
	}
}

func TestPlanUsesTheCatalogsDeferFlag(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name     string
		flags    []*bool
		restarts float64
		maybe    bool
	}{
		{"all new installers: one restart", []*bool{&yes, &yes}, 1, false},
		{"one old installer: its own restart plus the batch", []*bool{&yes, &no}, 2, false},
		{"only old installers: one each", []*bool{&no, &no}, 2, false},
		{"flag unknown: at least one, maybe more", []*bool{nil}, 1, true},
		{"mixed with unknown", []*bool{&no, nil}, 2, true},
	}
	for _, c := range cases {
		h := newHarness(t)
		var ids []string
		h.app.cat = nil
		for i, f := range c.flags {
			id := fmt.Sprintf("p%d", i)
			h.app.cat = append(h.app.cat, CatPlugin{ID: id, Name: id, Version: "1.0.0", Defer: f})
			ids = append(ids, id)
		}
		h.app.catAt = time.Now()
		code, plan := h.post("/api/plan", map[string]any{"catalog": ids})
		if code != 200 || plan["restarts"].(float64) != c.restarts || plan["maybeMore"].(bool) != c.maybe {
			t.Errorf("%s: %d %v", c.name, code, plan)
		}
	}
}
