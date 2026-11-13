# MPC Installer for Android

Install, update and remove VST plugins on an Akai MPC or Force from your Android device. No computer, no SSH commands: connect over Wi-Fi, tick plugins from the catalog, press Install.

**MPC OS 3.10.1 and later:** the app installs into the **VST2 Plugins** folder that MPC now loads plugins from by itself (inMusic's [MPC Plugin Builder SDK](https://github.com/inmusic-software/mpc-plugin-builder-sdk) format). Catalog plugins are converted on the fly before they are sent, the folder can be created from the app (on a card or USB drive first), and MPC keeps running. Older, modded MPCs keep using the Synths folder method.

## Quick start

1. Download `mpc-installer-<version>-android.apk` from the project's releases and open it on the phone. Allow installing from your browser or Files app when Android asks.
2. On MPC OS 3.10.1+, turn on developer mode on the MPC (Preferences, Info: tap the version 3 times, the serial number twice, the firmware version once), and Preferences > Enhancements (Beta) > **Enable Enhancements** and **External Plugin Support**.
3. Phone and MPC on the same Wi-Fi. Open **MPC Installer**, type the MPC's IP address and password, tick **Remember this MPC** if you like, tap **Connect**.
4. Tick plugins (you can browse before connecting), choose the drive under **Install to** (tap **Create VST2 Plugins folder** if it has none), tap **Install…**, confirm. Keep the app open until it says Done. On the MPC the plugins are under **External Plugins**.

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
- SSH access on the MPC: developer mode on MPC OS 3.10.1+, or a modded unit with root SSH on older firmware
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