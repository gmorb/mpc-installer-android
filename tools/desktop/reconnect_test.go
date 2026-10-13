package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The connection drops while a package is copied (a phone cutting a background app's network, a VPN reconnecting): the app
// reconnects, copies again and installs, with MPC stopped and started once. A second drop gives up before MPC is touched.
func TestACopyThatLosesTheConnectionIsRetriedOnce(t *testing.T) {
	fd := newFakeDevice(t)
	writeSettings(fd, nil)
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a := installerPkg(t, "A-1", "a-plug", "me - VST - A", fakeInstaller("A", true, 0))
	a.Defer = true

	fd.dropOnCopy.Store(1)
	j := &Job{ID: "x", State: "running"}
	if err := RunInstall(d, d.Info.primaryRoot(), []Item{{Pkg: a}}, t.TempDir(), j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fd.cfg().SynthsDir, "me - VST - A")); err != nil {
		t.Fatal("not installed after the reconnect")
	}
	if _, log, _, _ := j.snapshot(0); !strings.Contains(strings.Join(log, "\n"), "Reconnecting") {
		t.Errorf("the log does not mention the reconnect: %q", log)
	}
	if got := fd.calls(); len(got) != 2 {
		t.Errorf("MPC calls %q, want one stop and one start", got)
	}

	fd.dropOnCopy.Store(2)
	os.Remove(fd.mpcLog)
	err = RunInstall(d, d.Info.primaryRoot(), []Item{{Pkg: a}}, t.TempDir(), &Job{ID: "y", State: "running"}, nil)
	if err == nil || !strings.Contains(err.Error(), "MPC was not stopped") {
		t.Fatalf("two drops must give up with a clear message: %v", err)
	}
	if got := fd.calls(); len(got) != 0 {
		t.Errorf("MPC was touched after the copy failed: %q", got)
	}
	if code, err := d.Run("true", nil, nil); err != nil || code != 0 { // and the device is usable again afterwards
		t.Errorf("the device connection did not recover: %d %v", code, err)
	}
}

// "Register every plugin folder" after an install: a folder copied in by hand (never registered) ends up in MPC's plugin
// list along with the new plugin, MPC.settings is backed up, and MPC is stopped and started once.
func TestInstallCanRegisterEveryPluginFolder(t *testing.T) {
	fd := newFakeDevice(t)
	settings := writeSettings(fd, nil)
	card := fd.addCard("SSD")
	plugin(t, card, "hand - VST - Copied", "copied.so", "abcd0001", "Copied")
	d, err := Dial("127.0.0.1", "secret", fd.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a := installerPkg(t, "A-1", "a-plug", "me - VST - A", fakeInstaller("A", true, 0))
	a.Defer = true
	j := &Job{ID: "x", State: "running"}
	if err := RunInstall(d, d.Info.primaryRoot(), []Item{{Pkg: a}}, t.TempDir(), j, nil, InstallOpts{Register: true}); err != nil {
		_, log, _, _ := j.snapshot(0)
		t.Fatalf("%v\n%s", err, strings.Join(log, "\n"))
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `uid="abcd0001"`) || !strings.Contains(string(b), card+"/hand - VST - Copied/copied.so") {
		t.Fatalf("the hand-copied folder was not registered:\n%s", b)
	}
	if got := fd.calls(); len(got) != 2 {
		t.Errorf("MPC calls %q, want one stop and one start", got)
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(settings), "MPC.settings*")); len(m) < 2 {
		t.Errorf("no backup of MPC.settings next to it: %q", m)
	}
}
