#!/bin/sh
# Makes the pictures that the site shows of the viewer: the screenshots of the
# documentation (docs/img/serve) and the pictures of the landing page
# (site/assets/shots). They are not stored in git, since every run changes every
# picture a little; the site is published from a run of this script.
#
#   sh tools/ci/pictures.sh [real|placeholders]
#
# real          (default) takes every shot in headless Chrome, JOBS at a time
#               (default: one for each processor, up to four), needs bin/pravo
#               and Chrome ($CHROME names it), and fails when a shot fails
# placeholders  writes flat panels of the right size: enough for the site to
#               build and for its links to resolve, with no Chrome and no pravo
#
# Prints a warning, and does not fail, when the viewer changed since the callouts
# of the landing page were checked.
set -eu
cd "$(dirname "$0")/../.."
mode=${1:-real}
python3 -c 'import PIL' 2>/dev/null || python3 -m pip install --quiet pillow
case $mode in
placeholders) go run ./tools/serveshots -out docs/img/serve -placeholders >/dev/null ;;
real) go run ./tools/serveshots -bin "${PRAVO_BIN:-bin/pravo}" -out docs/img/serve ${JOBS:+-jobs "$JOBS"} ;;
*) echo "usage: sh tools/ci/pictures.sh [real|placeholders]" >&2; exit 2 ;;
esac
sh site/demos/views/build.sh
go run ./tools/serveshots -check-stamp
