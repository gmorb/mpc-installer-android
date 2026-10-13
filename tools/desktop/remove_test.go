package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const settingsWith = `<?xml version="1.0" encoding="UTF-8"?>
<PROPERTIES>
  <VALUE name="pluginList-arm">
    <KNOWNPLUGINS>
      <PLUGIN name="Other" format="VST" manufacturer="x" version="1.0" file="/sdcard/vst/other.so" uid="6f746872" isInstrument="1"/>
      <PLUGIN name="A" format="VST" manufacturer="me" version="1.0" file="%[1]s/me - VST - A/a.so" uid="aaaa0001" isInstrument="1"/>
      <PLUGIN name="B" format="VST" manufacturer="me" version="1.0" file="%[1]s/me - VST - B/b.so" uid="bbbb0002" isInstrument="1"/>
    </KNOWNPLUGINS>
  </VALUE>
</PROPERTIES>
`

// seed puts two plugin folders, a settings file and a recorded state on the fake device
func seed(t *testing.T, fd *fakeDevice) (syn, settings string) {
	t.Helper()
	syn = fd.cfg().SynthsDir
	settings = filepath.Join(fd.dir, "Settings", "MPC", "MPC.settings")
	os.WriteFile(settings, []byte(strings.ReplaceAll(settingsWith, "%[1]s", syn)), 0o644)
	for _, p := range []struct{ folder, so, uid, name string }{{"me - VST - A", "a.so", "aaaa0001", "A"}, {"me - VST - B", "b.so", "bbbb0002", "B"}} {
		d := filepath.Join(syn, p.folder)
		os.MkdirAll(filepath.Join(d, "roms"), 0o755)
		os.MkdirAll(filepath.Join(d, "Plugin Skins"), 0o755)
		os.WriteFile(filepath.Join(d, p.so), []byte("ELF"), 0o755)
		os.WriteFile(filepath.Join(d, "roms", "mine.rom"), []byte("my own file"), 0o644)
		os.WriteFile(filepath.Join(d, "plugin-meta.xml"), []byte(`<PLUGIN name="`+p.name+`" file="%payload-path%/`+p.folder+`/`+p.so+`" uid="`+p.uid+`"/>`), 0o644)
	}
	os.WriteFile(filepath.Join(syn, ".mpc-store"), []byte("a\t1.0.0\tme - VST - A\t1\nb\t1.0.0\tme - VST - B\t1\n"), 0o644)
	return
}

func runRemove(t *testing.T, fd *fakeDevice, plans ...RemovePlan) (*Job, error) {
	t.Helper()
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	j := &Job{ID: "x", State: "running"}
	return j, RunRemove(d, plans, j, nil)
}

func TestInfoListsPluginFolders(t *testing.T) {
	fd := newFakeDevice(t)
	seed(t, fd)
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got := map[string]DevPlugin{}
	for _, p := range d.Info.Plugins {
		got[p.Folder] = p
	}
	if got["me - VST - A"].UID != "aaaa0001" || got["me - VST - A"].Name != "A" || got["me - VST - B"].UID != "bbbb0002" {
		t.Fatalf("plugin folders not read: %+v", d.Info.Plugins)
	}
}

func TestRemoveDeletesTheFolderButKeepsYourFilesAndTouchesNothingElse(t *testing.T) {
	fd := newFakeDevice(t)
	syn, settings := seed(t, fd)
	j, err := runRemove(t, fd, RemovePlan{Root: syn, Folder: "me - VST - A", UID: "aaaa0001", ID: "a", Keep: []string{"roms"}})
	if err != nil {
		t.Fatal(err, j.Lines)
	}
	a := filepath.Join(syn, "me - VST - A")
	if _, err := os.Stat(filepath.Join(a, "a.so")); err == nil {
		t.Error("the plugin file must be gone")
	}
	if b, _ := os.ReadFile(filepath.Join(a, "roms", "mine.rom")); string(b) != "my own file" {
		t.Errorf("your own files must stay: %q", b)
	}
	if _, err := os.Stat(filepath.Join(a, "plugin-meta.xml")); err == nil {
		t.Error("plugin-meta.xml must be gone, or a scanner would register it again")
	}
	if _, err := os.Stat(filepath.Join(syn, "me - VST - B", "b.so")); err != nil {
		t.Error("another plugin must be untouched")
	}
	st, _ := os.ReadFile(settings)
	if strings.Contains(string(st), "aaaa0001") || !strings.Contains(string(st), "bbbb0002") || !strings.Contains(string(st), "6f746872") {
		t.Errorf("only the removed plugin's entry may go:\n%s", st)
	}
	state, _ := os.ReadFile(filepath.Join(syn, ".mpc-store"))
	if strings.Contains(string(state), "a\t1.0.0") || !strings.Contains(string(state), "b\t1.0.0") {
		t.Errorf("the recorded version of the removed plugin goes, the other stays: %q", state)
	}
	if got := strings.Join(fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("MPC must be stopped once and started once: %s", got)
	}
	baks, _ := filepath.Glob(settings + ".bak-remove-*")
	if len(baks) != 1 {
		t.Errorf("a settings backup is made: %v", baks)
	}
	if ents, _ := os.ReadDir(fd.cfg().RemoteTmp); len(ents) != 0 {
		t.Errorf("cleanup: %v", ents)
	}
}

func TestRemoveWithNothingToKeepDeletesTheWholeFolder(t *testing.T) {
	fd := newFakeDevice(t)
	syn, _ := seed(t, fd)
	if _, err := runRemove(t, fd, RemovePlan{Root: syn, Folder: "me - VST - A", UID: "aaaa0001", ID: "a"}, RemovePlan{Root: syn, Folder: "me - VST - B", UID: "bbbb0002", ID: "b"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"me - VST - A", "me - VST - B"} {
		if _, err := os.Stat(filepath.Join(syn, f)); err == nil {
			t.Errorf("%s must be gone", f)
		}
	}
	if got := strings.Join(fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("one stop and one start for the batch: %s", got)
	}
}

func TestABrokenSettingsFileChangesNothingAndMPCStarts(t *testing.T) {
	fd := newFakeDevice(t)
	syn, settings := seed(t, fd)
	bad := strings.Replace(strings.ReplaceAll(settingsWith, "%[1]s", syn), "</PROPERTIES>\n", "", 1) // no closing root element
	os.WriteFile(settings, []byte(bad), 0o644)
	_, err := runRemove(t, fd, RemovePlan{Root: syn, Folder: "me - VST - A", UID: "aaaa0001", ID: "a", Keep: []string{"roms"}})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if b, _ := os.ReadFile(settings); string(b) != bad {
		t.Error("MPC.settings must be left as it was")
	}
	if _, err := os.Stat(filepath.Join(syn, "me - VST - A", "a.so")); err != nil {
		t.Error("nothing is deleted when the settings edit cannot be trusted")
	}
	if got := strings.Join(fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("MPC must be started again: %s", got)
	}
}

func TestRemovePlansAreChecked(t *testing.T) {
	for _, p := range []RemovePlan{
		{Folder: "../x", UID: "aaaa0001"}, {Folder: "a/b", UID: "aaaa0001"}, {Folder: "..", UID: "aaaa0001"}, {Folder: "", UID: "aaaa0001"},
		{Folder: "ok", UID: ""}, {Folder: "ok", UID: "zz; rm -rf /"}, {Folder: "ok", UID: "aaaa0001", Keep: []string{"../etc"}},
		{Folder: "ok", UID: "aaaa0001", Keep: []string{"a b"}}, {Folder: "ok", UID: "aaaa0001", Keep: []string{"$(x)"}},
		{Root: "relative/path", Folder: "ok", UID: "aaaa0001"}, {Root: "/sdcard/Synths/../..", Folder: "ok", UID: "aaaa0001"},
		{Root: "/sd card/$(x)", Folder: "ok", UID: "aaaa0001"},
	} {
		if p.Root == "" {
			p.Root = "/sdcard/Synths"
		}
		if p.check() == nil {
			t.Errorf("%+v must be refused", p)
		}
	}
	if (RemovePlan{Root: "/sdcard/Synths", Folder: "me - VST - A", UID: "aaaa0001", Keep: []string{"roms", "jv880-roms/roms"}}).check() != nil {
		t.Error("a normal plan is fine")
	}
}

func TestEmbeddedAwkIsTheCanonicalOne(t *testing.T) {
	canon, err := os.ReadFile("../release/plugin_list.awk")
	if err != nil {
		t.Skip("run from the repo: ", err)
	}
	if string(canon) != string(pluginListAWK) {
		t.Fatal("tools/desktop/plugin_list.awk differs from tools/release/plugin_list.awk: copy it again")
	}
}

func TestServerRemovesKnownPluginsAndRefusesUnknownOnes(t *testing.T) {
	h := newHarness(t)
	syn, _ := seed(t, h.fd)
	// A is from the catalog (its uid and user data are known); B is not known to the app
	h.app.cat = []CatPlugin{{ID: "a", Name: "A", Version: "1.0.0", Skin: "me - VST - A", UserData: []string{"roms"}}}
	h.app.catAt = time.Now()
	if code, _ := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "secret"}); code != 200 {
		t.Fatal("connect")
	}
	_, list := h.post("/api/remove", map[string]any{"items": []map[string]string{{"root": syn, "folder": "me - VST - A"}}}) // no confirm
	if list["error"] == nil {
		t.Error("needs confirmation")
	}
	if code, m := h.post("/api/remove", map[string]any{"items": []map[string]string{{"root": syn, "folder": "me - VST - B"}}, "confirm": true}); code != 400 || !strings.Contains(m["error"].(string), "cannot tell which files") {
		t.Errorf("an unknown plugin is refused with a reason: %d %v", code, m)
	}
	if code, _ := h.post("/api/remove", map[string]any{"items": []map[string]string{{"root": syn, "folder": "../../etc"}}, "confirm": true}); code != 400 {
		t.Errorf("a path is not a plugin folder: %d", code)
	}
	if _, err := os.Stat(filepath.Join(syn, "me - VST - B", "b.so")); err != nil {
		t.Fatal("the refused plugin must be untouched")
	}
	if code, m := h.post("/api/remove", map[string]any{"items": []map[string]string{{"root": syn, "folder": "me - VST - A"}}, "confirm": true}); code != 200 {
		t.Fatalf("remove: %d %v", code, m)
	}
	var st map[string]any
	for i := 0; i < 100; i++ {
		_, b := h.do("GET", "/api/job?since=0", nil, nil)
		st = map[string]any{}
		jsonUnmarshal(b, &st)
		if st["state"] != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("job: %v", st)
	}
	if _, err := os.Stat(filepath.Join(syn, "me - VST - A", "a.so")); err == nil {
		t.Error("A must be removed")
	}
	if b, _ := os.ReadFile(filepath.Join(syn, "me - VST - A", "roms", "mine.rom")); string(b) != "my own file" {
		t.Error("the catalog's user_data must be kept")
	}
	resp, body := h.do("GET", "/api/device", nil, nil)
	if resp.StatusCode != 200 || strings.Contains(string(body), "me - VST - A") && !strings.Contains(string(body), "\"known\"") {
		t.Errorf("device list: %d %s", resp.StatusCode, body)
	}
}
