#!/usr/bin/env bash
# Local replacement for the e2e-android CI job: builds the Go core and a
# release APK (debug-signed, as in CI), installs it on the phone or emulator
# that adb sees, and runs the Maestro demo flow on it (demo-device.yaml: it
# never erases the app's data, unlike the CI flow). CI only runs on tags;
# this is what proves a mobile change before a push.
#
# Usage: scripts/mobile/local-e2e.sh [--no-build] [--reinstall] [--serial <adb serial>] [--flow <steps.yaml>]...
#   --flow       run only these steps files of mobile/.maestro (the new
#                ticket's), after opening the demo; repeatable. Without it,
#                the whole demo-device.yaml runs (every ticket so far).
#   --no-build   reuse the last APK in mobile/android/app/build/outputs
#   --reinstall  uninstall a differently signed app first (ERASES its data)
#   --serial     pick one device when several are connected
# Needs: ANDROID_HOME with the NDK, JDK 21 (or 17), node, adb, Maestro
# (~/.maestro/bin/maestro, installed from https://get.maestro.mobile.dev).
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
build=1
reinstall=0
serial=""
flows=()
while [ $# -gt 0 ]; do
  case "$1" in
    --no-build) build=0 ;;
    --reinstall) reinstall=1 ;;
    --serial) serial="$2"; shift ;;
    --flow) flows+=("$(basename "$2")"); shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

export PATH="$HOME/.maestro/bin:$PATH"
command -v maestro >/dev/null || { echo "Maestro is not installed: curl -fsSL https://get.maestro.mobile.dev | bash" >&2; exit 1; }

# Gradle and the Android Gradle plugin do not run on JDK 25 yet.
for jdk in /usr/lib/jvm/java-21-openjdk-amd64 /usr/lib/jvm/java-17-openjdk-amd64; do
  if [ -d "$jdk" ]; then export JAVA_HOME="$jdk"; export PATH="$JAVA_HOME/bin:$PATH"; break; fi
done

if [ -z "$serial" ]; then
  mapfile -t devices < <(adb devices | awk 'NR>1 && $2=="device" {print $1}')
  [ "${#devices[@]}" -ge 1 ] || { echo "no device: connect a phone (adb devices) or start an emulator" >&2; exit 1; }
  [ "${#devices[@]}" -eq 1 ] || { echo "several devices, pick one with --serial: ${devices[*]}" >&2; exit 1; }
  serial="${devices[0]}"
fi
export ANDROID_SERIAL="$serial"
echo "device: $serial"

apk="$root/mobile/android/app/build/outputs/apk/release/app-release.apk"
if [ "$build" = 1 ]; then
  make -C "$root" mobile-core-android
  cd "$root/mobile"
  npm ci
  npx expo prebuild --platform android --no-install
  (cd android && ./gradlew --no-daemon assembleRelease)
fi
[ -f "$apk" ] || { echo "no APK at $apk (run without --no-build)" >&2; exit 1; }

# The build takes a while; a wireless phone may have dropped off meanwhile.
case "$serial" in *:*) adb connect "$serial" >/dev/null 2>&1 || true ;; esac
[ "$(adb -s "$serial" get-state 2>/dev/null)" = device ] || {
  echo "device $serial is gone (asleep, off Wi-Fi or restarted). Reconnect it, then: $0 --no-build --serial $serial" >&2
  exit 1
}

# A debug-signed build cannot replace a store-signed one. Uninstalling erases
# the app's data on the phone, so it only happens when asked.
if ! adb install -r "$apk"; then
  if [ "$reinstall" = 1 ]; then
    adb uninstall org.kergui.app
    adb install "$apk"
  else
    echo "install failed (likely a differently signed app is installed)." >&2
    echo "Re-run with --reinstall to uninstall it first; this ERASES the app's data on the phone." >&2
    exit 1
  fi
fi

out="$root/mobile/maestro-debug"
rm -rf "$out"
adb logcat -c
flow="$root/mobile/.maestro/demo-device.yaml"
if [ "${#flows[@]}" -gt 0 ]; then
  # Only the new ticket's steps: the demo-device.yaml header (open the demo),
  # then those steps files. Written next to the others so relative paths hold.
  flow="$root/mobile/.maestro/.only.yaml"
  sed '/^- runFlow: .*-steps\.yaml$/,$d' "$root/mobile/.maestro/demo-device.yaml" > "$flow"
  for f in "${flows[@]}"; do
    [ -f "$root/mobile/.maestro/$f" ] || { echo "no flow mobile/.maestro/$f" >&2; exit 2; }
    echo "- runFlow: $f" >> "$flow"
  done
  echo "flows: ${flows[*]}"
fi
if ! maestro --device "$serial" test --debug-output "$out" "$flow"; then
  adb logcat -d -v brief | grep -E 'AndroidRuntime|FATAL|KerguiCore|ReactNativeJS' | tail -n 100
  echo "Maestro demo flow FAILED (debug output in $out)" >&2
  exit 1
fi
echo "Maestro demo flow passed on $serial"
