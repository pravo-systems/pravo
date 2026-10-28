#!/bin/sh
# The Go tests that each linux job of CI runs, by the name of the job.
#
#   sh tools/ci/tests.sh SHARD
#
# RACE and SHORT are the flags of go test that the workflow sets: -race on main,
# -short on a pull request. The shards of cmd are by file (tools/ci/shards.txt).
set -eu
here=$(dirname "$0")
case $1 in
engine)
	# The engine tests sync many small files, and run faster with the temporary
	# files in memory. Other packages compare paths under the temporary directory
	# and expect the usual one.
	TMPDIR=/dev/shm exec sh "$here/gotest.sh" engine ${RACE:-} ${SHORT:-}
	;;
serve) exec sh "$here/gotest.sh" serve ${SHORT:-} ;;
state) exec sh "$here/gotest.sh" state ${RACE:-} ${SHORT:-} ;;
cmd-plans) exec sh "$here/gotest.sh" cmd shard plans ${SHORT:-} ;;
cmd-outcomes) exec sh "$here/gotest.sh" cmd shard outcomes ${SHORT:-} ;;
cmd-sealing) exec sh "$here/gotest.sh" cmd shard sealing ${SHORT:-} ;;
cmd-identities) exec sh "$here/gotest.sh" cmd shard identities ${SHORT:-} ;;
rest)
	# The tests of cmd that no other job has, and the packages that no other job has.
	sh "$here/gotest.sh" cmd shard commands ${SHORT:-}
	exec sh "$here/gotest.sh" other
	;;
*) echo "tests: unknown shard $1" >&2; exit 2 ;;
esac
