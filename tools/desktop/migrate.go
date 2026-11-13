package main

// Moving plugins from the old Synths folders into a VST2 Plugins folder, for an MPC that was set up the old way and now runs
// MPC OS 3.10.1 or later. The plugin is already on the device, so it is converted there: its folder is copied into a staging
// folder on the target drive, gets the plugin.json and version.xml MPC 3.10.1 needs (written here from its plugin-meta.xml, the
// same way as a catalog zip is converted, native.go), loses the old installer's files, has every file's size compared with the
// original, and is moved into VST2 Plugins as "<Manufacturer> - VST - <Name>". The old folder is left alone: the page then asks
// whether to delete it (RunMigrateCleanup), which also takes it out of MPC's old plugin list.

import (
	"archive/tar"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MoveItem is one plugin folder in a Synths folder to move, with the version the app recorded for it ("" when unknown).
type MoveItem struct {
	DevPlugin
	Version string
}

func moveKey(root, folder string) string { return "move:" + root + "\t" + folder }

// RunMigrate converts and copies each plugin into the drive's VST2 Plugins folder. One that fails does not stop the others. Each one
// that made it is recorded in the job (Job.OK, "move:<root>\t<folder>"), so the page can offer to delete exactly those.
func RunMigrate(dev *Device, drive VDrive, items []MoveItem, j *Job, refresh func()) (err error) {
	defer func() {
		if refresh != nil {
			refresh()
		}
		j.finish(err)
	}()
	if p := dev.Info.nativeProblems(); len(p) > 0 {
		return errors.New("this device cannot be changed: " + strings.Join(p, "; "))
	}
	if !drive.HasFolder {
		return fmt.Errorf("%s has no %s folder yet: create it first", drive.Label, VST2Dir)
	}
	if len(items) == 0 {
		return errors.New("nothing selected")
	}
	j.log("Moving %d plugin(s) into %s. MPC keeps running; the old folders stay until you choose to delete them.", len(items), drive.Folder())
	var failed []string
	for _, it := range items {
		name, err := migrateOne(dev, drive, it, j)
		if err != nil {
			j.log("%s was not moved: %v", it.Folder, err)
			failed = append(failed, it.Folder)
			continue
		}
		j.log("%s is now in %s/%s (every file checked against the original).", it.Folder, VST2Dir, name)
		j.mu.Lock()
		j.OK = append(j.OK, moveKey(it.Root, it.Folder))
		j.mu.Unlock()
	}
	j.log("On the MPC, the plugins are under External Plugins in the plugin browser.")
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d could not be moved (%s): see the log. Their old folders are untouched", len(failed), len(items), strings.Join(failed, ", "))
	}
	return nil
}

func migrateOne(dev *Device, drive VDrive, it MoveItem, j *Job) (string, error) {
	if it.Folder == "" || it.Folder == "." || it.Folder == ".." || strings.ContainsAny(it.Folder, "/\\\x00\n") || !strings.HasPrefix(it.Root, "/") || strings.Contains(it.Root, "..") {
		return "", fmt.Errorf("unsafe plugin folder %q", it.Folder)
	}
	q := shQuote
	old := it.Root + "/" + it.Folder
	// what the conversion needs from the device: plugin-meta.xml, whether there is a version.xml, symbolic links
	var meta []string
	hasVX, links := false, 0
	code, err := dev.Run("D="+q(old)+`; [ -f "$D/plugin-meta.xml" ] || exit 3
[ -f "$D/version.xml" ] && echo '@@vx'
echo "@@links $(find "$D" -type l 2>/dev/null | wc -l)"
cat "$D/plugin-meta.xml"`, nil, func(l string) {
		switch {
		case l == "@@vx":
			hasVX = true
		case strings.HasPrefix(l, "@@links "):
			links, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "@@links ")))
		default:
			meta = append(meta, l)
		}
	})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.New("its plugin-meta.xml is missing")
	}
	m, err := parsePluginMeta([]byte(strings.Join(meta, "\n")))
	if err != nil {
		return "", err
	}
	if m.Shell {
		return "", fmt.Errorf("it is a shell plugin (several plugins in one binary), which MPC's %s folder does not support", VST2Dir)
	}
	if links > 0 && noSymlinkFS(drive.FS) {
		return "", fmt.Errorf("it contains %d symbolic links, but %s is formatted %s, which cannot store them: use a drive formatted ext4", links, drive.Label, strings.ToUpper(drive.FS))
	}
	rel := binaryRel(m.File, it.Folder)
	if c, err := cleanName(rel); err != nil || c != rel {
		return "", fmt.Errorf("plugin-meta.xml names an unsafe library path %q", rel)
	}
	var hexOut []string
	code, err = dev.Run("F="+q(old+"/"+rel)+`; [ -f "$F" ] || exit 3; head -c 20 "$F" | od -An -tx1 -v`, nil, func(l string) { hexOut = append(hexOut, l) })
	if err != nil {
		return "", err
	}
	if code == 3 {
		return "", fmt.Errorf("its library %s is missing", rel)
	}
	arch, key := devArchKey(dev.Info.Arch)
	if head, herr := hex.DecodeString(strings.Join(strings.Fields(strings.Join(hexOut, " ")), "")); herr == nil && len(head) >= 20 {
		got, err := elfArch(head)
		if err != nil {
			return "", fmt.Errorf("%s: %v", rel, err)
		}
		if got != arch {
			return "", fmt.Errorf("its library is built for %s, but this MPC (%s) needs %s", got, dev.Info.Arch, arch)
		}
	} else {
		j.log("  Could not read %s's header on the device; it was running from here, so it is taken to fit this MPC.", rel)
	}
	version := it.Version
	if version == "" {
		version = m.Version
	}
	if version == "" {
		version = "1.0.0"
	}
	pj, err := pluginJSONFor(m, version, "", key, rel)
	if err != nil {
		return "", err
	}
	name := legalFolderName(m.Manufacturer + " - VST - " + m.Name)
	if len(name) >= 128 || name == "" {
		return "", fmt.Errorf("the folder name %q does not suit MPC", name)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(n string, b []byte) {
		tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(b))})
		tw.Write(b)
	}
	add("plugin.json", pj)
	if !hasVX {
		add("version.xml", versionXML(version))
	}
	tw.Close()

	vst, stage := drive.Folder(), strings.TrimRight(drive.Path, "/")+"/.mpc-installer-stage"
	vars := "D=" + q(old) + "; V=" + q(vst) + "; T=" + q(stage) + "; N=" + q(name) + "; R=" + q(rel) + "\n"
	var out []string
	code, err = dev.Run(vars+`set -e
rm -rf "$T"; mkdir -p "$T/x" "$V"
tar -xof - -C "$T/x"
cp -a "$D" "$T/$N"
rm -f "$T/$N/plugin-meta.xml" "$T/$N/mpc-plugin.json"
cp "$T/x/"* "$T/$N/"
chmod 755 "$T/$N/$R"
(cd "$D" && find . -type f ! -path ./plugin-meta.xml ! -path ./mpc-plugin.json -exec wc -c {} \;) | sed 's/^/@@old /'
(cd "$T/$N" && find . -type f -exec wc -c {} \;) | sed 's/^/@@new /'`, &buf, func(l string) { out = append(out, l) })
	cleanup := func() { dev.Run("rm -rf "+q(stage), nil, nil) }
	if err != nil {
		cleanup()
		return "", err
	}
	if code != 0 {
		cleanup()
		return "", fmt.Errorf("copying it failed (status %d): %s", code, strings.Join(out, " "))
	}
	oldSizes, newSizes := map[string]string{}, map[string]string{}
	for _, l := range out {
		for prefix, mp := range map[string]map[string]string{"@@old ": oldSizes, "@@new ": newSizes} {
			if rest, ok := strings.CutPrefix(l, prefix); ok {
				if f := wcRe.FindStringSubmatch(rest); f != nil {
					mp[f[2]] = f[1]
				}
			}
		}
	}
	if len(oldSizes) == 0 {
		cleanup()
		return "", errors.New("could not list its files")
	}
	for f, sz := range oldSizes {
		if f == "version.xml" && !hasVX {
			continue
		}
		if newSizes[f] != sz {
			cleanup()
			return "", fmt.Errorf("%s did not copy intact (%s bytes, the original has %s)", f, newSizes[f], sz)
		}
	}
	if _, ok := newSizes["plugin.json"]; !ok {
		cleanup()
		return "", errors.New("plugin.json did not arrive")
	}
	code, err = dev.Run(vars+`set -e
rm -rf "$T/.old"; if [ -e "$V/$N" ]; then mv "$V/$N" "$T/.old"; fi
mv "$T/$N" "$V/$N"
rm -rf "$T"; sync`, nil, func(l string) { out = append(out, l) })
	if err != nil || code != 0 {
		dev.Run(vars+`if [ ! -e "$V/$N" ] && [ -e "$T/.old" ]; then mv "$T/.old" "$V/$N"; fi; rm -rf "$T"`, nil, nil)
		return "", fmt.Errorf("moving it into %s failed (status %d) %v", VST2Dir, code, err)
	}
	return name, nil
}

// RunMigrateCleanup deletes old Synths copies after they were moved. Where the MPC has the old setup (MPC.settings, systemctl) the
// usual removal runs, which also takes them out of MPC's old plugin list with one stop and start of MPC; otherwise the folders are
// simply deleted. Nothing in them is kept: the moved copy in VST2 Plugins has every file.
func RunMigrateCleanup(dev *Device, plans []RemovePlan, j *Job, refresh func()) error {
	for i := range plans {
		plans[i].Keep = nil
	}
	if len(dev.Info.problems()) == 0 {
		return RunRemove(dev, plans, j, refresh)
	}
	var err error
	defer func() {
		if refresh != nil {
			refresh()
		}
		j.finish(err)
	}()
	var failed []string
	for _, p := range plans {
		if p.Folder == "" || p.Folder == "." || p.Folder == ".." || strings.ContainsAny(p.Folder, "/\\\x00\n") || !strings.HasPrefix(p.Root, "/") {
			failed = append(failed, p.Folder)
			continue
		}
		code, rerr := dev.Run("rm -rf "+shQuote(p.Root+"/"+p.Folder)+" && sync", nil, nil)
		if rerr != nil || code != 0 {
			failed = append(failed, p.Folder)
			continue
		}
		j.log("Removed %s/%s", p.Root, p.Folder)
	}
	if len(failed) > 0 {
		err = fmt.Errorf("could not delete: %s", strings.Join(failed, ", "))
	}
	return err
}
