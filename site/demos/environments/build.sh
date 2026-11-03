#!/bin/sh
# Records the environments scenario and renders the environments band of the
# landing page from it. Run from the root of the module:
#   sh site/demos/environments/build.sh [/path/to/pravo]
# The picture of the viewer is the shot environments-flow of tools/serveshots,
# cut to the flow (below the bar, without the side margins), made smaller and
# written as WebP; make the shot first when the viewer changed, and check the
# cut. The conversion needs python3 with Pillow.
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
for theme in dark light; do
	python3 -c 'import sys; from PIL import Image; Image.open(sys.argv[1]).convert("RGB").crop((150, 185, 2150, 1140)).resize((2160, 1031), Image.LANCZOS).save(sys.argv[2], "WEBP", quality=82, method=6)' \
		"docs/img/serve/environments-flow-$theme.png" "site/assets/shots/environments-$theme.webp"
done
go run site/demos/idea/mk.go environments
