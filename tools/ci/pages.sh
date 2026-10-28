#!/bin/sh
# Prints the pages of the documentation that one job of CI checks, one per line.
#
#   sh tools/ci/pages.sh SHARD     the pages of a shard
#   sh tools/ci/pages.sh check     every page is in exactly one shard
#
# The shards are concepts (the concepts and the migration), cue (the CUE lessons,
# the specification and the policies), how-to, learn (the lessons about pravo and
# the lessons of the team track), labs
# (the cheat sheets and the labs), commands (the rest of the reference), and slow:
# the pages that wait for a minute, found by the `sleep` of ten seconds or more in
# them, and the lab that waits for a key. The slow ones have a job of their own
# that a pull request runs when it changes what they are about (the waits flag of
# tools/ci/changes.sh) or one of the pages itself; main runs it always.
#
# A pull request that does not change the binary checks only the pages that it
# changed. Set CI_EVENT, CI_BINARY and CI_WAITS as the workflow does.
set -eu
cd "$(dirname "$0")/../.."

# lab2 waits for the generation of an RSA key of 8192 bits, which takes between ten
# seconds and a minute whatever the machine.
slow() {
	{ find docs -name '*.md' | xargs grep -l -E '^sleep [0-9]{2,}' || true; echo docs/learn/lab2-two-applies-at-once.md; } | sort
}

members() {
	case $1 in
	concepts) ls docs/concepts/*.md docs/migrate/*.md ;;
	cue) ls docs/learn/a*.md docs/learn/cue*.md docs/reference/spec-*.md docs/reference/policies-*.md docs/reference/policy.md ;;
	how-to) ls docs/how-to/*.md ;;
	learn) ls docs/learn/b*.md docs/learn/d*.md docs/learn/index.md ;;
	labs) ls docs/learn/c[0-9]*.md docs/learn/lab*.md ;;
	commands) find docs/reference -name '*.md' ! -name 'spec-*' ! -name 'policies-*' ! -name policy.md ;;
	*) echo "pages: unknown shard $1" >&2; exit 2 ;;
	esac
}

# The pages of a shard without the slow ones, or the slow ones for slow.
shard() {
	if [ "$1" = slow ]; then slow; return; fi
	members "$1" | sort | { grep -v -x -F -f "$slowlist" || true; }
}

slowlist=$(mktemp)
trap 'rm -f "$slowlist"' EXIT
slow > "$slowlist"

what=${1:?usage: sh tools/ci/pages.sh concepts|cue|how-to|learn|labs|commands|slow|check}
if [ "$what" = check ]; then
	status=0
	listed=$( { for s in concepts cue how-to learn labs commands slow; do shard $s; done; } | sort)
	dup=$(echo "$listed" | uniq -d)
	[ -z "$dup" ] || { echo "pages: in more than one shard:"; echo "$dup"; status=1; }
	# AUTHORING.md tells how to write a page and is not one.
	missing=$(find docs -name '*.md' ! -name AUTHORING.md | sort | grep -v -x -F "$listed" || true)
	[ -z "$missing" ] || { echo "pages: in no shard (add them to tools/ci/pages.sh):"; echo "$missing"; status=1; }
	exit $status
fi

pages=$(shard "$what")
if [ "${CI_EVENT:-}" = pull_request ]; then
	whole=${CI_BINARY:-false}
	[ "$what" != slow ] || whole=${CI_WAITS:-false}
	if [ "$whole" != true ]; then
		changed=$(git diff --name-only HEAD^1 HEAD)
		if [ -z "$changed" ]; then pages=""; else pages=$(echo "$pages" | { grep -x -F "$changed" || true; }); fi
	fi
fi
[ -z "$pages" ] || echo "$pages"
