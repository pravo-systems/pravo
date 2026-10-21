#!/bin/sh
# Records the large estate scenario for the plans band of the landing page.
# Run from the root of the module, with the pravo binary built and tofu installed:
#   go build -o bin/pravo ./cmd/pravo && sh site/demos/scale/build.sh bin/pravo
set -eu
cd "$(dirname "$0")/../../.."
bin=${1:-bin/pravo}
go run ./tools/capture -update -bin "$bin" site/demos/scale/page.md
go run ./tools/capture -check -bin "$bin" site/demos/scale/page.md
go run ./tools/capture -json site/data/scale.json -bin "$bin" site/demos/scale/page.md
