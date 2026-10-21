#!/bin/sh
# Runs tofu to check the statements about OpenTofu behaviour that the
# documentation makes, and fails when one of them no longer holds.
#
#   sh tools/hclcheck/claims.sh [tofu-binary]
#
# It needs tofu, jq and the network (the providers local and random come from
# the registry the first time; set TF_PLUGIN_CACHE_DIR to keep them). Every
# check prints "ok" or "FAIL" and a name. The exit status is the number of
# checks that failed, at most 255. Each check names the page that makes the
# statement.
set -u

tofu=${1:-tofu}
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/claims.XXXXXX")
trap 'rm -rf "$work"' EXIT
export TF_IN_AUTOMATION=1 TF_INPUT=0 CHECKPOINT_DISABLE=1 NO_COLOR=1
unset TF_LOG TF_WORKSPACE TF_CLI_ARGS
for v in $(env | sed -n 's/^\(TF_VAR_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done
if [ -z "${TF_PLUGIN_CACHE_DIR:-}" ]; then
	mkdir -p "$work/cache"
	export TF_PLUGIN_CACHE_DIR="$work/cache"
fi

failed=0
check() { # check NAME ACTUAL EXPECTED
	if [ "$2" = "$3" ]; then
		printf 'ok    %s\n' "$1"
	else
		printf 'FAIL  %s\n      got:  %s\n      want: %s\n' "$1" "$2" "$3"
		failed=$((failed + 1))
	fi
}
contains() { # contains NAME TEXT PATTERN
	if printf '%s' "$2" | grep -q -- "$3"; then
		printf 'ok    %s\n' "$1"
	else
		printf 'FAIL  %s\n      pattern: %s\n      text:    %s\n' "$1" "$3" "$(printf '%s' "$2" | head -n 5)"
		failed=$((failed + 1))
	fi
}
lacks() { # lacks NAME TEXT PATTERN
	if printf '%s' "$2" | grep -q -- "$3"; then
		printf 'FAIL  %s\n      text holds: %s\n' "$1" "$3"
		failed=$((failed + 1))
	else
		printf 'ok    %s\n' "$1"
	fi
}
esc=$(printf '\033')
plain() { sed "s/${esc}\[[0-9;]*m//g"; }
lab() { rm -rf "$work/$1" && mkdir -p "$work/$1" && cd "$work/$1" || exit 2; }
quiet() { "$@" >/dev/null 2>&1; }
exitcode() { "$@" >/dev/null 2>&1; echo $?; }

printf 'tofu: %s\n' "$($tofu version | head -n 1)"

# --- Variable precedence (docs/learn/a04, a03; docs/how-to/quick-start.md) --------
lab precedence
echo 'variable "v" { default = "default" }' >main.tf
var() { echo 'var.v' | $tofu console "$@" 2>&1 | plain; }
check "a default is the lowest" "$(var)" '"default"'
check "TF_VAR_ beats the default" "$(TF_VAR_v=env var)" '"env"'
echo 'v = "tfvars"' >terraform.tfvars
check "terraform.tfvars beats TF_VAR_" "$(TF_VAR_v=env var)" '"tfvars"'
echo '{"v":"json"}' >terraform.tfvars.json
check "terraform.tfvars.json beats terraform.tfvars" "$(var)" '"json"'
echo 'v = "b"' >b.auto.tfvars
echo '{"v":"a"}' >a.auto.tfvars.json
check "auto files load in file-name order" "$(var)" '"b"'
echo '{"v":"z"}' >z.auto.tfvars.json
check "a later auto file wins" "$(var)" '"z"'
echo 'v = "base"' >base.tfvars
echo 'v = "prod"' >prod.tfvars
check "-var-file: the last file wins (prod last)" "$(var -var-file=base.tfvars -var-file=prod.tfvars)" '"prod"'
check "-var-file: the last file wins (base last)" "$(var -var-file=prod.tfvars -var-file=base.tfvars)" '"base"'
check "-var after -var-file wins" "$(var -var-file=base.tfvars -var v=cli)" '"cli"'
check "-var-file after -var wins" "$(var -var v=cli -var-file=base.tfvars)" '"base"'

# --- merge and a caller that replaces a module default (hero, a03, b10) ---
lab merge
check "merge: the last map wins" "$(printf '%s\n' 'merge({ encrypted = true }, { encrypted = false })' | $tofu console | tr -d '\n ')" '{"encrypted"=false}'
lab module_default
mkdir modules
cat >modules/main.tf <<'EOF'
variable "filename" {}
variable "file_permission" { default = "0600" }
resource "local_file" "this" {
  filename        = var.filename
  content         = "x"
  file_permission = var.file_permission
}
EOF
cat >main.tf <<'EOF'
module "keyfile" {
  source          = "./modules"
  filename        = "deploy.key"
  file_permission = "0644"
}
EOF
quiet $tofu init
plan=$($tofu plan 2>&1 | plain)
contains "a caller replaces the default of a module" "$plan" 'file_permission *= "0644"'
lacks "no warning accompanies the replacement" "$plan" 'Warning'

# --- count and for_each identity (a10) ------------------------------------
lab identity
cat >main.tf <<'EOF'
variable "subnets" {
  type = list(object({ zone = string, cidr = string }))
}
resource "local_file" "by_index" {
  count    = length(var.subnets)
  filename = "i_${var.subnets[count.index].zone}.txt"
  content  = var.subnets[count.index].cidr
}
resource "local_file" "by_key" {
  for_each = { for s in var.subnets : s.zone => s }
  filename = "k_${each.key}.txt"
  content  = each.value.cidr
}
EOF
cat >one.tfvars <<'EOF'
subnets = [
  { zone = "a", cidr = "10.0.1.0/24" },
  { zone = "b", cidr = "10.0.2.0/24" },
  { zone = "c", cidr = "10.0.3.0/24" },
]
EOF
cat >two.tfvars <<'EOF'
subnets = [
  { zone = "b", cidr = "10.0.2.0/24" },
  { zone = "a", cidr = "10.0.1.0/24" },
  { zone = "c", cidr = "10.0.3.0/24" },
]
EOF
quiet $tofu init && quiet $tofu apply -auto-approve -var-file=one.tfvars
plan=$($tofu plan -var-file=two.tfvars 2>&1 | plain)
contains "count: swapping two entries replaces two instances" "$plan" 'Plan: 2 to add, 0 to change, 2 to destroy'
contains "count: the instances are replaced by position" "$plan" 'local_file.by_index\[0\] must be replaced'
lacks "for_each: the same swap touches no instance" "$plan" '# local_file.by_key'

# --- override files (docs/how-to/quick-start.md, a04) ----------------------------
lab override
cat >main.tf <<'EOF'
resource "local_file" "config" {
  filename        = "server.env"
  content         = "x"
  file_permission = "0600"
}
EOF
printf 'resource "local_file" "config" {\n  file_permission = "0644"\n}\n' >override.tf
quiet $tofu init
contains "override.tf replaces an argument of another file" "$($tofu plan 2>&1 | plain)" 'file_permission *= "0644"'
mv override.tf other.tf
contains "the same resource in an ordinary file is a duplicate" "$($tofu plan 2>&1 | plain)" 'Duplicate resource'
rm other.tf main.tf
printf 'resource "local_file" "config" {\n  file_permission = "0644"\n}\n' >override.tf
contains "an override without a base is an error" "$($tofu plan 2>&1 | plain)" 'Missing resource to override'

# --- workspaces (b04, m1, m5) ---------------------------------------------
lab workspaces
cat >main.tf <<'EOF'
output "ws" { value = terraform.workspace }
resource "terraform_data" "x" { input = terraform.workspace }
EOF
quiet $tofu init
quiet $tofu workspace new prod
check "workspace select is stored in the directory" "$(cat .terraform/environment)" 'prod'
quiet $tofu apply -auto-approve
check "a later command reads the stored workspace" "$($tofu output -raw ws)" 'prod'
check "a workspace state is a file of its own" "$(find . -name '*.tfstate' | sort | tr '\n' ' ')" './terraform.tfstate.d/prod/terraform.tfstate '

# --- exit codes, refresh-only, import and moved (m1, b08, b09) -----------
lab exitcodes
cat >main.tf <<'EOF'
resource "random_integer" "server" {
  min = 1
  max = 10
}
resource "local_file" "web" {
  filename = "web.conf"
  content  = "x"
}
EOF
quiet $tofu init && quiet $tofu apply -auto-approve
check "plan -detailed-exitcode: 0 when nothing changes" "$(exitcode $tofu plan -detailed-exitcode)" 0
rm web.conf
check "plan -refresh-only -detailed-exitcode: 2 on drift" "$(exitcode $tofu plan -refresh-only -detailed-exitcode)" 2
quiet $tofu apply -refresh-only -auto-approve
sed -i.bak 's/"server"/"primary"/' main.tf
contains "a rename without moved destroys and creates" "$($tofu plan -no-color 2>&1 | plain)" 'random_integer.server will be destroyed'
cat >>main.tf <<'EOF'

moved {
  from = random_integer.server
  to   = random_integer.primary
}
EOF
plan=$($tofu plan -detailed-exitcode 2>&1 | plain)
contains "a moved block is a plan with no actions" "$plan" 'random_integer.server has moved to random_integer.primary'
check "a pending moved block makes the plan exit 2" "$(exitcode $tofu plan -detailed-exitcode)" 2
cat >>main.tf <<'EOF'

resource "random_integer" "dice" {
  min = 1
  max = 10
}

import {
  to = random_integer.dice
  id = "5,1,10"
}
EOF
contains "an import block is planned" "$($tofu plan 2>&1 | plain)" 'random_integer.dice will be imported'
echo 'x' >bad.tf
check "an error exits 1" "$(exitcode $tofu plan -detailed-exitcode)" 1
rm bad.tf

# --- state commands (m1, m7, m8) ------------------------------------------
lab state
cat >main.tf <<'EOF'
resource "random_pet" "region" {
  for_each = toset(["eu", "us"])
  prefix   = each.key
}
EOF
quiet $tofu init && quiet $tofu apply -auto-approve
mkdir archive
$tofu state pull >archive/final.tfstate
check "state pull writes version 4" "$(jq -r .version archive/final.tfstate)" 4
check "state list quotes a keyed address" "$($tofu state list | head -n 1)" 'random_pet.region["eu"]'
contains "state rm -dry-run names each address" "$($tofu state rm -dry-run 'random_pet.region["eu"]' 2>&1 | plain)" 'Would remove random_pet.region\["eu"\]'
check "the state keeps its resources after a dry run" "$($tofu state list | wc -l | tr -d ' ')" 2
$tofu state list | while read -r addr; do $tofu state rm "$addr" >/dev/null 2>&1; done
check "state list prints nothing after the removal" "$($tofu state list)" ''
check "state list exits 0 on an empty state" "$(exitcode $tofu state list)" 0
quiet $tofu state push archive/final.tfstate
$tofu state rm 'random_pet.region["us"]' >/dev/null 2>&1
contains "push of a lower serial over a non-empty state is refused" "$($tofu state push archive/final.tfstate 2>&1 | plain)" 'cannot import state with serial'
check "push -force writes the lower serial" "$(exitcode $tofu state push -force archive/final.tfstate)" 0
check "push -force restores both resources" "$($tofu state list | wc -l | tr -d ' ')" 2

# --- locks (lab 2) ---------------------------------------------------------
lab lock
cat >main.tf <<'EOF'
resource "terraform_data" "slow" {
  provisioner "local-exec" {
    command = "sleep 5"
  }
}
resource "terraform_data" "other" {
  input = "x"
}
EOF
quiet $tofu init
($tofu apply -auto-approve -target=terraform_data.slow >/dev/null 2>&1 &)
sleep 2
contains "one lock covers the whole state" "$($tofu apply -auto-approve -target=terraform_data.other 2>&1 | plain)" 'Error acquiring the state lock'
sleep 5

# --- variables and validation (a02, a05, a07, b03, b06) -------------------
lab variables
cat >main.tf <<'EOF'
variable "retention_days" { type = number }
variable "port" {
  type    = number
  default = 443
  validation {
    condition     = var.port >= 1 && var.port <= 65535
    error_message = "Use a port from 1 to 65535."
  }
}
variable "bucket" {
  type = object({
    name        = string
    description = optional(string)
    versioning  = optional(bool, false)
  })
  default = { name = "logs" }
}
variable "secret" {
  type      = string
  sensitive = true
  default   = "s3cr3t"
}
resource "local_file" "key" {
  filename = "key.txt"
  content  = var.secret
}
EOF
echo 'retention_days = "thirty"' >terraform.tfvars
quiet $tofu init
contains "validate does not read a tfvars file" "$($tofu validate 2>&1 | plain)" 'is valid'
contains "plan type-checks a tfvars file" "$($tofu plan 2>&1 | plain)" 'a number is required'
echo 'retention_days = 30' >terraform.tfvars
out=$($tofu plan -var port=70000 2>&1 | plain)
contains "a failed validation block: heading" "$out" 'Error: Invalid value for variable'
contains "a failed validation block: the error_message" "$out" 'Use a port from 1 to 65535\.'
contains "a failed validation block: the rule" "$out" 'This was checked by the validation rule at main.tf'
bucket=$(printf '%s\n' 'var.bucket' | $tofu console 2>&1 | plain | tr -d '\n ')
check "optional() gives null and the default" "$bucket" '{"description"=tostring(null)"name"="logs""versioning"=false}'
quiet $tofu apply -auto-approve
contains "a sensitive variable reaches the state in clear" "$(cat terraform.tfstate)" '"content": "s3cr3t"'
contains "a sensitive variable is hidden in the plan" "$($tofu plan -var secret=other 2>&1 | plain)" 'content *= (sensitive value)'

# --- the plan JSON (m6) ------------------------------------------------------
lab planjson
cat >main.tf <<'EOF'
resource "local_file" "note" {
  filename        = "note.txt"
  content         = "a"
  file_permission = "0644"
}
output "o" { value = local_file.note.filename }
EOF
quiet $tofu init && quiet $tofu apply -auto-approve
sed -i.bak 's/content         = "a"/content         = "b"/' main.tf
quiet $tofu plan -out=change.plan
$tofu show -json change.plan >plan.json
check "plan JSON: top-level keys" "$(jq -r 'keys | join(",")' plan.json)" 'configuration,errored,format_version,output_changes,planned_values,prior_state,resource_changes,terraform_version,timestamp'
check "plan JSON: change keys" "$(jq -r '.resource_changes[0].change | keys | join(",")' plan.json)" 'actions,after,after_sensitive,after_unknown,before,before_sensitive,replace_paths'
check "plan JSON: a replacement is delete and create" "$(jq -c '.resource_changes[0].change.actions' plan.json)" '["delete","create"]'
check "plan JSON: the reason of a replacement" "$(jq -r '.resource_changes[0].action_reason' plan.json)" 'replace_because_cannot_update'
check "plan JSON: the address key" "$(jq -r '.resource_changes[0] | keys | join(",")' plan.json)" 'action_reason,address,change,mode,name,provider_name,type'
check "the jq rule of M6 fails on a replacement" "$(jq -e '[.resource_changes[] | select(.change.actions | index("delete"))] | length == 0' plan.json)" false

# --- constructs (docs/migrate/construct-matrix.md) --------------------------
lab constructs
mkdir modules
echo 'output "o" { value = 1 }' >modules/main.tf
cat >main.tf <<'EOF'
terraform {
  required_version = ">= 1.6"
  required_providers {
    random = { source = "hashicorp/random" }
    local  = { source = "hashicorp/local" }
  }
  backend "local" {
    path = "x.tfstate"
  }
  encryption {
    key_provider "pbkdf2" "k" {
      passphrase = "correct horse battery staple"
    }
    method "aes_gcm" "m" {
      keys = key_provider.pbkdf2.k
    }
    state {
      method = method.aes_gcm.m
    }
  }
}

provider "local" {
  alias = "west"
}

variable "env" {
  type    = string
  default = "dev"
}

locals {
  names = ["a", "b"]
  tmpl  = "%{ for n in local.names }${n},%{ endfor }"
  here  = "${path.module} ${path.root} ${terraform.workspace}"
  splat = random_integer.port[*].result
}

resource "random_integer" "port" {
  count = 2
  min   = 1
  max   = 9
}

resource "random_pet" "p" {
  for_each   = toset(local.names)
  prefix     = each.key
  depends_on = [random_integer.port]
  lifecycle {
    create_before_destroy = true
    prevent_destroy       = false
    ignore_changes        = [prefix]
    replace_triggered_by  = [random_integer.port[0]]
    precondition {
      condition     = var.env != ""
      error_message = "env is empty"
    }
    postcondition {
      condition     = self.id != ""
      error_message = "empty id"
    }
  }
}

resource "terraform_data" "td" {
  input = var.env
  provisioner "local-exec" {
    command = "echo hi"
  }
}

resource "local_file" "f" {
  provider = local.west
  filename = "f.txt"
  content  = "x"
  connection {
    type = "ssh"
    host = "example.com"
  }
}

ephemeral "random_password" "pw" {
  length = 8
}

data "terraform_remote_state" "r" {
  backend = "local"
  config  = { path = "x.tfstate" }
}

module "m" {
  source     = "./modules"
  count      = 1
  depends_on = [random_integer.port]
}

output "o" {
  value      = module.m[0].o
  depends_on = [random_integer.port]
  precondition {
    condition     = var.env != ""
    error_message = "never"
  }
}

moved {
  from = random_integer.old
  to   = random_integer.port
}

removed {
  from = random_pet.gone
  lifecycle {
    destroy = false
  }
}

check "c" {
  assert {
    condition     = var.env != ""
    error_message = "x"
  }
}
EOF
quiet $tofu init
check "every construct of the matrix validates" "$($tofu validate 2>&1 | plain | head -n 1)" 'Success! The configuration is valid.'

# --- functions (docs/migrate/function-map.md) --------------------------------
lab functions
ev() { printf '%s\n' "$1" | $tofu console 2>&1 | plain | tr -d '\n' | sed 's/  */ /g'; }
check "keys sorts" "$(ev 'keys({b=1,a=2})')" '[ "a", "b",]'
check "length counts characters" "$(ev 'length("héllo")')" 5
check "urlencode uses +" "$(ev 'urlencode("a b&c")')" '"a+b%26c"'
check "timecmp returns a number" "$(ev 'timecmp("2023-11-14T22:13:20Z", "2023-11-15T00:00:00Z")')" -1
check "indent leaves the first line" "$(ev 'indent(2, "a\nb")')" '<<EOTa bEOT'
check "coalesce skips null and empty strings" "$(ev 'coalesce(null, "", "b", "c")')" '"b"'
check "element wraps around" "$(ev 'element(["a","b","c"], 4)')" '"b"'
check "type exists in console" "$(ev 'type([1])' | cut -c1-5)" 'tuple'
count=$($tofu metadata functions -json | jq '[.function_signatures | keys[] | select(startswith("core::") | not)] | length')
contains "the function map states the number of functions" "$(cat "$root/docs/migrate/function-map.md")" "OpenTofu 1.13 has $count built-in functions"
missing=$($tofu metadata functions -json | jq -r '.function_signatures | keys[] | select(startswith("core::") | not)' | while read -r f; do grep -q "^| \`$f(" "$root/docs/migrate/function-map.md" || printf '%s ' "$f"; done)
check "the function map has a row for every function" "$missing" ''

exit $((failed > 255 ? 255 : failed))
