#!/usr/bin/env bash
# Builds SpyDB for Ubuntu with Docker, so it works from a Mac too.
#
#   build/linux/build-docker.sh [gtk3|gtk4] [amd64|arm64]
#
#   gtk3 (default)  Ubuntu 22.04 and newer: one binary for 22.04 and 24.04.
#                   Uses Wails' legacy GTK3 backend.
#   gtk4            Ubuntu 24.04 and newer, Wails' default GTK4 backend.
#   amd64 (default) most PCs; arm64 for ARM machines.
#
# Output: bin/linux/ubuntu-<release>-<gtk>-<arch>/spydb
set -euo pipefail

GTK=${1:-gtk3}
ARCH=${2:-amd64}
case "$GTK" in
  gtk3) UBUNTU=22.04 ;;
  gtk4) UBUNTU=24.04 ;;
  *) echo "usage: $0 [gtk3|gtk4] [amd64|arm64]" >&2; exit 2 ;;
esac
case "$ARCH" in
  amd64|arm64) ;;
  *) echo "usage: $0 [gtk3|gtk4] [amd64|arm64]" >&2; exit 2 ;;
esac

cd "$(dirname "$0")/../.."
OUT="bin/linux/ubuntu-$UBUNTU-$GTK-$ARCH"
docker buildx build \
  --platform "linux/$ARCH" \
  -f build/linux/Dockerfile.ubuntu \
  --build-arg UBUNTU="$UBUNTU" \
  --build-arg GTK="$GTK" \
  --output "type=local,dest=$OUT" \
  .
echo "Built $OUT/spydb"
