#!/usr/bin/env bash
# RFC-006 (Two channels): the play flavor, sold on Google Play, must carry no
# NabooPay wording, link or XOF price. babel.config.js drops the NabooPay
# module from that bundle; this builds the play JS bundle and checks it.
# Usage: scripts/check-play-bundle.sh   (from mobile/, or anywhere)
set -euo pipefail
cd "$(dirname "$0")/.."
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

KERGUI_FLAVOR=play npx expo export --platform android --output-dir "$out" --no-bytecode >/dev/null
pattern='NabooPay|naboopay\.|Orange Money|F CFA|FCFA|XOF|\bWave\b|priceXof|amountXof'
if grep -rEoh "$pattern" "$out/_expo" | sort | uniq -c | grep .; then
  echo "::error::the play bundle mentions NabooPay or an XOF price (see above)"
  exit 1
fi
echo "play bundle: no NabooPay wording, link or XOF price"
