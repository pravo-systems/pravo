#!/bin/sh
# Prints the Go packages that a pull request can change the result of: the
# packages that it changed, those that import them, and those whose tests do. It
# prints ALL when it cannot tell, and for everything but a pull request.
#
#   sh tools/ci/affected.sh
set -eu
if [ "${CI_EVENT:-}" != pull_request ] || [ "${CI_ALL:-true}" = true ]; then
	echo ALL; exit 0
fi
mod=$(go list -m)
changed=""
for f in $(git diff --name-only HEAD^1 HEAD); do
	case $f in
	docs/*|design/*|site/*|*.md|LICENSE|tools/_weblint/*) continue ;;
	esac
	# The package that owns the file: the nearest directory above it with Go files.
	d=$(dirname "$f")
	while [ "$d" != . ] && ! ls "$d"/*.go >/dev/null 2>&1; do d=$(dirname "$d"); done
	if [ "$d" = . ] && ! ls ./*.go >/dev/null 2>&1; then echo ALL; exit 0; fi
	if [ "$d" = . ]; then changed="$changed $mod"; else changed="$changed $mod/$d"; fi
done
[ -n "$changed" ] || exit 0
# Into a variable first, so that a failure of go list fails the script instead of
# reading as "nothing is affected".
graph=$(go list -f '{{.ImportPath}}|{{join .Imports " "}}|{{join .TestImports " "}} {{join .XTestImports " "}}' ./...)
echo "$graph" | awk -F'|' -v changed="$changed" '
	BEGIN { n = split(changed, c, " "); for (i = 1; i <= n; i++) hit[c[i]] = 1 }
	{ pkg[NR] = $1; imp[NR] = $2; tst[NR] = $3 }
	END {
		# Packages that import a changed package, however far.
		do {
			grew = 0
			for (i = 1; i <= NR; i++) {
				if (hit[pkg[i]]) continue
				m = split(imp[i], a, " ")
				for (j = 1; j <= m; j++) if (hit[a[j]]) { hit[pkg[i]] = 1; grew = 1; break }
			}
		} while (grew)
		# Packages whose tests import one: their importers are not affected.
		for (i = 1; i <= NR; i++) {
			if (hit[pkg[i]]) { print pkg[i]; continue }
			m = split(tst[i], a, " ")
			for (j = 1; j <= m; j++) if (hit[a[j]]) { print pkg[i]; break }
		}
	}'
