#!/bin/sh
# usage: verify.sh TASK DIR   -- prints PASS/FAIL lines
P=${PRAVO:-pravo}
t=$1; d=$2
ok(){ echo "PASS $1"; }; bad(){ echo "FAIL $1"; }
case $t in
t1)
  cd $d || exit 1
  $P init >/dev/null 2>&1 && ok init || bad init
  $P validate >/dev/null 2>&1 && ok validate || bad validate
  j=$($P plan --json 2>/dev/null)
  n=$(echo "$j" | jq '[.plan.changes // .changes // [] | .[] | select(.mode=="managed" and .action=="create")] | length' 2>/dev/null)
  [ "$n" = 9 ] && ok "9 creates" || bad "creates: $n (want 9)"
  echo "$j" | jq -e '(.plan.outputs // .outputs) | has("files")' >/dev/null 2>&1 && ok "output files" || bad "output files"
  ;;
t2)
  cd $d || exit 1
  $P validate >/dev/null 2>&1 && ok validate || bad validate
  c=$($P state list 2>/dev/null | wc -l | tr -d ' ')
  [ "$c" = 4 ] && ok "4 records in state" || bad "records: $c (want 4)"
  [ -f plan.pravo ] && ok "plan.pravo saved" || bad "plan.pravo missing"
  [ -f plan.pravo ] && { $P show plan.pravo --json 2>/dev/null | jq -e '[(.plan.changes // [])[] | select(.action!="no-op")] | length >= 1' >/dev/null && ok "saved plan has a change" || bad "saved plan has no change"; }
  grep -q 'web.*3000\|3000' out/web.json 2>/dev/null && ok "second change not applied" || bad "web.json is not at 3000"
  # port constraint holds
  cp -R . ../chk.$$ 2>/dev/null && (cd ../chk.$$ && sed -i.bak 's/port: *3001/port: 80/; s/port: *3000/port: 80/' *.cue && ! $P validate >/dev/null 2>&1 && echo "PASS constraint rejects 80" || echo "FAIL constraint does not reject 80"); rm -rf ../chk.$$
  ;;
t3)
  cd $d || exit 1
  grep -q 'pravo.#Policy' root/*.cue && ok "policy node" || bad "policy node"
  ls root/pravo.state/policy/* >/dev/null 2>&1 && ok "policy version in the state" || bad "policy version in the state"
  out=$($P -C root plan --detailed-exitcode 2>&1); rc=$?
  [ $rc = 0 ] && ok "plan: no changes, exit 0" || { bad "plan exit $rc"; echo "$out" | tail -5; }
  grep -q 'moved\|\$from' root/*.cue && ok "uses a move" || bad "no move declaration"
  # the policy must refuse a destroy
  $P -C root plan --destroy >/dev/null 2>&1; rc=$?
  [ $rc = 5 ] && ok "policy refuses a destroy (exit 5)" || bad "destroy plan exit $rc (want 5)"
  ;;
esac
