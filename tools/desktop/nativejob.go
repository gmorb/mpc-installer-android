package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// RunNativeInstall puts plugins into the VST2 Plugins folder of a drive, for MPC OS 3.10.1 and later (native.go). Each package is
// downloaded (catalog) or taken as dropped in, converted on the fly when it is in the catalog's older format, and copied plugin
// folder by plugin folder: unpacked into a staging folder on the same drive, moved into place (an older copy is replaced), and
// its file sizes checked. MPC keeps running; with opts.Restart it is restarted at the end so it lists them straight away.
// A plugin that fails does not stop the others: the job fails at the end, naming the ones that did not install.
func RunNativeInstall(dev *Device, drive VDrive, items []Item, workDir string, j *Job, refresh func(), opts ...InstallOpts) (err error) {
	var o InstallOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	defer func() {
		if refresh != nil {
			refresh()
		}
		j.finish(err)
	}()
	if p := dev.Info.nativeProblems(); len(p) > 0 {
		return errors.New("this device cannot be installed to: " + strings.Join(p, "; "))
	}
	if !drive.HasFolder {
		return fmt.Errorf("%s has no %s folder yet: create it first", drive.Label, VST2Dir)
	}
	var pkgs []*Package
	for _, it := range items {
		if it.Catalog == nil {
			it.Pkg.Key = "upload:" + it.Pkg.Handle
			pkgs = append(pkgs, it.Pkg)
			continue
		}
		c, err := it.Catalog.ForArch(dev.Info.Arch)
		if err != nil {
			return err
		}
		dest := filepath.Join(workDir, c.ID+"-"+c.Version+".zip")
		j.log("Downloading %s %s (%d KB)", c.Name, c.Version, c.Size/1024)
		next := int64(0)
		if err := Download(c, dest, func(done, total int64) {
			if total > 0 && done*4/total >= next && done*4/total > 0 {
				j.log("  %d%%", done*100/total)
				next = done*4/total + 1
			}
		}); err != nil {
			return err
		}
		p, err := OpenPackage(dest, "catalog")
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		p.Key = "catalog:" + c.ID
		j.log("%s %s downloaded and checked against the catalog's sha256.", c.Name, c.Version)
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return errors.New("nothing selected")
	}

	// convert everything first, so a package that cannot go in is found before anything is copied
	type planned struct {
		p       *Package
		folders []NFolder
	}
	var plan []planned
	var need int64
	for _, p := range pkgs {
		fs, err := p.NativeFolders(dev.Info.Arch)
		if err != nil {
			return err
		}
		for _, f := range fs {
			if f.Links > 0 && noSymlinkFS(drive.FS) {
				return fmt.Errorf("%s contains %d symbolic links, but %s is formatted %s, which cannot store them: use a drive formatted ext4", f.Title, f.Links, drive.Label, strings.ToUpper(drive.FS))
			}
			need += f.Unpacked
			if !p.Native {
				j.log("Converted %s %s for MPC OS 3.10.1: %s/%s", f.Title, f.Version, VST2Dir, f.Name)
			}
			for _, n := range f.Notes {
				j.log("  Note: %s", n)
			}
		}
		plan = append(plan, planned{p, fs})
	}
	if needKB := (need*11/10 + 1023) / 1024; drive.FreeKB > 0 && needKB > drive.FreeKB {
		return fmt.Errorf("%s has %d MB free, these plugins need about %d MB", drive.Label, drive.FreeKB/1024, (needKB+1023)/1024)
	}
	if drive.NoExec {
		j.log("Note: %s is mounted noexec right now. If a plugin does not load in MPC, restart the MPC with the drive in.", drive.Label)
	}
	j.log("Installing to %s (%s)", drive.Label, drive.Folder())

	total := 0
	for _, pl := range plan {
		total += len(pl.folders)
	}
	var failed []string
	for _, pl := range plan {
		okAll := true
		for _, f := range pl.folders {
			if err := copyNativeFolder(dev, drive, f, j); err != nil {
				j.log("%s did not install: %v", f.Title, err)
				failed = append(failed, f.Title)
				okAll = false
				continue
			}
			j.log("%s is in place (%d files checked).", f.Title, len(f.sizes()))
		}
		if okAll {
			o.done(j, pl.p)
		}
	}
	if o.Restart && len(failed) < total {
		j.log("Restarting MPC so it lists the new plugins (unsaved work in the open project is lost)")
		code, rerr := dev.Run(`SVC=acvs; systemctl cat acvs >/dev/null 2>&1 || ! systemctl cat inmusic-mpc >/dev/null 2>&1 || SVC=inmusic-mpc; systemctl restart $SVC`, nil, func(l string) { j.log("  %s", l) })
		if rerr != nil || code != 0 {
			j.log("MPC could not be restarted from here (status %d): restart it on the MPC to see the new plugins.", code)
		}
	}
	j.log("On the MPC, the plugins are under External Plugins in the plugin browser. Preferences > Enhancements (Beta) > Enable Enhancements and External Plugin Support must be on.")
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d plugins did not install (%s): see the log. They stay ticked so you can try again", len(failed), total, strings.Join(failed, ", "))
	}
	return nil
}

var wcRe = regexp.MustCompile(`^\s*(\d+)\s+\./(.*)$`)

// copyNativeFolder streams one plugin folder into <drive>/.mpc-installer-stage, moves it into VST2 Plugins (an older copy is set
// aside, and put back if the new one does not check out) and compares every file's size. A dropped connection during the copy
// is retried once after reconnecting: nothing in VST2 Plugins has changed at that point.
func copyNativeFolder(dev *Device, drive VDrive, f NFolder, j *Job) error {
	if f.Name == "" || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, "/\\\x00\n") {
		return fmt.Errorf("unsafe plugin folder name %q", f.Name)
	}
	vst, stage := drive.Folder(), strings.TrimRight(drive.Path, "/")+"/.mpc-installer-stage"
	q := shQuote
	unpack := "set -e; T=" + q(stage) + "; rm -rf \"$T\"; mkdir -p \"$T\"; tar -xof - -C \"$T\"; [ -f \"$T\"/" + q(f.Name) + "/plugin.json ]"
	var out []string
	copyOnce := func() (int, error) {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(f.WriteTar(pw)) }()
		out = nil
		code, err := dev.Run(unpack, pr, func(l string) { out = append(out, l) })
		pr.Close()
		return code, err
	}
	j.log("Copying %s (%d KB)", f.Name, (f.Unpacked+1023)/1024)
	code, err := copyOnce()
	if connectionLost(err) && dev.host != "" {
		j.log("The connection dropped while copying %s (%v). Reconnecting and copying it again.", f.Title, err)
		if cerr := dev.reconnect(); cerr != nil {
			return fmt.Errorf("the connection dropped and could not be made again (%v); keep this app in front during an install and check the Wi-Fi", cerr)
		}
		code, err = copyOnce()
	}
	if err != nil || code != 0 {
		dev.Run("rm -rf "+q(stage), nil, nil)
		if err != nil {
			return fmt.Errorf("copy failed: %v", err)
		}
		return fmt.Errorf("copy failed (status %d) %s", code, strings.Join(out, " "))
	}
	move := "set -e; V=" + q(vst) + "; T=" + q(stage) + "; N=" + q(f.Name) + "\n" +
		"rm -rf \"$T/.old\"; if [ -e \"$V/$N\" ]; then mv \"$V/$N\" \"$T/.old\"; fi\n" +
		"mv \"$T/$N\" \"$V/$N\"\n" +
		"cd \"$V/$N\" && find . -type f -exec wc -c {} \\;"
	got := map[string]int64{}
	out = nil
	code, err = dev.Run(move, nil, func(l string) {
		if m := wcRe.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			got[m[2]] = n
		} else {
			out = append(out, l)
		}
	})
	restore := "V=" + q(vst) + "; T=" + q(stage) + "; N=" + q(f.Name) + "; rm -rf \"$V/$N\"; if [ -e \"$T/.old\" ]; then mv \"$T/.old\" \"$V/$N\"; fi; rm -rf \"$T\""
	if err != nil || code != 0 {
		dev.Run(restore, nil, nil)
		return fmt.Errorf("moving it into %s failed (status %d) %v %s", VST2Dir, code, err, strings.Join(out, " "))
	}
	for rel, size := range f.sizes() {
		if got[rel] != size {
			dev.Run(restore, nil, nil)
			return fmt.Errorf("%s did not arrive intact (%d bytes, expected %d); the previous version, if any, is back", rel, got[rel], size)
		}
	}
	dev.Run("rm -rf "+q(stage)+"; sync", nil, nil)
	return nil
}

// RunNativeRemove deletes plugin folders from VST2 Plugins folders (MPC OS 3.10.1+). A folder there is the whole plugin, so it goes
// as a whole, Content (samples and the like it shipped with) included. MPC is not stopped: it forgets a plugin whose folder is gone.
func RunNativeRemove(dev *Device, plans []VPlugin, j *Job, refresh func()) (err error) {
	defer func() {
		if refresh != nil {
			refresh()
		}
		j.finish(err)
	}()
	if len(plans) == 0 {
		return errors.New("nothing selected")
	}
	for _, p := range plans {
		if p.Folder == "" || p.Folder == "." || p.Folder == ".." || strings.ContainsAny(p.Folder, "/\\\x00\n") || !strings.HasPrefix(p.Drive, "/") || strings.Contains(p.Drive, "..") {
			return fmt.Errorf("unsafe plugin folder %q", p.Folder)
		}
	}
	var failed []string
	for _, p := range plans {
		dir := strings.TrimRight(p.Drive, "/") + "/" + VST2Dir + "/" + p.Folder
		var out []string
		code, rerr := dev.Run("[ -f "+shQuote(dir+"/plugin.json")+" ] && rm -rf "+shQuote(dir)+" && sync", nil, func(l string) { out = append(out, l) })
		if rerr != nil || code != 0 {
			j.log("%s was not removed (status %d) %v %s", p.Folder, code, rerr, strings.Join(out, " "))
			failed = append(failed, p.Folder)
			continue
		}
		j.log("Removed %s", dir)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d could not be removed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}
