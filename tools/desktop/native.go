package main

// MPC OS 3.10.1 and later load third-party VST2 plugins themselves, from a folder named "VST2 Plugins" at the root of any drive
// (inMusic's MPC Plugin Builder SDK, docs/plugin-folder-format.md: https://github.com/inmusic-software/mpc-plugin-builder-sdk).
// A plugin there is one self-contained folder "<Manufacturer> - VST - <Name>" with a plugin.json (and optionally a version.xml)
// that says what it is and where its binaries are. Nothing else is needed on the device: no MPC.settings entry, no install
// script, no restart of MPC while it runs.
//
// The catalog's release zips are made for the older way (a Synths folder, plugin-meta.xml, install.sh). This file converts them on
// the fly, the way the SDK's tools/legacy_convert.py does: the plugin's folder (portable/<skin>/) keeps its files, gains a
// plugin.json and a version.xml, loses the old installer's plugin-meta.xml and mpc-plugin.json, and is copied straight into
// "<drive>/VST2 Plugins/<Manufacturer> - VST - <Name>". Zips already in the new format (made with the SDK) go in as they are.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// VST2Dir is the folder MPC OS 3.10.1+ looks in, at the root of each drive.
const VST2Dir = "VST2 Plugins"

// nativeMinVersion: the first MPC OS that loads plugins from VST2 Plugins.
var nativeMinVersion = []int{3, 10, 1}

// VDrive is a drive the plugin folder can go on: a card, USB drive or SSD under /media, or the internal storage (/nvme on units
// that have one, the internal SD area on the others).
type VDrive struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	FS        string `json:"fs"`
	FreeKB    int64  `json:"freeKB"`
	NoExec    bool   `json:"noExec"`
	HasFolder bool   `json:"hasFolder"` // it has a VST2 Plugins folder
	Internal  bool   `json:"internal"`  // the MPC's built-in storage: offered last, behind the same warning as the internal Synths folder
}

// Folder is the drive's VST2 Plugins folder.
func (d VDrive) Folder() string { return strings.TrimRight(d.Path, "/") + "/" + VST2Dir }

// VPlugin is a plugin folder found in a drive's VST2 Plugins folder.
type VPlugin struct {
	Drive   string `json:"drive"`
	Folder  string `json:"folder"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// versionAtLeast compares dotted versions ("3.10.1.2" against 3.10.1); anything unreadable is not new enough.
func versionAtLeast(v string, min []int) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	parts := regexp.MustCompile(`\d+`).FindAllString(v, -1)
	if len(parts) == 0 {
		return false
	}
	for i, m := range min {
		n := 0
		if i < len(parts) {
			n, _ = strconv.Atoi(parts[i])
		}
		if n != m {
			return n > m
		}
	}
	return true
}

func vdriveLabel(p string) string {
	n := path.Base(p)
	switch {
	case p == "/nvme":
		return "Internal storage (NVMe)"
	case strings.HasPrefix(n, "az01-internal"):
		return "Internal storage"
	}
	return "Drive " + n
}

func vdriveInternal(p string) bool {
	n := path.Base(p)
	return p == "/nvme" || strings.HasPrefix(n, "az01-internal") || strings.HasPrefix(n, "nvme")
}

// nativeProblems lists why a VST2 Plugins install must not go ahead (empty when it is fine). Unlike the Synths install it needs
// no MPC.settings and no systemctl: MPC picks the folder up itself.
func (i DeviceInfo) nativeProblems() []string {
	var p []string
	if i.UID != "0" {
		p = append(p, "you are not logged in as root")
	}
	if !strings.HasPrefix(i.Arch, "armv7") && i.Arch != "aarch64" {
		p = append(p, "this is "+i.Arch+", not a 32-bit ARM or aarch64 MPC OS device")
	}
	if !i.Tar {
		p = append(p, "the device has no tar")
	}
	return p
}

func (i DeviceInfo) vdrive(p string) (VDrive, bool) {
	for _, d := range i.VDrives {
		if d.Path == p {
			return d, true
		}
	}
	return VDrive{}, false
}

// MakeVST2 creates the VST2 Plugins folder at the top of a drive the last scan listed in Info.VDrives, then scans again. Only
// listed drives are accepted, so the path cannot be anything the user typed.
func (d *Device) MakeVST2(drive string) error {
	dr, ok := d.Info.vdrive(drive)
	if !ok {
		return fmt.Errorf("%s is not a drive the app can add a %s folder to (connect again to rescan)", drive, VST2Dir)
	}
	var out []string
	code, err := d.Run("mkdir -p "+shQuote(dr.Folder())+" && sync", nil, func(l string) { out = append(out, l) })
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("could not create %s: %s", dr.Folder(), strings.TrimSpace(strings.Join(out, " ")))
	}
	return d.readInfo(d.Info.Host, d.Info.Fingerprint)
}

// ---- conversion ------------------------------------------------------------------------------------------------------------

const illegalFolderChars = `"#@,;:<>*^|?\/` // what MPC strips when it builds a plugin's folder name

func legalFolderName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(illegalFolderChars, r) {
			return -1
		}
		return r
	}, s)
}

// NEntry is one file, folder or link of a converted plugin folder, relative to the folder.
type NEntry struct {
	Rel  string
	Dir  bool
	Link string // symlink target
	Zip  string // the content: this entry of the package's zip (NFolder.ZipPath)
	Size int64  // its size
	Data []byte // or generated content (plugin.json, version.xml)
	Mode int64
}

func (e NEntry) size() int64 {
	if e.Zip != "" {
		return e.Size
	}
	return int64(len(e.Data))
}

// NFolder is one plugin folder ready for VST2 Plugins.
type NFolder struct {
	Name     string // "<Manufacturer> - VST - <Name>"
	ZipPath  string // the package the entries' content comes from
	Title    string
	Version  string
	Entries  []NEntry
	Links    int
	Unpacked int64
	Notes    []string
}

// sizes is what each regular file should measure on the device, to check the copy.
func (f NFolder) sizes() map[string]int64 {
	m := map[string]int64{}
	for _, e := range f.Entries {
		if !e.Dir && e.Link == "" {
			m[e.Rel] = e.size()
		}
	}
	return m
}

// WriteTar streams the folder as a tar whose top folder is f.Name.
func (f NFolder) WriteTar(w io.Writer) error {
	zr, err := zip.OpenReader(f.ZipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	byName := map[string]*zip.File{}
	for _, zf := range zr.File {
		byName[zf.Name] = zf
	}
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{Name: f.Name + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		return err
	}
	for _, e := range f.Entries {
		h := &tar.Header{Name: f.Name + "/" + e.Rel}
		switch {
		case e.Dir:
			h.Name += "/"
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		case e.Link != "":
			h.Typeflag, h.Linkname, h.Mode = tar.TypeSymlink, e.Link, 0o777
		default:
			h.Typeflag, h.Size, h.Mode = tar.TypeReg, e.size(), e.Mode
		}
		zf := byName[e.Zip]
		if e.Zip != "" && zf == nil {
			return fmt.Errorf("%s is no longer in %s", e.Zip, f.ZipPath)
		}
		if zf != nil {
			h.ModTime = zf.Modified
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if zf == nil {
			if _, err := tw.Write(e.Data); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

var junkNames = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

func junk(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if junkNames[seg] || strings.HasPrefix(seg, "._") || seg == "__MACOSX" {
			return true
		}
	}
	return false
}

// legacyMeta is what a plugin-meta.xml says.
type legacyMeta struct {
	Name, Manufacturer, UID, Category, File, Version string
	Instrument, Shell                                bool
	Inputs, Outputs                                  int
}

func parsePluginMeta(b []byte) (legacyMeta, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var found []xml.StartElement
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return legacyMeta{}, fmt.Errorf("cannot read plugin-meta.xml: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "PLUGIN" {
			found = append(found, se)
		}
	}
	if len(found) != 1 {
		return legacyMeta{}, fmt.Errorf("plugin-meta.xml should describe one plugin, it describes %d", len(found))
	}
	a := map[string]string{}
	for _, at := range found[0].Attr {
		a[at.Name.Local] = at.Value
	}
	num := func(k string, def int) int {
		if n, err := strconv.Atoi(strings.TrimSpace(a[k])); err == nil {
			return n
		}
		return def
	}
	m := legacyMeta{Name: a["name"], Manufacturer: a["manufacturer"], UID: a["uid"], Category: a["category"], File: a["file"],
		Version: a["version"], Instrument: a["isInstrument"] == "1", Shell: a["isShell"] == "1", Inputs: num("numInputs", 0), Outputs: num("numOutputs", 2)}
	if m.Outputs <= 0 {
		m.Outputs = 2
	}
	if m.Name == "" || m.Manufacturer == "" {
		return m, errors.New("plugin-meta.xml has no name or manufacturer")
	}
	return m, nil
}

// convertUID: the old files give the VST unique id as 8 hex digits ("41634964" is the code "AcId"); plugin.json prefers the
// four-character code, or "0x…" when the bytes are not printable.
func convertUID(t string) (string, error) {
	t = strings.TrimSpace(t)
	var v uint64
	var err error
	switch {
	case regexp.MustCompile(`^[0-9A-Fa-f]{8}$`).MatchString(t):
		v, err = strconv.ParseUint(t, 16, 32)
	case regexp.MustCompile(`^[0-9]+$`).MatchString(t):
		v, err = strconv.ParseUint(t, 10, 32)
	default:
		err = errors.New("not a number")
	}
	if err != nil {
		return "", fmt.Errorf("cannot read the plugin uid %q", t)
	}
	b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	for _, c := range b {
		if c <= 0x20 || c >= 0x7f {
			return fmt.Sprintf("0x%08X", v), nil
		}
	}
	return string(b), nil
}

// elfArch reads an ELF header: "arm32" or "arm64".
func elfArch(head []byte) (string, error) {
	if len(head) < 20 || string(head[:4]) != "\x7fELF" {
		return "", errors.New("not an ELF binary")
	}
	switch binary.LittleEndian.Uint16(head[18:20]) {
	case 0x28:
		return "arm32", nil
	case 0xB7:
		return "arm64", nil
	}
	return "", fmt.Errorf("not an ARM binary (machine 0x%x)", binary.LittleEndian.Uint16(head[18:20]))
}

func devArchKey(devArch string) (arch, key string) {
	if devArch == "aarch64" {
		return "arm64", "file64"
	}
	return "arm32", "file32"
}

func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

func padVersion(v string) string {
	nums := regexp.MustCompile(`\d+`).FindAllString(v, 4)
	if len(nums) == 0 {
		return "1.0.0.0"
	}
	for len(nums) < 4 {
		nums = append(nums, "0")
	}
	return strings.Join(nums, ".")
}

// entriesUnder lists the zip's entries below dir (a folder inside the zip), relative to it, skipping junk and the files in skip
// (at the folder's top level only).
func entriesUnder(zr *zip.Reader, dir string, skip map[string]bool) ([]NEntry, int, error) {
	var out []NEntry
	links := 0
	for _, f := range zr.File {
		n, err := cleanName(f.Name)
		if err != nil {
			return nil, 0, err
		}
		if !strings.HasPrefix(n, dir+"/") {
			continue
		}
		rel := strings.TrimPrefix(n, dir+"/")
		if rel == "" || junk(rel) || skip[rel] {
			continue
		}
		e := NEntry{Rel: rel, Zip: f.Name, Size: int64(f.UncompressedSize64), Mode: int64(f.Mode().Perm())}
		switch {
		case f.FileInfo().IsDir():
			e = NEntry{Rel: rel, Dir: true}
		case f.Mode()&fs.ModeSymlink != 0:
			t, err := readZipFile(f, 4096)
			if err != nil {
				return nil, 0, err
			}
			e = NEntry{Rel: rel, Link: string(t)}
			links++
		default:
			if e.Mode == 0 {
				e.Mode = 0o644
			}
			if h, err := readZipFile(f, 4); err == nil && string(h) == "\x7fELF" {
				e.Mode = 0o755
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, links, nil
}

// pluginJSON is the subset of plugin.json the app reads from a package already in the SDK's format.
type pluginJSON struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Version      string `json:"version"`
	Format       string `json:"format"`
	File         string `json:"file"`
	File32       string `json:"file32"`
	File64       string `json:"file64"`
}

func (p pluginJSON) binaryFor(key string) string {
	switch {
	case key == "file32" && p.File32 != "":
		return p.File32
	case key == "file64" && p.File64 != "":
		return p.File64
	case p.File32 == "" && p.File64 == "":
		return p.File
	}
	return ""
}

// NativeFolders turns a package into the plugin folders to copy into VST2 Plugins on a device whose `uname -m` is devArch. A
// catalog (Synths-style) package is converted; one made with the SDK is checked and passed through.
func (p *Package) NativeFolders(devArch string) ([]NFolder, error) {
	if p.Addin {
		return nil, fmt.Errorf("%s is an addin (a library MPC preloads on a modified MPC): it cannot go in %s. Install it with the Synths folder method", p.Title, VST2Dir)
	}
	zr, err := zip.OpenReader(p.Path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	names := map[string]*zip.File{}
	for _, f := range zr.File {
		if n, err := cleanName(f.Name); err == nil {
			names[n] = f
		}
	}
	arch, key := devArchKey(devArch)
	var out []NFolder
	if p.Native {
		for _, dir := range p.NativeDirs {
			b, err := readZipFile(names[dir+"/plugin.json"], 1<<20)
			if err != nil {
				return nil, err
			}
			var pj pluginJSON
			if err := json.Unmarshal(b, &pj); err != nil {
				return nil, fmt.Errorf("%s: bad plugin.json: %w", dir, err)
			}
			bin := pj.binaryFor(key)
			if bin == "" {
				return nil, fmt.Errorf("%s has no %s build in its plugin.json, which this MPC (%s) needs", dir, arch, devArch)
			}
			if c, err := cleanName(bin); err != nil || names[dir+"/"+c] == nil {
				return nil, fmt.Errorf("%s: the binary %q named in plugin.json is not in the zip", dir, bin)
			}
			ents, links, err := entriesUnder(&zr.Reader, dir, nil)
			if err != nil {
				return nil, err
			}
			f := NFolder{Name: dir, ZipPath: p.Path, Title: pj.Name, Version: pj.Version, Entries: ents, Links: links}
			for _, e := range ents {
				f.Unpacked += e.size()
			}
			out = append(out, f)
		}
		return out, nil
	}
	for i, m := range p.Plugins {
		base := p.Top
		if p.Bundle {
			base = p.BundleDirs[i]
		}
		dir := base + "/portable/" + m.Skin
		mb, err := readZipFile(names[dir+"/plugin-meta.xml"], 1<<20)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		meta, err := parsePluginMeta(mb)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		if meta.Shell {
			return nil, fmt.Errorf("%s is a shell plugin (several plugins in one binary), which MPC's %s folder does not support", m.Name, VST2Dir)
		}
		uid, err := convertUID(meta.UID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		// file="%payload-path%/<folder>/<binary path>": the binary's path inside the plugin folder is what follows the folder name
		file := strings.ReplaceAll(meta.File, `\`, "/")
		rel := path.Base(file)
		if i := strings.Index(file, "/"+m.Skin+"/"); i >= 0 {
			rel = file[i+len(m.Skin)+2:]
		}
		if names[dir+"/"+rel] == nil && m.So != "" {
			rel = m.So
		}
		if c, err := cleanName(rel); err != nil || names[dir+"/"+c] == nil {
			return nil, fmt.Errorf("%s: the plugin binary %q named in plugin-meta.xml is not in the zip", m.Name, rel)
		}
		head, err := readZipFile(names[dir+"/"+rel], 20)
		if err != nil {
			return nil, err
		}
		got, err := elfArch(head)
		if err != nil {
			return nil, fmt.Errorf("%s: %s %v", m.Name, rel, err)
		}
		if got != arch {
			return nil, fmt.Errorf("%s is built for %s, but this MPC (%s) needs %s: get the %s zip", m.Name, got, devArch, arch, otherZip(devArch))
		}
		folder := legalFolderName(meta.Manufacturer + " - VST - " + meta.Name)
		if len(folder) >= 128 {
			return nil, fmt.Errorf("%s: the folder name %q is too long for MPC", m.Name, folder)
		}
		ents, links, err := entriesUnder(&zr.Reader, dir, map[string]bool{"plugin-meta.xml": true, "mpc-plugin.json": true})
		if err != nil {
			return nil, err
		}
		version := m.Version
		if version == "" {
			version = meta.Version
		}
		if version == "" {
			version = "1.0.0"
		}
		kind, category := "effect", meta.Category
		if meta.Instrument {
			kind = "instrument"
		}
		if category == "" {
			category = map[string]string{"instrument": "Synth", "effect": "Effect"}[kind]
		}
		// key order as the SDK writes it
		var pj bytes.Buffer
		w := func(k string, v any, last bool) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(&pj, "  %q: %s", k, b)
			if !last {
				pj.WriteString(",")
			}
			pj.WriteString("\n")
		}
		pj.WriteString("{\n")
		w("name", meta.Name, false)
		w("manufacturer", meta.Manufacturer, false)
		w("version", version, false)
		w("format", "vst2", false)
		w("kind", kind, false)
		w("uid", uid, false)
		w("category", category, false)
		w("numInputs", meta.Inputs, false)
		w("numOutputs", meta.Outputs, false)
		w(key, rel, false)
		if m.About != "" {
			w("description", m.About, false)
		}
		w("convertedFrom", "community mpc-vst-plugins package (MPC Installer)", true)
		pj.WriteString("}\n")
		f := NFolder{Name: folder, ZipPath: p.Path, Title: meta.Name, Version: version, Links: links}
		hasVersionXML := false
		for _, e := range ents {
			if e.Rel == rel {
				e.Mode = 0o755
			}
			if e.Rel == "version.xml" {
				hasVersionXML = true
			}
			f.Entries = append(f.Entries, e)
		}
		f.Entries = append(f.Entries, NEntry{Rel: "plugin.json", Data: pj.Bytes(), Mode: 0o644})
		if !hasVersionXML {
			vx := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<plugincontent version=\"1.0\">\n    <version>" + padVersion(version) + "</version>\n</plugincontent>\n"
			f.Entries = append(f.Entries, NEntry{Rel: "version.xml", Data: []byte(vx), Mode: 0o644})
		}
		if m.Skin != folder {
			f.Notes = append(f.Notes, fmt.Sprintf("the package folder was named %q; the plugin reports %q, so that is the folder's name now", m.Skin, folder))
		}
		for _, e := range f.Entries {
			f.Unpacked += e.size()
		}
		out = append(out, f)
	}
	return out, nil
}
