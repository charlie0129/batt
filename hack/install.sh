#!/bin/bash

set -o errexit
set -o pipefail

# must have curl
if ! command -v curl >/dev/null; then
  echo "curl is required"
  exit 1
fi

launch_daemon="/Library/LaunchDaemons/cc.chlc.batt.plist"

# Uninstall old versions (if present)
if [[ -f "$launch_daemon" ]]; then
  echo "Removing old version of batt..."
  sudo launchctl unload "$launch_daemon"
  sudo rm -f "$launch_daemon"
fi

tarball_suffix="darwin-arm64.tar.gz"

echo -n "Querying latest batt release..."
# jq is intentionally not used here because it is not available on macOS by default
res=$(curl -fsSL https://api.github.com/repos/charlie0129/batt/releases/latest)
tarball_url=$(echo "$res" |
  grep -o "browser_download_url.*$tarball_suffix" |
  grep -o "https.*")
version=$(echo "$res" | grep -o "tag_name.*" | grep -o "\"v.*\"")
echo " $version"

if [[ -z "$PREFIX" ]]; then
  PREFIX="/usr/local/bin"
fi

echo "Downloading batt from $tarball_url to $PREFIX (to change install location, set \$PREFIX env)"
sudo mkdir -p "$PREFIX"
curl -L "$tarball_url" | sudo tar -xzC "$PREFIX"

install_cmd="sudo $PREFIX/batt install --allow-non-root-access"
echo "Installing batt..."
echo "- $install_cmd"
$install_cmd
