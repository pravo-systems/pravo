#!/bin/sh
# Says which parts of the repository a pull request touches, so that CI can leave
# out the jobs that cannot be affected. Everything runs for anything but a pull
# request, and for a change to the workflows or to go.mod.
#
#   sh tools/ci/changes.sh
#
# Writes name=true|false lines to $GITHUB_OUTPUT (stdout when it is not set):
#   code    Go code, its tests, data and tooling changed
#   binary  something that goes into the pravo binary changed
#   docs    the pages of the documentation changed
#   site    the site or its demos changed
#   web     the viewer assets or the web linters changed
#   hcl     the OpenTofu listings or their checker changed
#   platform  something that behaves differently by system changed: files for one
#           system, and the packages that work with files, locks and processes
#   slow    something changed that the scenario with two thousand resources is
#           about: the engine, the state and sealing, the plugins, the commands that
#           seal and sign, or the checks themselves
#   waits   something changed that the pages of the documentation that wait are
#           about: sealing, the keys and the readers, and the locks. These pages sit
#           out a minute or more, one of them generates a key of 8192 bits
#           (tools/ci/pages.sh); main always checks them
#   limit   minutes that a job of a pull request may take before it is cancelled: a
#           job that hangs should not hold the author. Longer when the change runs
#           the slow checks or the tests of the other systems; a run of main, a tag
#           or a dispatch is left twenty
# The PR is the merge commit of GitHub, so its first parent is the base.
set -eu
out=${GITHUB_OUTPUT:-/dev/stdout}
all=false code=false binary=false docs=false site=false web=false hcl=false platform=false slow=false waits=false

if [ "${CI_EVENT:-}" != pull_request ]; then
	all=true
else
	files=$(git diff --name-only HEAD^1 HEAD)
	echo "changed:"; echo "$files" | sed 's/^/  /'
	for f in $files; do
		case $f in
		.github/*|tools/ci/*|go.mod|go.sum|.golangci.yml) all=true ;;
		docs/*) docs=true; hcl=true ;;
		site/demos/scale/*) site=true; hcl=true; slow=true ;;
		site/demos/*) site=true; hcl=true ;;
		site/*) site=true; web=true ;;
		skills/*) code=true; binary=true; docs=true ;;
		tools/_weblint/*) web=true ;;
		tools/hclcheck/*) code=true; hcl=true ;;
		tools/*|actions/*) code=true ;;
		internal/serve/web/*) code=true; binary=true; web=true ;;
		design/*|*.md|LICENSE) ;;
		*_test.go|*/testdata/*) code=true ;;
		*) code=true; binary=true ;;
		esac
		case $f in
		*_test.go|*/testdata/*) ;;
		internal/serve/*|internal/ui/*|internal/telemetry/*|internal/plog/*) ;;
		internal/*|cmd/pravo/protect*|cmd/pravo/roles*|cmd/pravo/id*|cmd/pravo/recovery*|cmd/pravo/unseal*|cmd/pravo/downgrade*) slow=true ;;
		esac
		case $f in
		*_test.go|*/testdata/*) ;;
		internal/protect/*|internal/state/*|internal/filelock/*|internal/engine/*lock*|internal/engine/planseal*|cmd/pravo/protect*|cmd/pravo/roles*|cmd/pravo/id*|cmd/pravo/recovery*|cmd/pravo/unseal*|cmd/pravo/downgrade*) waits=true ;;
		esac
		case $f in
		*.go|go.mod) ;;
		*) continue ;;
		esac
		case $f in
		*_windows*.go|*_darwin*.go|*_linux*.go|*_unix.go|*_posix.go|*_other.go|internal/state/*|internal/filelock/*|internal/plugin/*|internal/protect/*) platform=true ;;
		*) [ -f "$f" ] && grep -q '^//go:build' "$f" && platform=true ;;
		esac
	done
fi
if [ $all = true ]; then code=true binary=true docs=true site=true web=true hcl=true platform=true slow=true waits=true; fi

limit=3
if [ "${CI_EVENT:-}" != pull_request ]; then limit=20
elif [ $all = true ] || [ $platform = true ]; then limit=8
elif [ $slow = true ] || [ $waits = true ]; then limit=5
fi

for n in all code binary docs site web hcl platform slow waits limit; do
	eval "v=\$$n"
	echo "$n=$v" >> "$out"
done
