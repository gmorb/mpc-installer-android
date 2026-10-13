package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zent struct {
	name string
	mode fs.FileMode // 0 = 0644; fs.ModeSymlink|0777 for a link
	body string
}

// writeZip makes a zip the way tools/release.py does: unix entries, modes in external attributes.
func writeZip(t *testing.T, dir string, ents []zent) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("t%d.zip", len(ents)))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.CreatorVersion = 3 << 8
		m := e.mode
		if m == 0 {
			m = 0o644
		}
		h.SetMode(m)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	zw.Close()
	f.Close()
	return path
}

func manifest(id, skin, ver string, mod func(*Manifest)) string {
	m := Manifest{Schema: 1, ID: id, Name: strings.ToUpper(id[:1]) + id[1:], Version: ver, Kind: "instrument", Layout: "portable", Skin: skin, Arch: "armv7", ParamCompat: 1}
	if mod != nil {
		mod(&m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// pluginEntries: the files of one release package under dir/ (dir "" = at the top of `top`).
func pluginEntries(top, dir, id, skin string, install string, mod func(*Manifest)) []zent {
	p := top + "/"
	if dir != "" {
		p += dir + "/"
	}
	return []zent{
		{p + "install.sh", 0o755, install},
		{p + "uninstall.sh", 0o755, "#!/bin/sh\n"},
		{p + "SHA256SUMS", 0, ""},
		{p + "plugin_list.awk", 0, ""},
		{p + "mpc-plugin.json", 0, manifest(id, skin, "1.2.3", mod)},
		{p + "portable/" + skin + "/plugin-meta.xml", 0, "<PLUGIN/>"},
		{p + "portable/" + skin + "/" + id + ".so", 0o755, "ELF"},
	}
}

const newInstall = "#!/bin/sh\nDEFER=0\n"
const oldInstall = "#!/bin/sh\nYES=0\n"

func TestOpenPackageValid(t *testing.T) {
	dir := t.TempDir()
	p, err := OpenPackage(writeZip(t, dir, pluginEntries("Acid-1.2.3", "", "acid", "me - VST - Acid", newInstall, nil)), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Acid" || p.Version != "1.2.3" || len(p.Plugins) != 1 || p.Bundle || !p.Defer || len(p.SHA256) != 64 {
		t.Fatalf("unexpected package: %+v", p)
	}
	old, err := OpenPackage(writeZip(t, t.TempDir(), pluginEntries("Acid-1.2.3", "", "acid", "me - VST - Acid", oldInstall, nil)), "upload")
	if err != nil || old.Defer {
		t.Fatalf("an installer without -n support must not be marked deferrable: %v %+v", err, old)
	}
}

func TestOpenPackageBundle(t *testing.T) {
	ents := []zent{{"Mono-1/install.sh", 0o755, "#!/bin/sh\nsh one/install.sh -y \"$@\"\nsh fx/install.sh -y \"$@\"\n"}, {"Mono-1/uninstall.sh", 0o755, ""}}
	ents = append(ents, pluginEntries("Mono-1", "one", "mono-one", "me - VST - One", newInstall, nil)...)
	ents = append(ents, pluginEntries("Mono-1", "fx", "mono-fx", "me - VST - FX", newInstall, nil)...)
	p, err := OpenPackage(writeZip(t, t.TempDir(), ents), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Bundle || len(p.Plugins) != 2 || p.Title != "Mono-fx + Mono-one" || !p.Defer {
		t.Fatalf("unexpected bundle: %+v", p)
	}
}

func TestOpenPackageRefusesBadZips(t *testing.T) {
	good := func() []zent { return pluginEntries("A-1", "", "acid", "me - VST - Acid", newInstall, nil) }
	cases := map[string][]zent{
		"traversal":      append(good(), zent{"A-1/../evil", 0, "x"}),
		"absolute":       {{"/etc/passwd", 0, "x"}},
		"two top dirs":   append(good(), zent{"B-1/file", 0, "x"}),
		"no manifest":    {{"A-1/install.sh", 0o755, newInstall}, {"A-1/uninstall.sh", 0o755, ""}},
		"link escapes":   append(good(), zent{"A-1/portable/x/link", fs.ModeSymlink | 0o777, "../../../../etc"}),
		"absolute link":  append(good(), zent{"A-1/portable/x/link", fs.ModeSymlink | 0o777, "/etc/passwd"}),
		"old layout":     pluginEntries("A-1", "", "acid", "s", newInstall, func(m *Manifest) { m.Layout = "" }),
		"wrong arch":     pluginEntries("A-1", "", "acid", "s", newInstall, func(m *Manifest) { m.Arch = "x86_64" }),
		"bad id":         pluginEntries("A-1", "", "acid", "s", newInstall, func(m *Manifest) { m.ID = "Not Valid" }),
		"skin has slash": pluginEntries("A-1", "", "acid", "s", newInstall, func(m *Manifest) { m.Skin = "a/b" }),
		"schema 2":       pluginEntries("A-1", "", "acid", "s", newInstall, func(m *Manifest) { m.Schema = 2 }),
	}
	for name, ents := range cases {
		if _, err := OpenPackage(writeZip(t, t.TempDir(), ents), "upload"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	notzip := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(notzip, []byte("hello"), 0o644)
	if _, err := OpenPackage(notzip, "upload"); err == nil {
		t.Error("a text file is not a zip")
	}
}

func TestLinkInsidePackageIsAllowed(t *testing.T) {
	ents := append(pluginEntries("A-1", "", "acid", "me - VST - Acid", newInstall, nil),
		zent{"A-1/portable/me - VST - Acid/bin/python3", fs.ModeSymlink | 0o777, "python3.11"},
		zent{"A-1/portable/me - VST - Acid/bin/python3.11", 0o755, "x"})
	if _, err := OpenPackage(writeZip(t, t.TempDir(), ents), "upload"); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTarKeepsModesAndSymlinksAndDropsTheTopFolder(t *testing.T) {
	ents := append(pluginEntries("A-1", "", "acid", "me - VST - Acid", newInstall, nil),
		zent{"A-1/portable/me - VST - Acid/bin/python3", fs.ModeSymlink | 0o777, "python3.11"},
		zent{"A-1/portable/me - VST - Acid/bin/python3.11", 0o755, "x"},
		zent{"A-1/portable/me - VST - Acid/data.txt", 0o644, "plain"})
	p, err := OpenPackage(writeZip(t, t.TempDir(), ents), "upload")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := p.WriteTar(&buf); err != nil {
		t.Fatal(err)
	}
	got := map[string]*tar.Header{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h
	}
	if _, ok := got["A-1/install.sh"]; ok {
		t.Error("the top folder must be dropped")
	}
	if h := got["install.sh"]; h == nil || h.Mode&0o111 == 0 {
		t.Errorf("install.sh lost its exec bit: %+v", h)
	}
	if h := got["portable/me - VST - Acid/bin/python3"]; h == nil || h.Typeflag != tar.TypeSymlink || h.Linkname != "python3.11" {
		t.Errorf("symlink lost: %+v", h)
	}
	if h := got["portable/me - VST - Acid/data.txt"]; h == nil || h.Mode&0o111 != 0 {
		t.Errorf("a plain file must not become executable: %+v", h)
	}
	if h := got["install.sh"]; h.Uid != 0 || h.Gid != 0 {
		t.Errorf("files must be owned by root on the device: %+v", h)
	}
}
