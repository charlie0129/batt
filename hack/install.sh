#!/bin/bash

set -o errexit
set -o pipefail

bold="\033[1m"
reset="\033[0m"

# must run on Apple Silicon
if [[ ! $(sysctl -n machdep.cpu.brand_string) =~ "Apple" ]]; then
  echo "This script must be run on Apple Silicon."
  exit 1
fi

# must have curl
if ! command -v curl >/dev/null; then
  echo "curl is required"
  exit 1
fi

if [[ "$1" == "--help" || "$1" == "-h" ]]; then
  echo "This script installs batt on your macOS."
  echo "Usage: $0 [-y]"
  echo "  -y: auto confirm"
  echo "Environment variables:"
  echo "  PREFIX: install location (default: /usr/local/bin)"
  echo "  VERSION: version to install (default: latest stable release)"
  exit 0
fi

# check -y
if [[ "$1" == "-y" ]]; then
  AUTO_CONFIRM=true
fi

confirm() {
  if [[ "$AUTO_CONFIRM" == "true" ]]; then
    return 0
  fi
  while true; do
    echo -n "$1 [y/n]: "
    read -r -n 1 REPLY
    case $REPLY in
    [yY])
      echo
      return 0
      ;;
    [nN])
      echo
      return 1
      ;;
    *) printf " is invalid. Press 'y' to continue; 'n' to exit. \n" ;;
    esac
  done
}

info() {
    echo -e "$(date +'%Y-%m-%d %H:%M:%S') \033[34m[INFO]\033[0m $*"
}

# If the full path to batt has Homebrew prefix ("/opt"), stop here.
if which batt 2>/dev/null | grep -q /opt; then
  echo "You have batt installed via Homebrew. Please use Homebrew to upgrade batt:"
  echo "  - brew update"
  echo "  - sudo brew services stop batt"
  echo "  - brew upgrade batt"
  echo "  - sudo brew services start batt"
  echo "If you want to use this script to install batt, please uninstall Homebrew-installed batt first by:"
  echo "  - sudo brew services stop batt"
  echo "  - brew uninstall batt"
  echo "  - sudo rm -rf /opt/homebrew/Cellar/batt"
  exit 1
fi

if [[ -z "$VERSION" ]]; then
  tarball_suffix="darwin-arm64.tar.gz"

  info "Querying latest batt release..."
  # jq is intentionally not used here because it is not available on macOS by default
  res=$(curl -fsSL https://api.github.com/repos/charlie0129/batt/releases/latest)
  tarball_url=$(echo "$res" |
    grep -o "browser_download_url.*$tarball_suffix" |
    grep -o "https.*")
  version=$(echo "$res" | grep -o "tag_name.*" | grep -o "\"v.*\"")
  version=${version//\"/}
  info "Latest stable version is ${version}."
else
  version="$VERSION"
  tarball_url="https://github.com/charlie0129/batt/releases/download/$version/batt-$version-darwin-arm64.tar.gz"
fi

launch_daemon="/Library/LaunchDaemons/cc.chlc.batt.plist"

# Uninstall old versions (if present)
if [[ -f "$launch_daemon" ]]; then
  echo "You have old versions of batt installed, which need to be uninstalled before installing the latest version. We will uninstall it for you now."
  confirm "Is this OK?" || exit 0
  info "Stopping old versions of batt..."
  sudo launchctl unload "$launch_daemon"
  sudo rm -f "$launch_daemon"
  old_batt_bin="$(which batt || true)"
  if [[ -f "$old_batt_bin" ]]; then
    info "Removing old versions of batt..."
    sudo rm -f "$old_batt_bin"
  fi
fi

if [[ -z "$PREFIX" ]]; then
  PREFIX="/usr/local/bin"
fi

echo -e "Will install batt ${bold}${version}${reset} to ${bold}$PREFIX${reset} (to change install location, set \$PREFIX environment variable)."
confirm "Ready to install?" || exit 0
info "Downloading batt ${version} from $tarball_url and installing to $PREFIX..."
sudo mkdir -p "$PREFIX"
curl -fsSL "$tarball_url" | sudo tar -xzC "$PREFIX" batt
sudo xattr -r -d com.apple.quarantine "$PREFIX/batt"

install_cmd="sudo $PREFIX/batt install --allow-non-root-access"
info "Installing batt..."
echo "- $install_cmd"
$install_cmd

info "Installation finished."
echo "Further instructions:"
echo '- If you see an alert says "batt cannot be opened because XXX", please go to System Preferences -> Security & Privacy -> General -> Open Anyway.'
echo -e "- Be sure to ${bold}disable${reset} macOS's optimized charging: Go to System Preferences -> Battery -> uncheck Optimized battery charging."
echo '- To set charge limit to 80%, run "batt limit 80".'
echo '- To see batt help: run "batt help".'
echo '- To see disable charge limit: run "batt disable".'
echo '- To uninstall: run "sudo batt uninstall" and follow the instructions.'
echo '- To upgrade: just run this script again when a new version is released.'
