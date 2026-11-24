#!/usr/bin/env bash
# Changes ONE environment variable or build argument of a Kergui Dokploy
# application, or redeploys it, and nothing else. Values are never printed.
#
#   scripts/ops/dokploy-set.sh <hub|site> env   KEY VALUE   set or replace KEY
#   scripts/ops/dokploy-set.sh <hub|site> build KEY VALUE   same, build argument
#   scripts/ops/dokploy-set.sh <hub|site> deploy            redeploy the app
#
# A VALUE of "-" is read from stdin (for secrets, so they stay out of argv).
# The API key is read from ~/.config/dokploy-api-key. After a write the script
# reads the application back and checks that only KEY changed; any other
# difference is reported and the exit status is 1.
set -euo pipefail

api="https://dokploy.rbscrewsn.com/api"
key_file="${DOKPLOY_API_KEY_FILE:-$HOME/.config/dokploy-api-key}"
die() { echo "dokploy-set: $*" >&2; exit 1; }

[[ $# -ge 2 ]] || die "usage: $0 <hub|site> <env|build> KEY VALUE | <hub|site> deploy"
case "$1" in
  hub) app=rVA7bfH3BydSumPKVsnz3 ;;
  site) app=PBPOwB6F9NHVmBabqOvLv ;;
  *) die "unknown application '$1' (hub or site)" ;;
esac
what="$2"
[[ -r "$key_file" ]] || die "no API key at $key_file"
api_key="$(tr -d '\n' <"$key_file")"
hdr=(-H "x-api-key: $api_key")

fetch() { curl -fsS -m 30 "${hdr[@]}" "$api/application.one?applicationId=$app"; }

if [[ "$what" == deploy ]]; then
  jq -n --arg id "$app" '{applicationId: $id}' |
    curl -fsS -m 30 -o /dev/null -X POST "${hdr[@]}" -H 'content-type: application/json' --data-binary @- "$api/application.deploy"
  echo "dokploy-set: $1 deploy requested"
  exit 0
fi

[[ $# -eq 4 ]] || die "usage: $0 <hub|site> <env|build> KEY VALUE"
case "$what" in
  env) field=env ;;
  build) field=buildArgs ;;
  *) die "unknown target '$what' (env, build or deploy)" ;;
esac
name="$3"
[[ "$name" =~ ^[A-Z][A-Z0-9_]*$ ]] || die "bad KEY '$name'"
value="$4"
if [[ "$value" == "-" ]]; then value="$(cat)"; fi
[[ "$value" != *$'\n'* ]] || die "VALUE must be one line"

before="$(fetch)"
# Replace every KEY=… line (or append one), keep every other line as is.
new_text="$(jq -r --arg f "$field" '.[$f] // ""' <<<"$before" |
  NAME="$name" VALUE="$value" awk '
    BEGIN { n = ENVIRON["NAME"]; v = ENVIRON["VALUE"]; done = 0 }
    index($0, n "=") == 1 { if (!done) { print n "=" v; done = 1 }; next }
    { print }
    END { if (!done) print n "=" v }')"

jq --arg f "$field" --arg t "$new_text" '{
    applicationId, env: (.env // ""), buildArgs: (.buildArgs // ""),
    buildSecrets: (.buildSecrets // ""), createEnvFile: (.createEnvFile // false)
  } | .[$f] = $t' <<<"$before" |
  curl -fsS -m 30 -o /dev/null -X POST "${hdr[@]}" -H 'content-type: application/json' --data-binary @- "$api/application.saveEnvironment"

after="$(fetch)"
strip() { jq -r --arg f "$1" '.[$f] // ""' | grep -v "^$name=" || true; }
ok=1
for f in env buildArgs buildSecrets; do
  if [[ "$(strip "$f" <<<"$before")" != "$(strip "$f" <<<"$after")" ]]; then
    echo "dokploy-set: WARNING: $f changed beyond $name" >&2; ok=0
  fi
done
got="$(jq -r --arg f "$field" '.[$f] // ""' <<<"$after" | grep "^$name=" | tail -1 | cut -d= -f2-)"
[[ "$got" == "$value" ]] || { echo "dokploy-set: $name was not saved" >&2; ok=0; }
[[ $ok -eq 1 ]] || exit 1
echo "dokploy-set: $1 $what $name set (value not shown); redeploy for it to take effect"
