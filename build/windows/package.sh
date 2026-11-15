#!/usr/bin/env bash
# Builds SpyDB for Windows (64-bit Intel/AMD): bin/SpyDB.exe and its
# installer, bin/SpyDB-windows-setup.exe. The release workflow runs it on
# Windows (Git Bash); it runs on a Mac or Linux too.
#   build/windows/package.sh 0.1.9
# Needs the frontend built (frontend/dist), wails3, and makensis ($MAKENSIS
# for another one, e.g. in a container: it's run from the repo's folder).
set -euo pipefail
version=${1:?usage: build/windows/package.sh <version, e.g. 0.1.9>}
case $version in '' | *[!0-9.]*) echo "Not a version: $version" >&2; exit 1 ;; esac
cd "$(dirname "$0")/../.." # the repo

# 1. The exe's icon, manifest and version (File Explorer → Properties →
# Details), stamped with this version in a copy. Relative paths: Git Bash
# would rewrite /tmp/… for wails3.
res=$(mktemp -d build/windows/stamped.XXXXXX)
trap 'rm -rf "$res" wails_windows_amd64.syso' EXIT
cp build/windows/icon.ico build/windows/info.json build/windows/wails.exe.manifest "$res/"
perl -pi -e 's/("(?:file_version|product_version|FileVersion|ProductVersion)":\s*")[^"]*"/${1}'"$version"'"/' "$res/info.json"
perl -pi -e 's/(name="com\.anujkaushik\.spydb" version=")[^"]*"/${1}'"$version"'.0"/' "$res/wails.exe.manifest"
wails3 generate syso -arch amd64 -icon "$res/icon.ico" -manifest "$res/wails.exe.manifest" -info "$res/info.json" -out wails_windows_amd64.syso

# 2. SpyDB.exe: Windows needs no cgo; -H windowsgui opens no console window with it.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags production -trimpath -buildvcs=false \
  -ldflags="-w -s -H windowsgui" -o bin/SpyDB.exe .

# 3. The installer, carrying the WebView2 bootstrapper for a Windows without WebView2.
wails3 generate webview2bootstrapper -dir build/windows/nsis
${MAKENSIS:-makensis} -V2 -DARG_WAILS_AMD64_BINARY=../../../bin/SpyDB.exe -DINFO_PRODUCTVERSION="$version" build/windows/nsis/project.nsi
echo "Built bin/SpyDB.exe and bin/SpyDB-windows-setup.exe ($version)."
