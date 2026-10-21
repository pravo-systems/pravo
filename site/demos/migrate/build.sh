#!/bin/sh
# Records the migration demo and derives the view and the generated regions of site/demos/migrate/embed.html.
# Run from the root of the module, with the pravo binary built:
#   go build -o bin/pravo ./cmd/pravo && sh site/demos/migrate/build.sh bin/pravo
#
# The state file of page.md is the one that OpenTofu wrote, kept by tofu/record.sh. To record
# it again, run `sh site/demos/migrate/tofu/record.sh`, then `go run site/demos/migrate/fixtures.go -update`.
set -eu
bin=${1:-bin/pravo}
go run site/demos/migrate/fixtures.go
go run ./tools/capture -update -bin "$bin" site/demos/migrate/page.md
go run ./tools/capture -check -bin "$bin" site/demos/migrate/page.md
go run ./tools/capture -json site/data/migrate.json -bin "$bin" site/demos/migrate/page.md
go run site/demos/migrate/derive.go
