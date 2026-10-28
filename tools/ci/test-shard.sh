#!/bin/sh
# Runs one shard of the tests of a package, so that a slow package can be run
# by several jobs at once.
#
#   sh tools/ci/test-shard.sh PACKAGE INDEX COUNT [go test flags]
#
# The top-level tests of PACKAGE, in the order that go test lists them, are
# dealt out to COUNT shards; shard INDEX (0 to COUNT-1) runs every COUNT-th.
set -eu
[ $# -ge 3 ] || { echo "usage: sh tools/ci/test-shard.sh PACKAGE INDEX COUNT [go test flags]" >&2; exit 2; }
pkg=$1 idx=$2 count=$3
shift 3
names=$(go test -list '.*' "$pkg" | grep '^Test' | awk -v i="$idx" -v c="$count" '(NR-1)%c==i')
if [ -z "$names" ]; then
	echo "shard $idx of $count of $pkg has no tests"
	exit 0
fi
echo "shard $idx of $count of $pkg: $(echo "$names" | wc -l | tr -d ' ') tests"
pattern="^($(echo "$names" | paste -sd'|' -))\$"
exec go test -count=1 -run "$pattern" "$@" "$pkg"
