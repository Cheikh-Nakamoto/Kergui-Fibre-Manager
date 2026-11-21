#!/usr/bin/env bash
# RFC-006 (decision of 2026-11-21): the app, the one build published on Google
# Play, has no payment surface. Passes are bought on the hub's web page only,
# and the app must not steer anyone there: no payment method, price, offer,
# "buy" wording or link to a purchase page. This builds the Android JS bundle
# and fails on any such wording.
# Usage: scripts/check-play-bundle.sh   (from mobile/, or anywhere)
set -euo pipefail
cd "$(dirname "$0")/.."
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

npx expo export --platform android --output-dir "$out" --no-bytecode >/dev/null
pattern='NabooPay|Wave|Orange Money|FCFA|XOF|prix|abonne|payer|acheter|achat|s’abonner|site web|https?://[^"]*buy'
if grep -rEoh "$pattern" "$out/_expo" | sort | uniq -c | grep .; then
  echo "::error::the app bundle has payment wording (see above): the Play app must not mention paying"
  exit 1
fi
echo "app bundle: no payment wording, price or purchase link"
