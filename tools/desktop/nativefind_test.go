package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wobbleJSON = `{"name": "Wobble", "manufacturer": "Acme", "version": "1.0.0", "format": "vst2", "kind": "instrument", "uid": "AcWb", "numOutputs": 2, "file32": "VST/wobble-arm32.so", "file64": "VST/wobble-arm64.so"}`

// wobble returns the entries of the Wobble plugin's folder under dir ("" for the top of the zip), with plugin.json pj.
func wobble(dir, pj string, bins ...string) []zent {
	if len(bins) == 0 {
		bins = []string{"VST/wobble-arm32.so", "VST/wobble-arm64.so"}
	}
	j := func(rel string) string { return zjoin(dir, rel) }
	return []zent{{j("plugin.json"), 0, pj}, {j(bins[0]), 0o755, elf(0x28)}, {j(bins[1]), 0o755, elf(0xB7)},
		{j("Plugin Skins/TUI.json"), 0, "{}"}, {j("Content/kick.wav"), 0, "RIFF"}}
}

func openZip(t *testing.T, ents []zent) (*Package, error) {
	t.Helper()
	return OpenPackage(writeZip(t, t.TempDir(), ents), "upload")
}

func mustFolders(t *testing.T, ents []zent, arch string) (*Package, []NFolder) {
	t.Helper()
	p, err := openZip(t, ents)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := p.NativeFolders(arch)
	if err != nil {
		t.Fatal(err)
	}
	return p, fs
}

func pjOf(t *testing.T, f NFolder) map[string]any {
	t.Helper()
	e, ok := entryMap(f)["plugin.json"]
	if !ok {
		t.Fatal("no plugin.json")
	}
	if e.Data == nil {
		return nil // the zip's own, unchanged
	}
	var m map[string]any
	if err := json.Unmarshal(e.Data, &m); err != nil {
		t.Fatal(err, string(e.Data))
	}
	return m
}

func hasFix(p *Package, sub string) bool {
	for _, f := range p.Fixes {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

// The folder inside a release folder and a "VST2 Plugins" folder, with a Mac's leftovers and a readme around it.
func TestSDKZipFoundInsideOtherFolders(t *testing.T) {
	ents := wobble("Wobble-1.0.0/VST2 Plugins/Acme - VST - Wobble", wobbleJSON)
	ents = append(ents, zent{"Wobble-1.0.0/README.md", 0, "hi"}, zent{"__MACOSX/Wobble-1.0.0/._README.md", 0, "x"},
		zent{"Wobble-1.0.0/.DS_Store", 0, "x"}, zent{"Wobble-1.0.0/screenshot.png", 0, "png"})
	p, fs := mustFolders(t, ents, "aarch64")
	if len(fs) != 1 || fs[0].Name != "Acme - VST - Wobble" {
		t.Fatalf("%+v", fs)
	}
	m := entryMap(fs[0])
	for _, want := range []string{"plugin.json", "VST/wobble-arm64.so", "VST/wobble-arm32.so", "Plugin Skins/TUI.json", "Content/kick.wav"} {
		if _, ok := m[want]; !ok {
			t.Errorf("%s is missing", want)
		}
	}
	if len(m) != 8 { // the five files and VST, Plugin Skins, Content as folders, if listed; nothing from outside the folder
		for k := range m {
			if strings.Contains(k, "README") || strings.Contains(k, "png") || strings.Contains(k, "DS_Store") {
				t.Errorf("%s from outside the plugin folder got in", k)
			}
		}
	}
	if pjOf(t, fs[0]) != nil {
		t.Error("an intact plugin.json must be copied as it is")
	}
	if !hasFix(p, `inside "Wobble-1.0.0/VST2 Plugins/"`) {
		t.Errorf("fixes: %q", p.Fixes)
	}
}

// The plugin's files zipped without their folder, and a folder with the wrong name.
func TestSDKZipFolderMadeOrRenamed(t *testing.T) {
	p, fs := mustFolders(t, wobble("", wobbleJSON), "armv7l")
	if len(fs) != 1 || fs[0].Name != "Acme - VST - Wobble" || !hasFix(p, "were not in a folder") {
		t.Fatalf("flat: %+v %q", fs, p.Fixes)
	}
	if _, ok := entryMap(fs[0])["VST/wobble-arm32.so"]; !ok {
		t.Error("flat: the binary is missing")
	}
	p, fs = mustFolders(t, wobble("wobble-v1", wobbleJSON), "armv7l")
	if fs[0].Name != "Acme - VST - Wobble" || !hasFix(p, `folder "wobble-v1" is renamed`) {
		t.Fatalf("renamed: %+v %q", fs[0].Name, p.Fixes)
	}
	// characters MPC drops from names are dropped from the folder too
	pj := strings.Replace(wobbleJSON, `"Acme"`, `"Acme: Labs"`, 1)
	if _, fs = mustFolders(t, wobble("x", pj), "armv7l"); fs[0].Name != "Acme Labs - VST - Wobble" {
		t.Errorf("illegal characters: %q", fs[0].Name)
	}
}

// plugin.json names the binaries at paths where they are not: found by file name, or else by processor, and plugin.json corrected.
func TestSDKZipBinaryPathsRepaired(t *testing.T) {
	p, fs := mustFolders(t, wobble("Acme - VST - Wobble", wobbleJSON, "wobble-arm32.so", "bin/wobble-arm64.so"), "aarch64")
	got := pjOf(t, fs[0])
	if got["file32"] != "wobble-arm32.so" || got["file64"] != "bin/wobble-arm64.so" || got["uid"] != "AcWb" || got["format"] != "vst2" {
		t.Fatalf("by name: %v", got)
	}
	if !hasFix(p, `wrong place for its 64-bit build: now "bin/wobble-arm64.so"`) {
		t.Errorf("fixes: %q", p.Fixes)
	}
	// different names altogether: one library per processor
	_, fs = mustFolders(t, wobble("Acme - VST - Wobble", wobbleJSON, "lib/a.so", "lib/b.so"), "armv7l")
	if got := pjOf(t, fs[0]); got["file32"] != "lib/a.so" || got["file64"] != "lib/b.so" {
		t.Fatalf("by processor: %v", got)
	}
	// the key order and the other keys are kept
	e := entryMap(fs[0])["plugin.json"]
	if s := string(e.Data); strings.Index(s, `"name"`) > strings.Index(s, `"uid"`) || !strings.Contains(s, `"numOutputs": 2`) {
		t.Errorf("plugin.json rewritten badly:\n%s", s)
	}
}

// A plugin.json made before the SDK added "format" gets it; nothing else changes.
func TestSDKZipGetsFormat(t *testing.T) {
	old := strings.Replace(wobbleJSON, `"format": "vst2", `, "", 1)
	p, fs := mustFolders(t, wobble("Acme - VST - Wobble", old), "armv7l")
	e := entryMap(fs[0])["plugin.json"]
	s := string(e.Data)
	if !strings.Contains(s, `"version": "1.0.0",`+"\n"+`  "format": "vst2",`) || !hasFix(p, `"format": "vst2"`) {
		t.Fatalf("format not added after version:\n%s\n%q", s, p.Fixes)
	}
}

// A zip of an SDK project with its build output: the built folder is the plugin, not the project.
func TestSDKProjectZipUsesTheBuild(t *testing.T) {
	ents := []zent{{"Wobble/plugin.json", 0, wobbleJSON}, {"Wobble/src/wobble.cpp", 0, "//"}, {"Wobble/ui.json", 0, "{}"}}
	ents = append(ents, wobble("Wobble/build/Acme - VST - Wobble", wobbleJSON)...)
	p, fs := mustFolders(t, ents, "aarch64")
	if len(fs) != 1 || fs[0].Name != "Acme - VST - Wobble" {
		t.Fatalf("%+v", fs)
	}
	if _, ok := entryMap(fs[0])["src/wobble.cpp"]; ok {
		t.Error("the project's source got in")
	}
	if pjOf(t, fs[0]) != nil || len(p.NativeRoots) != 1 || p.NativeRoots[0].Dir != "Wobble/build/Acme - VST - Wobble" {
		t.Errorf("roots: %+v", p.NativeRoots)
	}
}

// Two plugins in one zip, one of them with a wrong path: both go in.
func TestSDKZipTwoPlugins(t *testing.T) {
	acid := strings.NewReplacer("Wobble", "Acid", "wobble", "acid", "AcWb", "AcAc").Replace(wobbleJSON)
	ents := append(wobble("pack/Acme - VST - Wobble", wobbleJSON), wobble("pack/Acid", acid, "acid32.so", "acid64.so")...)
	p, fs := mustFolders(t, ents, "aarch64")
	if len(fs) != 2 || fs[0].Name != "Acme - VST - Acid" || fs[1].Name != "Acme - VST - Wobble" || p.Title != "Acid + Wobble" {
		t.Fatalf("%+v %q", fs, p.Title)
	}
	if got := pjOf(t, fs[0]); got["file64"] != "acid64.so" {
		t.Errorf("acid: %v", got)
	}
}

// What cannot be fixed is said plainly.
func TestSDKZipProblemsExplained(t *testing.T) {
	// only a 32-bit build, on a 64-bit MPC
	only32 := strings.Replace(wobbleJSON, `, "file64": "VST/wobble-arm64.so"`, "", 1)
	p, err := openZip(t, []zent{{"W/plugin.json", 0, only32}, {"W/VST/wobble-arm32.so", 0o755, elf(0x28)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NativeFolders("aarch64"); err == nil || !strings.Contains(err.Error(), "no arm64 build") {
		t.Errorf("32-bit only on a 64-bit MPC: %v", err)
	}
	// file64 names a 32-bit library
	p, err = openZip(t, wobble("W", wobbleJSON, "VST/wobble-arm64.so", "VST/wobble-arm32.so"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NativeFolders("aarch64"); err == nil || !strings.Contains(err.Error(), "built for arm32") {
		t.Errorf("swapped libraries: %v", err)
	}
	// no binary at all
	if _, err := openZip(t, []zent{{"W/plugin.json", 0, wobbleJSON}, {"W/readme.txt", 0, "x"}}); err == nil || !strings.Contains(err.Error(), "not in the zip") {
		t.Errorf("no binary: %v", err)
	}
	// two copies of the same plugin
	if _, err := openZip(t, append(wobble("a/Acme - VST - Wobble", wobbleJSON), wobble("b/Acme - VST - Wobble", wobbleJSON)...)); err == nil || !strings.Contains(err.Error(), "two copies") {
		t.Errorf("two copies: %v", err)
	}
}

// A messy zip installs onto a 3.10.1 MPC in the right shape, the log saying what was put right.
func TestMessySDKZipInstalls(t *testing.T) {
	_, cfg, usb, _ := nativeDevice(t)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.MakeVST2(usb); err != nil {
		t.Fatal(err)
	}
	drive, _ := d.Info.vdrive(usb)
	old := strings.Replace(wobbleJSON, `"format": "vst2", `, "", 1)
	ents := append(wobble("", old, "wobble-arm32.so", "wobble-arm64.so"), zent{"__MACOSX/._plugin.json", 0, "x"})
	p, err := openZip(t, ents)
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{ID: "m", State: "running"}
	if err := RunNativeInstall(d, drive, []Item{{Pkg: p}}, t.TempDir(), j, nil); err != nil {
		t.Fatal(err, jobLog(j))
	}
	dir := filepath.Join(usb, VST2Dir, "Acme - VST - Wobble")
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pj map[string]any
	json.Unmarshal(b, &pj)
	if pj["file64"] != "wobble-arm64.so" || pj["format"] != "vst2" {
		t.Errorf("plugin.json on the MPC: %s", b)
	}
	for _, f := range []string{"wobble-arm64.so", "Content/kick.wav", "Plugin Skins/TUI.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s did not arrive", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "__MACOSX")); err == nil {
		t.Error("__MACOSX was copied")
	}
	if log := jobLog(j); !strings.Contains(log, "were not in a folder") || !strings.Contains(log, `"format": "vst2"`) {
		t.Errorf("the log does not say what was put right:\n%s", log)
	}
}
