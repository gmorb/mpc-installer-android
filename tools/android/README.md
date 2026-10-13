# MPC Installer for Android

Install, update and remove VST plugins on a modded Akai MPC or Force **from your phone**. No computer, no SSH commands: connect over
Wi-Fi, tick plugins from the catalog, press Install.

- **Full user manual (PDF, with pictures): [MPC-Installer-Android-Manual.pdf](MPC-Installer-Android-Manual.pdf)** (source: `manual/manual.html`)
- Works on Android 6.0 and later (built for Android 16, so Play Protect accepts it). 64-bit and 32-bit ARM phones, and x86_64 emulators.
- Installs to a card, USB drive or SSD under `/media` (it can create the `Synths` folder for you); the MPC's internal flash is
  hidden behind a wear warning. Registers the plugins with MPC by itself: nothing to put on the device.
- Also: remove plugins (your own files are kept), repair a plugin list that lost entries, clean up old settings backups, dark mode,
  plugin banners, and it reconnects and copies again if the phone cuts the connection mid-copy.
- Same program, same release zips and same `install.sh` as the desktop installer (`tools/desktop`); only the wrapper is new.

## Quick start

1. Download `mpc-installer-<version>-android.apk` from the project's releases and open it on the phone. Allow installing from your
   browser or Files app when Android asks.
2. Phone and MPC on the same Wi-Fi. Open **MPC Installer**, type the MPC's IP address and root password, tap **Connect**.
3. Tick plugins, choose the drive under **Install to** (tap **Create Synths folder** if it has none), tap **Install…**, confirm.
   Save your project first: MPC restarts. Keep the app open until it says Done.

Use at your own risk: plugins run as root on your device and can crash MPC or lose unsaved work. Unofficial; not affiliated with
Akai Professional.

# For developers

The MPC plugin installer (`tools/desktop`) on an Android phone or tablet. It is the same program: the same page, the same SSH
connection to the MPC, the same release zips and `install.sh`. Only the wrapper is new.

## How it works

- The Go installer is built for Android with no cgo and shipped inside the APK as `lib/<abi>/libmpcinstaller.so`. Android only lets
  an app run a program from its extracted native-library folder, so the manifest sets `extractNativeLibs="true"` and the program
  carries a `lib…so` name. arm64 is a real `GOOS=android` build; 32-bit ARM and x86_64 (emulators, Chromebooks) are static Linux
  builds, since Go needs cgo and the NDK for those on Android, and Android's kernel runs them the same way.
- `MainActivity` (the only Java file) starts it with `-no-browser`, reads the link it prints (`http://127.0.0.1:PORT/?t=TOKEN`) and
  shows that page full screen in a WebView. The page's "choose zips" opens the phone's file picker; links to guides open in the
  phone's browser. Back asks before quitting, since quitting stops a running install. The screen stays on while the app is open.
- What a phone lacks, the app passes in the environment: `MPC_DNS` (the network's DNS servers: Android has no `/etc/resolv.conf`, see
  `tools/desktop/dns.go`), `SSL_CERT_DIR` (Android's CA folders, for the catalog over HTTPS), `HOME` and `TMPDIR` (the app's own
  folders). Work folders left behind when Android ends the app are removed on the next start.
- `InstallService` (a foreground service, type dataSync, with a "MPC Installer is running" notification) runs while the app is
  open. Without it, Android 15+ cuts the network of an app that is not in front, and a copy failed with "software caused
  connection abort" when the person switched apps (user report, 2026-10-13). The installer also reconnects and copies again
  once when the connection drops during a copy (`tools/desktop`).
- Dark mode follows the phone: a night theme for the window, dark bars, and `data-sys` on the page (more dependable in a
  WebView than `prefers-color-scheme`). The page's Theme button can force light or dark.
- The phone must be on the same Wi-Fi as the MPC. Nothing about the device is stored, as on a computer. SSH keys are not used
  (there is no `~/.ssh` on a phone): log in with the root password, or none on a device without one.

## Build

```
tools/android/build_apk.sh 0.5.4 [out.apk]
```

Needs Go (the version in `tools/desktop/go.mod`), a JDK and either the Android SDK (`$ANDROID_HOME`, as on GitHub's runners) or
Ubuntu's packages (`apt install aapt dalvik-exchange zipalign apksigner android-sdk-platform-23`). No Gradle, no NDK. The app
targets Android 16 (`TARGET_SDK`, default 36) and runs on Android 6.0 (API 23) and later. It targets the current Android because
Play Protect blocks sideloaded apps that target an old one ("Unsafe app blocked", seen on a Galaxy S23 Ultra, 2026-10-13); it
compiles against any android.jar, since the two newer APIs it uses (the Android 13+ back callback, needed because apps targeting
16 no longer get `onBackPressed`, and the display cutout) are called through reflection. Edge to edge (forced from Android 15) is
handled by padding the page with the window insets.

The Java code uses API 26-35 classes (guarded by version checks), so `javac` needs an android.jar of API 35 or later: the SDK's,
or `ANDROID_COMPILE_JAR` on the Ubuntu route. There, aapt still links resources against API 23 (the old aapt cannot read newer
resource tables), and `manifest_fgs.py` (needs `pip install pyaxml`) adds the service's `foregroundServiceType`, which that aapt
cannot compile, to the binary manifest, checking that nothing else changed.

Signing: set `KEYSTORE` (PKCS12), `KEYSTORE_PASS` and `KEY_ALIAS` (default `mpc`). Without them the script signs with a throwaway key,
fine for trying a build, but Android installs an update only over an app signed with the same key, so releases must always use the
project's key. Make one once and keep it safe (losing it means users uninstall and reinstall to update):

```
keytool -genkeypair -keystore mpc-android.p12 -storetype PKCS12 -alias mpc -keyalg RSA -keysize 3072 -validity 10000 -dname "CN=MPC plugin installer"
base64 -w0 mpc-android.p12   # -> repository secret ANDROID_KEYSTORE_B64; the password -> ANDROID_KEYSTORE_PASS
```

The release workflow (`.github/workflows/desktop.yml`) builds the APK beside the desktop binaries when those secrets exist, and checks
on every pull request that the app still builds. `make_icons.py` redraws the launcher icon.

## Status

2026-10-13 (offline only): the APK builds and verifies (v1/v2/v3 signatures, aligned); the bundled x86_64 program ran with `/etc`
hidden (as on Android) and fetched over HTTPS through `SSL_CERT_DIR`; the 32-bit ARM program ran under qemu; Go tests pass,
including the DNS fallback against a local DNS server. Not yet run on a phone with an MPC: try the draft release on one before
publishing it.
