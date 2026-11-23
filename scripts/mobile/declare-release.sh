#!/usr/bin/env bash
# Declares a website APK build to the hub (issue #72), after mobile-release has
# put it on a GitHub Release: POST <hub>/releases/v1/declare with the version,
# the build number (the Android versionCode), the APK's release-asset URL, its
# SHA-256 and its size. The hub records it as « pending »; an admin publishes
# it from the back-office (« Versions »). Declaring the same build again is a
# no-op (200), so a re-run of the job is safe.
#
# Environment:
#   KERGUI_HUB_RELEASES_TOKEN  bearer token (secret). Empty: the declaration is
#                              skipped with a notice and the script exits 0.
#   KERGUI_HUB_URL             hub base URL (default https://hub.kergui.click)
#   VERSION                    X.Y.Z (versionName)
#   BUILD                      build number = versionCode (github.run_number)
#   TAG                        the GitHub Release's tag (to find the asset URL)
#   APK                        path to the APK file (hashed and measured here)
#   APK_URL                    optional: the asset URL; otherwise read from
#                              `gh release view "$TAG" --json assets`
#   NOTES                      optional: release notes (default
#                              "Kergui <version> (build <N>)")
#   DECLARE_RETRY_DELAY        seconds between attempts (default 15)
#
# Exit: 0 on 201 (new) or 200 (already known), or when the token is not set;
# 1 with a ::error:: line (status and body) on any other answer. The token is
# never printed: it goes to curl through a private header file, not argv.
set -euo pipefail

if [[ -z "${KERGUI_HUB_RELEASES_TOKEN:-}" ]]; then
  echo "::notice::Hub declaration skipped: set the KERGUI_HUB_RELEASES_TOKEN secret (release environment) to the hub's value (docs/hub-production.md, App updates)"
  exit 0
fi
[[ -n "${GITHUB_ACTIONS:-}" ]] && echo "::add-mask::${KERGUI_HUB_RELEASES_TOKEN}"

hub="${KERGUI_HUB_URL:-}"
hub="${hub:-https://hub.kergui.click}"
hub="${hub%/}"
delay="${DECLARE_RETRY_DELAY:-15}"

fail() { echo "::error::Hub declaration: $*"; exit 1; }

[[ "${VERSION:-}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "bad VERSION '${VERSION:-}' (want X.Y.Z)"
[[ "${BUILD:-}" =~ ^[1-9][0-9]{0,9}$ ]] && (( BUILD <= 2100000000 )) || fail "bad BUILD '${BUILD:-}' (want 1..2100000000)"
[[ -f "${APK:-}" ]] || fail "APK '${APK:-}' not found"
name="$(basename "$APK")"

sha256="$(sha256sum "$APK" | cut -d' ' -f1)"
if [[ -f "$APK.sha256" ]]; then
  listed="$(cut -d' ' -f1 < "$APK.sha256")"
  [[ "$listed" == "$sha256" ]] || fail "$name does not match $name.sha256 ($sha256 vs $listed)"
fi
size="$(wc -c < "$APK" | tr -d ' ')"

url="${APK_URL:-}"
if [[ -z "$url" ]]; then
  [[ -n "${TAG:-}" ]] || fail "TAG is not set and no APK_URL given"
  asset="$(gh release view "$TAG" --json assets --jq ".assets[] | select(.name == \"$name\") | [.url, (.size|tostring)] | join(\" \")")" \
    || fail "cannot read release $TAG with gh"
  [[ -n "$asset" ]] || fail "no asset $name on release $TAG"
  url="${asset% *}"
  [[ "${asset#* }" == "$size" ]] || fail "asset $name on $TAG is ${asset#* } bytes, the local APK $size"
fi
[[ "$url" == https://* ]] || fail "asset URL '$url' is not https"

notes="${NOTES:-Kergui ${VERSION} (build ${BUILD})}"
notes="${notes:0:4000}"

work="$(umask 077 && mktemp -d)"
trap 'rm -rf "$work"' EXIT
(umask 077 && printf 'Authorization: Bearer %s\n' "$KERGUI_HUB_RELEASES_TOKEN" > "$work/auth")
jq -n --arg versionName "$VERSION" --argjson build "$BUILD" --arg url "$url" \
  --arg sha256 "$sha256" --argjson size "$size" --arg notes "$notes" \
  '{platform: "android", channel: "website", versionName: $versionName, build: $build,
    url: $url, sha256: $sha256, size: $size, notes: $notes}' > "$work/body.json"

echo "Declaring Kergui $VERSION (build $BUILD) to $hub: $url ($size bytes, sha256 $sha256)"
for attempt in 1 2 3; do
  code="$(curl --silent --show-error --fail-with-body --connect-timeout 10 --max-time 30 \
    -X POST "$hub/releases/v1/declare" \
    -H @"$work/auth" -H 'Content-Type: application/json' \
    --data-binary @"$work/body.json" -o "$work/resp" -w '%{http_code}')" || true
  case "$code" in
    201) echo "Declared: the release waits « pending » in the back-office (Versions)"; exit 0 ;;
    200) echo "Already declared: nothing changed"; exit 0 ;;
    000|5??) ;; # network error or hub error: try again
    *) fail "HTTP $code from $hub/releases/v1/declare: $(head -c 2000 "$work/resp" 2>/dev/null)" ;;
  esac
  [[ "$attempt" -lt 3 ]] || break
  echo "::warning::Hub declaration attempt $attempt got HTTP $code, retrying in ${delay} s"
  sleep "$delay"
done
fail "HTTP $code from $hub/releases/v1/declare after 3 attempts: $(head -c 2000 "$work/resp" 2>/dev/null)"
