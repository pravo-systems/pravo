#!/bin/sh
# The tests that the windows jobs of CI run, by the name of the job.
#
#   sh tools/ci/windows.sh SHARD
#
# The shards of the engine are by file, and the last takes every other test of the
# package (tools/ci/test-files.sh). Set SHORT=-short for a pull request.
set -eu
t="-timeout 25m ${SHORT:-}"
here=$(dirname "$0")
case $1 in
"engine locks") exec sh "$here/gotest.sh" engine in 'applylocks_test.go interrupt_test.go apply_scope_test.go' -v $t ;;
"engine equivalence") exec sh "$here/gotest.sh" engine in 'equivalence_test.go' -v $t ;;
"engine plans and discovery") exec sh "$here/gotest.sh" engine out 'applylocks_test.go interrupt_test.go apply_scope_test.go equivalence_test.go' -v $t ;;
"commands and viewer") exec sh "$here/gotest.sh" commands $t ;;
"state and libraries") exec sh "$here/gotest.sh" libraries $t ;;
*) echo "windows: unknown shard $1" >&2; exit 2 ;;
esac
