#!/bin/sh
# Records the self-service scenario and renders the self-service band of the
# landing page from it. Run from the root of the module:
#   sh site/demos/self-service/build.sh [/path/to/pravo]
# The picture of the form is the shot templates-form of tools/serveshots,
# cut to the wizard (below the head of the section, without the side margins),
# made smaller and written as WebP; make the shot first when the viewer
# changed, and check the cut. The conversion needs python3 with Pillow.
set -eu
cd "$(dirname "$0")/../../.."
bin=${1:-}
flag=
[ -n "$bin" ] && flag="-bin $bin"
# shellcheck disable=SC2086
go run ./tools/capture -update $flag site/demos/self-service/page.md
# shellcheck disable=SC2086
go run ./tools/capture -check $flag site/demos/self-service/page.md
# shellcheck disable=SC2086
go run ./tools/capture -json site/data/self-service.json $flag site/demos/self-service/page.md
for theme in dark light; do
	python3 -c 'import sys; from PIL import Image; Image.open(sys.argv[1]).convert("RGB").crop((150, 455, 2730, 1735)).resize((2160, 1072), Image.LANCZOS).save(sys.argv[2], "WEBP", quality=82, method=6)' \
		"docs/img/serve/templates-form-$theme.png" "site/assets/shots/self-service-$theme.webp"
done
go run site/demos/idea/mk.go self-service
