#!/bin/sh
# Records the blast radius scenes and derives the band that site/demos/blast/embed.html shows.
# Run from the root of the module, with the pravo binary built:
#   go build -o bin/pravo ./cmd/pravo && sh site/demos/blast/build.sh bin/pravo
# page.md needs sh, seq, grep, cut and tr, and a machine that finishes an RSA 4096 key in a few seconds.
set -eu
bin=${1:-bin/pravo}
go run ./tools/capture -update -bin "$bin" site/demos/blast/page.md
go run ./tools/capture -check -bin "$bin" site/demos/blast/page.md
go run ./tools/capture -json site/data/blast.json -bin "$bin" site/demos/blast/page.md
go run site/demos/blast/derive.go site/demos/blast/render.go
