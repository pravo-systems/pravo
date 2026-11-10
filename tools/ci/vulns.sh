#!/bin/sh
# Fails when the code of pravo reaches a known vulnerability, in the Go that
# builds it (the line `toolchain` of go.mod) or in a module that it requires.
#
#   sh tools/ci/vulns.sh
#
# A vulnerability in the standard library is fixed by a newer patch release of Go
# on the line `toolchain` of go.mod, and one in a module by `go get module@fixed`.
# The release is built with that Go (release.yml), so this is also the answer for
# the binaries that people download.
set -eu
# The tool is not a module of pravo, and is run outside go.mod.
GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
