#!/bin/sh
# Records the demos with tools/capture and builds their data and partials.
# Usage: site/demos/idea/build.sh [/path/to/pravo]   (default: build ./cmd/pravo into a temporary directory)
set -eu
cd "$(dirname "$0")/../../.."
bin=${1:-}
flag=
[ -n "$bin" ] && flag="-bin $bin"
for d in idea; do
  # shellcheck disable=SC2086
  go run ./tools/capture -update $flag "site/demos/$d/page.md"
  # shellcheck disable=SC2086
  go run ./tools/capture -check $flag "site/demos/$d/page.md"
  # shellcheck disable=SC2086
  go run ./tools/capture -json "site/data/$d.json" $flag "site/demos/$d/page.md"
  go run site/demos/idea/mk.go "$d"
done
