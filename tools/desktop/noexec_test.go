package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noexecCard makes a card whose mount the device reports as noexec, and returns the config that sees it.
func noexecCard(t *testing.T, fd *fakeDevice, name string) (string, Config) {
	t.Helper()
	card := fd.addCard(name)
	mounts := filepath.Join(fd.dir, "mounts")
	os.WriteFile(mounts, []byte("/dev/sda1 "+filepath.Dir(card)+" ext4 rw,nosuid,nodev,noexec,relatime 0 0\n"), 0o644)
	cfg := fd.cfg()
	cfg.MountsFile = mounts
	return card, cfg
}

// A drive the connection sees as noexec is installed to like any other, with a note in the log (its noexec state changes over
// time, and the app registers the folders itself).
func TestNoexecLocationInstallsWithANote(t *testing.T) {
	fd := newFakeDevice(t)
	writeSettings(fd, nil)
	card, cfg := noexecCard(t, fd, "SSD")
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	root, ok := d.Info.root(card)
	if !ok || !root.NoExec {
		t.Fatalf("the card is not seen as noexec: %+v", d.Info.Roots)
	}
	a := installerPkg(t, "A-1", "a-plug", "me - VST - A", fakeInstaller("A", true, 0))
	j := &Job{ID: "x", State: "running"}
	if err := RunInstall(d, root, []Item{{Pkg: a}}, t.TempDir(), j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(card, "me - VST - A")); err != nil {
		t.Fatal("the plugin was not installed on the noexec drive")
	}
	if _, log, _, _ := j.snapshot(0); !strings.Contains(strings.Join(log, "\n"), "mounted noexec right now") {
		t.Errorf("the log has no noexec note: %q", log)
	}
}

// A job refused before it logged anything (a noexec drive) must still report a list: the page read null.length and kept
// showing "Working…" for ever.
func TestAJobWithNoLinesReportsAnEmptyList(t *testing.T) {
	j := &Job{ID: "x", State: "running"}
	j.finish(errors.New("refused"))
	_, lines, _, _ := j.snapshot(0)
	if b, _ := json.Marshal(lines); string(b) != "[]" {
		t.Fatalf("lines = %s, want []", b)
	}
}
