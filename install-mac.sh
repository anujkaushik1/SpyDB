#!/bin/sh
# Installs or updates SpyDB on this Mac from the latest GitHub release:
#   curl -fsSL https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-mac.sh | sh
# Saved connections and queries are kept (they live outside the app).
# Downloaded with curl, the app isn't marked as from the internet, so macOS
# doesn't ask about it on the first open.
set -eu

URL="https://github.com/anujkaushik1/SpyDB/releases/latest/download/SpyDB-mac.zip"
APP=/Applications/SpyDB.app
APP_ID=com.anujkaushik.spydb

# Everything runs from main, called on the last line: a half-downloaded
# script stops at a syntax error instead of running halfway.
main() {
  # 1. The right kind of Mac: Apple silicon (M-chip), macOS 12 or newer.
  [ "$(uname -m)" = arm64 ] || { echo "SpyDB needs an Apple-silicon (M-chip) Mac."; exit 1; }
  [ "$(sw_vers -productVersion | cut -d. -f1)" -ge 12 ] || { echo "SpyDB needs macOS 12 or newer."; exit 1; }

  # 2. Download and unzip into a temporary folder, removed at the end.
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  echo "Downloading SpyDB…"
  curl -fL --progress-bar -o "$tmp/SpyDB-mac.zip" "$URL" ||
    { echo "Couldn't download SpyDB (no internet, or GitHub is down?). Nothing was changed."; exit 1; }
  ditto -x -k "$tmp/SpyDB-mac.zip" "$tmp" 2>/dev/null ||
    { echo "The download isn't a SpyDB zip. Nothing was changed."; exit 1; }

  # 3. Check the download before touching the installed app.
  codesign --verify --deep --strict "$tmp/SpyDB.app" 2>/dev/null ||
    { echo "The download looks damaged. Nothing was changed."; exit 1; }

  # 4. Quit SpyDB if it's open (waiting up to 10 seconds), then swap the app.
  # (Asked only when it runs: naming an app that isn't installed is an error.)
  if pgrep -f "$APP/Contents/MacOS/" >/dev/null; then
    osascript -e "tell application id \"$APP_ID\" to quit" >/dev/null 2>&1 || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      pgrep -f "$APP/Contents/MacOS/" >/dev/null || break
      sleep 1
    done
    if pgrep -f "$APP/Contents/MacOS/" >/dev/null; then
      echo "SpyDB is still open. Quit it (⌘Q), then run this again. Nothing was changed."
      exit 1
    fi
  fi
  rm -rf "$APP"
  mv "$tmp/SpyDB.app" "$APP"

  # 5. Say which version, and open it.
  echo "SpyDB $(defaults read "$APP/Contents/Info.plist" CFBundleShortVersionString) installed."
  open "$APP"
}

main
