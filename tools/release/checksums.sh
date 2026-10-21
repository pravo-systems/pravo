#!/bin/sh
# Writes SHA256SUMS for the archives in a directory.
#
#   sh tools/release/checksums.sh DIR
set -eu
[ $# -eq 1 ] || { echo "usage: sh tools/release/checksums.sh DIR" >&2; exit 2; }
cd "$1"
if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi
$sum pravo_*.tar.gz pravo_*.zip 2>/dev/null > SHA256SUMS
cat SHA256SUMS
