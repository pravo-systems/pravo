#!/bin/sh
# Makes the five pictures of the viewer band of the landing page from the shots
# of tools/serveshots: each is the PNG at 2880x1800, made smaller to 2160x1350
# and written as WebP. Make the shots first when the viewer changed, and move
# the numbered callouts of site/landing/12-views.html onto what they point at.
# Run from the root of the module:
#   sh site/demos/views/build.sh
# The conversion needs python3 with Pillow.
set -eu
cd "$(dirname "$0")/../../.."
for theme in dark light; do
	for pair in graph:overview plan:plan policies:policies access:members traces:traces; do
		"${PYTHON:-python3}" -c 'import sys; from PIL import Image; Image.open(sys.argv[1]).convert("RGB").resize((2160, 1350), Image.LANCZOS).save(sys.argv[2], "WEBP", quality=82, method=6)' \
			"docs/img/serve/${pair#*:}-$theme.png" "site/assets/shots/${pair%%:*}-$theme.webp"
	done
done
