#!/usr/bin/env bash
set -euo pipefail
umask 077
sdk=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}
if [[ -z "$sdk" ]]; then echo 'Set ANDROID_SDK_ROOT to an SDK with platform/build-tools 35.' >&2; exit 1; fi
base=$(cd -- "$(dirname -- "$0")" && pwd)
build="$base/build"
tools="$sdk/build-tools/${ANDROID_BUILD_TOOLS:-35.0.0}"
platform="$sdk/platforms/${ANDROID_PLATFORM:-android-35}/android.jar"
keydir="${XDG_DATA_HOME:-$HOME/.local/share}/recgo/android-helper"
mkdir -p "$build/compiled" "$build/gen" "$build/classes" "$build/dex" "$keydir"
if [[ ! -f "$keydir/debug.p12" ]]; then
  keytool -genkeypair -keystore "$keydir/debug.p12" -storetype PKCS12 -storepass android -keypass android -alias recgo-helper -keyalg RSA -keysize 2048 -validity 10000 -dname 'CN=Recgo local development helper'
fi
"$tools/aapt2" compile --dir "$base/res" -o "$build/compiled/resources.zip"
"$tools/aapt2" link -I "$platform" --manifest "$base/AndroidManifest.xml" --java "$build/gen" -o "$build/unsigned.apk" "$build/compiled/resources.zip"
javac --release 8 -classpath "$platform" -d "$build/classes" "$build/gen/dev/eordano/recgo/android/R.java" "$base"/src/dev/eordano/recgo/android/*.java
jar cf "$build/classes.jar" -C "$build/classes" .
"$tools/d8" --lib "$platform" --min-api 31 --output "$build/dex" "$build/classes.jar"
jar uf "$build/unsigned.apk" -C "$build/dex" classes.dex
"$tools/zipalign" -f 4 "$build/unsigned.apk" "$build/aligned.apk"
"$tools/apksigner" sign --ks "$keydir/debug.p12" --ks-pass pass:android --out "$build/recgo-android-helper.apk" "$build/aligned.apk"
"$tools/apksigner" verify "$build/recgo-android-helper.apk"
printf '%s\n' "$build/recgo-android-helper.apk"
