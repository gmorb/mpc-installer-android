package main

// Stopping, starting and restarting MPC over a connection that may not survive it. On an MPC, stopping or restarting MPC can take
// the device's Wi-Fi down for a moment, and with it the SSH connection the app is using. A script that stops MPC, changes things
// and starts it again must not die halfway when that happens (MPC would stay stopped), and the app must not go on using the dead
// connection afterwards (the next step would hang, or a restart would never be sent).

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// How long the app waits for a script that lost its connection to finish, and for MPC to be running again after a restart.
// Variables, so the tests can shorten them.
var (
	finishWait  = 4 * time.Minute
	restartWait = 90 * time.Second
	pollPause   = 3 * time.Second
)

// svcLine picks MPC's service: acvs on stock firmware, inmusic-mpc on Hakai.
const svcLine = "SVC=acvs; systemctl cat acvs >/dev/null 2>&1 || ! systemctl cat inmusic-mpc >/dev/null 2>&1 || SVC=inmusic-mpc"

// RunToEnd runs a script that stops and starts MPC so that it runs to the end on the device whatever happens to the connection:
// it runs in the background with its output going to a log file on the device, which is streamed back while the connection
// lasts. When the connection drops, the app reconnects, waits for the script to finish, and passes on the lines it missed.
// note logs a line for people (the job log). It returns the script's exit status.
func (d *Device) RunToEnd(script string, onLine func(string), note func(string, ...any)) (int, error) {
	base := strings.TrimRight(d.cfg.RemoteTmp, "/") + "/mpc-installer-run-" + randHex(4)
	logf, mark := base+".log", base+".rc"
	q := shQuote
	wrapped := "trap '' HUP PIPE\nL=" + q(logf) + "; M=" + q(mark) + "; rm -f \"$M\"; : > \"$L\"\n" +
		"( " + script + "\n) > \"$L\" 2>&1 < /dev/null &\nP=$!\n" +
		"tail -f \"$L\" & TP=$!\n" +
		"wait $P; rc=$?\necho $rc > \"$M\"\nsleep 1; kill $TP 2>/dev/null\nexit $rc"
	shown := 0
	code, err := d.Run(wrapped, nil, func(l string) { shown++; onLine(l) })
	cleanup := func() { d.Run("rm -f "+q(logf)+" "+q(mark), nil, nil) }
	if err == nil {
		cleanup()
		return code, nil
	}
	if !connectionLost(err) || d.host == "" {
		return code, err
	}
	note("The connection to the MPC dropped while MPC was stopped or restarting (that can take the MPC's Wi-Fi down for a moment). The MPC carries on by itself: reconnecting and waiting for it to finish.")
	for until := time.Now().Add(finishWait); time.Now().Before(until); time.Sleep(pollPause) {
		var rc []string
		c, e := d.Run("cat "+q(mark), nil, func(l string) { rc = append(rc, l) })
		if e != nil || c != 0 || len(rc) == 0 {
			continue
		}
		n, perr := strconv.Atoi(strings.TrimSpace(rc[0]))
		if perr != nil {
			continue
		}
		note("Reconnected. The rest of the device's log:")
		seen := 0
		d.Run("cat "+q(logf), nil, func(l string) {
			if seen++; seen > shown {
				onLine(l)
			}
		})
		cleanup()
		return n, nil
	}
	return -1, fmt.Errorf("the connection to the MPC dropped while MPC was stopped and it could not be reached again for %d minutes (%v). Check on the MPC that it is running; restart it if not", int(finishWait/time.Minute), err)
}

// RestartMPC restarts MPC and waits until it is running again, reconnecting if the restart took the connection down, so whatever
// the app does next (reading the device again, the next install) runs on a live connection. Problems are logged, not returned:
// by then the plugins are in place either way.
func RestartMPC(dev *Device, j *Job) {
	j.log("Restarting MPC (unsaved work in the open project is lost)")
	var before []string
	dev.Run("pidof MPC", nil, func(l string) { before = append(before, l) })
	// --no-block: systemd restarts MPC even if this connection is gone a moment later
	code, err := dev.Run(svcLine+"; systemctl --no-block restart $SVC", nil, func(l string) { j.log("  %s", l) })
	if err != nil || code != 0 {
		j.log("MPC could not be restarted from here (status %d %v): restart it on the MPC.", code, err)
		return
	}
	old := strings.Join(before, " ")
	for until := time.Now().Add(restartWait); time.Now().Before(until); {
		time.Sleep(pollPause)
		var now []string
		c, e := dev.Run("pidof MPC", nil, func(l string) { now = append(now, l) })
		if e == nil && c == 0 && len(now) > 0 && strings.Join(now, " ") != old {
			j.log("MPC is running again.")
			return
		}
	}
	j.log("MPC has not come back within %d seconds. Check on the MPC; if it is running, reconnect here.", int(restartWait/time.Second))
}
