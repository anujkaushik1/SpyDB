#!/bin/sh
# Installs or updates SpyDB on Ubuntu from the latest GitHub release:
#   wget -qO- https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-ubuntu.sh | sh
# Saved connections and queries are kept (they live in ~/.config/SpyDB).
# It uses wget, not curl: Ubuntu Desktop comes with wget only.
set -eu

REPO=https://github.com/anujkaushik1/SpyDB

# Everything runs from main, called on the last line: a half-downloaded
# script stops at a syntax error instead of running halfway.
main() {
  # 1. The right kind of computer: Ubuntu 22.04+ (or another apt system), 64-bit Intel/AMD.
  command -v apt-get >/dev/null || { echo "This is for Ubuntu (it needs apt)."; exit 1; }
  command -v wget >/dev/null || { echo "Install wget first: sudo apt install wget"; exit 1; }
  [ "$(dpkg --print-architecture)" = amd64 ] ||
    { echo "SpyDB's Ubuntu build is for 64-bit Intel/AMD computers (amd64)."; exit 1; }
  if [ -r /etc/os-release ]; then
    . /etc/os-release
    if [ "${ID:-}" = ubuntu ] && [ "${VERSION_ID%%.*}" -lt 22 ]; then
      echo "SpyDB needs Ubuntu 22.04 or newer (this is $VERSION_ID)."; exit 1
    fi
  fi

  # 2. The latest version: GitHub's latest-release page redirects to its
  # tag (Location: …/releases/tag/v0.1.4); read that, without following it.
  # (wget prints it as "Location: <url> [following]"; keep just the url.)
  latest=$(wget -S --spider --max-redirect=0 "$REPO/releases/latest" 2>&1 |
    sed -n 's/^ *[Ll]ocation: *\([^ ]*\).*/\1/p' | tail -n 1 | tr -d '\r')
  version=${latest##*/v}
  case $version in
    '' | *[!0-9.]*) echo "Couldn't reach GitHub to find the latest version. Nothing was changed."; exit 1 ;;
  esac

  # 3. Download it into a temporary folder, removed at the end. (Readable by
  # all, so apt's own downloader user can open it.)
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  chmod 755 "$tmp"
  deb="$tmp/spydb_${version}_amd64.deb"
  echo "Downloading SpyDB $version…"
  wget -q --show-progress -O "$deb" "$REPO/releases/download/v$version/spydb_${version}_amd64.deb" ||
    { echo "Couldn't download SpyDB $version. Nothing was changed."; exit 1; }
  chmod 644 "$deb"

  # 4. Install it, or update the installed one. sudo asks for your password.
  # The package list is refreshed first: apt fetches the libraries SpyDB
  # needs (GTK, WebKit), and an old list points at files that are gone.
  echo "Installing SpyDB $version (your password may be asked for)…"
  sudo apt-get update -qq </dev/null || echo "(Couldn't refresh the package list; trying anyway.)"
  sudo apt-get install -y "$deb" </dev/null
  # (dpkg says 0.1.4-1: the -1 is the package's own revision.)
  echo "SpyDB $(dpkg-query -W -f '${Version}' spydb | sed 's/-[^-]*$//') installed. Open it from your apps, or run: spydb"
}

main
