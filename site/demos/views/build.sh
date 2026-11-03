#!/bin/sh
# Makes the pictures of the viewer on the landing page, the tour and the bands
# of environments and self-service, from the shots of tools/serveshots, and
# records the viewer that they show in site/demos/views/viewer.stamp. A test
# fails when the interface of the viewer changed since.
#
# Make the shots first, from the root of the module:
#   go build -o bin/pravo ./cmd/pravo
#   go run ./tools/serveshots -bin bin/pravo -out docs/img/serve
#   sh site/demos/views/build.sh
# then look at the callouts of site/landing/12-views.html, which are placed by
# hand on what they name, and at the cuts below, which follow the layout of the
# page. The conversion needs python3 with Pillow ($PYTHON names another one).
set -eu
cd "$(dirname "$0")/../../.."
py=${PYTHON:-python3}
shot() { # shot theme left top right bottom width height name
	"$py" -c 'import sys; from PIL import Image
a = sys.argv
im = Image.open(a[1]).convert("RGB")
if a[3] != "-":
    im = im.crop(tuple(int(v) for v in a[3:7]))
im.resize((int(a[7]), int(a[8])), Image.LANCZOS).save(a[2], "WEBP", quality=82, method=6)' \
		"docs/img/serve/$1-$2.png" "site/assets/shots/$9-$2.webp" "$3" "$4" "$5" "$6" "$7" "$8"
}
for theme in dark light; do
	# The five pages of the tour: the whole shot, smaller.
	shot overview "$theme" - - - - 2160 1350 graph
	shot plan "$theme" - - - - 2160 1350 plan
	shot policies "$theme" - - - - 2160 1350 policies
	shot members "$theme" - - - - 2160 1350 access
	shot traces "$theme" - - - - 2160 1350 traces
	# Cut to the flow, below the bar and without the side margins.
	shot environments-flow "$theme" 150 185 2150 1140 2160 1031 environments
	# Cut to the wizard, below the head of the section.
	shot templates-form "$theme" 150 525 2730 1735 2160 1013 self-service
done
cp tools/serveshots/viewer.stamp site/demos/views/viewer.stamp
