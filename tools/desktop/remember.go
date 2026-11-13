package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Remember me: the MPC's address and password, saved only when the person ticks "Remember this MPC", so the form is filled in next
// time. They go in a file only this app's user can read (0600): on Android that is inside the app's private storage, which other
// apps cannot open; on a computer it is in the user's config folder. Unticking the box deletes the file.
type remembered struct {
	Host     string `json:"host"`
	Password string `json:"password"`
}

func rememberPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mpc-installer", "remember.json"), nil
}

func loadRemembered() (remembered, bool) {
	var r remembered
	p, err := rememberPath()
	if err != nil {
		return r, false
	}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &r) != nil || r.Host == "" {
		return remembered{}, false
	}
	return r, true
}

func saveRemembered(r remembered) error {
	p, err := rememberPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	tmp := p + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func forgetRemembered() {
	if p, err := rememberPath(); err == nil {
		os.Remove(p)
	}
}
