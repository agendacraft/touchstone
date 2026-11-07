#!/bin/sh
# Installs the Touchstone CLI from GitHub Releases into your user's local bin
# directory. No root privileges or package manager are required.
#
# Usage: install.sh [--version TAG] [--install-dir DIR] [--dry-run]
#
# The script downloads the release archive for your platform, verifies its
# SHA-256 digest against the checksums file published with the release, and
# only then installs the binary. A failed download or checksum mismatch aborts
# the script before anything is installed.
#
# Environment overrides (documented for mirrors and tests):
#   TOUCHSTONE_VERSION       release tag to install, for example v0.1.0
#   TOUCHSTONE_INSTALL_DIR   target directory, default ~/.local/bin
#   TOUCHSTONE_RELEASE_URL   release base URL, default the Touchstone repository

# shellcheck shell=sh
set -eu

readonly RELEASE_URL_DEFAULT="https://github.com/agendacraft/touchstone/releases"
readonly BINARY_NAME="touchstone"
readonly CHECKSUMS_FILE="checksums.txt"

usage() {
  cat <<'EOF'
Usage: install.sh [--version TAG] [--install-dir DIR] [--dry-run]

Options:
  --version TAG            Release tag to install, for example v0.1.0.
                           Defaults to $TOUCHSTONE_VERSION, then the latest release.
  --install-dir DIR        Target directory.
                           Defaults to $TOUCHSTONE_INSTALL_DIR, then ~/.local/bin.
  --install-directory DIR  Alias for --install-dir.
  --dry-run                Print what would be installed, then exit.
  -h, --help               Show this help.

Environment:
  TOUCHSTONE_VERSION       Same as --version.
  TOUCHSTONE_INSTALL_DIR   Same as --install-dir.
  TOUCHSTONE_RELEASE_URL   Release base URL (testing and mirror override).
EOF
}

info() { printf 'install.sh: %s\n' "$1"; }
fail() { printf 'install.sh: error: %s\n' "$1" >&2; exit 1; }

fetch() {
  curl -fsSL --retry 3 --retry-delay 1 -o "$2" "$1" || fail "download failed: $1"
}

# detect_platform prints the detected platform as "os/arch" and fails on
# platforms no release archive is built for.
detect_platform() {
  kernel=$(uname -s)
  case $kernel in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported operating system: $kernel. Supported: macOS, Linux." ;;
  esac
  machine=$(uname -m)
  case $machine in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "unsupported architecture: $machine. Supported: amd64, arm64." ;;
  esac
  printf '%s/%s\n' "$os" "$arch"
}

# resolve_latest_version sets $version to the newest published release tag by
# following the /releases/latest redirect to its final ".../tag/<tag>" URL.
resolve_latest_version() {
  effective=$(curl -fsSI -L -o /dev/null -w '%{url_effective}' "$release_url/latest") ||
    fail "could not resolve the latest release; pin one with --version"
  version=${effective##*/}
}

# checksum_for prints the SHA-256 digest recorded for FILENAME in CHECKSUMS_FILE.
checksum_for() {
  recorded=$(awk -v name="$1" '$2 == name { print $1 }' "$2")
  case $recorded in
    '') fail "checksums file has no entry for $1" ;;
  esac
  lines=$(printf '%s\n' "$recorded" | wc -l | tr -d ' ')
  [ "$lines" -eq 1 ] || fail "checksums file has $lines entries for $1, expected exactly one"
  recorded=$(printf '%s' "$recorded" | tr 'A-F' 'a-f')
  case $recorded in
    *[!0-9a-f]*) fail "checksum for $1 is not hexadecimal: $recorded" ;;
  esac
  [ "${#recorded}" -eq 64 ] || fail "checksum for $1 is not a SHA-256 digest: $recorded"
  printf '%s\n' "$recorded"
}

# sha256_of prints the SHA-256 digest of FILE using the first available tool.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    fail "need sha256sum or shasum to verify the download"
  fi
}

version_flag=${TOUCHSTONE_VERSION:-}
install_dir=${TOUCHSTONE_INSTALL_DIR:-}
release_url=${TOUCHSTONE_RELEASE_URL:-$RELEASE_URL_DEFAULT}
dry_run=0

while [ "$#" -gt 0 ]; do
  case $1 in
    --version)
      [ "$#" -ge 2 ] || fail "option $1 requires an argument"
      version_flag=$2
      shift 2
      ;;
    --version=*)
      version_flag=${1#*=}
      shift
      ;;
    --install-dir | --install-directory)
      [ "$#" -ge 2 ] || fail "option $1 requires an argument"
      install_dir=$2
      shift 2
      ;;
    --install-dir=* | --install-directory=*)
      install_dir=${1#*=}
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
done
[ "$#" -eq 0 ] || fail "unexpected extra arguments: $*"

[ -n "$install_dir" ] || install_dir=$HOME/.local/bin
[ -n "$version_flag" ] || resolve_latest_version
version=$version_flag
case $version in
  v*) ;;
  *) version="v$version" ;;
esac
case $version in
  v | *[!0-9A-Za-z._-]*) fail "invalid version tag: $version" ;;
esac

platform=$(detect_platform)
os=${platform%/*}
arch=${platform#*/}

archive_name="${BINARY_NAME}_${version}_${os}_${arch}.tar.gz"
archive_url="$release_url/download/$version/$archive_name"
checksums_url="$release_url/download/$version/$CHECKSUMS_FILE"

if [ "$dry_run" -eq 1 ]; then
  info "dry run - nothing was downloaded or installed"
  info "version: $version"
  info "platform: $os/$arch"
  info "archive: $archive_url"
  info "checksums: $checksums_url"
  info "install directory: $install_dir"
  info "binary: $install_dir/$BINARY_NAME"
  exit 0
fi

command -v curl >/dev/null 2>&1 || fail "curl is required to download the release"
workdir=$(mktemp -d) || fail "could not create a temporary directory"
trap 'rm -rf "$workdir"' EXIT

info "downloading $archive_url"
fetch "$archive_url" "$workdir/$archive_name"
info "downloading $checksums_url"
fetch "$checksums_url" "$workdir/$CHECKSUMS_FILE"

expected=$(checksum_for "$archive_name" "$workdir/$CHECKSUMS_FILE")
actual=$(sha256_of "$workdir/$archive_name")
if [ "$actual" != "$expected" ]; then
  fail "checksum mismatch for $archive_name: expected $expected, got $actual"
fi
info "checksum verified: $actual"

tar -xzf "$workdir/$archive_name" -C "$workdir" || fail "could not extract $archive_name"
[ -f "$workdir/$BINARY_NAME" ] || fail "$archive_name does not contain a $BINARY_NAME binary"

mkdir -p "$install_dir" || fail "could not create the install directory: $install_dir"
temp_target="$install_dir/.$BINARY_NAME.tmp"
mv -f "$workdir/$BINARY_NAME" "$temp_target"
mv -f "$temp_target" "$install_dir/$BINARY_NAME"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    info "add $install_dir to your PATH, for example in your shell profile:"
    info "  export PATH=\"$install_dir:\$PATH\""
    ;;
esac
info "installed $version to $install_dir/$BINARY_NAME"
info "verify with: $install_dir/$BINARY_NAME version"
info "uninstall with: rm $install_dir/$BINARY_NAME"
