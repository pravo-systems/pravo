#!/bin/sh
# Installs pravo from a GitHub release.
#
#   curl -fsSL https://pravo.systems/install.sh | sh
#   curl -fsSL https://pravo.systems/install.sh | PRAVO_VERSION=v0.1.0-rc1 sh
#
# It finds the archive for this system, checks it against SHA256SUMS and puts
# the binary in $PRAVO_INSTALL_DIR, which is ~/.local/bin by default. A mirror
# of the releases is named by PRAVO_DOWNLOAD_BASE, with a directory for each tag
# below it. It needs
# curl, tar and sha256sum or shasum. A file that curl fetches is not marked
# as downloaded from the internet, so macOS does not block it.
set -eu

repo=pravo-systems/pravo
dir=${PRAVO_INSTALL_DIR:-$HOME/.local/bin}

fail() { echo "install: $*" >&2; exit 1; }

case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "this script installs pravo on Linux and macOS; on Windows use install.ps1" ;;
esac
case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "no build of pravo for $(uname -m)" ;;
esac

tag=${PRAVO_VERSION:-}
if [ -z "$tag" ]; then
	tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$tag" ] || fail "no release found; set PRAVO_VERSION to a tag such as v0.1.0-rc1"
fi
version=${tag#v}
name=pravo_${version}_${os}_${arch}
base=${PRAVO_DOWNLOAD_BASE:-https://github.com/$repo/releases/download}/$tag

tmp=$(mktemp -d "${TMPDIR:-/tmp}/pravo-install.XXXXXX")
trap 'rm -r "$tmp"' EXIT
echo "Downloading pravo $version for $os/$arch"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "cannot download $base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "cannot download $base/SHA256SUMS"

want=$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | awk '{print $1}')
[ -n "$want" ] || fail "SHA256SUMS has no entry for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
else
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
fi
[ "$got" = "$want" ] || fail "the checksum of the download is $got, want $want"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dir"
cp "$tmp/$name/pravo" "$dir/pravo"
chmod 755 "$dir/pravo"
echo "Installed $("$dir/pravo" version | head -n 1) in $dir"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "Add $dir to your PATH:  export PATH=\"$dir:\$PATH\"" ;;
esac
