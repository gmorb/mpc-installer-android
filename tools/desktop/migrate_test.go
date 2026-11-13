package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// oldPlugin puts a plugin installed the old way into a Synths folder: a real ARM library header, plugin-meta.xml, a ROM of the
// user's own, and its entry in MPC.settings.
func oldPlugin(t *testing.T, fd *fakeDevice, root, name, uid string, machine uint16) string {
	t.Helper()
	folder := "me - VST - " + name
	d := plugin(t, root, folder, "lib/"+strings.ToLower(name)+".so", uid, name)
	os.MkdirAll(filepath.Join(d, "lib"), 0o755)
	os.WriteFile(filepath.Join(d, "lib", strings.ToLower(name)+".so"), []byte(elf(machine)), 0o755)
	os.WriteFile(filepath.Join(d, "Plugin Skins.json"), []byte("{}"), 0o644)
	return d
}

func TestOldSynthsPluginsMoveToVST2Plugins(t *testing.T) {
	fd, cfg, usb, _ := nativeDevice(t)
	internal := cfg.SynthsDir
	acid := oldPlugin(t, fd, internal, "Acid", "41634964", 0x28)
	wrong := oldPlugin(t, fd, internal, "Big", "42696721", 0xB7) // a 64-bit library on this 32-bit MPC
	writeSettings(fd, []string{internal}, entry("Acid", acid+"/lib/acid.so", "41634964"), entry("Big", wrong+"/lib/big.so", "42696721"))
	os.MkdirAll(filepath.Join(usb, VST2Dir), 0o755)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	drive, _ := d.Info.vdrive(usb)
	var items []MoveItem
	for _, p := range d.Info.Plugins {
		items = append(items, MoveItem{DevPlugin: p, Version: map[string]string{"Acid": "2.1.0"}[p.Name]})
	}
	if len(items) != 2 {
		t.Fatalf("plugins in Synths: %+v", d.Info.Plugins)
	}
	j := &Job{ID: "m", State: "running"}
	err = RunMigrate(d, drive, items, j, nil)
	log := strings.Join(j.Lines, "\n")
	if err == nil || !strings.Contains(err.Error(), "me - VST - Big") || !strings.Contains(log, "needs arm32") {
		t.Fatalf("the 64-bit one must fail on its own: %v\n%s", err, log)
	}
	if len(j.okList()) != 1 || j.okList()[0] != moveKey(internal, "me - VST - Acid") {
		t.Fatalf("only Acid is reported as moved: %v", j.okList())
	}
	nd := filepath.Join(usb, VST2Dir, "me - VST - Acid")
	b, err := os.ReadFile(filepath.Join(nd, "plugin.json"))
	if err != nil {
		t.Fatal(err, log)
	}
	var pj map[string]any
	json.Unmarshal(b, &pj)
	if pj["uid"] != "AcId" || pj["file32"] != "lib/acid.so" || pj["version"] != "2.1.0" || pj["manufacturer"] != "me" || pj["format"] != "vst2" {
		t.Errorf("plugin.json: %s", b)
	}
	for _, f := range []string{"version.xml", "roms/mine.rom", "lib/acid.so", "Plugin Skins.json"} {
		if _, err := os.Stat(filepath.Join(nd, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(nd, "plugin-meta.xml")); err == nil {
		t.Error("plugin-meta.xml must not be in the VST2 copy")
	}
	if _, err := os.Stat(filepath.Join(acid, "plugin-meta.xml")); err != nil {
		t.Error("the old folder stays until the user says to delete it")
	}
	if _, err := os.Stat(filepath.Join(usb, VST2Dir, "me - VST - Big")); err == nil {
		t.Error("a plugin that failed must not appear in VST2 Plugins")
	}
	if len(fd.calls()) != 0 {
		t.Errorf("moving does not stop MPC: %v", fd.calls())
	}
}

// Through the API: move, then delete the old copy (its MPC.settings entry goes too, MPC restarts once); only moved ones can go.
func TestServerMoveThenDeleteOldCopy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := newHarness(t)
	fd, cfg, usb, _ := nativeDevice(t)
	h.fd, h.app.cfg = fd, cfg
	internal := cfg.SynthsDir
	acid := oldPlugin(t, fd, internal, "Acid", "41634964", 0x28)
	keep := oldPlugin(t, fd, internal, "Keep", "4b656570", 0x28)
	settings := writeSettings(fd, []string{internal}, entry("Acid", acid+"/lib/acid.so", "41634964"), entry("Keep", keep+"/lib/keep.so", "4b656570"))
	os.MkdirAll(filepath.Join(usb, VST2Dir), 0o755)
	if code, m := h.post("/api/connect", map[string]any{"host": "127.0.0.1", "password": "secret"}); code != 200 {
		t.Fatal(m)
	}
	wait := func() map[string]any {
		var job map[string]any
		for i := 0; i < 200; i++ {
			_, b := h.do("GET", "/api/job?since=0", nil, nil)
			jsonUnmarshal(b, &job)
			if job["state"] != "running" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		return job
	}
	ref := []map[string]string{{"root": internal, "folder": "me - VST - Acid"}}
	if code, _ := h.post("/api/migrate/cleanup", map[string]any{"items": ref, "confirm": true}); code != 400 {
		t.Fatal("nothing was moved yet: deleting must be refused")
	}
	if code, m := h.post("/api/migrate", map[string]any{"items": ref, "drive": usb, "confirm": true}); code != 200 {
		t.Fatal(m)
	}
	if job := wait(); job["state"] != "done" {
		t.Fatalf("move: %v", job)
	}
	if code, _ := h.post("/api/migrate/cleanup", map[string]any{"items": []map[string]string{{"root": internal, "folder": "me - VST - Keep"}}, "confirm": true}); code != 400 {
		t.Fatal("a plugin that was not moved must not be deleted by the cleanup")
	}
	if code, m := h.post("/api/migrate/cleanup", map[string]any{"items": ref, "confirm": true}); code != 200 {
		t.Fatal(m)
	}
	if job := wait(); job["state"] != "done" {
		t.Fatalf("cleanup: %v", job)
	}
	if _, err := os.Stat(acid); err == nil {
		t.Error("the old copy must be gone")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the other plugin must stay")
	}
	s, _ := os.ReadFile(settings)
	if strings.Contains(string(s), `uid="41634964"`) || !strings.Contains(string(s), `uid="4b656570"`) {
		t.Errorf("only the moved plugin's entry leaves MPC's old plugin list:\n%s", s)
	}
	if got := strings.Join(fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("one stop and start for the cleanup: %s", got)
	}
	if _, err := os.Stat(filepath.Join(usb, VST2Dir, "me - VST - Acid", "roms", "mine.rom")); err != nil {
		t.Errorf("the moved copy keeps the user's files: %v", err)
	}
}
