#!/usr/bin/env bash
# Render the README and social graphics in docs/assets/src/*.html to PNGs in
# docs/assets/, at 2x for sharp text, with headless Chrome or Chromium.
# Usage: docs/assets/render.sh [name…]   (default: all)
# CHROME overrides the browser; otherwise Playwright's chrome-headless-shell
# or Chromium (PLAYWRIGHT_BROWSERS_PATH), Google Chrome, or one on PATH.
# Fonts load from Google Fonts (through HTTPS_PROXY when set).
set -euo pipefail

cd "$(dirname "$0")"

find_chrome() {
  if [ -n "${CHROME:-}" ]; then
    echo "$CHROME"
    return
  fi
  # chrome-headless-shell sizes the viewport exactly; prefer it.
  local c
  for c in "${PLAYWRIGHT_BROWSERS_PATH:-/opt/pw-browsers}"/chromium_headless_shell-*/*/headless_shell \
    "${PLAYWRIGHT_BROWSERS_PATH:-/opt/pw-browsers}"/chromium-*/chrome-linux/chrome \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; do
    if [ -x "$c" ]; then
      echo "$c"
      return
    fi
  done
  for c in chrome-headless-shell google-chrome chromium chromium-browser; do
    if command -v "$c" >/dev/null; then
      command -v "$c"
      return
    fi
  done
  echo "render.sh: no Chrome found; set CHROME=/path/to/chrome" >&2
  exit 1
}
chrome=$(find_chrome)

proxy=()
if [ -n "${HTTPS_PROXY:-}" ]; then proxy=(--proxy-server="$HTTPS_PROXY"); fi

# name output width height (CSS pixels; the PNG is twice that)
targets="
banner header 1280 400
social-preview social-preview 1280 640
terminal terminal 1280 640
"

echo "$targets" | while read -r name out w h; do
  [ -n "$name" ] || continue
  if [ $# -gt 0 ] && ! printf '%s\n' "$@" | grep -qx "$name"; then continue; fi
  "$chrome" --headless --no-sandbox --disable-gpu --hide-scrollbars --no-first-run \
    "${proxy[@]}" --force-device-scale-factor=2 --window-size="$w,$h" \
    --virtual-time-budget=8000 --run-all-compositor-stages-before-draw \
    --screenshot="$PWD/$out.png" "file://$PWD/src/$name.html" 2>/dev/null
  echo "rendered $out.png"
done
