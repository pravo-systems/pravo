#!/bin/sh
# bench.sh: times plans and applies of pravo and OpenTofu on estates of local_file
# resources, counts the records each run reads from its provider, and writes the
# medians as JSON. tools/scale/README.md describes the scenarios and the file.
#
# Usage, from anywhere:
#   sh tools/scale/bench.sh [N ...]    sizes; default 500 2000 5000 10000
#   sh tools/scale/bench.sh -s FILE    print the table of a finished run (results.tsv)
#
# Environment
#   PRAVO_BIN   the pravo binary     (default: bin/pravo of the module)
#   TOFU_BIN    the tofu binary      (default: tofu on the path)
#   TOOLS       tools to run         (default: "pravo tofu")
#   SHAPES      estates to run       (default: "flat hub layered")
#   REPS        repetitions of each scenario; the file holds the medians (default 3)
#   WORK        scratch directory    (default: a new /tmp/pravo-bench-work.*)
#   OUT         the JSON file        (default: $WORK/scale-timings.json)
#   PLUGIN_CACHE a directory with the hashicorp/local plugin for tofu
#               (default: $WORK/plugin-cache; tofu init downloads the plugin into it)
#   MAX_LOAD    the load average above which the run warns that the machine is busy
#               (default 2)
#   WAIT_IDLE   seconds to wait before the run for the load average over one minute to
#               fall to MAX_LOAD or below (default 0: do not wait); the run starts
#               when the time is up, busy or not, and says so
#   TOFU_LAYERED_MAX  the largest size at which tofu runs the layered estate
#               (default 2000); OpenTofu relates each instance of a layer to the
#               whole layer below, and its time on this estate grows steeply
#   PRAVO_LAYERED_MAX  the largest size at which pravo runs the layered estate
#               (default 5000); the evaluation of a specification whose resources read
#               each other costs memory in proportion to the number of resources
#   RESULTS_FROM the results.tsv of an earlier run; its rows join the rows of this
#               one, so that a run can be split into several
#
# Output
#   $WORK/results.tsv   one row per run:
#       tool n scenario rep wall_s user_s sys_s rss_mb exit reads locks planfile summary
#   $WORK/n<N>/logs/    the output of every run
#   $OUT                the medians, with the machine, the tools and the date
#
# Estates (N is the number of resources; the files are local_file)
#   flat      N files and one more, "release", that carries a version
#   hub       N files that each read the content of one more, "hub"
#   layered   three layers of N/3 files; each reads the id of the file with the same
#             index in the layer below, and the first of the first layer carries a
#             version
# A change is a new value of the version, or of the hub's content in the hub estate.
#
# Scenarios
#   init, apply_create, destroy
#   plan_nochange_<mode>, plan_change_<mode>, apply_change_<mode>   (flat)
#   plan_out_change_default                                          (flat)
#   hub_*, layered_*: apply_create, plan_change, plan_nochange (layered), apply_change, destroy
#   <mode>  default    pravo with the default refresh age (always); tofu with refresh
#           6h         pravo with $settings refresh "6h"
#           norefresh  pravo -refresh=false; tofu -refresh=false
# apply_create and destroy of the flat estate run REPS times, one after the other,
# and one more apply_create counts the locks. Repetition 0 of a row is a run made
# to count the locks of an apply (pravo -v); it is not in the medians.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
PRAVO_BIN=${PRAVO_BIN:-$ROOT/bin/pravo}
TOFU_BIN=${TOFU_BIN:-$(command -v tofu || echo tofu)}
TOOLS=${TOOLS:-"pravo tofu"}
SHAPES=${SHAPES:-"flat hub layered"}
REPS=${REPS:-3}
MAX_LOAD=${MAX_LOAD:-2}
WAIT_IDLE=${WAIT_IDLE:-0}
TOFU_LAYERED_MAX=${TOFU_LAYERED_MAX:-2000}
PRAVO_LAYERED_MAX=${PRAVO_LAYERED_MAX:-5000}

summary() {
	awk -F '\t' '
	function median(key,   n, i, j, t, a) {
		n = cnt[key]
		for (i = 1; i <= n; i++) a[i] = val[key, i]
		for (i = 2; i <= n; i++) { t = a[i]; for (j = i - 1; j >= 1 && a[j] > t; j--) a[j + 1] = a[j]; a[j + 1] = t }
		return (n % 2) ? a[(n + 1) / 2] : (a[n / 2] + a[n / 2 + 1]) / 2
	}
	$4 == 0 { next }
	{
		k = $1 SUBSEP $2 SUBSEP $3
		if (!(k in seen)) { seen[k] = 1 }
		cnt[k]++; val[k, cnt[k]] = $5
		cpu[k] += $6 + $7; rss[k] = ($8 > rss[k]) ? $8 : rss[k]
		reads[k] = $10
		if (!($2 in nseen)) { nseen[$2] = 1; ns[++nn] = $2 }
		if (!($3 in sseen)) { sseen[$3] = 1; ss[++nsc] = $3 }
	}
	END {
		printf "%-28s %6s | %9s %8s %8s %7s | %9s %8s %8s %7s\n", "scenario", "N", "pravo_s", "cpu_s", "rss_MB", "reads", "tofu_s", "cpu_s", "rss_MB", "reads"
		for (a = 1; a <= nn; a++) for (b = 1; b <= nsc; b++) {
			n = ns[a]; s = ss[b]
			p = "pravo" SUBSEP n SUBSEP s; t = "tofu" SUBSEP n SUBSEP s
			if (!(p in seen) && !(t in seen)) continue
			if (p in seen) ps = sprintf("%9.2f %8.2f %8.0f %7s", median(p), cpu[p] / cnt[p], rss[p], reads[p]); else ps = sprintf("%9s %8s %8s %7s", "-", "-", "-", "-")
			if (t in seen) ts = sprintf("%9.2f %8.2f %8.0f %7s", median(t), cpu[t] / cnt[t], rss[t], reads[t]); else ts = sprintf("%9s %8s %8s %7s", "-", "-", "-", "-")
			printf "%-28s %6s | %s | %s\n", s, n, ps, ts
		}
	}' "$1"
}

if [ "${1:-}" = "-s" ]; then
	summary "$2"
	exit 0
fi

SIZES=${*:-"500 2000 5000 10000"}
WORK=${WORK:-$(mktemp -d /tmp/pravo-bench-work.XXXXXX)}
OUT=${OUT:-$WORK/scale-timings.json}
PLUGIN_CACHE=${PLUGIN_CACHE:-$WORK/plugin-cache}
mkdir -p "$WORK" "$PLUGIN_CACHE"
RESULTS=$WORK/results.tsv
: >"$RESULTS"
if [ -n "${RESULTS_FROM:-}" ]; then
	cat "$RESULTS_FROM" >>"$RESULTS"
fi
SCALE=$WORK/scale
(cd "$ROOT" && go build -o "$SCALE" ./tools/scale)

load() { uptime | sed 's/.*load averages*: *//'; }
# The load over one minute, on three readings in a row: one reading can fall below
# the limit between two bursts.
waited=0
calm=0
while [ "$WAIT_IDLE" -gt 0 ] && [ "$calm" -lt 3 ] && [ "$waited" -lt "$WAIT_IDLE" ]; do
	if [ "$(load | awk -v m="$MAX_LOAD" '{ print ($1 > m) ? "busy" : "idle" }')" = idle ]; then
		calm=$((calm + 1))
	else
		calm=0
	fi
	[ "$calm" -ge 3 ] || { sleep 10; waited=$((waited + 10)); }
done
[ "$WAIT_IDLE" -le 0 ] || echo "waited ${waited}s for the load average to fall to $MAX_LOAD; it is now $(load)"
LOAD_START=$(load)

# measure TOOL N SCENARIO REP DIR CMD...: runs CMD in DIR, appends a row to the
# results and prints a one-line report. PLANFILE names a file whose size the row
# records, and COUNT_LOCKS=1 makes the row record the "locked N resources" line of
# a pravo -v run. Both are set and cleared by the caller.
PLANFILE=""
COUNT_LOCKS=0
measure() {
	m_tool=$1 m_n=$2 m_sc=$3 m_rep=$4 m_dir=$5
	shift 5
	m_log=$WORK/n$m_n/logs/$m_tool.$m_sc.$m_rep.log
	m_res=$WORK/n$m_n/logs/$m_tool.$m_sc.$m_rep.time
	(cd "$m_dir" && "$SCALE" time -r "$m_res" -- "$@") >"$m_log" 2>&1 || true
	read -r m_wall m_user m_sys m_rss m_exit <"$m_res"
	m_reads=$(grep -c 'Refreshing state' "$m_log" || true)
	m_locks=-
	if [ "$COUNT_LOCKS" = 1 ]; then
		m_locks=$(sed -n 's/.* debug locked \([0-9][0-9]*\) resources.*/\1/p' "$m_log" | head -n 1)
		m_locks=${m_locks:--}
	fi
	m_plan=-
	if [ -n "$PLANFILE" ] && [ -f "$PLANFILE" ]; then
		m_plan=$(wc -c <"$PLANFILE" | tr -d ' ')
	fi
	m_sum=$(grep -E '^(Plan:|Graph:|No changes|Apply complete|Destroy complete|OpenTofu will|Resources:)' "$m_log" | tr '\n' '|' | sed 's/|$//')
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$m_tool" "$m_n" "$m_sc" "$m_rep" "$m_wall" "$m_user" "$m_sys" "$m_rss" "$m_exit" "$m_reads" "$m_locks" "$m_plan" "$m_sum" >>"$RESULTS"
	printf '%-6s N=%-5s %-28s rep %s  wall %8ss  user %8ss  sys %7ss  rss %6sMB  exit %s  reads %s  locks %s  plan file %s\n' "$m_tool" "$m_n" "$m_sc" "$m_rep" "$m_wall" "$m_user" "$m_sys" "$m_rss" "$m_exit" "$m_reads" "$m_locks" "$m_plan"
	if [ "$m_exit" != 0 ]; then
		printf 'FAILED: %s %s %s (exit %s); see %s\n' "$m_tool" "$m_n" "$m_sc" "$m_exit" "$m_log" >&2
		tail -n 5 "$m_log" >&2
		return 1
	fi
}

# reps TOOL N SCENARIO DIR CMD...: measure REPS times.
reps() {
	r_tool=$1 r_n=$2 r_sc=$3 r_dir=$4
	shift 4
	r_i=1
	while [ "$r_i" -le "$REPS" ]; do
		measure "$r_tool" "$r_n" "$r_sc" "$r_i" "$r_dir" "$@"
		r_i=$((r_i + 1))
	done
}

# ---- pravo ---------------------------------------------------------------

# pravo_cue DIR N SHAPE REFRESH: writes main.cue; REFRESH is "" or a refresh age.
pravo_cue() {
	pc_imp=""
	pc_set=""
	if [ -n "$4" ]; then
		pc_imp='	"pravo.dev/pravo"'
		pc_set="\$settings: pravo.#Settings & {refresh: \"$4\"}"
	fi
	cat >"$1/main.cue" <<EOF
package estate

import (
	"list"
$pc_imp
	loc "pravo.dev/providers/hashicorp/local"
)

$pc_set

\$local: loc.#Provider

version: *"1" | string @tag(version)

EOF
	case $3 in
	flat)
		cat >>"$1/main.cue" <<EOF
files: {for i in list.Range(0, $2, 1) {
	"f\(i)": \$local.#Resource.local_file & {filename: "out/f\(i).txt", content: "item \(i)\n"}
}}

release: \$local.#Resource.local_file & {filename: "out/release.txt", content: "version \(version)\n"}
EOF
		;;
	hub)
		cat >>"$1/main.cue" <<EOF
hub: \$local.#Resource.local_file & {filename: "out/hub.txt", content: "hub \(version)\n"}

files: {for i in list.Range(0, $2, 1) {
	"f\(i)": \$local.#Resource.local_file & {filename: "out/f\(i).txt", content: "item \(i) \(hub.content)"}
}}
EOF
		;;
	layered)
		pc_l=$(($2 / 3))
		cat >>"$1/main.cue" <<EOF
l0: {for i in list.Range(0, $pc_l, 1) {
	"f\(i)": \$local.#Resource.local_file & {
		filename: "out/l0-\(i).txt"
		if i == 0 {content: "l0 0 v\(version)\n"}
		if i != 0 {content: "l0 \(i)\n"}
	}
}}

l1: {for i in list.Range(0, $pc_l, 1) {
	"f\(i)": \$local.#Resource.local_file & {filename: "out/l1-\(i).txt", content: "\(l0["f\(i)"].id)\n"}
}}

l2: {for i in list.Range(0, $pc_l, 1) {
	"f\(i)": \$local.#Resource.local_file & {filename: "out/l2-\(i).txt", content: "\(l1["f\(i)"].id)\n"}
}}
EOF
		;;
	esac
}

# records_wanted SHAPE N: the number of records after the create.
records_wanted() {
	case $1 in
	layered) echo $((($2 / 3) * 3)) ;;
	*) echo $(($2 + 1)) ;;
	esac
}

# pravo_check DIR N SHAPE: fails unless the state holds the records of the estate.
pravo_check() {
	pk_have=$(ls "$1/pravo.state/resources" | wc -l | tr -d ' ')
	pk_want=$(records_wanted "$3" "$2")
	[ "$pk_have" -eq "$pk_want" ] || { echo "pravo $3: $pk_have records, want $pk_want" >&2; return 1; }
}

# The create and the destroy of an estate run REPS times in turn; the estate that
# stays is made by the last create.
pravo_cycles() { # N SHAPE DIR PREFIX CYCLES
	pcy_i=1
	while [ "$pcy_i" -le "$5" ]; do
		measure pravo "$1" "${4}apply_create" "$pcy_i" "$3" "$PRAVO_BIN" apply --auto-approve -t version=1
		pravo_check "$3" "$1" "$2"
		if [ "$pcy_i" -lt "$5" ]; then
			measure pravo "$1" "${4}destroy" "$pcy_i" "$3" "$PRAVO_BIN" destroy --auto-approve -t version=1
		fi
		pcy_i=$((pcy_i + 1))
	done
}

run_pravo_flat() {
	n=$1
	d=$WORK/n$n/pravo
	mkdir -p "$d/out"
	pravo_cue "$d" "$n" flat ""
	measure pravo "$n" init 1 "$d" "$PRAVO_BIN" init
	# One create counts the locks: it runs first, with -v, and is destroyed.
	COUNT_LOCKS=1
	measure pravo "$n" apply_create 0 "$d" "$PRAVO_BIN" apply --auto-approve -v -t version=1
	COUNT_LOCKS=0
	measure pravo "$n" destroy 0 "$d" "$PRAVO_BIN" destroy --auto-approve -t version=1
	pravo_cycles "$n" flat "$d" "" "$REPS"
	v=1
	for mode in default 6h norefresh; do
		case $mode in
		6h) pravo_cue "$d" "$n" flat 6h; flag="" ;;
		norefresh) pravo_cue "$d" "$n" flat ""; flag="--refresh=false" ;;
		*) pravo_cue "$d" "$n" flat ""; flag="" ;;
		esac
		# $flag is empty or one word.
		reps pravo "$n" "plan_nochange_$mode" "$d" "$PRAVO_BIN" plan -t version=$v $flag
		reps pravo "$n" "plan_change_$mode" "$d" "$PRAVO_BIN" plan -t version=$((v + 1)) $flag
		if [ "$mode" = default ]; then
			PLANFILE=$d/change.plan
			reps pravo "$n" plan_out_change_default "$d" "$PRAVO_BIN" plan --out "$d/change.plan" -t version=$((v + 1))
			PLANFILE=""
		fi
		i=1
		while [ "$i" -le "$REPS" ]; do
			v=$((v + 1))
			measure pravo "$n" "apply_change_$mode" "$i" "$d" "$PRAVO_BIN" apply --auto-approve -t version=$v $flag
			i=$((i + 1))
		done
		v=$((v + 1))
		COUNT_LOCKS=1
		measure pravo "$n" "apply_change_$mode" 0 "$d" "$PRAVO_BIN" apply --auto-approve -v -t version=$v $flag
		COUNT_LOCKS=0
	done
	pravo_cue "$d" "$n" flat ""
	measure pravo "$n" destroy "$REPS" "$d" "$PRAVO_BIN" destroy --auto-approve -t version=$v
}

# run_pravo_whole N SHAPE PREFIX: the hub and the layered estate. One create and one
# destroy are timed; the plans and the applies of a change run REPS times.
run_pravo_whole() {
	n=$1 shape=$2 prefix=$3
	d=$WORK/n$n/pravo-$shape
	mkdir -p "$d/out"
	pravo_cue "$d" "$n" "$shape" ""
	measure pravo "$n" "${prefix}init" 1 "$d" "$PRAVO_BIN" init
	measure pravo "$n" "${prefix}apply_create" 1 "$d" "$PRAVO_BIN" apply --auto-approve -t version=1
	pravo_check "$d" "$n" "$shape"
	v=1
	if [ "$shape" = layered ]; then
		reps pravo "$n" "${prefix}plan_nochange" "$d" "$PRAVO_BIN" plan -t version=$v
	fi
	reps pravo "$n" "${prefix}plan_change" "$d" "$PRAVO_BIN" plan -t version=$((v + 1))
	i=1
	while [ "$i" -le "$REPS" ]; do
		v=$((v + 1))
		measure pravo "$n" "${prefix}apply_change" "$i" "$d" "$PRAVO_BIN" apply --auto-approve -t version=$v
		i=$((i + 1))
	done
	measure pravo "$n" "${prefix}destroy" 1 "$d" "$PRAVO_BIN" destroy --auto-approve -t version=$v
}

run_pravo() {
	n=$1
	mkdir -p "$WORK/n$n/logs"
	export PRAVO_HOME="${PRAVO_HOME:-$HOME/.pravo}"
	for shape in $SHAPES; do
		case $shape in
		flat) run_pravo_flat "$n" ;;
		hub) run_pravo_whole "$n" hub "hub_" ;;
		layered)
			if [ "$n" -le "$PRAVO_LAYERED_MAX" ]; then
				run_pravo_whole "$n" layered "layered_"
			else
				echo "pravo N=$n layered: not run, the size is above PRAVO_LAYERED_MAX=$PRAVO_LAYERED_MAX"
			fi
			;;
		esac
	done
}

# ---- tofu ----------------------------------------------------------------

# tofu_hcl DIR N SHAPE
tofu_hcl() {
	cat >"$1/main.tf" <<EOF
terraform {
  required_providers {
    local = { source = "hashicorp/local", version = "2.9.1" }
  }
}

variable "version_tag" { default = "1" }

EOF
	case $3 in
	flat)
		cat >>"$1/main.tf" <<EOF
resource "local_file" "files" {
  count    = $2
  filename = "out/f\${count.index}.txt"
  content  = "item \${count.index}\n"
}

resource "local_file" "release" {
  filename = "out/release.txt"
  content  = "version \${var.version_tag}\n"
}
EOF
		;;
	hub)
		cat >>"$1/main.tf" <<EOF
resource "local_file" "hub" {
  filename = "out/hub.txt"
  content  = "hub \${var.version_tag}\n"
}

resource "local_file" "files" {
  count    = $2
  filename = "out/f\${count.index}.txt"
  content  = "item \${count.index} \${local_file.hub.content}"
}
EOF
		;;
	layered)
		cat >>"$1/main.tf" <<EOF
resource "local_file" "l0" {
  count    = $(($2 / 3))
  filename = "out/l0-\${count.index}.txt"
  content  = count.index == 0 ? "l0 0 v\${var.version_tag}\n" : "l0 \${count.index}\n"
}

resource "local_file" "l1" {
  count    = $(($2 / 3))
  filename = "out/l1-\${count.index}.txt"
  content  = "\${local_file.l0[count.index].id}\n"
}

resource "local_file" "l2" {
  count    = $(($2 / 3))
  filename = "out/l2-\${count.index}.txt"
  content  = "\${local_file.l1[count.index].id}\n"
}
EOF
		;;
	esac
}

tofu_env() {
	export TF_PLUGIN_CACHE_DIR=$PLUGIN_CACHE TF_IN_AUTOMATION=1
	common="-no-color -input=false"
}

tofu_cycles() { # N DIR PREFIX CYCLES
	tcy_i=1
	while [ "$tcy_i" -le "$4" ]; do
		measure tofu "$1" "${3}apply_create" "$tcy_i" "$2" "$TOFU_BIN" apply -auto-approve $common -var version_tag=1
		if [ "$tcy_i" -lt "$4" ]; then
			measure tofu "$1" "${3}destroy" "$tcy_i" "$2" "$TOFU_BIN" destroy -auto-approve $common -var version_tag=1
		fi
		tcy_i=$((tcy_i + 1))
	done
}

run_tofu_flat() {
	n=$1
	d=$WORK/n$n/tofu
	mkdir -p "$d/out"
	tofu_hcl "$d" "$n" flat
	measure tofu "$n" init 1 "$d" "$TOFU_BIN" init $common
	tofu_cycles "$n" "$d" "" "$REPS"
	v=1
	for mode in default norefresh; do
		flag=""
		[ "$mode" = norefresh ] && flag="-refresh=false"
		reps tofu "$n" "plan_nochange_$mode" "$d" "$TOFU_BIN" plan $common -var version_tag=$v $flag
		reps tofu "$n" "plan_change_$mode" "$d" "$TOFU_BIN" plan $common -var version_tag=$((v + 1)) $flag
		if [ "$mode" = default ]; then
			PLANFILE=$d/change.tfplan
			reps tofu "$n" plan_out_change_default "$d" "$TOFU_BIN" plan $common -out="$d/change.tfplan" -var version_tag=$((v + 1))
			PLANFILE=""
		fi
		i=1
		while [ "$i" -le "$REPS" ]; do
			v=$((v + 1))
			measure tofu "$n" "apply_change_$mode" "$i" "$d" "$TOFU_BIN" apply -auto-approve $common -var version_tag=$v $flag
			i=$((i + 1))
		done
	done
	measure tofu "$n" destroy "$REPS" "$d" "$TOFU_BIN" destroy -auto-approve $common -var version_tag=$v
}

run_tofu_whole() {
	n=$1 shape=$2 prefix=$3
	d=$WORK/n$n/tofu-$shape
	mkdir -p "$d/out"
	tofu_hcl "$d" "$n" "$shape"
	measure tofu "$n" "${prefix}init" 1 "$d" "$TOFU_BIN" init $common
	measure tofu "$n" "${prefix}apply_create" 1 "$d" "$TOFU_BIN" apply -auto-approve $common -var version_tag=1
	v=1
	if [ "$shape" = layered ]; then
		reps tofu "$n" "${prefix}plan_nochange" "$d" "$TOFU_BIN" plan $common -var version_tag=$v
	fi
	reps tofu "$n" "${prefix}plan_change" "$d" "$TOFU_BIN" plan $common -var version_tag=$((v + 1))
	i=1
	while [ "$i" -le "$REPS" ]; do
		v=$((v + 1))
		measure tofu "$n" "${prefix}apply_change" "$i" "$d" "$TOFU_BIN" apply -auto-approve $common -var version_tag=$v
		i=$((i + 1))
	done
	measure tofu "$n" "${prefix}destroy" 1 "$d" "$TOFU_BIN" destroy -auto-approve $common -var version_tag=$v
}

run_tofu() {
	n=$1
	mkdir -p "$WORK/n$n/logs"
	tofu_env
	for shape in $SHAPES; do
		case $shape in
		flat) run_tofu_flat "$n" ;;
		hub) run_tofu_whole "$n" hub "hub_" ;;
		layered)
			if [ "$n" -le "$TOFU_LAYERED_MAX" ]; then
				run_tofu_whole "$n" layered "layered_"
			else
				echo "tofu N=$n layered: not run, the size is above TOFU_LAYERED_MAX=$TOFU_LAYERED_MAX"
			fi
			;;
		esac
	done
}

echo "work directory: $WORK"
echo "pravo: $PRAVO_BIN ($("$PRAVO_BIN" version 2>&1 | head -n 1))   tofu: $("$TOFU_BIN" version | head -n 1)   reps: $REPS   sizes: $SIZES"
echo "load average at the start: $LOAD_START"
case $(echo "$LOAD_START" | awk '{ print ($1 > m) ? "busy" : "idle" }' m="$MAX_LOAD") in
busy) echo "warning: the load average is above $MAX_LOAD; the machine is not idle, and the timings carry that" >&2 ;;
esac
for n in $SIZES; do
	mkdir -p "$WORK/n$n/logs"
	for tool in $TOOLS; do
		"run_$tool" "$n"
	done
done
LOAD_END=$(load)

# The sizes of every row, those of the run that RESULTS_FROM names included.
ALL_SIZES=$(awk -F '\t' '{ print $2 }' "$RESULTS" | sort -un | tr '\n' ' ')
set -- -pravo "$PRAVO_BIN" -tofu "$TOFU_BIN" -root "$ROOT" -reps "$REPS" -sizes "$ALL_SIZES" -load-start "$LOAD_START" -load-end "$LOAD_END"
# The versions of the providers, read from what the tools recorded.
pv=$(sed -n 's/.*hashicorp\/local v\([0-9][0-9.]*\).*/\1/p' "$WORK"/n*/logs/pravo.init.1.log 2>/dev/null | head -n 1)
[ -z "$pv" ] || set -- "$@" -provider "pravo=hashicorp/local@$pv"
tv=$(find "$WORK" -name .terraform.lock.hcl -print 2>/dev/null | head -n 1)
if [ -n "$tv" ]; then
	tv=$(awk -F'"' '/^ *version *=/ { print $2; exit }' "$tv")
	[ -z "$tv" ] || set -- "$@" -provider "tofu=hashicorp/local@$tv"
fi
if [ -n "${RESULTS_FROM:-}" ]; then
	set -- "$@" -note "The rows of some sizes are from an earlier run: $RESULTS_FROM."
fi
# The load at the end includes the run itself, so only the load at the start says
# whether something else was running.
if [ "$(echo "$LOAD_START" | awk -v m="$MAX_LOAD" '{ print ($1 > m) ? "busy" : "idle" }')" = busy ]; then
	set -- "$@" -note "The machine was not idle: the load average was $LOAD_START at the start, over 1, 5 and 15 minutes."
fi
for n in $ALL_SIZES; do
	if [ "$n" -gt "$TOFU_LAYERED_MAX" ] && echo " $TOOLS " | grep -q ' tofu '; then
		set -- "$@" -note "OpenTofu was not run on the layered estate above $TOFU_LAYERED_MAX resources."
		break
	fi
done
for n in $ALL_SIZES; do
	if [ "$n" -gt "$PRAVO_LAYERED_MAX" ] && echo " $TOOLS " | grep -q ' pravo '; then
		set -- "$@" -note "pravo was not run on the layered estate above $PRAVO_LAYERED_MAX resources."
		break
	fi
done
"$SCALE" meta "$@" >"$WORK/meta.json"
"$SCALE" json -results "$RESULTS" -meta "$WORK/meta.json" -o "$OUT"
echo
summary "$RESULTS"
echo
echo "results: $RESULTS"
echo "json:    $OUT"
