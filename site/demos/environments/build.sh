#!/bin/sh
# Records the environments scenario and renders the environments band of the
# landing page from it. Run from the root of the module:
#   sh site/demos/environments/build.sh [/path/to/pravo]
# The picture of the viewer is the shot environments-flow of tools/serveshots,
# made smaller and written as WebP; make the shot first when the viewer
# changed. The conversion needs python3 with Pillow.
set -eu
cd "$(dirname "$0")/../../.."
bin=${1:-}
flag=
[ -n "$bin" ] && flag="-bin $bin"
# shellcheck disable=SC2086
go run ./tools/capture -update $flag site/demos/environments/page.md
# shellcheck disable=SC2086
go run ./tools/capture -check $flag site/demos/environments/page.md
# shellcheck disable=SC2086
go run ./tools/capture -json site/data/environments.json $flag site/demos/environments/page.md
go run site/demos/idea/mk.go environments
