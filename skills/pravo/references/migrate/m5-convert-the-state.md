<!-- Generated from docs/migrate/m5-convert-the-state.md by tools/skillgen. Do not edit. -->

# Convert the state

Convert the OpenTofu state into pravo records, point each record at its address, and plan until nothing changes.

This is step 5 of the runbook. You convert the OpenTofu state once, tell pravo where each record now sits, and plan until the plan is empty. An empty plan is the gate: it says that the specification and the real objects agree.

Every command up to the first apply changes the pravo state directory and nothing else. The `.tfstate` file is read and never written. The real objects are read and never changed. Data sources and ephemeral resources run during a plan, as they do in OpenTofu.

For a cloud root, the first `pravo plan` reads every object through its provider, because a converted record has no configuration hash yet. Run it with the credentials and the provider configuration that OpenTofu used.

> [!WARNING]
> Stop rule: never apply a plan that replaces or destroys a resource during the migration. Such a plan means that the specification does not yet describe the object. Fix the specification, or the `$from` fields, and plan again.

The outputs on this page come from a sample root: the estate of [M4](m4-translate-the-specification.md), the objects that OpenTofu 1.13.1 created for it, and the state file `terraform.tfstate` that OpenTofu wrote. The page does not run OpenTofu. The sample files are not printed. Your project has its own. Run the same commands in your root. The addresses and the counts differ. [M3](m3-worked-example.md) prints complete state files that OpenTofu wrote.

## Get the state file

`pravo state import-tfstate` reads a file. For a local backend that file is `terraform.tfstate`. For a remote backend, write the current state to a file first and keep a second copy.

```sh
tofu state pull > terraform.tfstate
cp terraform.tfstate terraform.tfstate.backup
```

The command accepts state format 4, which OpenTofu and Terraform 0.12 or newer write. OpenTofu names its providers on `registry.opentofu.org`. A state from legacy Terraform names them on `registry.terraform.io`. pravo reads each as the same provider on the OpenTofu registry, and keeps a provider on any other host as written.

The state in this workspace holds six resource blocks. Its shape:

```json
{
  "version": 4,
  "terraform_version": "1.13.1",
  "resources": [
    {
      "module": "module.notes",
      "mode": "managed",
      "type": "local_file",
      "name": "note",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"].archive",
      "instances": [
        { "index_key": "alpha", "attributes": { "filename": "out/notes/alpha.txt", "...": "..." } },
        { "index_key": "beta", "attributes": { "filename": "out/notes/beta.txt", "...": "..." } }
      ]
    },
    "..."
  ]
}
```

To see the host rule, rewrite the host in a copy of the file and convert the copy in a throwaway root. The copy stands in for a state that legacy Terraform wrote.

```sh
rm -rf legacy && mkdir legacy && cp -R cue.mod *.cue legacy/
grep -c 'registry.opentofu.org/' terraform.tfstate
sed 's#registry.opentofu.org/#registry.terraform.io/#' terraform.tfstate > legacy.tfstate
grep -c 'registry.terraform.io/' legacy.tfstate
pravo -C legacy state import-tfstate ../legacy.tfstate | head -n 1
pravo -C legacy state show random_pet.server | head -n 1
rm -r legacy legacy.tfstate
```

Output:

```text
6
6
Imported 9 resources and 3 outputs from ../legacy.tfstate into ./legacy/pravo.state
// random_pet.server (provider registry.opentofu.org/hashicorp/random as random, schema version 0, revision 1)
```

The record names `registry.opentofu.org/hashicorp/random`, as it does for the original file.

## Import it

```sh
pravo state import-tfstate terraform.tfstate
```

Output:

```text
Imported 9 resources and 3 outputs from terraform.tfstate into ./pravo.state

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  module.notes.local_file.note["alpha"] -> local_file.notes_note_alpha
  module.notes.local_file.note["beta"] -> local_file.notes_note_beta
  random_integer.port[0] -> random_integer.port_0
  random_integer.port[1] -> random_integer.port_1
  random_pet.region["eu"] -> random_pet.region_eu
  random_pet.region["us"] -> random_pet.region_us
```

The first line counts what was converted: eight managed resources, one data source and three outputs. Each instance is one record in the new state directory, `pravo.state`.

An address in pravo is the place of a resource in the specification. The records keep the OpenTofu address until you say where each one went. Instances with a key, and resources in modules, have no plain address. The command renames them and prints every rename:

- An instance of `count` or `for_each` becomes `<name>_<key>`.
- A resource in a module takes the module path as a prefix: `module.notes.local_file.note["alpha"]` becomes `local_file.notes_note_alpha`.
- Any character other than a letter, a digit, `_` or `-` becomes `_`.

| In the OpenTofu state | Record in the pravo state | Place in the specification |
|---|---|---|
| `data.local_file.banner` | `data.local_file.banner` | `banner` |
| `random_pet.server` | `random_pet.server` | `server` |
| `local_file.hello` | `local_file.hello` | `hello` |
| `random_integer.port[0]` | `random_integer.port_0` | `ports[0]` |
| `random_pet.region["eu"]` | `random_pet.region_eu` | `region.eu` |
| `module.notes.local_file.note["alpha"]` | `local_file.notes_note_alpha` | `notes.files.alpha` |

The command also prints a warning for each thing it cannot carry over, and skips the records of the built-in provider, such as `terraform_remote_state`. [M10](m10-not-in-pravo.md) lists them.

## A root with workspaces

pravo has no workspaces. A tag selects the environment, and the state path follows it, as in [B4](../learn/b04-inputs-tags-environments.md). Each workspace of the OpenTofu root has a state of its own, so each one is converted into the state path of its environment. The tag names the environment, and `import-tfstate` writes to the state path that the tag selects.

The sample is a root in the directory `ws` with the workspaces `dev` and `prod`. The pet takes its prefix from the workspace: `prefix = terraform.workspace`. OpenTofu 1.13.1 listed the workspaces and pulled the state of each. The page does not run these commands.

```sh
tofu workspace list
tofu workspace select dev
tofu state pull > dev.tfstate
tofu workspace select prod
tofu state pull > prod.tfstate
```

```text
  default
  dev
* prod
```

The workspace `default` holds no resource, so it has nothing to convert. The two files below are the output of `tofu state pull`, unedited. The pull prints a state on one line.

File `ws/main.cue`:

```cue
package ws

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

env: *"dev" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {$from: "random_pet.pet", length: 1, prefix: env}
```

File `ws/dev.tfstate`:

```json
{"version":4,"terraform_version":"1.13.1","serial":1,"lineage":"73892dda-414b-edc9-6a58-654fdd6613cc","outputs":{},"resources":[{"mode":"managed","type":"random_pet","name":"pet","provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[{"schema_version":0,"attributes":{"id":"dev-flounder","keepers":null,"length":1,"prefix":"dev","separator":"-"},"sensitive_attributes":[]}]}],"check_results":null}
```

File `ws/prod.tfstate`:

```json
{"version":4,"terraform_version":"1.13.1","serial":1,"lineage":"2849ce56-ff2d-cb00-33d9-0e23a6b5eab3","outputs":{},"resources":[{"mode":"managed","type":"random_pet","name":"pet","provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[{"schema_version":0,"attributes":{"id":"prod-chimp","keepers":null,"length":1,"prefix":"prod","separator":"-"},"sensitive_attributes":[]}]}],"check_results":null}
```

Convert each state with its own tag, and plan each environment with the same tag:

```sh
pravo -C ws init > /dev/null
pravo -C ws state import-tfstate dev.tfstate | head -n 1
pravo -C ws state import-tfstate prod.tfstate -t env=prod | head -n 1
pravo -C ws plan | grep -E '^(  # |Plan:)'
pravo -C ws plan -t env=prod | grep -E '^(  # |Plan:)'
ls ws/state
```

Output:

```text
Imported 1 resources and 0 outputs from dev.tfstate into ./ws/state/dev
Imported 1 resources and 0 outputs from prod.tfstate into ./ws/state/prod
  # random_pet.pet will be moved to pet (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
  # random_pet.pet will be moved to pet (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
dev
prod
```

The specification and its `$from` fields are shared. Each state moves its own record. Work through the rest of this page once for each environment, and pause the pipeline for every workspace of the root before the cut-over of [M7](m7-cut-over.md).

## Plan before you point

Run the plan now, and judge it with the stop rule. The rule refuses any plan that deletes or replaces a managed resource. It is a file that `cue vet -c` checks against the plan document. [M6](m6-rewrite-plan-checks.md) puts the rules into the root as policies, where every plan is judged by them.

File `policy/no-destroy.cue`:

```cue
// During the migration a plan may not delete or replace a managed resource.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
```

The specification does not mention the old addresses yet. pravo cannot tell that `random_pet.server` and `server` are one object.

Status: unintended.

```sh
pravo plan > plan.txt 2>&1
grep -E '^(  # |Plan:)' plan.txt
pravo plan --json 2>/dev/null > plan.json
cue vet -c policy/no-destroy.cue plan.json 2>&1 | grep -E '^changes\.' | sort -V | head -n 3
```

Output:

```text
  # notes.files.alpha (local_file) will be created
  # notes.files.beta (local_file) will be created
  # ports[0] (random_integer) will be created
  # ports[1] (random_integer) will be created
  # region.eu (random_pet) will be created
  # region.us (random_pet) will be created
  # server (random_pet) will be created
  # hello (local_file) will be created
  # local_file.hello (local_file) will be destroyed
  # local_file.notes_note_alpha (local_file) will be destroyed
  # local_file.notes_note_beta (local_file) will be destroyed
  # random_integer.port_0 (random_integer) will be destroyed
  # random_integer.port_1 (random_integer) will be destroyed
  # random_pet.region_eu (random_pet) will be destroyed
  # random_pet.region_us (random_pet) will be destroyed
  # random_pet.server (random_pet) will be destroyed
  # data.local_file.banner (local_file) will be destroyed
Plan: 8 to add, 0 to change, 9 to destroy.
changes.9: local_file.hello: delete is not allowed:
changes.10: local_file.notes_note_alpha: delete is not allowed:
changes.11: local_file.notes_note_beta: delete is not allowed:
```

The plan creates every resource at its new address and deletes every record at its old one. The rule refuses it: nothing here may be applied. The plan changed no object. The only effect was a refresh of the records that it plans to delete.

## Point each record at its address

`$from` on a resource names the address its record has now. When the plan is applied the record moves to the resource. The field belongs in the specification, so a reviewer can read it.

Put every `$from` in one file. pravo unifies `moves.cue` with the declarations in the other files, and the migration adds one file to the repository. A loop in the specification gets a loop in `moves.cue`.

File `moves.cue`:

```cue
package estate

import "list"

banner: $from: "data.local_file.banner"
server: $from: "random_pet.server"
hello:  $from: "local_file.hello"

ports: [for i in list.Range(0, 2, 1) {$from: "random_integer.port_\(i)"}]

region: {
	for r in regions {
		"\(r)": $from: "random_pet.region_\(r)"
	}
}

notes: files: {
	for n in notes.names {
		"\(n)": $from: "local_file.notes_note_\(n)"
	}
}
```

Take the old names from the `Renamed instances` list and from the table above. A resource without a rename keeps its OpenTofu address: `server` names `random_pet.server`.

The same move written on the resource itself reads like this. Use one form for each resource.

```cue
server: $random.#Resource.random_pet & {
	$from:  "random_pet.server"
	prefix: _prefix
	length: 2
}
```

`pravo state mv random_pet.server server` does the same once, without a declaration. It does not check the new address against the specification. Use `$from` for a migration, because the next environment needs the same change.

```sh
pravo plan
```

Output:

```text
pravo will perform the following actions:

  # data.local_file.banner will be moved to banner (record renamed; no change to the object)

  # random_pet.server will be moved to server (record renamed; no change to the object)

  # local_file.hello will be moved to hello (record renamed; no change to the object)

  # random_integer.port_0 will be moved to ports[0] (record renamed; no change to the object)

  # random_integer.port_1 will be moved to ports[1] (record renamed; no change to the object)

  # random_pet.region_eu will be moved to region.eu (record renamed; no change to the object)

  # random_pet.region_us will be moved to region.us (record renamed; no change to the object)

  # local_file.notes_note_alpha will be moved to notes.files.alpha (record renamed; no change to the object)

  # local_file.notes_note_beta will be moved to notes.files.beta (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 9 to move.
Graph: planned 8 · refreshed 9 · to move 9

Outputs: 3 outputs unchanged.
```

Read the plan. Each line says that a record will be renamed and that the object does not change. The summary adds nothing and destroys nothing. The plan deletes and replaces nothing, so the stop rule holds.

The `Graph:` line says `planned 8`. Imported records carry no configuration hash yet, so this plan asks the provider about every one of them, once.

The plan writes nothing. It holds the renamed records in memory, so that it compares each one with the specification, and the pravo state keeps the OpenTofu addresses until the first apply renames them:

```sh
pravo state list
```

Output:

```text
data.local_file.banner
local_file.hello
local_file.notes_note_alpha
local_file.notes_note_beta
random_integer.port_0
random_integer.port_1
random_pet.region_eu
random_pet.region_us
random_pet.server
```

A move is no change to an object, so the exit code of a plan that has moves and nothing else is 0:

```sh
pravo plan --detailed-exitcode > /dev/null
echo "exit=$?"
```

Output:

```text
exit=0
```

With `--detailed-exitcode` the exit code is 0 for a plan that changes no object and 2 when the plan has changes. A CI job uses it as the gate. Moves count as no change, and so do the imports of `$import`.

This plan read all nine objects, as `9 refreshed` says. After the first apply the records carry hashes, and a plan reads only the records it looks at. A gate that must read every object runs `pravo plan -refresh=all --detailed-exitcode`.

## When the plan is not empty

Read the first non-empty plan from the top. The line that differs from the object names the cause.

| Symptom | Cause | Fix |
|---|---|---|
| `# random_pet.server (random_pet) will be destroyed` and `# server (random_pet) will be created` | The resource has no `$from`, or `$from` names an address that is not in the state. | Add `$from` with the name from `Renamed instances`. |
| One resource is created and one destroyed, and the rest moved. | `$from` uses the OpenTofu spelling `random_pet.region["eu"]`. The record is `random_pet.region_eu`. | Write the name that the import printed. |
| `Error: $from local_file.hello -> server: the record is a managed local_file but server is declared as a managed random_pet; the type must stay the same` | `$from` names a record of another type. | Correct the address. |
| `Error: $from random_integer.port_0 -> ports[0]: both addresses exist in state; remove one with "pravo state rm"` | Both records exist, for example after `pravo import` of an object that the converted state holds. | Keep the converted record. Run `pravo state rm 'ports[0]'`, then plan again. |
| `# server (random_pet) must be replaced` with `~ length: 2 -> 3 # forces replacement` | The translated value differs from the object. | Set the specification to the value that `pravo state show server` prints. |
| A change on a resource that you did not touch, such as a new default. | The provider is newer than the one OpenTofu applied with. | Pin `$version` to the version in `.terraform.lock.hcl`, run `pravo init`, and plan again. |
| A progress line says that `hello` does not exist and will be created again, and the plan has `# hello (local_file) will be created`. | The provider cannot find the object. The file is missing, the working directory is another one, or the provider reads another region, account or endpoint. | Run from the directory OpenTofu used. Give the provider the same configuration and credentials. |
| `Graph: planned 9` and no change | Imported records have no hash yet. | None. The first apply writes it. |

Do not apply a plan that matches any row except the last. Apply only after the fix in the table has produced an empty plan. The deep dive below runs five of them.

**Reproduce each symptom**

Each trial copies the project to a directory, edits the copy and plans in it. The copy has its own `pravo.state`, so the project is not touched.

A resource without `$from`: the broken plan shown earlier. The next trial writes the OpenTofu spelling of a key.

```sh
rm -rf t1 && mkdir t1 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t1/
cat > t1/moves.cue <<'CUE'
package estate

banner: $from: "data.local_file.banner"
server: $from: "random_pet.server"
hello:  $from: "local_file.hello"
region: eu: $from: "random_pet.region[\"eu\"]"
region: us: $from: "random_pet.region_us"
ports: [{$from: "random_integer.port_0"}, {$from: "random_integer.port_1"}]
notes: files: alpha: $from: "local_file.notes_note_alpha"
notes: files: beta: $from: "local_file.notes_note_beta"
CUE
pravo -C t1 state import-tfstate terraform.tfstate > /dev/null
pravo -C t1 plan 2>&1 | grep -E '^(  # .* will be|Plan:)'
```

Output:

```text
  # data.local_file.banner will be moved to banner (record renamed; no change to the object)
  # random_pet.server will be moved to server (record renamed; no change to the object)
  # local_file.hello will be moved to hello (record renamed; no change to the object)
  # random_integer.port_0 will be moved to ports[0] (record renamed; no change to the object)
  # random_integer.port_1 will be moved to ports[1] (record renamed; no change to the object)
  # random_pet.region_us will be moved to region.us (record renamed; no change to the object)
  # local_file.notes_note_alpha will be moved to notes.files.alpha (record renamed; no change to the object)
  # local_file.notes_note_beta will be moved to notes.files.beta (record renamed; no change to the object)
  # region.eu (random_pet) will be created
  # random_pet.region_eu (random_pet) will be destroyed
Plan: 1 to add, 0 to change, 1 to destroy, 8 to move.
```

A `$from` that names a record of another type:

```sh
rm -rf t2 && mkdir t2 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t2/
printf 'package estate\n\nserver: $from: "local_file.hello"\n' > t2/moves.cue
pravo -C t2 state import-tfstate terraform.tfstate > /dev/null
pravo -C t2 plan 2>&1 | grep '^Error'
```

Output:

```text
Error: $from local_file.hello -> server: the record is a managed local_file but server is declared as a managed random_pet; the type must stay the same
```

A value in the specification that differs from the object:

```sh
rm -rf t3 && mkdir t3 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t3/
cp moves.cue t3/ 2>/dev/null || true
sed 's/length: 2/length: 3/' t3/resources.cue > t3/resources.new && mv t3/resources.new t3/resources.cue
pravo -C t3 state import-tfstate terraform.tfstate > /dev/null
pravo -C t3 plan 2>&1 | grep -E '(must be replaced|forces replacement)'
```

Output:

```text
  # server (random_pet) must be replaced
  ~     length:    2 -> 3 # forces replacement
  # hello (local_file) must be replaced
  ~     content:              "estate\nHello, acme-dev-charmed-buzzard!\n" -> (known after apply) # forces replacement
```

An object that the provider cannot find:

```sh
rm -rf t4 && mkdir t4 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t4/
rm t4/out/hello.txt
pravo -C t4 state import-tfstate terraform.tfstate > /dev/null
pravo -C t4 plan 2>&1 | grep -E '(no longer exists|will be created)'
```

Output:

```text
hello: no longer exists; it will be created again
  # hello (local_file) will be created
```

Both records exist, because the object was also imported by hand:

```sh
rm -rf t5 && mkdir t5 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t5/
pravo -C t5 state import-tfstate terraform.tfstate > /dev/null
pravo -C t5 import 'ports[0]' 8111,8000,8999 > /dev/null
pravo -C t5 plan 2>&1 | grep '^Error'
pravo -C t5 state rm 'ports[0]'
pravo -C t5 plan 2>&1 | grep '^Plan'
```

Output:

```text
Error: $from random_integer.port_0 -> ports[0]: both addresses exist in state; remove one with "pravo state rm"
Removed ports[0]
Plan: 0 to add, 0 to change, 0 to destroy, 9 to move.
```

## The first apply

The first apply carries out the moves, and then finalises the records: it writes the configuration hash, the provider version and the instance key of every record. It changes no object. The records of the converted state show the difference: the plan left the record of `hello` as the import wrote it.

```sh
pravo state show local_file.hello | head -n 3 | sed -E 's/revision [0-9]+/revision <n>/'
pravo output
```

Output:

```text
// local_file.hello (provider registry.opentofu.org/hashicorp/local as local, schema version 0, revision <n>)
// refreshed <age> ago, applied never, config hash none
// depends on data.local_file.banner, random_integer.port_0, random_integer.port_1, random_pet.server
note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
port_numbers: (sensitive)
server_name: "acme-dev-charmed-buzzard"
```

The import copies the output values and their `sensitive` mark. `pravo output` lists `port_numbers` as `(sensitive)`.

```sh
pravo apply --auto-approve | grep '^Apply complete'
pravo state list
pravo state show hello | head -n 3 | sed -E 's/revision [0-9]+/revision <n>/'
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
banner
hello
notes.files.alpha
notes.files.beta
ports[0]
ports[1]
region.eu
region.us
server
// hello (provider registry.opentofu.org/hashicorp/local v<version> as local.$local, schema version 0, revision <n>)
// refreshed <age> ago, applied never, config hash <hash>
// depends on banner, ports[0], ports[1], server
```

The records are at their new addresses now. `config hash none` became a hash, and the provider version and the instance key `local.$local` appeared. `applied never` stays, because pravo created and changed nothing. The apply left the objects and the OpenTofu state as they were, so `tofu plan -detailed-exitcode` on the root still exits 0 in OpenTofu 1.13.1. The next plan skips every record whose hash and provider version match, so it is faster.

The first apply records the outputs again, from the specification. An output marked `sensitive` there stays hidden in the list.

```sh
pravo plan --detailed-exitcode
echo "exit=$?"
```

Output:

```text
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.
exit=0
```

## A first real change

The empty plan proved that pravo can read the objects. A small change proves that it can write them. Add a third region. The only edit is the default of `regions`.

File `variables.cue`:

```cue
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us", "ap"]
```

Save the plan to a file and read it before you apply.

```sh
pravo plan -out change.plan
```

Output:

```text
pravo will perform the following actions:

  # region.ap (random_pet) will be created
  + region: ap: {
  +     id:        (known after apply)
  +     length:    2
  +     prefix:    "ap"
  +     separator: "-"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.

Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

The plan creates `region.ap` and touches no other resource. Applying the saved plan performs exactly these actions.

```sh
pravo apply change.plan
pravo state list
```

Output:

```text
region.ap: Creating...
region.ap: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]

3 outputs, none changed. See them with "pravo output" or "pravo serve".
banner
hello
notes.files.alpha
notes.files.beta
ports[0]
ports[1]
region.ap
region.eu
region.us
server
```

The `$from` fields have done their work. Delete `moves.cue` once every environment has applied. The plan after the deletion is still empty.

```sh
rm moves.cue
pravo plan
```

Output:

```text
No changes. The specification matches the state. 9 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 9 · refreshed 1 · not read 9

Outputs: 3 outputs unchanged.
```

## Recap

- `pravo state import-tfstate` converts the state once. Each instance is a record, renamed when it has a key or a module path.
- `$from` points a record at its resource. Plan until the plan is empty, and refuse any plan that deletes or replaces.
- The first apply of an empty plan finalises the records. A small saved plan proves that pravo can write.

Next: [M6 Rewrite the checks on plans](m6-rewrite-plan-checks.md) turns the CI checks that read the OpenTofu plan JSON into plan policies.
