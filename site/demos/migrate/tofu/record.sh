#!/bin/sh
# Runs the OpenTofu stacks behind the migration pages and keeps what they wrote.
#
#   sh site/demos/migrate/tofu/record.sh [OUT_DIR [SECTION...]]
#
# Needs OpenTofu 1.13 and jq on the path, and network access to
# registry.opentofu.org. Each stack is copied to a temporary directory and run
# there. The sections are m3, lock, cut, estate, rollback, ws, landing, m1, m9 and ref; the
# default is all of them. Section cut starts from m3-platform.tfstate and
# section rollback from estate.tfstate in OUT_DIR, so run m3 and estate first
# when OUT_DIR is new. OUT_DIR (default: state/ next to this script) receives:
#
#   *.tfstate    the terraform.tfstate files, as OpenTofu wrote them
#   objects/     the files that the estate and landing stacks created
#   log/NAME.log the output of one OpenTofu command, without colour
#   log/NAME.exit its exit status
#
# The pages embed the state files as file= blocks and excerpts of the logs as
# shown output. site/demos/migrate/fixtures.go compares them with this
# directory. The state files hold random values, so a new run of a section
# writes new values; run fixtures.go -update after it, then tools/capture
# -update on the pages and read the result.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here/state}
[ $# -gt 0 ] && shift
mkdir -p "$out"
out=$(cd "$out" && pwd)
sections=${*:-m3 lock cut estate rollback ws landing m1 m9 ref}
want() { case " $sections " in *" $1 "*) return 0 ;; esac; return 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

command -v tofu > /dev/null || { echo "record: tofu is not on the path" >&2; exit 1; }
command -v jq > /dev/null || { echo "record: jq is not on the path" >&2; exit 1; }
tofu version | head -n 1 | grep -q 'v1\.13\.' || echo "record: warning: $(tofu version | head -n 1), the pages say 1.13" >&2

export TF_IN_AUTOMATION=1 TF_CLI_ARGS=-no-color TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$work/plugins}"
mkdir -p "$TF_PLUGIN_CACHE_DIR" "$out/log" "$out/objects"

# step NAME COMMAND...: run a command, keep its output and exit status.
step() {
	name=$1
	shift
	set +e
	"$@" > "$out/log/$name.log" 2>&1
	echo "$?" > "$out/log/$name.exit"
	set -e
}

# copy SRC DEST: copy a stack's HCL.
copy() {
	mkdir -p "$2"
	(cd "$here/$1" && tar cf - .) | (cd "$2" && tar xf -)
}

# objects STATE: write the files that the local_file resources of a state
# describe, so that a plan finds them.
objects() {
	jq -r '.resources[] | select(.type == "local_file" and .mode == "managed") | .instances[] | .attributes.filename' "$1" |
		while read -r f; do
			mkdir -p "$(dirname "$f")"
			jq -j --arg f "$f" '.resources[] | select(.type == "local_file" and .mode == "managed") | .instances[] | .attributes | select(.filename == $f) | .content' "$1" > "$f"
		done
}

# --- m3: two roots, a module with for_each, a sensitive output, a moved history
if want m3; then
	copy m3 "$work/m3"
	cd "$work/m3/platform"
	# Stage 1 of the history: the password is called root and there is no moved block.
	cp main.tf main.tf.final
	sed -e 's/"random_password" "admin"/"random_password" "root"/' \
		-e 's/random_password\.admin\.result/random_password.root.result/' \
		-e '/^moved {/,/^}/d' main.tf.final > main.tf
	step m3-platform-init tofu init
	step m3-platform-apply-1 tofu apply -auto-approve
	# Stage 2: rename with a moved block, plan, apply.
	mv main.tf.final main.tf
	step m3-platform-plan-moved tofu plan -detailed-exitcode
	step m3-platform-apply-2 tofu apply -auto-approve
	step m3-platform-plan-empty tofu plan -detailed-exitcode
	cd "$work/m3/gateway"
	step m3-gateway-init tofu init
	step m3-gateway-apply tofu apply -auto-approve
	step m3-gateway-plan-empty tofu plan -detailed-exitcode
	cp "$work/m3/platform/terraform.tfstate" "$out/m3-platform.tfstate"
	cp "$work/m3/gateway/terraform.tfstate" "$out/m3-gateway.tfstate"
fi

# --- m1: the lock file that tofu init writes, and the registry check of M1
if want lock; then
	copy m3 "$work/lock"
	cd "$work/lock/platform"
	tofu init > /dev/null
	step m1-lock-versions sh -c "grep -A1 '^provider ' .terraform.lock.hcl"
	step m1-registry-check sh -c 'grep -h "^provider " .terraform.lock.hcl | sed "s/provider \"\(.*\)\" {/\1/" | while read -r p; do
		ns_type=${p#*/}
		curl -fsS -o /dev/null "https://registry.opentofu.org/v1/providers/$ns_type/versions" && echo "found   $ns_type" || echo "missing $ns_type"
	done'
fi

# --- m7, m8: remove OpenTofu's claim on the platform root, then restore it
if want cut; then
	copy m3 "$work/cut"
	cd "$work/cut/platform"
	cp "$out/m3-platform.tfstate" terraform.tfstate
	objects terraform.tfstate
	tofu init > /dev/null
	mkdir -p archive
	tofu state pull > archive/platform-final.tfstate
	tofu state list > archive/platform-claimed.txt
	step m7-state-list cat archive/platform-claimed.txt
	step m7-state-rm-dry-run sh -c 'tofu state list | while read -r addr; do tofu state rm -dry-run "$addr"; done'
	step m7-state-rm sh -c 'tofu state list | while read -r addr; do tofu state rm "$addr"; done'
	step m7-state-list-after tofu state list
	step m7-plan-after tofu plan -detailed-exitcode
	step m7-outputs-after tofu output
	# The rollback after the cut over: the archived state has a lower serial.
	step m8-push-without-force tofu state push archive/platform-final.tfstate
	step m8-push-force tofu state push -force archive/platform-final.tfstate
	step m8-state-list-restored tofu state list
	step m8-plan-restored tofu plan -detailed-exitcode
fi

# --- m5, m8: the estate of M4
if want estate; then
	copy estate "$work/estate"
	cd "$work/estate"
	step estate-init tofu init
	step estate-apply tofu apply -auto-approve
	step estate-plan-empty tofu plan -detailed-exitcode
	cp terraform.tfstate "$out/estate.tfstate"
	cp banner.txt "$out/objects/banner.txt"
	mkdir -p "$out/objects/out/notes"
	cp out/hello.txt "$out/objects/out/hello.txt"
	cp out/notes/alpha.txt out/notes/beta.txt "$out/objects/out/notes/"
fi

# --- m8: a resource type without import support
if want rollback; then
	copy estate "$work/rollback"
	cd "$work/rollback"
	cp "$out/estate.tfstate" terraform.tfstate
	objects terraform.tfstate
	tofu init > /dev/null
	step m8-plan-estate tofu plan -detailed-exitcode
	sed -i.bak 's/default = \["eu", "us"\]/default = ["eu", "us", "ap"]/' variables.tf
	step m8-plan-ap tofu plan -detailed-exitcode
	step m8-import-random-pet tofu import 'random_pet.region["ap"]' ap-caring-leech
fi

# --- m5: a root with two workspaces
if want ws; then
	copy ws "$work/ws"
	cd "$work/ws"
	tofu init > /dev/null
	step ws-workspace-new-dev tofu workspace new dev
	step ws-apply-dev tofu apply -auto-approve
	step ws-workspace-new-prod tofu workspace new prod
	step ws-apply-prod tofu apply -auto-approve
	step ws-workspace-list tofu workspace list
	tofu workspace select dev > /dev/null
	step ws-state-pull-dev tofu state pull
	tofu workspace select prod > /dev/null
	step ws-state-pull-prod tofu state pull
	# The state files of the pages are what "tofu state pull" prints for each workspace.
	cp "$out/log/ws-state-pull-dev.log" "$out/ws-dev.tfstate"
	cp "$out/log/ws-state-pull-prod.log" "$out/ws-prod.tfstate"
fi

# --- landing: the stack of the migration band
if want landing; then
	copy landing "$work/landing"
	cd "$work/landing"
	step landing-init tofu init
	step landing-apply tofu apply -auto-approve
	step landing-plan-empty tofu plan -detailed-exitcode
	cp terraform.tfstate "$out/landing.tfstate"
	cp motd.txt "$out/objects/motd.txt"
	mkdir -p "$out/objects/notes"
	cp notes/alpha.txt notes/beta.txt "$out/objects/notes/"
fi

# --- m1: a root whose plan is not empty: a tainted instance, a deposed object,
# a counted resource, a module and a terraform_remote_state data source
if want m1; then
	mkdir -p "$work/m1"
	copy net "$work/m1/net"
	copy m1/platform "$work/m1/platform"
	cd "$work/m1/net"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cd "$work/m1/platform"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	step m1-taint tofu taint random_pet.logs
	step m1-apply-fails tofu apply -auto-approve -var rev=2 -var fail=true -target=null_resource.gate
	cp terraform.tfstate "$out/m1-platform.tfstate"
	step m1-plan-before tofu plan -var rev=2 -detailed-exitcode
	step m1-apply-fix tofu apply -auto-approve -var rev=2
	step m1-plan-after tofu plan -var rev=2 -detailed-exitcode
fi

# --- m9: small states for the pitfalls
if want m9; then
	copy ok "$work/ok"
	cd "$work/ok"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cp terraform.tfstate "$out/ok.tfstate"

	copy taint "$work/taint"
	cd "$work/taint"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	tofu taint random_pet.server > /dev/null
	tofu apply -auto-approve -var rev=2 -var fail=true -target=null_resource.gate > /dev/null 2>&1 || true
	cp terraform.tfstate "$out/taint.tfstate"

	mkdir -p "$work/builtin"
	copy net "$work/builtin/net"
	copy builtin "$work/builtin/builtin"
	cd "$work/builtin/net"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cd "$work/builtin/builtin"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cp terraform.tfstate "$out/builtin.tfstate"

	copy clash "$work/clash"
	cd "$work/clash"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cp terraform.tfstate "$out/clash.tfstate"
	# The fix: rename one resource with a moved block, then apply.
	sed -e 's/"random_pet" "region_eu"/"random_pet" "eu_extra"/' main.tf > main.new
	cat >> main.new <<'HCL'

moved {
  from = random_pet.region_eu
  to   = random_pet.eu_extra
}
HCL
	mv main.new main.tf
	step clash-plan-moved tofu plan -detailed-exitcode
	step clash-apply-fix tofu apply -auto-approve
	step clash-plan-after tofu plan -detailed-exitcode
	cp terraform.tfstate "$out/clash-fixed.tfstate"
fi

# --- reference: a state with a module, a counted resource, a sensitive output
# and a terraform_remote_state data source
if want ref; then
	mkdir -p "$work/ref"
	copy net "$work/ref/net"
	copy ref "$work/ref/ref"
	cd "$work/ref/net"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cd "$work/ref/ref"
	tofu init > /dev/null
	tofu apply -auto-approve > /dev/null
	cp terraform.tfstate "$out/ref.tfstate"
fi

echo "record: wrote $out"
