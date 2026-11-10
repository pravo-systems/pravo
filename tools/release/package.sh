#!/bin/sh
# Builds pravo for one platform and packs it into an archive.
#
#   sh tools/release/package.sh VERSION OS ARCH OUTDIR
#
# VERSION is the release version without the leading v (0.1.0, 0.1.0-rc1). OS
# and ARCH are Go names: linux, darwin or windows; amd64 or arm64. The archive
# is OUTDIR/pravo_VERSION_OS_ARCH.tar.gz, or .zip for windows, and holds the
# binary, LICENSE and README.md. The build is static and does not depend on the
# path it was built in, and so is the archive: the files have one time (the
# time of the commit, or SOURCE_DATE_EPOCH), one owner and one order, so that
# two builds of one commit give the same archive and the same checksum.
set -eu
[ $# -eq 4 ] || { echo "usage: sh tools/release/package.sh VERSION OS ARCH OUTDIR" >&2; exit 2; }
version=$1 goos=$2 goarch=$3 out=$4
root=$(cd "$(dirname "$0")/../.." && pwd)
name=pravo_${version}_${goos}_${goarch}
stage=$(mktemp -d "${TMPDIR:-/tmp}/pravo-release.XXXXXX")
trap 'rm -r "$stage"' EXIT
mkdir -p "$out" "$stage/$name"
bin=pravo
[ "$goos" = windows ] && bin=pravo.exe
(
	cd "$root"
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
		-ldflags "-s -w -X main.Version=$version" -o "$stage/$name/$bin" ./cmd/pravo
)
cp "$root/LICENSE" "$root/README.md" "$stage/$name/"
out=$(cd "$out" && pwd)
# One time for every file: the commit's, unless the caller names one.
epoch=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct 2>/dev/null || echo 0)}
stamp=$(date -u -d "@$epoch" +%Y%m%d%H%M.%S 2>/dev/null || date -u -r "$epoch" +%Y%m%d%H%M.%S)
chmod 644 "$stage/$name/LICENSE" "$stage/$name/README.md"
chmod 755 "$stage/$name" "$stage/$name/$bin"
touch -t "$stamp" "$stage/$name/$bin" "$stage/$name/LICENSE" "$stage/$name/README.md" "$stage/$name"
# The files in a fixed order, the same on GNU tar and on the tar of macOS.
files="$name/LICENSE $name/README.md $name/$bin"
if [ "$goos" = windows ]; then
	(cd "$stage" && TZ=UTC zip -q -X "$out/$name.zip" $files)
	echo "$out/$name.zip"
else
	own=
	if tar --version 2>/dev/null | grep -q 'GNU tar'; then
		own="--owner=0 --group=0 --numeric-owner"
	else
		own="--uid 0 --gid 0 --numeric-owner"
	fi
	# shellcheck disable=SC2086
	(cd "$stage" && tar $own -cf - $files | gzip -n -9 > "$out/$name.tar.gz")
	echo "$out/$name.tar.gz"
fi
