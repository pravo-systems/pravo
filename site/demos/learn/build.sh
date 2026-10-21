#!/bin/sh
# Reads the front matter of docs/learn/*.md into site/data/learn.json (the page map fills any page that does not exist yet).
# Run from the root of the module.
set -eu
go run site/demos/learn/derive.go
