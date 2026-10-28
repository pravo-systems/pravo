#!/bin/sh
# The static checks of CI. The static job runs them, and a job of main also vets
# the module on each system.
#
#   sh tools/ci/static.sh format      every file is gofmt'd
#   sh tools/ci/static.sh modules     go mod tidy changes nothing
#   sh tools/ci/static.sh build       every package builds on every system and
#                                     architecture that pravo supports
#   sh tools/ci/static.sh vet GOOS    go vet on the files of a system
#
# A pull request leaves the vet to golangci-lint on the packages that it changed
# (tools/ci/lint.sh): a package that it did not change but that stops compiling is
# found by the build and by the tests.
set -eu
what=${1:?usage: sh tools/ci/static.sh format|modules|build|vet GOOS}
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
build)
	# All six at once: they share the module cache and compile different files.
	pids=""
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
		(GOOS=${target%/*} GOARCH=${target#*/} go build ./... && echo "built $target") &
		pids="$pids $!"
	done
	status=0
	for pid in $pids; do wait "$pid" || status=1; done
	exit $status
	;;
vet)
	GOOS=${2:?usage: sh tools/ci/static.sh vet GOOS} GOARCH=amd64 go vet ./...
	;;
*)
	echo "static: unknown check $what" >&2; exit 2
	;;
esac
