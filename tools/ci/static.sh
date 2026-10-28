#!/bin/sh
# The static checks of CI. Each job of the static matrix runs some of them.
#
#   sh tools/ci/static.sh format      every file is gofmt'd
#   sh tools/ci/static.sh modules     go mod tidy changes nothing
#   sh tools/ci/static.sh check GOOS  every package builds for both architectures of
#                                     the system, and is vetted
#   sh tools/ci/static.sh job SYSTEM  what the job of a system does: for linux, the
#                                     first two as well; macos is darwin
#
# A pull request leaves the vet of the whole module to golangci-lint on the
# packages that it changed (tools/ci/lint.sh): a package that it did not change but
# that stops compiling is found by the build and by the tests. Main, the release and
# a change to CI itself (tools/ci/changes.sh) vet everything.
set -eu
what=${1:?usage: sh tools/ci/static.sh format|modules|check GOOS|job SYSTEM}
case $what in
format)
	unformatted=$(gofmt -l .)
	if [ -n "$unformatted" ]; then
		echo "These files need gofmt:"; echo "$unformatted"; exit 1
	fi
	echo "gofmt: nothing to do"
	;;
modules)
	go mod tidy
	git diff --exit-code go.mod go.sum
	;;
check)
	goos=${2:?usage: sh tools/ci/static.sh check GOOS}
	# Both architectures at once: they share the module cache and compile different files.
	pids=""
	for arch in amd64 arm64; do
		(GOOS=$goos GOARCH=$arch go build ./... && echo "built $goos/$arch") &
		pids="$pids $!"
	done
	status=0
	for pid in $pids; do wait "$pid" || status=1; done
	[ $status = 0 ] || exit $status
	if [ "${CI_EVENT:-}" = pull_request ] && [ "${CI_ALL:-}" != true ]; then
		echo "go vet: left to golangci-lint on the changed packages"
	else
		GOOS=$goos GOARCH=amd64 go vet ./...
		echo "vetted $goos/amd64"
	fi
	;;
job)
	here=$(dirname "$0")
	case ${2:?usage: sh tools/ci/static.sh job linux|macos|windows} in
	linux) sh "$here/static.sh" format && sh "$here/static.sh" modules && exec sh "$here/static.sh" check linux ;;
	macos) exec sh "$here/static.sh" check darwin ;;
	windows) exec sh "$here/static.sh" check windows ;;
	*) echo "static: unknown system $2" >&2; exit 2 ;;
	esac
	;;
*)
	echo "static: unknown check $what" >&2; exit 2
	;;
esac
