#!/bin/sh
# Records the hero scenario and renders the hero band of the landing page from it.
# Run from the root of the module:
#   sh site/demos/hero/build.sh [/path/to/pravo]
set -eu
cd "$(dirname "$0")/../../.."
bin=${1:-}
flag=
[ -n "$bin" ] && flag="-bin $bin"
# shellcheck disable=SC2086
go run ./tools/capture -update $flag site/demos/hero/page.md
# shellcheck disable=SC2086
go run ./tools/capture -check $flag site/demos/hero/page.md
# shellcheck disable=SC2086
go run ./tools/capture -json site/data/hero.json $flag site/demos/hero/page.md
go run site/demos/idea/mk.go hero
