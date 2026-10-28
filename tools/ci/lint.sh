#!/bin/sh
# Runs golangci-lint on the packages that a pull request changed, and on the whole
# module for anything else.
#
#   sh tools/ci/lint.sh GOLANGCI_LINT_BINARY
set -eu
bin=$1
mod=$(go list -m)
changed=$(sh "$(dirname "$0")/affected.sh" changed)
if [ "$changed" = ALL ]; then
	exec "$bin" run ./...
fi
if [ -z "$changed" ]; then
	echo "no Go package changed"; exit 0
fi
# A changed directory that holds no Go files any more is not a package.
pkgs=""
for p in $changed; do
	d=${p#"$mod"}; d=.${d:-/}
	ls "$d"/*.go >/dev/null 2>&1 && pkgs="$pkgs $d"
done
[ -n "$pkgs" ] || { echo "no Go package changed"; exit 0; }
echo "golangci-lint: $pkgs"
exec "$bin" run $pkgs
