#!/bin/sh
# Runs go test for one group of packages, leaving out the packages that a pull
# request cannot affect (tools/ci/affected.sh).
#
#   sh tools/ci/gotest.sh GROUP [in|out "FILE..."] [go test flags]
#
# GROUP is engine, state, cmd, serve, commands (cmd and the viewer), libraries
# (the other packages of internal without the engine and the viewer), or other
# (the rest of the module, without tools). With in or out and test files, a group
# of one package runs only the tests of these files, or all the others
# (tools/ci/test-files.sh), so that jobs can be named for what they cover.
set -eu
group=$1; shift
mode="" files=""
case ${1:-} in
in|out) mode=$1 files=$2; shift 2 ;;
esac
mod=$(go list -m)
all=$(go list ./...)
members=$(echo "$all" | sed "s|^$mod/||" | grep -v '^tools/' | grep -E "$(case $group in
	engine) echo '^internal/engine$' ;;
	state) echo '^internal/state(/|$)' ;;
	cmd) echo '^cmd(/|$)' ;;
	serve) echo '^internal/serve$' ;;
	commands) echo '^(cmd|internal/serve)(/|$)' ;;
	libraries) echo '^internal/' ;;
	other) echo '^' ;;
	*) echo "unknown group $group" >&2; exit 2 ;;
esac)" | { if [ "$group" = libraries ]; then grep -v -E '^internal/(engine|serve)(/|$)'; elif [ "$group" = other ]; then grep -v -E '^(internal/(engine|state|serve)(/|$)|cmd(/|$))'; else cat; fi; })
affected=$(sh "$(dirname "$0")/affected.sh")
if [ "$affected" = ALL ]; then
	pkgs=$members
else
	pkgs=$(echo "$members" | while read -r p; do echo "$affected" | grep -qx "$mod/$p" && echo "$p"; done || true)
fi
if [ -z "$pkgs" ]; then
	echo "no package of $group is affected by this change"; exit 0
fi
if [ -n "$mode" ]; then
	if [ "$(echo "$pkgs" | wc -l | tr -d ' ')" != 1 ]; then
		echo "gotest: $mode needs a group of one package, and $group has several" >&2; exit 2
	fi
	exec sh "$(dirname "$0")/test-files.sh" "./$pkgs" "$mode" "$files" "$@"
fi
echo "go test: $(echo "$pkgs" | wc -l | tr -d ' ') packages"
exec go test -count=1 "$@" $(echo "$pkgs" | sed 's|^|./|')
