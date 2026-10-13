# MPC Installer for Android

Install, update and remove VST plugins on a modded Akai MPC or Force from your Android device. No computer, no SSH commands: connect over Wi-Fi, tick plugins from the catalog, press Install.

## Quick start

1. Download `mpc-installer-<version>-android.apk` from the project's releases and open it on the phone. Allow installing from your browser or Files app when Android asks.
2. Phone and MPC on the same Wi-Fi. Open **MPC Installer**, type the MPC's IP address and root password, tap **Connect**.
3. Tick plugins, choose the drive under **Install to** (tap **Create Synths folder** if it has none), tap **Install…**, confirm. Save your project first: MPC restarts. Keep the app open until it says Done.

## User manual

Full user manual with pictures: [MPC-Installer-Android-Manual.pdf](tools/android/MPC-Installer-Android-Manual.pdf)

## Status

Tested by the author on a real MPC OS device from an Android phone (a Galaxy S23 Ultra); other devices and phones are untested, so reports and issues are welcome.

## Download

Download the latest release from the [releases page](https://github.com/gmorb/mpc-installer-android/releases).

**Install steps:**
1. Open the APK on your Android device
2. Allow installing from your browser or Files app when Android asks
3. Play Protect may ask for a scan - this is normal for sideloaded apps

**Note:** If you installed an earlier test build, uninstall it once; releases from v1.0.0 on are signed with the project's own key and update in place.

## Needs

- Android 6.0+
- Phone and MPC on the same Wi-Fi
- Root SSH access on the MPC
- Internet on the phone for the catalog and plugin downloads (they come from sd88me/mpc-vst-plugins)

## Build

See the [Build section in tools/android/README.md](tools/android/README.md#build) for detailed instructions. The basic command is:

```bash
tools/android/build_apk.sh <version> [out.apk]
```

## License

MIT License, see [LICENSE](LICENSE).

## Credit

Built on the desktop installer from [sd88me/mpc-vst-plugins](https://github.com/sd88me/mpc-vst-plugins) (tag desktop-v0.5.4). Catalog and plugin zips come from that project. Unofficial, not affiliated with Akai Professional. Use at your own risk.