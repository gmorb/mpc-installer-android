#!/usr/bin/env bash
# Builds the Android app of the MPC plugin installer: tools/android/README.md.
#
#   tools/android/build_apk.sh <version> [out.apk]
#
# Needs Go (the version in tools/desktop/go.mod), a JDK (javac, keytool) and Android build tools, from either
#   - the Android SDK ($ANDROID_HOME or $ANDROID_SDK_ROOT: the newest platforms/android-NN and build-tools/*), as on GitHub's
#     runners; or
#   - Ubuntu/Debian packages: apt install aapt dalvik-exchange zipalign apksigner android-sdk-platform-23.
# Either way the app targets TARGET_SDK (default 36, Android 16): the API level it promises to behave correctly on, which
# Play Protect checks (an old target gets "Unsafe app blocked"). It is independent of the android.jar compiled against: the
# few newer calls MainActivity needs (back gesture, display cutout) go through reflection, so API 23's jar is enough.
# No Gradle and no NDK: the app is one Java file, and the installer is the desktop Go program built without cgo.
#
# Signing: KEYSTORE (a .jks/.p12 file), KEYSTORE_PASS and KEY_ALIAS (default "mpc"). Without KEYSTORE a throwaway key is made in
# the build folder, which is fine for trying it out, but Android only installs an update over an app signed with the same key,
# so releases must always use the same KEYSTORE.
set -euo pipefail

VERSION=${1:?usage: build_apk.sh <version, e.g. 0.5.4> [out.apk]}
echo "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.]+)?$' || { echo "bad version: $VERSION" >&2; exit 1; }
HERE=$(cd "$(dirname "$0")" && pwd)
DESKTOP="$HERE/../desktop"
OUT=$(realpath -m "${2:-mpc-installer-$VERSION-android.apk}")
B=$(mktemp -d)
trap 'rm -rf "$B"' EXIT
MIN_SDK=23

# versionCode: 0.5.4 -> 504, 1.2.13 -> 10213 (a newer version always has a larger code)
IFS=. read -r MA MI PA <<<"$(echo "$VERSION" | grep -Eo '^[0-9]+\.[0-9]+\.[0-9]+')"
CODE=$((MA * 10000 + MI * 100 + PA))

# --- tools ----------------------------------------------------------------------------------------------------------------
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
BT=
if [ -n "$SDK" ] && ls "$SDK"/platforms/android-*/android.jar >/dev/null 2>&1; then
  for d in $(ls -d "$SDK"/build-tools/* 2>/dev/null | sort -rV); do [ -x "$d/aapt" ] && [ -x "$d/d8" ] && { BT=$d; break; }; done
fi
if [ -n "$BT" ]; then
  JAR=$(ls "$SDK"/platforms/android-*/android.jar | sort -V | tail -1)
  AAPT=$BT/aapt DEX="$BT/d8" ZIPALIGN=$BT/zipalign APKSIGNER=$BT/apksigner
else
  JAR=/usr/lib/android-sdk/platforms/android-23/android.jar
  AAPT=aapt DEX=dalvik-exchange ZIPALIGN=zipalign APKSIGNER=apksigner
fi
JAR=${ANDROID_JAR:-$JAR}
[ -f "$JAR" ] || { echo "no android.jar (set ANDROID_HOME, or apt install android-sdk-platform-23)" >&2; exit 1; }
# The Java code uses API 26-35 classes (notification channel, foreground-service type), guarded by version checks, so javac
# needs an android.jar of API 35 or later: the SDK's, or ANDROID_COMPILE_JAR on the Ubuntu route (aapt there still links
# resources against API 23, which it can read; the foregroundServiceType it cannot compile is added by manifest_fgs.py).
CJAR=${ANDROID_COMPILE_JAR:-$JAR}
API=$(basename "$(dirname "$JAR")" | sed 's/^android-//'); case "$API" in ''|*[!0-9]*) API=0 ;; esac
TARGET_SDK=${TARGET_SDK:-36}
for t in "$AAPT" "$ZIPALIGN" "$APKSIGNER" javac keytool go; do command -v "$t" >/dev/null || { echo "missing: $t" >&2; exit 1; }; done
echo "compiling against $JAR, targetSdk $TARGET_SDK, dex: $(basename "$DEX")"

# --- the installer program, one per ABI -----------------------------------------------------------------------------------
# arm64 (nearly every phone since 2017) is a real Android build. 32-bit ARM and x86_64 (emulators, Chromebooks) cannot be
# built for Android without cgo and the NDK, so they are static Linux programs, which Android's kernel runs just the same;
# the app hands them the things only the Android build finds by itself (MainActivity: SSL_CERT_DIR, MPC_DNS).
mkdir -p "$B/apk"
build() { # abi goos goarch [goarm]
  mkdir -p "$B/apk/lib/$1"
  (cd "$DESKTOP" && GOOS=$2 GOARCH=$3 GOARM=${4:-} CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$B/apk/lib/$1/libmpcinstaller.so" .)
}
build arm64-v8a android arm64
build armeabi-v7a linux arm 7
build x86_64 linux amd64

# --- the app --------------------------------------------------------------------------------------------------------------
mkdir -p "$B/classes" "$B/dex"
javac -source 8 -target 8 -bootclasspath "$CJAR" -classpath "$CJAR" -encoding UTF-8 -Xlint:-options -d "$B/classes" \
  $(find "$HERE/src" -name '*.java') || { echo "javac failed: the app needs an android.jar of API 35+ (ANDROID_COMPILE_JAR)" >&2; exit 1; }
if [ "$(basename "$DEX")" = d8 ]; then
  "$DEX" --release --min-api $MIN_SDK --lib "$CJAR" --output "$B/dex" $(find "$B/classes" -name '*.class')
else
  "$DEX" --dex --min-sdk-version=$MIN_SDK --output="$B/dex/classes.dex" "$B/classes"
fi
cp "$B/dex/classes.dex" "$B/apk/"

MANIFEST=$HERE/AndroidManifest.xml
if [ "$API" -lt 29 ]; then   # this android.jar has no foregroundServiceType: leave it out for aapt, add it to the binary after
  MANIFEST=$B/AndroidManifest.xml
  sed 's/ *android:foregroundServiceType="dataSync"//' "$HERE/AndroidManifest.xml" > "$MANIFEST"
fi
"$AAPT" package -f -M "$MANIFEST" -S "$HERE/res" -I "$JAR" \
  --min-sdk-version $MIN_SDK --target-sdk-version "$TARGET_SDK" --version-code $CODE --version-name "$VERSION" \
  -F "$B/unsigned.apk"
if [ "$API" -lt 29 ]; then
  (cd "$B" && unzip -q -o unsigned.apk AndroidManifest.xml && python3 "$HERE/manifest_fgs.py" AndroidManifest.xml .InstallService=0x1 \
    && zip -q unsigned.apk AndroidManifest.xml) || { echo "could not add foregroundServiceType (pip install pyaxml)" >&2; exit 1; }
fi
(cd "$B/apk" && zip -q -r "$B/unsigned.apk" classes.dex lib)
"$ZIPALIGN" -p -f 4 "$B/unsigned.apk" "$B/aligned.apk"

if [ -z "${KEYSTORE:-}" ]; then
  KEYSTORE=$B/throwaway.p12 KEYSTORE_PASS=throwaway KEY_ALIAS=mpc
  keytool -genkeypair -keystore "$KEYSTORE" -storetype PKCS12 -storepass "$KEYSTORE_PASS" -alias "$KEY_ALIAS" \
    -keyalg RSA -keysize 3072 -validity 10000 -dname "CN=MPC plugin installer (test build)" >/dev/null 2>&1
  echo "WARNING: signed with a throwaway key; set KEYSTORE for a release (see the top of this script)" >&2
fi
"$APKSIGNER" sign --ks "$KEYSTORE" --ks-pass "pass:${KEYSTORE_PASS:?set KEYSTORE_PASS}" --ks-key-alias "${KEY_ALIAS:-mpc}" \
  --out "$OUT" "$B/aligned.apk"
"$APKSIGNER" verify "$OUT"
rm -f "$OUT.idsig"
echo "built $OUT (version $VERSION, code $CODE, targetSdk $TARGET_SDK)"
