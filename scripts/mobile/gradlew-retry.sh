#!/usr/bin/env bash
# Runs ./gradlew with its arguments, from the current directory (mobile/android),
# and tries again — up to 3 runs, 30 s apart — only when Gradle could not
# fetch a plugin or a dependency. The Gradle Plugin Portal often fails from
# GitHub's runners ("Plugin [id: 'org.gradle.toolchains.foojay-resolver-
# convention', version: '1.0.0'] was not found", React Native's own settings
# plugin): a network failure, not a build one. A real build error fails at
# once, without a second run.
#
# Usage (from mobile/android): ../../scripts/mobile/gradlew-retry.sh --no-daemon assembleRelease
set -uo pipefail

log="$(mktemp)"
trap 'rm -f "$log"' EXIT
for attempt in 1 2 3; do
  ./gradlew "$@" 2>&1 | tee "$log"
  status=${PIPESTATUS[0]}
  [ "$status" -eq 0 ] && exit 0
  if ! grep -qE "was not found in any of the following sources|Could not resolve|Could not GET|Could not HEAD|Connection reset|Read timed out|Connect timed out" "$log"; then
    exit "$status"
  fi
  [ "$attempt" -lt 3 ] || break
  echo "::warning::Gradle could not fetch a plugin or dependency (attempt $attempt), retrying in 30 s"
  sleep 30
done
exit "$status"
