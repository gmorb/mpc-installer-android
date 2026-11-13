package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// elf makes the start of an ARM shared object: the header is all the app reads (machine 0x28 = 32-bit ARM, 0xB7 = AArch64).
func elf(machine uint16) string {
	h := make([]byte, 64)
	copy(h, "\x7fELF")
	h[4], h[5], h[16] = 1, 1, 3
	h[18], h[19] = byte(machine), byte(machine>>8)
	return string(h) + "rest of the library"
}

// catalogPkg: a catalog release zip (the Synths-folder kind) for "Acme - VST - Acid", with a skin, a ROM folder and a junk file.
func catalogPkg(t *testing.T, machine uint16, skin string) *Package {
	t.Helper()
	top := "Acid-2.1.0"
	d := top + "/portable/" + skin + "/"
	ents := []zent{
		{top + "/install.sh", 0o755, newInstall}, {top + "/uninstall.sh", 0o755, "#!/bin/sh\n"}, {top + "/SHA256SUMS", 0, ""}, {top + "/plugin_list.awk", 0, ""},
		{top + "/mpc-plugin.json", 0, manifest("acid", skin, "2.1.0", func(m *Manifest) { m.About = "A 303-style bass line." })},
		{d + "plugin-meta.xml", 0, `<?xml version="1.0"?><PLUGIN name="Acid" format="VST" category="Synth" manufacturer="Acme" version="2.1" file="%payload-path%/` + skin + `/lib/acid.so" uid="41634964" isInstrument="1" numInputs="0" numOutputs="2"/>`},
		{d + "mpc-plugin.json", 0, "{}"},
		{d + "lib/acid.so", 0o644, elf(machine)},
		{d + "Plugin Skins/TUI.json", 0, "{}"},
		{d + "roms/readme.txt", 0, "put your ROM here"},
		{d + ".DS_Store", 0, "junk"},
	}
	p, err := OpenPackage(writeZip(t, t.TempDir(), ents), "upload")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sdkPkg: a zip made with inMusic's SDK (tools/package.py): the plugin folder and an INSTALL.txt beside it.
func sdkPkg(t *testing.T) *Package {
	t.Helper()
	f := "Acme - VST - Wobble/"
	pj := `{"name": "Wobble", "manufacturer": "Acme", "version": "1.0.0", "format": "vst2", "kind": "instrument", "uid": "AcWb", "numOutputs": 2, "file32": "VST/wobble-arm32.so", "file64": "VST/wobble-arm64.so"}`
	p, err := OpenPackage(writeZip(t, t.TempDir(), []zent{
		{f + "plugin.json", 0, pj}, {f + "VST/wobble-arm32.so", 0o755, elf(0x28)}, {f + "VST/wobble-arm64.so", 0o755, elf(0xB7)},
		{f + "Plugin Skins/TUI.json", 0, "{}"}, {"INSTALL.txt", 0, "copy the folder into VST2 Plugins"},
	}), "upload")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func entryMap(f NFolder) map[string]NEntry {
	m := map[string]NEntry{}
	for _, e := range f.Entries {
		m[e.Rel] = e
	}
	return m
}

// A catalog package becomes a VST2 Plugins folder the way the SDK's legacy_convert.py makes one.
func TestCatalogPackageIsConvertedForVST2Plugins(t *testing.T) {
	p := catalogPkg(t, 0x28, "Acme - VST - Acid")
	fs, err := p.NativeFolders("armv7l")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Name != "Acme - VST - Acid" || fs[0].Version != "2.1.0" {
		t.Fatalf("folders: %+v", fs)
	}
	m := entryMap(fs[0])
	for _, gone := range []string{"plugin-meta.xml", "mpc-plugin.json", ".DS_Store"} {
		if _, ok := m[gone]; ok {
			t.Errorf("%s must not be in the converted folder", gone)
		}
	}
	for _, kept := range []string{"lib/acid.so", "Plugin Skins/TUI.json", "roms/readme.txt", "plugin.json", "version.xml"} {
		if _, ok := m[kept]; !ok {
			t.Errorf("%s is missing from the converted folder", kept)
		}
	}
	if m["lib/acid.so"].Mode != 0o755 {
		t.Errorf("the library must be executable: %o", m["lib/acid.so"].Mode)
	}
	var pj map[string]any
	if err := json.Unmarshal(m["plugin.json"].Data, &pj); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "Acid", "manufacturer": "Acme", "version": "2.1.0", "format": "vst2", "kind": "instrument", "uid": "AcId",
		"category": "Synth", "numInputs": 0.0, "numOutputs": 2.0, "file32": "lib/acid.so", "description": "A 303-style bass line."}
	for k, v := range want {
		if pj[k] != v {
			t.Errorf("plugin.json %s = %v, want %v", k, pj[k], v)
		}
	}
	if _, ok := pj["file64"]; ok {
		t.Error("a 32-bit library must not be listed as file64")
	}
	if !strings.Contains(string(m["version.xml"].Data), "<version>2.1.0.0</version>") || strings.Contains(string(m["version.xml"].Data), "uiversion") {
		t.Errorf("version.xml: %s", m["version.xml"].Data)
	}
	// a folder named differently from what the plugin reports is renamed, with a note
	q := catalogPkg(t, 0x28, "acid-old-name")
	fs2, err := q.NativeFolders("armv7l")
	if err != nil {
		t.Fatal(err)
	}
	if fs2[0].Name != "Acme - VST - Acid" || len(fs2[0].Notes) == 0 {
		t.Errorf("renamed folder: %+v", fs2[0])
	}
}

func TestConversionChecksTheLibraryFitsTheMPC(t *testing.T) {
	if _, err := catalogPkg(t, 0x28, "Acme - VST - Acid").NativeFolders("aarch64"); err == nil || !strings.Contains(err.Error(), "-mpc-aarch64.zip") {
		t.Fatalf("a 32-bit library on a 64-bit MPC must be refused, pointing to the other zip: %v", err)
	}
	if _, err := catalogPkg(t, 0xB7, "Acme - VST - Acid").NativeFolders("aarch64"); err != nil {
		t.Fatalf("an aarch64 library on a 64-bit MPC: %v", err)
	}
	if _, err := catalogPkg(t, 0x3E, "Acme - VST - Acid").NativeFolders("armv7l"); err == nil {
		t.Fatal("an x86-64 library must be refused")
	}
}

func TestUIDConversion(t *testing.T) {
	for in, want := range map[string]string{"41634964": "AcId", "41636964": "Acid", "0000002A": "0x0000002A", "1097025892": "AcId"} {
		if got, err := convertUID(in); err != nil || got != want {
			t.Errorf("convertUID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := convertUID("zz"); err == nil {
		t.Error("garbage uid accepted")
	}
}

func TestSDKZipGoesInAsItIs(t *testing.T) {
	p := sdkPkg(t)
	if !p.Native || p.Title != "Wobble" || len(p.NativeDirs) != 1 {
		t.Fatalf("package: %+v", p)
	}
	for arch, want := range map[string]string{"armv7l": "", "aarch64": ""} {
		fs, err := p.NativeFolders(arch)
		if err != nil || len(fs) != 1 || fs[0].Name != "Acme - VST - Wobble" {
			t.Fatalf("%s: %v %+v %s", arch, err, fs, want)
		}
		if _, ok := entryMap(fs[0])["INSTALL.txt"]; ok {
			t.Error("INSTALL.txt is beside the folder, not in it")
		}
	}
	// the Synths method cannot install it
	fd := newFakeDevice(t)
	writeSettings(fd, nil)
	if _, err := runJob(t, fd, Item{Pkg: p}); err == nil || !strings.Contains(err.Error(), VST2Dir) {
		t.Fatalf("the Synths method must refuse an SDK zip: %v", err)
	}
}

func TestVersionCheck(t *testing.T) {
	for v, want := range map[string]bool{"3.10.1": true, "3.10.1.21": true, "3.11.0": true, "4.0": true, "3.10.0.9": false, "3.9.1.2": false, "2.15.1": false, "": false, "unknown": false} {
		if got := versionAtLeast(v, nativeMinVersion); got != want {
			t.Errorf("versionAtLeast(%q) = %v", v, got)
		}
	}
}

// nativeDevice: a fake MPC on MPC OS 3.10.1 with a USB drive (no VST2 Plugins folder yet) and internal storage.
func nativeDevice(t *testing.T) (*fakeDevice, Config, string, string) {
	fd := newFakeDevice(t)
	writeSettings(fd, nil)
	os.WriteFile(filepath.Join(fd.dir, "mpc.version"), []byte("3.10.1.21\n"), 0o644)
	usb := filepath.Join(fd.dir, "Drives", "USB STICK")
	internal := filepath.Join(fd.dir, "Drives", "az01-internal-sd")
	sys := filepath.Join(fd.dir, "Drives", "az01-internal")
	for _, d := range []string{usb, internal, sys} {
		os.MkdirAll(d, 0o755)
	}
	mounts := filepath.Join(fd.dir, "mounts")
	os.WriteFile(mounts, []byte("/dev/root / ext4 rw,relatime 0 0\n"+
		"/dev/mmcblk0p8 "+sys+" ext4 rw,relatime 0 0\n"+
		"/dev/mmcblk0p9 "+internal+" ext4 rw,relatime 0 0\n"+
		"/dev/sda1 "+strings.ReplaceAll(usb, " ", `\040`)+" ext4 rw,relatime 0 0\n"), 0o644)
	cfg := fd.cfg()
	cfg.MountsFile = mounts
	return fd, cfg, usb, internal
}

func TestVST2InstallOnAnMPC3101(t *testing.T) {
	_, cfg, usb, internal := nativeDevice(t)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.Info.Native || d.Info.MPCVersion != "3.10.1.21" {
		t.Fatalf("an MPC on 3.10.1 loads VST2 Plugins itself: %+v", d.Info)
	}
	if len(d.Info.VDrives) != 2 || d.Info.VDrives[0].Path != usb || d.Info.VDrives[0].Internal || d.Info.VDrives[0].HasFolder ||
		d.Info.VDrives[1].Path != internal || !d.Info.VDrives[1].Internal || d.Info.VDrives[0].Label != "Drive USB STICK" {
		t.Fatalf("drives (the USB drive first, the internal storage last, MPC's own /data never): %+v", d.Info.VDrives)
	}
	if p := d.Info.nativeProblems(); len(p) != 0 {
		t.Fatal(p)
	}
	j := &Job{ID: "x", State: "running"}
	if err := RunNativeInstall(d, d.Info.VDrives[0], []Item{{Pkg: catalogPkg(t, 0x28, "Acme - VST - Acid")}}, t.TempDir(), j, nil); err == nil {
		t.Fatal("installing before the folder exists must be refused")
	}
	for _, bad := range []string{filepath.Join(filepath.Dir(usb), "az01-internal"), "/etc", usb + "/.."} {
		if err := d.MakeVST2(bad); err == nil {
			t.Fatalf("MakeVST2(%q) was accepted", bad)
		}
	}
	if err := d.MakeVST2(usb); err != nil {
		t.Fatal(err)
	}
	drive, _ := d.Info.vdrive(usb)
	if !drive.HasFolder {
		t.Fatalf("the rescan must see the new folder: %+v", d.Info.VDrives)
	}
	up := catalogPkg(t, 0x28, "Acme - VST - Acid")
	up.Handle = "h1"
	var told []string
	j = &Job{ID: "y", State: "running"}
	if err := RunNativeInstall(d, drive, []Item{{Pkg: up}, {Pkg: sdkPkg(t)}}, t.TempDir(), j, nil, InstallOpts{OnOK: func(p *Package) { told = append(told, p.Key) }}); err != nil {
		t.Fatal(err, strings.Join(j.Lines, "\n"))
	}
	dir := filepath.Join(usb, VST2Dir, "Acme - VST - Acid")
	for _, f := range []string{"plugin.json", "version.xml", "lib/acid.so", "Plugin Skins/TUI.json", "roms/readme.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "lib/acid.so")); st == nil || st.Mode().Perm()&0o111 == 0 {
		t.Error("the library must arrive executable")
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin-meta.xml")); err == nil {
		t.Error("the old installer's plugin-meta.xml must not be copied")
	}
	if _, err := os.Stat(filepath.Join(usb, VST2Dir, "Acme - VST - Wobble", "VST", "wobble-arm32.so")); err != nil {
		t.Errorf("the SDK zip's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(usb, ".mpc-installer-stage")); err == nil {
		t.Error("the staging folder must be cleaned up")
	}
	if len(told) != 2 || told[0] != "upload:h1" || len(j.okList()) != 2 {
		t.Errorf("both packages are reported as installed: %v %v", told, j.okList())
	}
	if len(d.Info.VPlugins) != 0 { // RunNativeInstall was given no refresh: rescan by hand
		t.Fatal("unexpected")
	}
	d.readInfo(d.Info.Host, d.Info.Fingerprint)
	if len(d.Info.VPlugins) != 2 || d.Info.VPlugins[0].Name != "Acid" || d.Info.VPlugins[0].Version != "2.1.0" {
		t.Fatalf("the scan reads plugin.json: %+v", d.Info.VPlugins)
	}
	// installing again replaces the folder (a file the new version no longer has is gone)
	os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("old"), 0o644)
	j = &Job{ID: "z", State: "running"}
	if err := RunNativeInstall(d, drive, []Item{{Pkg: catalogPkg(t, 0x28, "Acme - VST - Acid")}}, t.TempDir(), j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); err == nil {
		t.Error("an update replaces the whole folder")
	}
	// a plugin that does not fit fails alone: the other one still installs, and only it is reported
	told = nil
	j = &Job{ID: "w", State: "running"}
	err = RunNativeInstall(d, drive, []Item{{Pkg: catalogPkg(t, 0xB7, "Acme - VST - Acid")}}, t.TempDir(), j, nil, InstallOpts{OnOK: func(p *Package) { told = append(told, p.Key) }})
	if err == nil || len(told) != 0 {
		t.Fatalf("a 64-bit library on a 32-bit MPC: %v %v", err, told)
	}
	// removing deletes the folder, MPC is not stopped
	j = &Job{ID: "r", State: "running"}
	if err := RunNativeRemove(d, d.Info.VPlugins[:1], j, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("removed folder is still there")
	}
}

// A copy whose files do not measure what was sent is not left in place: the earlier version comes back.
func TestVST2CopyIsVerified(t *testing.T) {
	fd, cfg, usb, _ := nativeDevice(t)
	old := filepath.Join(usb, VST2Dir, "Acme - VST - Acid")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "plugin.json"), []byte(`{"name":"Acid","version":"1.0"}`), 0o644)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	drive, _ := d.Info.vdrive(usb)
	// from here on the device's wc reports every file as 1 byte, as if the copy came out truncated
	os.WriteFile(filepath.Join(fd.shims, "wc"), []byte("#!/bin/sh\necho \"1 $2\"\n"), 0o755)
	j := &Job{ID: "v", State: "running"}
	err = RunNativeInstall(d, drive, []Item{{Pkg: catalogPkg(t, 0x28, "Acme - VST - Acid")}}, t.TempDir(), j, nil)
	if err == nil || !strings.Contains(strings.Join(j.Lines, "\n"), "did not arrive intact") {
		t.Fatalf("a bad copy must fail: %v\n%s", err, strings.Join(j.Lines, "\n"))
	}
	b, _ := os.ReadFile(filepath.Join(old, "plugin.json"))
	if !strings.Contains(string(b), `"1.0"`) {
		t.Errorf("the earlier version must be back: %s", b)
	}
	if _, err := os.Stat(filepath.Join(usb, ".mpc-installer-stage")); err == nil {
		t.Error("the staging folder must be cleaned up")
	}
}

func TestRememberMe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, ok := loadRemembered(); ok {
		t.Fatal("nothing saved yet")
	}
	if err := saveRemembered(remembered{Host: "192.168.1.50", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	r, ok := loadRemembered()
	if !ok || r.Host != "192.168.1.50" || r.Password != "secret" {
		t.Fatalf("loaded: %+v %v", r, ok)
	}
	p, _ := rememberPath()
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the file must be readable by this user only: %v %v", st.Mode(), err)
	}
	forgetRemembered()
	if _, ok := loadRemembered(); ok {
		t.Fatal("forgotten details came back")
	}
}

// In the Synths method too, each package that installs is reported (the server drops an uploaded zip then).
func TestSynthsInstallReportsEachPackage(t *testing.T) {
	fd := newFakeDevice(t)
	writeSettings(fd, nil)
	a := installerPkg(t, "A-1", "a-plug", "me - VST - A", fakeInstaller("A", true, 0))
	a.Handle = "ha"
	b := installerPkg(t, "B-1", "b-plug", "me - VST - B", fakeInstaller("B", true, 3))
	b.Handle = "hb"
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var told []string
	j := &Job{ID: "x", State: "running"}
	err = RunInstall(d, d.Info.primaryRoot(), []Item{{Pkg: a}, {Pkg: b}}, t.TempDir(), j, nil, InstallOpts{OnOK: func(p *Package) { told = append(told, p.Key) }})
	if err == nil {
		t.Fatal("B's installer fails")
	}
	if strings.Join(told, ",") != "upload:ha" || strings.Contains(strings.Join(j.Lines, "\n"), "@@ok") {
		t.Fatalf("only A is reported, and the marker is not shown: %v\n%s", told, strings.Join(j.Lines, "\n"))
	}
}

// Through the API: a VST2 install drops the uploaded zip once it is in, the folder can be made, and Remember me saves only
// details that connected.
func TestServerVST2FlowAndRememberMe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := newHarness(t)
	fd, cfg, usb, _ := nativeDevice(t)
	h.fd, h.app.cfg = fd, cfg
	if code, _ := h.post("/api/connect", map[string]any{"host": "127.0.0.1", "password": "wrong", "remember": true}); code == 200 {
		t.Fatal("a wrong password connected")
	}
	if _, ok := loadRemembered(); ok {
		t.Fatal("details that did not connect must not be saved")
	}
	code, m := h.post("/api/connect", map[string]any{"host": "127.0.0.1", "password": "secret", "remember": true})
	if code != 200 {
		t.Fatal(m)
	}
	if dev := m["device"].(map[string]any); dev["native"] != true {
		t.Fatalf("an MPC on 3.10.1: %v", dev)
	}
	_, rb := h.do("GET", "/api/remember", nil, nil)
	if !strings.Contains(string(rb), `"host":"127.0.0.1"`) || !strings.Contains(string(rb), `"password":"secret"`) || !strings.Contains(string(rb), `"remember":true`) {
		t.Fatalf("remember: %s", rb)
	}
	zipBytes, _ := os.ReadFile(catalogPkg(t, 0x28, "Acme - VST - Acid").Path)
	handle := upload(t, h, map[string][]byte{"Acid.zip": zipBytes})["results"].([]any)[0].(map[string]any)["package"].(map[string]any)["handle"].(string)
	if code, _ := h.post("/api/install", map[string]any{"uploads": []string{handle}, "method": "vst2", "drive": usb, "confirm": true}); code != 400 {
		t.Fatal("no VST2 Plugins folder yet: refused")
	}
	if code, m := h.post("/api/mkvst2", map[string]any{"drive": usb}); code != 200 {
		t.Fatal(m)
	}
	if code, m := h.post("/api/install", map[string]any{"uploads": []string{handle}, "method": "vst2", "drive": usb, "confirm": true}); code != 200 {
		t.Fatal(m)
	}
	var job map[string]any
	for i := 0; i < 100; i++ {
		_, b := h.do("GET", "/api/job?since=0", nil, nil)
		jsonUnmarshal(b, &job)
		if job["state"] != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job["state"] != "done" || fmt.Sprint(job["ok"]) != "[upload:"+handle+"]" {
		t.Fatalf("job: %v", job)
	}
	_, st := h.do("GET", "/api/state", nil, nil)
	if !strings.Contains(string(st), `"uploads":[]`) {
		t.Errorf("an uploaded zip that installed is dropped: %s", st)
	}
	_, cat := h.do("GET", "/api/device", nil, nil)
	if !strings.Contains(string(cat), `"folder":"Acme - VST - Acid"`) {
		t.Errorf("the device list shows the VST2 plugin: %s", cat)
	}
	if code, _ := h.post("/api/connect", map[string]any{"host": "127.0.0.1", "password": "secret", "remember": false}); code != 200 {
		t.Fatal("connect")
	}
	if _, ok := loadRemembered(); ok {
		t.Fatal("unticking Remember me forgets")
	}
}
