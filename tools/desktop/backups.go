package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Every install, removal and sync leaves a copy of MPC.settings named MPC.settings.bak-<what>-<yyyymmdd>-<hhmmss> next to it. They
// are small but never cleaned up. This lists them and deletes the older ones, never the newest, never anything not named like that.

type BackupInfo struct {
	Count   int    `json:"count"`
	Newest  string `json:"newest,omitempty"` // yyyy-mm-dd, from the file name when it has one
	Oldest  string `json:"oldest,omitempty"`
	TotalKB int64  `json:"totalKB"`
}

const (
	minKeep = 1
	maxKeep = 1000
)

var stampRe = regexp.MustCompile(`-(\d{4})(\d{2})(\d{2})-\d{6}$`)

func stampDate(name string) string {
	if m := stampRe.FindStringSubmatch(name); m != nil {
		return m[1] + "-" + m[2] + "-" + m[3]
	}
	return ""
}

func (d *Device) settingsPath() (string, error) {
	if d.Info.Settings == "" {
		return "", errors.New("MPC.settings was not found on the device")
	}
	return d.Info.Settings, nil
}

// Backups reports the backups of MPC.settings: how many, the dates of the newest and oldest, and the room they take.
func (d *Device) Backups() (BackupInfo, error) {
	set, err := d.settingsPath()
	if err != nil {
		return BackupInfo{}, err
	}
	script := fmt.Sprintf(`SET=%s
ls -t "$SET".bak-* 2>/dev/null | while IFS= read -r f; do echo "f=$f"; done
ls -l "$SET".bak-* 2>/dev/null | awk '{s += $5} END {print "total=" s + 0}'
true`, shQuote(set))
	var names []string
	var total int64
	var mu sync.Mutex
	code, rerr := d.Run(script, nil, func(l string) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(l, "f="):
			names = append(names, l[2:])
		case strings.HasPrefix(l, "total="):
			total, _ = strconv.ParseInt(l[6:], 10, 64)
		}
	})
	if rerr != nil || code != 0 {
		return BackupInfo{}, fmt.Errorf("cannot list the backups (status %d): %v", code, rerr)
	}
	bi := BackupInfo{Count: len(names), TotalKB: (total + 1023) / 1024}
	if len(names) > 0 {
		bi.Newest, bi.Oldest = stampDate(names[0]), stampDate(names[len(names)-1])
	}
	return bi, nil
}

// PruneBackups deletes every backup of MPC.settings except the newest `keep`; it returns how many it deleted.
func (d *Device) PruneBackups(keep int, onLine func(string)) (int, error) {
	if keep < minKeep || keep > maxKeep {
		return 0, fmt.Errorf("keep between %d and %d backups: the newest one is never deleted", minKeep, maxKeep)
	}
	set, err := d.settingsPath()
	if err != nil {
		return 0, err
	}
	script := fmt.Sprintf(`SET=%s
ls -t "$SET".bak-* 2>/dev/null | tail -n +%d | while IFS= read -r f; do
  if rm -f -- "$f"; then echo "deleted $f"; fi
done
true`, shQuote(set), keep+1)
	deleted := 0
	var mu sync.Mutex
	code, rerr := d.Run(script, nil, func(l string) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(l, "deleted ") {
			deleted++
		}
		if onLine != nil {
			onLine(l)
		}
	})
	if rerr != nil || code != 0 {
		return deleted, fmt.Errorf("the cleanup stopped (status %d): %v", code, rerr)
	}
	return deleted, nil
}
