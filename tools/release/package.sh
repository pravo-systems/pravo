#!/bin/sh
# Builds pravo for one platform and packs it into an archive.
#
#   sh tools/release/package.sh VERSION OS ARCH OUTDIR
#
# VERSION is the release version without the leading v (0.1.0, 0.1.0-rc1). OS
# and ARCH are Go names: linux, darwin or windows; amd64 or arm64. The archive
# is OUTDIR/pravo_VERSION_OS_ARCH.tar.gz, or .zip for windows, and holds the
# binary, LICENSE and README.md. The build is static and does not depend on the
# path it was built in.
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
if [ "$goos" = windows ]; then
	(cd "$stage" && zip -q -r "$out/$name.zip" "$name")
	echo "$out/$name.zip"
else
	(cd "$stage" && tar -czf "$out/$name.tar.gz" "$name")
	echo "$out/$name.tar.gz"
fi
