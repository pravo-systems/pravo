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
# The PR is the merge commit of GitHub, so its first parent is the base.
set -eu
out=${GITHUB_OUTPUT:-/dev/stdout}
all=false code=false binary=false docs=false site=false web=false hcl=false platform=false

if [ "${CI_EVENT:-}" != pull_request ]; then
	all=true
else
	files=$(git diff --name-only HEAD^1 HEAD)
	echo "changed:"; echo "$files" | sed 's/^/  /'
	for f in $files; do
		case $f in
		.github/*|tools/ci/*|go.mod|go.sum|.golangci.yml) all=true ;;
		docs/*) docs=true; hcl=true ;;
		design/*|*.md|LICENSE) ;;
		site/demos/*) site=true; hcl=true ;;
		site/*) site=true; web=true ;;
		skills/*) code=true; binary=true; docs=true ;;
		tools/_weblint/*) web=true ;;
		tools/hclcheck/*) code=true; hcl=true ;;
		tools/*) code=true ;;
		internal/serve/web/*) code=true; binary=true; web=true ;;
		*_test.go|*/testdata/*) code=true ;;
		*) code=true; binary=true ;;
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
if [ $all = true ]; then code=true binary=true docs=true site=true web=true hcl=true platform=true; fi

for n in all code binary docs site web hcl platform; do
	eval "v=\$$n"
	echo "$n=$v" >> "$out"
done
