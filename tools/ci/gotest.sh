#!/bin/sh
# Runs go test for one group of packages, leaving out the packages that a pull
# request cannot affect (tools/ci/affected.sh).
#
#   sh tools/ci/gotest.sh GROUP [INDEX COUNT] [go test flags]
#
# GROUP is engine, state, cmd, serve, other (the rest of the module, without
# tools) or nonengine (internal and cmd without the engine). With INDEX and COUNT
# the group is dealt out to COUNT jobs: a group of one package by the tests of
# the package (tools/ci/test-shard.sh), a larger one by package.
set -eu
group=$1; shift
idx="" count=""
case ${1:-} in
[0-9]*) idx=$1 count=$2; shift 2 ;;
esac
mod=$(go list -m)
all=$(go list ./...)
members=$(echo "$all" | sed "s|^$mod/||" | grep -v '^tools/' | grep -E "$(case $group in
	engine) echo '^internal/engine$' ;;
	state) echo '^internal/state(/|$)' ;;
	cmd) echo '^cmd(/|$)' ;;
	serve) echo '^internal/serve$' ;;
	nonengine) echo '^(internal|cmd)(/|$)' ;;
	other) echo '^' ;;
	*) echo "unknown group $group" >&2; exit 2 ;;
esac)" | { if [ "$group" = nonengine ]; then grep -v '^internal/engine$'; elif [ "$group" = other ]; then grep -v -E '^(internal/(engine|state|serve)(/|$)|cmd(/|$))'; else cat; fi; })
affected=$(sh "$(dirname "$0")/affected.sh")
if [ "$affected" = ALL ]; then
	pkgs=$members
else
	pkgs=$(echo "$members" | while read -r p; do echo "$affected" | grep -qx "$mod/$p" && echo "$p"; done || true)
fi
if [ -z "$pkgs" ]; then
	echo "no package of $group is affected by this change"; exit 0
fi
if [ -n "$count" ]; then
	if [ "$(echo "$pkgs" | wc -l | tr -d ' ')" = 1 ]; then
		exec sh "$(dirname "$0")/test-shard.sh" "./$pkgs" "$idx" "$count" "$@"
	fi
	pkgs=$(echo "$pkgs" | awk -v i="$idx" -v c="$count" '(NR-1)%c==i')
	[ -n "$pkgs" ] || { echo "shard $idx of $count has no packages"; exit 0; }
fi
echo "go test: $(echo "$pkgs" | wc -l | tr -d ' ') packages"
exec go test -count=1 "$@" $(echo "$pkgs" | sed 's|^|./|')
