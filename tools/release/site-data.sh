#!/bin/sh
# Writes the data of the download block of the front page from the releases of
# the repository: the newest release that is not a pre-release, or the newest
# pre-release when there is no other, or an empty record when there is no
# release at all.
#
#   sh tools/release/site-data.sh [OUT]       (OUT defaults to site/data/release.json)
#
# It needs gh, logged in or with GH_TOKEN, and jq. RELEASES_JSON names a file
# with the list that the API returns, for a test.
set -eu
out=${1:-site/data/release.json}
repo=${GITHUB_REPOSITORY:-pravo-systems/pravo}
list=$(mktemp "${TMPDIR:-/tmp}/releases.XXXXXX")
trap 'rm -f "$list"' EXIT
if [ -n "${RELEASES_JSON:-}" ]; then
	cp "$RELEASES_JSON" "$list"
else
	gh api "repos/$repo/releases?per_page=30" > "$list"
fi
jq '
  [.[] | select(.draft | not)] as $all
  | (($all | map(select(.prerelease | not)) | first) // ($all | first)) as $r
  | if $r == null then {version: "", tag: "", url: "", assets: []} else
    {version: ($r.tag_name | ltrimstr("v")), tag: $r.tag_name, url: $r.html_url,
     assets: [$r.assets[]
       | select(.name | test("^pravo_.+_(linux|darwin|windows)_(amd64|arm64)\\.(tar\\.gz|zip)$"))
       | (.name | capture("_(?<os>linux|darwin|windows)_(?<arch>amd64|arm64)\\.")) as $p
       | {name, url: .browser_download_url, size, os: $p.os, arch: $p.arch}]} end' "$list" > "$out"
