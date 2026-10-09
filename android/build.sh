#!/usr/bin/env bash
# Builds Drop.apk. Needs: Go, a JDK (17+), and the Android SDK (platform 33 + build-tools).
#   ANDROID_HOME            path to the Android SDK
#   KEYSTORE_FILE, KEYSTORE_PASS, KEY_ALIAS   your release key (keep it private and keep it forever:
#                           phones only accept updates signed with the same key)
# Without a keystore a throw-away key is made; such an APK works but cannot be updated in place.
set -euo pipefail
cd "$(dirname "$0")"
: "${ANDROID_HOME:?set ANDROID_HOME to your Android SDK}"
BT=$(ls -d "$ANDROID_HOME"/build-tools/* | sort -V | tail -1)
JAR="$ANDROID_HOME/platforms/android-33/android.jar"
[ -f "$JAR" ] || { echo "install platform android-33 first"; exit 1; }
OUT=out; rm -rf "$OUT" lib; mkdir -p "$OUT/cls" "$OUT/dex" "$OUT/res" "$OUT/stage"

echo "== Go program for each phone CPU"
for t in "arm64 arm64-v8a" "arm armeabi-v7a" "amd64 x86_64"; do
  set -- $t
  mkdir -p "lib/$2"
  (cd .. && GOOS=linux GOARCH=$1 GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "android/lib/$2/libdrop.so" .)
done

echo "== app shell"
javac --release 8 -nowarn -cp "$JAR" -d "$OUT/cls" $(find src -name '*.java')
"$BT/d8" --lib "$JAR" --min-api 24 --output "$OUT/dex" $(find "$OUT/cls" -name '*.class')
"$BT/aapt2" compile --dir res -o "$OUT/res/res.zip"
"$BT/aapt2" link -o "$OUT/base.apk" -I "$JAR" --manifest AndroidManifest.xml \
  --min-sdk-version 24 --target-sdk-version 33 --version-code "${VERSION_CODE:-1}" --version-name "${VERSION_NAME:-1.0}" "$OUT/res/res.zip"

echo "== package"
cp "$OUT/dex/classes.dex" "$OUT/stage/"
cp -r lib "$OUT/stage/lib"
cp "$OUT/base.apk" "$OUT/unaligned.apk"
(cd "$OUT/stage" && zip -r -X ../unaligned.apk classes.dex lib >/dev/null)
"$BT/zipalign" -f -p 4 "$OUT/unaligned.apk" "$OUT/aligned.apk"

if [ -z "${KEYSTORE_FILE:-}" ]; then
  echo "!! no KEYSTORE_FILE: using a throw-away key (not suitable for releases)"
  KEYSTORE_FILE="$OUT/throwaway.jks"; KEYSTORE_PASS=throwaway; KEY_ALIAS=drop
  keytool -genkeypair -keystore "$KEYSTORE_FILE" -storepass "$KEYSTORE_PASS" -alias "$KEY_ALIAS" \
    -keyalg RSA -keysize 3072 -validity 9000 -dname "CN=Drop" >/dev/null 2>&1
fi
"$BT/apksigner" sign --ks "$KEYSTORE_FILE" --ks-pass "pass:$KEYSTORE_PASS" --ks-key-alias "$KEY_ALIAS" \
  --out "$OUT/Drop.apk" "$OUT/aligned.apk"
"$BT/apksigner" verify --verbose "$OUT/Drop.apk" | head -5
echo "built: android/$OUT/Drop.apk"
