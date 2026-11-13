package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fastTimers shortens the connection and restart timings for a test.
func fastTimers(t *testing.T) {
	t.Helper()
	a, b, c, d, e, f, g, h := idleCheck, answerWait, watchEvery, reconnectFor, reconnectPause, finishWait, restartWait, pollPause
	idleCheck, answerWait, watchEvery, reconnectFor, reconnectPause = 0, 500*time.Millisecond, time.Second, 5*time.Second, 100*time.Millisecond
	finishWait, restartWait, pollPause = 10*time.Second, 5*time.Second, 100*time.Millisecond
	t.Cleanup(func() {
		idleCheck, answerWait, watchEvery, reconnectFor, reconnectPause, finishWait, restartWait, pollPause = a, b, c, d, e, f, g, h
	})
}

func jobLog(j *Job) string { _, l, _, _ := j.snapshot(0); return strings.Join(l, "\n") }

// The MPC restarts after a VST2 Plugins install and its Wi-Fi bounces: the connection the app holds stops answering without
// closing. The restart is still sent, the app notices the dead connection and reconnects, and the next install restarts MPC too.
func TestRestartAfterAnInstallSurvivesAStaleConnection(t *testing.T) {
	fastTimers(t)
	fd, cfg, usb, _ := nativeDevice(t)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.MakeVST2(usb); err != nil {
		t.Fatal(err)
	}
	drive, _ := d.Info.vdrive(usb)
	stall := "--no-block restart"
	for round := 1; round <= 2; round++ {
		fd.stallAfter.Store(&stall)
		j := &Job{ID: "x", State: "running"}
		done := make(chan error, 1)
		go func() {
			done <- RunNativeInstall(d, drive, []Item{{Pkg: catalogPkg(t, 0x28, "Acme - VST - Acid")}}, t.TempDir(), j, func() {
				d.readInfo(d.Info.Host, d.Info.Fingerprint)
			}, InstallOpts{Restart: true})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err, jobLog(j))
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("round %d hangs on the stale connection:\n%s", round, jobLog(j))
		}
		if !strings.Contains(jobLog(j), "MPC is running again") {
			t.Errorf("round %d: the app did not see MPC come back:\n%s", round, jobLog(j))
		}
		b, _ := os.ReadFile(fd.mpcLog)
		if got := strings.Count(string(b), "restart acvs"); got != round {
			t.Fatalf("round %d: %d restarts sent, want %d (%q)", round, got, round, b)
		}
	}
	if code, err := d.Run("true", nil, nil); err != nil || code != 0 {
		t.Errorf("the connection is not usable afterwards: %d %v", code, err)
	}
}

// Removing from VST2 Plugins restarts MPC when asked to, and only then.
func TestVST2RemoveCanRestartMPC(t *testing.T) {
	fastTimers(t)
	fd, cfg, usb, _ := nativeDevice(t)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.MakeVST2(usb)
	drive, _ := d.Info.vdrive(usb)
	if err := RunNativeInstall(d, drive, []Item{{Pkg: catalogPkg(t, 0x28, "Acme - VST - Acid")}}, t.TempDir(), &Job{ID: "i", State: "running"}, func() {
		d.readInfo(d.Info.Host, d.Info.Fingerprint)
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fd.mpcLog); strings.Contains(string(b), "restart") {
		t.Fatalf("restarted without being asked: %q", b)
	}
	j := &Job{ID: "r", State: "running"}
	if err := RunNativeRemove(d, d.Info.VPlugins[:1], j, nil, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fd.mpcLog); strings.Count(string(b), "restart acvs") != 1 || !strings.Contains(jobLog(j), "MPC is running again") {
		t.Fatalf("removing with restart: %q\n%s", b, jobLog(j))
	}
}

// Stopping MPC takes the MPC's Wi-Fi down and the connection with it, in the middle of a removal the old way (stop MPC, edit
// its settings, start it). The script still runs to the end on the device, so MPC is started again, and the app reconnects,
// waits for it, reports the rest of the log and the right result.
func TestAScriptThatStopsMPCFinishesWhenTheConnectionDrops(t *testing.T) {
	fastTimers(t)
	fd := newFakeDevice(t)
	syn, settings := seed(t, fd)
	os.WriteFile(filepath.Join(fd.dir, "slowstop"), nil, 0o644)
	m := "systemctl stop $SVC"
	fd.dropDuring.Store(&m)
	j, err := runRemove(t, fd, RemovePlan{Root: syn, Folder: "me - VST - A", UID: "aaaa0001", ID: "a"})
	if err != nil {
		t.Fatal(err, jobLog(j))
	}
	if fd.dropDuring.Load() != nil {
		t.Fatal("the connection was never cut: the test did not test anything")
	}
	if got := strings.Join(fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("MPC must be started again after the drop: %s", got)
	}
	if _, err := os.Stat(filepath.Join(syn, "me - VST - A", "a.so")); err == nil {
		t.Error("the plugin was not removed")
	}
	if st, _ := os.ReadFile(settings); strings.Contains(string(st), "aaaa0001") {
		t.Error("its entry is still in MPC's settings")
	}
	log := jobLog(j)
	if !strings.Contains(log, "carries on by itself") || !strings.Contains(log, "Reconnected") {
		t.Errorf("the log does not explain the drop:\n%s", log)
	}
	if left, _ := filepath.Glob(filepath.Join(fd.dir, "tmp", "mpc-installer-run-*")); len(left) != 0 {
		t.Errorf("the run's log and status files are left behind: %q", left)
	}
}
