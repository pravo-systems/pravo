#!/bin/sh
# Runs the tests of a package that are defined in some of its files, or all the
# others, so that a job can be named for what it covers.
#
#   sh tools/ci/test-files.sh PACKAGE in|out "FILE FILE..." [go test flags]
#
# The files are those of the package directory, without the directory, and may
# be patterns such as protect_*_test.go. With
# "out" it runs every test that the files do not define, so the jobs of a package
# together run all of its tests whatever is added to it.
set -eu
[ $# -ge 3 ] || { echo "usage: sh tools/ci/test-files.sh PACKAGE in|out \"FILE...\" [go test flags]" >&2; exit 2; }
pkg=$1 mode=$2 files=$3
shift 3
dir=$pkg
chosen=""
for f in $files; do
	found=""
	for m in $dir/$f; do
		[ -f "$m" ] && { chosen="$chosen $m"; found=1; }
	done
	[ -n "$found" ] || { echo "test-files: $dir/$f matches no file" >&2; exit 2; }
done
mine=$(grep -h -o -E '^func (Test|Fuzz|Example)[A-Za-z0-9_]*' $chosen | sed 's/^func //' | sort -u)
all=$(go test -list '.*' "$pkg" | grep -E '^(Test|Fuzz|Example)' | sort -u)
if [ "$mode" = in ]; then
	names=$mine
else
	names=$(printf '%s\n' "$all" | grep -v -x -F "$mine" || true)
fi
if [ -z "$names" ]; then
	echo "no tests to run for $pkg ($mode: $files)"
	exit 0
fi
echo "$pkg ($mode: $files): $(echo "$names" | wc -l | tr -d ' ') tests"
exec go test -count=1 -run "^($(echo "$names" | paste -sd'|' -))\$" "$@" "$pkg"
