# mpc-installer: install MPC OS plugins from your computer

A small local app for people who would rather not type SSH commands. It starts a web page **on your own computer**, opens it in your
browser, and from there you connect to your device, tick plugins from the catalog and/or drop release zips (including the ones you
built yourself, like Monomodule and Machinedrum), and press Install. It is one file (about 8 MB) with nothing to set up, for Windows,
macOS and Linux.

It uses the same release zips and the same `install.sh` as every other route (`https://github.com/sd88me/mpc-vst-plugins/blob/desktop-v0.5.4/docs/RELEASING.md`), so there is still one install
path. What it adds: a friendly page, one copy of each zip over SSH (as a tar stream, so executable bits and symlinks survive), and
one MPC stop and start around the whole batch when the installers allow it.

## What it does, in order

1. **Connect:** SSH as root with your password, a key in `~/.ssh` (no passphrase), or neither on a device whose root has no password. It reads the device (32-bit ARM or Gen2 aarch64: Gen2 gets the catalog's aarch64 zips; `tar`?
   `systemctl`? where is `MPC.settings`?) and refuses one that is not an MPC OS device. The device's key fingerprint is shown; nothing
   about the device is saved.
   The device scan lists every writable `Synths` location (the internal drive, and `/media/*/Synths` for cards and drives; read-only mounts such as
   MPC's own content folder are skipped, and the same storage reached by two paths is listed once; the internal drive's `Synths` folder is created at connect when it is missing, 2026-10-11).
   **Where to install (2026-10-13, offline):** a `Synths` folder on a card, USB drive or SSD under `/media` is the default. When there is
   none, Install stays off and the page asks you to make one: every writable drive under `/media` (its own mount point, not MPC OS's
   `az01-*`/`acvs-*`) without a `Synths` folder gets a **Create Synths folder** button. The internal drive is only offered behind
   "Use the MPC's internal drive instead (not recommended)", a note about wear on its built-in flash (eMMC) and an "I understand" tick;
   the server refuses an install that names no location instead of falling back to it. A drive that cannot store symbolic links
   (FAT, exFAT, NTFS) is refused for a package that needs them, and so is a drive without room. A drive mounted `noexec` gets a note,
   not a refusal (the same drive is mounted with and without it at different times, user report 2026-10-13); the drive exec patch is
   not offered by this app (`hiddenPatches`). **Register every plugin folder** is ticked by default: the app's own `sync.sh`
   runs while MPC is stopped for the batch, so nothing has to be on the device and MPC restarts once. When the SSH connection drops
   during a copy (a phone cutting a background app's network, a VPN reconnecting), the app reconnects (checking the device key is
   unchanged) and copies once more; nothing is installed and MPC is not stopped before every copy is done. The page has a dark theme
   that follows the system and a Theme button (auto, dark, light); the catalog list shows each plugin's `screenshot` as a banner, with
   a Banners switch to turn them off (remembered in the browser).
2. **Choose:** the catalog's newest stable release of every downloadable plugin, plus any zips you drop in. Tabs for Instruments, MIDI (generators and sequencers, from the catalog's `role: "midi"`), Effects, Addins and All (everything together) (2026-10-10, like the catalog page; the counts follow the search, so you can see which tab holds a match), a search box, a developer filter, a sort, and a "show" filter (not on the device, on the device, updates available, only the ones you ticked) keep a
   long list manageable; what you ticked stays ticked while you filter or change tab, and a bar at the bottom shows the count and an Install button.
   Versions installed by this app or `mpc-store.sh` are read from `<Synths>/.mpc-store` to flag updates. A zip is checked before
   it is accepted (one folder, the manifest, no paths that leave it, links that stay inside, no more than 2 GB unpacked).
3. **Install:** catalog downloads are checked against the catalog's sha256 first (a mismatch installs nothing). Then, after you confirm
   (save your project: MPC restarts), it copies every package to a private folder in the device's `/tmp`, stops MPC once, runs each
   package's `install.sh -y -n`, starts MPC once and removes the copies. An installer that predates `-n` runs first with its own
   restart. MPC is started again even if something fails, and nothing after the failing plugin is installed.

4. **Remove** (step 4 on the page): lists the plugin folders on the device. A plugin the app can identify (from the catalog, or from a
   zip you dropped in this session) can be ticked and removed: MPC is stopped once, `MPC.settings` is backed up and the plugin's entry is
   taken out (the edit is checked before anything is deleted), then its folder is deleted **except your own files** (the manifest's
   `user_data`: ROMs, kits, dumps), MPC is started again. A plugin it cannot identify is listed but not removable, because it cannot tell
   which files in it are yours: drop its release zip to manage it, or remove it by hand. The plugin-list edit uses
   `plugin_list.awk`, a copy of `tools/release/plugin_list.awk` embedded in the binary (a test fails if the two differ: copy it again).

**Something missing from the plugin list?** (collapsed, unnumbered, with a count when something is waiting): the plugin folders in any location that are not in MPC's
   plugin list (copied in by hand, or dropped by MPC because a card was out at startup), and entries whose plugin file is gone. It runs the
   embedded `sync.sh` (a test keeps it identical to `tools/release/sync.sh`) between one MPC stop and start; everything else in the list stays.
5. **Clean up old backups** (step 5, collapsed): every install, removal and sync leaves a copy of `MPC.settings` named
   `MPC.settings.bak-<what>-<date>` that nothing deletes. The page counts them and deletes all but the newest N (default 10, at least 1:
   the newest is never deleted, and only files named like that are touched). MPC is not stopped. `mpc-store.sh prune [--keep N]` does the
   same on the device.
The page no longer has a patches step (removed 2026-10-13: the 16-pad layout and button remap change Akai's own program and are not for a phone app). The server side (`patches.go`, `/api/patches`, `/api/patch/run`, `patches_test.go`) is kept; `https://github.com/sd88me/mpc-vst-plugins/blob/desktop-v0.5.4/docs/PATCHES.md` describes it. Pressing Install scrolls the page to the progress log.

**Addins** (catalog kind `addin`: libraries MPC loads when it starts, `https://github.com/sd88me/mpc-vst-plugins/blob/desktop-v0.5.4/docs/ADDINS.md`) go through the same steps. A catalog addin or a
dropped addin zip installs to `/data/mpc-addins/<id>` with its own `install.sh -y -n`, inside the same single MPC stop and start, whatever
location step 3 picked. Step 4 lists the addins found there after the plugins; one installed by its `install.sh` carries its own `uninstall.sh`,
which removal runs (it takes the addin out of MPC's `LD_PRELOAD` and deletes its folder; `MPC.settings` is not touched). A folder
without one is listed but not removable here.

The catalog says, for every version, whether its installer understands `-n` (`defer` in `catalog.json`, column 14 of `catalog.tsv`).
The page uses that to state the exact number of MPC restarts before you confirm, and marks releases whose older installer restarts MPC by
itself. A zip you drop in is inspected directly.

**MPC OS badge and warnings.** The catalog also says which MPC OS generations a version works on (`os_compat`, `os_compat_why` and `max_glibc` in
`catalog.json`; columns 15 and 16 of `catalog.tsv`; https://github.com/sd88me/mpc-vst-plugins/blob/desktop-v0.5.4/docs/OS2_SKINS.md). MPC OS 3.x is the standard, so the list shows only one
badge, "MPC OS 2.x tested", on a 2.x-capable version with a 2.x device test (`tested[].firmware` 2.x; 2026-10-12, as on the catalog site). Connecting
reads the device's glibc (`libc` in the device info: run the libc for its version, or take it from `libc-2.33.so`; nothing is guessed when
neither works). Against that glibc the list adds a note per plugin, and the install dialog repeats it: a plugin that needs a newer glibc than
the device has "will not load" (the button becomes *Install anyway*), and a 3.x-only plugin on a device below glibc 2.34 (MPC OS 2.x) "may
show an empty touchscreen page". It warns and never blocks. Tests: `catalog_test.go`, `device_test.go` (`TestDialReadsTheDevicesGlibc`) and
`ui_test/ui_os.py` (a browser test with the API stubbed). `tools/mpc-store.sh` prints the same notes before its confirmation.

What was installed is written to `<Synths>/.mpc-store` on the device, so `mpc-store.sh update` (the on-device script) knows about it.

## Safety

- The page is served on `127.0.0.1` only, behind a random token in the link the app prints. Every request must carry the token, the
  right `Host` and (for posts) the right `Origin`, and the page has a strict Content-Security-Policy, so other computers and other web
  pages cannot drive it.
- The password is used for that one connection and never written down. There is no telemetry.
- Only https downloads are accepted, and only what the catalog lists.

## Run it

Download the archive for your system from the releases, unpack it and start `mpc-installer` (double-click it, or run it in a
terminal). It prints a link and opens it in your browser. `--no-browser` only prints the link; `--port`, `--catalog` and `--version`
exist too.

The first run is unsigned, so the system warns you once:

- **Windows:** "Windows protected your PC": click *More info*, then *Run anyway*.
- **macOS:** it will not open on a double-click. Right-click the file, choose *Open*, then *Open* again (or run
  `xattr -d com.apple.quarantine mpc-installer` in a terminal).
- **Linux:** `chmod +x mpc-installer` if needed.
- **Android:** install `mpc-installer-<version>-android.apk` (allow your browser or file manager to install apps when it asks). The
  app runs this same program on the phone and shows its page; the phone must be on the MPC's Wi-Fi. `tools/android/README.md`.

## Build and test

Go 1.26 or newer, one dependency (`golang.org/x/crypto`):

```
cd tools/desktop
go test -race ./...                       # includes a fake SSH device that runs the whole install in-process
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=1.0.0" -o mpc-installer .
```

A browser test of the page against a stand-in device (sshd in a container) lives in `ui_test/`; see its header. The release workflow
is `.github/workflows/desktop.yml` (Actions, "Desktop installer", Run workflow: builds Windows, macOS and Linux binaries into a
draft release, plus the Android APK when the signing secrets are set; install it, try it, then publish). The Android
app is built by `tools/android/build_apk.sh`. `dns.go` is for Android: with `MPC_DNS` set and no `/etc/resolv.conf`, lookups go to
those servers (2026-10-13, offline).
