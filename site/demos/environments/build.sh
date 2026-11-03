#!/bin/sh
# Records the environments scenario and renders the environments band of the
# landing page from it. Run from the root of the module:
#   sh site/demos/environments/build.sh [/path/to/pravo]
# The picture of the viewer is made by site/demos/views/build.sh from the shots
# of tools/serveshots; make the shots first when the viewer changed.
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
sh site/demos/views/build.sh
go run site/demos/idea/mk.go environments
