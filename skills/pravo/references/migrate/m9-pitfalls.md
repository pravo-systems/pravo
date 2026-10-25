<!-- Generated from docs/migrate/m9-pitfalls.md by tools/skillgen. Do not edit. -->

# Pitfalls

Symptoms you may meet while you translate, convert and cut over, with the cause, the fix and the message as pravo prints it.

Every entry has the same parts: the message as pravo prints it, the cause, and the fix. Each message below comes from a run of the page. Paths are shown relative to the workspace.

| You see | Entry |
|---|---|
| `no CUE files in .` | [Import before the specification](#import-before-the-specification) |
| `unsupported state format version 3` | [A state that is not format 4](#a-state-that-is-not-format-4) |
| `legacy flatmap attributes` | [A state with flat attributes](#a-state-with-flat-attributes) |
| `parsing tfstate` | [A file that is not a state](#a-file-that-is-not-a-state) |
| `already holds 1 resources` | [A second import](#a-second-import) |
| `would both become` | [Two instances with one name](#two-instances-with-one-name) |
| `tainted in the source state`, `deposed object` | [Taint and deposed objects](#taint-and-deposed-objects) |
| `skipped, provider terraform.io/builtin/terraform` | [A record of the built-in provider](#a-record-of-the-built-in-provider) |
| `the state path ... is a file` | [A state path that is a file](#a-state-path-that-is-a-file) |
| `reference "loc" not found` | [An import in the wrong file](#an-import-in-the-wrong-file) |
| `imported and not used` | [An unused import](#an-unused-import) |
| `${env}` in a plan | [A string that keeps its dollar](#a-string-that-keeps-its-dollar) |
| `Missing Configuration for Required Attribute` | [A name that refers to itself](#a-name-that-refers-to-itself) |
| `conflicting values false and true` | [Merging with an override](#merging-with-an-override) |
| `is not concrete (is a -t tag missing?)` | [A tag that was not given](#a-tag-that-was-not-given) |
| `-t` with a list | [A list given on the command line](#a-list-given-on-the-command-line) |
| `provider notes.provider was not supplied`, `as local.notes.provider` | [A provider that was not passed](#a-provider-that-was-not-passed) |
| `is not in state` for a key with a dash | [An address that needs quotes](#an-address-that-needs-quotes) |
| `ephemeral values may only be used` | [A secret in the wrong place](#a-secret-in-the-wrong-place) |
| `one version of a provider is used` | [Two versions of one provider](#two-versions-of-one-provider) |
| `no version of ... matches` | [A version that does not exist](#a-version-that-does-not-exist) |
| `does not compile` for a policy | [A policy that does not load](#a-policy-that-does-not-load) |

Plans that are not empty after the conversion have their own table in [M5](m5-convert-the-state.md#when-the-plan-is-not-empty).

## Converting the state

The workspace holds a small root, `r`, that the conversion commands run in. OpenTofu 1.13.1 wrote the state files of this section, one for each small HCL root, and the page does not run OpenTofu. A file is not printed when the HCL that wrote it is shown.

File `r/main.cue`:

```cue
package r

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {$from: "random_pet.server", length: 2, prefix: "acme-dev"}
```

The first state, `ok.tfstate`, belongs to one resource:

```hcl
resource "random_pet" "server" {
  prefix = "acme-dev"
  length = 2
}
```

### Import before the specification

```sh
mkdir empty && cp ok.tfstate empty/
pravo -C empty state import-tfstate ok.tfstate
```

Output:

```text
Error: loading ./empty:
no CUE files in .
```

Cause: the command opens the state of the root, and a directory with no `.cue` file is not a root. Fix: write the specification and run `pravo init` first, as in [M4](m4-translate-the-specification.md). The conversion needs a root that loads.

### A state that is not format 4

OpenTofu 1.13.1 does not write format 3 or format 5, so this file is written by hand.

```sh
pravo -C r init > /dev/null
echo '{"version":3,"modules":[]}' > v3.tfstate
pravo -C r state import-tfstate ../v3.tfstate
```

Output:

```text
Error: ../v3.tfstate: unsupported state format version 3: pravo reads format version 4, which OpenTofu and Terraform 0.12 or newer write
```

Cause: the file has another format version. The message names it, for a version above 4 as well. OpenTofu 1.13.1 refuses format 3 too: it prints `Unsupported state file format: The state file uses format version 3, which is not supported by OpenTofu 1.13.1`. Fix: write the state again in format 4 with a tool that reads the old format, then pull it again.

### A state with flat attributes

No current tool writes `attributes_flat`, so this file is written by hand.

File `flat.tfstate`:

```json
{"version":4,"terraform_version":"0.12.31","serial":1,"lineage":"x","outputs":{},"resources":[{"mode":"managed","type":"random_pet","name":"server","provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[{"schema_version":0,"attributes_flat":{"id":"x","length":"2"}}]}]}
```

```sh
pravo -C r state import-tfstate ../flat.tfstate
```

Output:

```text
Error: ../flat.tfstate: random_pet.server: legacy flatmap attributes are not supported; OpenTofu cannot read them either, so refresh or apply the state with an older provider version first
```

Cause: an instance written by Terraform 0.11 or earlier keeps its attributes in the flat form. The file was never rewritten. OpenTofu 1.13.1 cannot read the form either: `tofu plan` stops with `flatmap states cannot be unmarshaled, only states written by Terraform 0.12 and higher can be unmarshaled`, and `tofu refresh` stops with the same text. Fix: refresh or apply the root with an older provider version, as OpenTofu asks, until `tofu plan` reads the state. Then pull the state again.

### A file that is not a state

The shell creates `terraform.tfstate` before `tofu state pull` runs, and `tofu` prints its errors on standard error. A pull that fails leaves an empty file. A root that has no state pulls an empty file too.

```sh
: > pulled.tfstate
pravo -C r state import-tfstate ../pulled.tfstate
```

Output:

```text
Error: ../pulled.tfstate: parsing tfstate: unexpected end of JSON input
```

A pull written with `2>&1` puts the error message into the file.

```sh
echo 'Error: backend initialization required' > pulled.tfstate
pravo -C r state import-tfstate ../pulled.tfstate
```

Output:

```text
Error: ../pulled.tfstate: parsing tfstate: invalid character 'E' looking for beginning of value
```

Cause: the file is empty or holds text that is not JSON. Fix: read the first lines of the file, run `tofu init`, and pull again. A good file starts with `{"version": 4`.

### A second import

```sh
pravo -C r state import-tfstate ../ok.tfstate
pravo -C r state import-tfstate ../ok.tfstate
```

Output:

```text
Imported 1 resources and 0 outputs from ../ok.tfstate into ./r/pravo.state

Error: ./r/pravo.state already holds 1 resources and 0 outputs; use -force to replace them
```

Cause: the state directory is not empty. Fix: before the first apply, add `-force`. It replaces the records with a fresh conversion and discards the `$from` moves already carried out. After an apply, do not import again. Use `pravo state mv` or `pravo import` for single records.

### Two instances with one name

OpenTofu 1.13.1 wrote `clash.tfstate` for a root with two resources. The instance `eu` of the first and the second resource would both be called `region_eu`.

```hcl
resource "random_pet" "region" {
  for_each = toset(["eu"])
  prefix   = each.key
  length   = 2
}

resource "random_pet" "region_eu" {
  prefix = "eu"
  length = 2
}
```

```sh
pravo -C r state import-tfstate ../clash.tfstate -force
```

Output:

```text
Error: ../clash.tfstate: random_pet.region["eu"] and random_pet.region_eu would both become random_pet.region_eu; rename one of them in OpenTofu first
```

Cause: an instance key is joined to the name with `_`, and a resource already has that name. Nothing is imported. Fix: rename one of the two in OpenTofu, with a `moved` block, and apply. Then pull the state again.

```hcl
resource "random_pet" "eu_extra" {
  prefix = "eu"
  length = 2
}

moved {
  from = random_pet.region_eu
  to   = random_pet.eu_extra
}
```

After `tofu apply`, the plan is empty and the state converts. The page converts `clash-fixed.tfstate`, the state that OpenTofu wrote after the rename.

```sh
pravo -C r state import-tfstate ../clash-fixed.tfstate -force | head -n 1
```

Output:

```text
Imported 2 resources and 0 outputs from ../clash-fixed.tfstate into ./r/pravo.state
```

### Taint and deposed objects

`tofu plan` reports both: a tainted instance is a replacement, and a deposed object is a destroy. A state that still holds one means that the plan was not empty when the state was copied. [M1](m1-before-you-start.md#task-1-make-the-old-plan-empty) makes the plan empty first. OpenTofu 1.13.1 wrote `taint.tfstate` for a root where `tofu taint` marked `random_pet.server`, and where an apply that failed halfway left a deposed object of `local_file.cfg` and a tainted `null_resource.gate`.

```sh
pravo -C r state import-tfstate ../taint.tfstate -force | grep Warning
```

Output:

```text
Warning: local_file.cfg: deposed object 8098c1fa skipped (pravo keeps one object per resource; destroy it with tofu first)
Warning: null_resource.gate: tainted in the source state; pravo has no taint, the object is recorded as healthy
Warning: random_pet.server: tainted in the source state; pravo has no taint, the object is recorded as healthy
```

Cause: pravo keeps one healthy object per resource. Fix for a tainted object: run `tofu apply`, which replaces it, or plan with `--replace server` after the conversion, which destroys and creates it again. Fix for a deposed object: run `tofu apply`, which destroys it, before you convert. [M10](m10-not-in-pravo.md) lists the state that does not carry over.

### A record of the built-in provider

OpenTofu 1.13.1 wrote `builtin.tfstate` for a root with a `terraform_remote_state` data source and a `terraform_data` resource.

```hcl
data "terraform_remote_state" "net" {
  backend = "local"

  config = {
    path = "../net/terraform.tfstate"
  }
}

resource "terraform_data" "stamp" {
  input = "1"
}
```

```sh
pravo -C r state import-tfstate ../builtin.tfstate -force | grep -E 'Imported|Warning'
```

Output:

```text
Imported 0 resources and 0 outputs from ../builtin.tfstate into ./r/pravo.state
Warning: data.terraform_remote_state.net: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)
Warning: terraform_data.stamp: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (declare a null_resource of hashicorp/null instead)
```

Cause: the provider `terraform.io/builtin/terraform` is part of OpenTofu and is not on a registry, so pravo cannot install it. The conversion leaves its records out. Fix for `terraform_remote_state`: read the other root with `pravo.#RemoteOutputs`, as [M3](m3-worked-example.md) does. Fix for `terraform_data`: declare a `null_resource` of `hashicorp/null`, imported as `"pravo.dev/providers/hashicorp/null:null_"`. The plan creates it, and the create changes no object. [M10](m10-not-in-pravo.md) lists the state that does not carry over.

### A state path that is a file

The state path of a root is a directory. This root names a file.

File `f/main.cue`:

```cue
package f

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {state: path: "terraform.tfstate"}

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo -C f init > /dev/null
cp ok.tfstate f/terraform.tfstate
pravo -C f plan
```

Output:

```text
Error: the state path ./f/terraform.tfstate is a file; a state path is a directory (set $settings.state.path to a directory such as pravo.state, and read an OpenTofu state file with "pravo state import-tfstate")
```

Cause: `state.path` names the file that OpenTofu keeps its state in. pravo does not read the file, does not write it and creates nothing beside it.

```sh
cmp ok.tfstate f/terraform.tfstate && ls f
```

Output:

```text
cue.mod
main.cue
pravo.lock.cue
terraform.tfstate
```

Fix: remove `state.path`, or set it to a directory. Convert the file with `pravo state import-tfstate`.

## Translating the specification

### An import in the wrong file

An import belongs to the file that writes it. A second file that uses the alias needs its own `import`.

File `scope/a.cue`:

```cue
package scope

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider
```

File `scope/b.cue`:

```cue
package scope

#Notes: {
	provider!: loc.#Provider
}
```

```sh
pravo -C scope init > /dev/null
pravo -C scope validate
```

Output:

```text
Error: evaluating ./scope:
#Notes.provider: reference "loc" not found:
    ./scope/b.cue:4:13
```

Cause: `loc` is defined in `a.cue` only. Fix: add `import loc "pravo.dev/providers/hashicorp/local"` to `b.cue`.

### An unused import

File `unused/main.cue`:

```cue
package unused

import "list"

size: 3
```

```sh
pravo -C unused eval
```

Output:

```text
Error: evaluating ./unused:
imported and not used: "list":
    ./unused/main.cue:3:8
```

Cause: CUE refuses an import that nothing uses. Fix: remove the import, or use it. A translation that replaces `slice()` by an expression often leaves one behind.

### A string that keeps its dollar

File `dollar/main.cue`:

```cue
package dollar

env:  *"dev" | "prod" @tag(env)
name: "${env}-logs"
```

```sh
pravo -C dollar eval
```

Output:

```text
env:  "dev"
name: "${env}-logs"
```

There is no error. Cause: `${env}` is valid CUE text, and `name` is the string with those characters. Fix: write `"\(env)-logs"`. Search every translated file for `${` before the first plan.

### A name that refers to itself

A variable and an attribute with the same name look alike in HCL: `filename = var.filename`. In CUE the right side of `filename: filename` is the attribute.

File `selfref/main.cue`:

```cue
package selfref

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

filename: string @tag(filename)

note: $local.#Resource.local_file & {
	filename: filename
	content:  "hello\n"
}
```

```sh
pravo -C selfref init > /dev/null
pravo -C selfref validate
pravo -C selfref plan -t filename=out/note.txt 2>&1 | grep -E '^(Error|Missing)'
```

Output:

```text
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
Error: note: Missing Configuration for Required Attribute (at filename): Must set a configuration value for the filename attribute as the provider has marked it as required.
```

`validate` passes. The plan fails, because the provider receives no `filename`: a field that refers to itself is not set. Fix: give the variable another name, or alias it outside the resource.

File `selfref/main.cue`:

```cue
package selfref

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

target: string @tag(filename)

note: $local.#Resource.local_file & {
	filename: target
	content:  "hello\n"
}
```

```sh
pravo -C selfref plan -t filename=out/note.txt 2>&1 | grep -E '^(  # |Plan:)'
```

Output:

```text
  # note (local_file) will be created
Plan: 1 to add, 0 to change, 0 to destroy.
```

### Merging with an override

`merge(local.base, var.override)` lets the last map win. Two CUE structs that disagree are an error.

File `merge/main.cue`:

```cue
package merge

base:     {encrypted: true, size: 10}
override: {encrypted: false}
bucket:   base & override
```

```sh
pravo -C merge eval
```

Output:

```text
Error: evaluating ./merge:
bucket.encrypted: conflicting values false and true:
    ./merge/main.cue:3:23
    ./merge/main.cue:4:23
    ./merge/main.cue:5:11
    ./merge/main.cue:5:18
```

Cause: `true` and `false` are two values for one field, and CUE does not pick one. Fix: mark the value that may be replaced as a default, and write the override as a concrete value.

File `merge/main.cue`:

```cue
package merge

base:     {encrypted: *true | bool, size: 10}
override: {encrypted: false}
bucket:   base & override
```

```sh
pravo -C merge eval -e bucket
```

Output:

```text
encrypted: false
size:      10
```

### A tag that was not given

File `tags/main.cue`:

```cue
package tags

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

suffix: string @tag(suffix)

note: $local.#Resource.local_file & {
	filename: "out/note.txt"
	content:  "suffix: \(suffix)\n"
}
```

```sh
pravo -C tags init > /dev/null
pravo -C tags validate
pravo -C tags plan
```

Output:

```text
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.

Error: note.content: value depends on suffix, which is not concrete (is a -t tag missing?)
```

Cause: `suffix` has no default and no `-t suffix=...` was given. `validate` accepts a value that is still a type. Fix: pass `-t suffix=value`, or give the field a default: `*"x" | string`.

### A list given on the command line

File `lists/main.cue`:

```cue
package lists

regions: [...string] | *["eu", "us"] @tag(regions)
```

```sh
pravo -C lists eval -t regions=eu
```

Output:

```text
Error: evaluating ./lists:
regions: 2 errors in empty disjunction:
regions: conflicting values "eu" and ["eu","us"] (mismatched types string and list):
    ./lists/main.cue:3:25
regions: conflicting values "eu" and [...string] (mismatched types string and list):
    ./lists/main.cue:3:10
```

Cause: `-t` injects a string, a number or a boolean. A list is a value in a file. Fix: put the list in a `.cue` file that the environment selects, or split a string: `strings.Split(csv, ",")`.

### A provider that was not passed

A module takes its provider as an input. When the input is a plain field and the caller leaves it out, pravo reads the empty field as an unconfigured provider of its own.

File `passed/main.cue`:

```cue
package passed

import loc "pravo.dev/providers/hashicorp/local"

$local:   loc.#Provider
$archive: loc.#Provider

#Notes: {
	provider: loc.#Provider
	dir:      string
	file: provider.#Resource.local_file & {
		filename: "\(dir)/a.txt"
		content:  "a\n"
	}
}

notes: #Notes & {dir: "out"}
```

```sh
pravo -C passed init > /dev/null
pravo -C passed validate
pravo -C passed apply --auto-approve > /dev/null 2>&1
pravo -C passed state show notes.file | head -n 1
```

Output:

```text
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
// notes.file (provider registry.opentofu.org/hashicorp/local v<version> as local.notes.provider, schema version 0, revision 1)
```

Nothing fails, and `$local` and `$archive` are both unused. The file was built through a third provider, `local.notes.provider`, with no configuration. For a provider that needs a region or a credential, the plan fails or reads the wrong account.

Fix: declare the input required, with `!`. The same instance then fails when the specification loads, and the message names the instance and the field.

File `passed/main.cue`:

```cue
package passed

import loc "pravo.dev/providers/hashicorp/local"

$local:   loc.#Provider
$archive: loc.#Provider

#Notes: {
	provider!: loc.#Provider
	dir:       string
	file: provider.#Resource.local_file & {
		filename: "\(dir)/a.txt"
		content:  "a\n"
	}
}

notes: #Notes & {dir: "out"}
```

```sh
pravo -C passed validate
```

Output:

```text
Error: provider notes.provider was not supplied: notes requires a hashicorp/local provider in its field provider. Pass one, for example
	notes: ... & {provider: $local}
```

Pass the provider, `notes: #Notes & {provider: $archive, dir: "out"}`. The record of `notes.file` keeps the key `local.notes.provider` until the next apply, and `pravo why notes.file` reports the change of key.

File `passed/main.cue`:

```cue
package passed

import loc "pravo.dev/providers/hashicorp/local"

$local:   loc.#Provider
$archive: loc.#Provider

#Notes: {
	provider!: loc.#Provider
	dir:       string
	file: provider.#Resource.local_file & {
		filename: "\(dir)/a.txt"
		content:  "a\n"
	}
}

notes: #Notes & {provider: $archive, dir: "out"}
```

```sh
pravo -C passed why notes.file
pravo -C passed apply --auto-approve > /dev/null 2>&1
pravo -C passed state show notes.file | head -n 1
```

Output:

```text
notes.file: no-op
  - provider instance changed (local.notes.provider -> local.$archive)
  - refreshed because the record is older than its refresh age; no drift
// notes.file (provider registry.opentofu.org/hashicorp/local v<version> as local.$archive, schema version 0, revision 2)
```

### An address that needs quotes

File `quotes/main.cue`:

```cue
package quotes

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

regions: ["eu-west", "us-east"]

region: {
	for r in regions {
		"\(r)": $random.#Resource.random_pet & {prefix: r, length: 1}
	}
}
```

```sh
pravo -C quotes init > /dev/null
pravo -C quotes apply --auto-approve > /dev/null 2>&1
pravo -C quotes state list
pravo -C quotes state show region.eu-west
pravo -C quotes state show 'region."eu-west"' | head -n 1
```

Output:

```text
region."eu-west"
region."us-east"

Error: region.eu-west is not in state
// region."eu-west" (provider registry.opentofu.org/hashicorp/random v<version> as random, schema version 0, revision 1)
```

Cause: a label that is not an identifier is quoted in the address, as CUE writes the path. Fix: quote the label, and quote the whole address for the shell. The same applies to `-replace` and to `pravo why`.

## Secrets

A variable with `sensitive = true` that OpenTofu stored in state becomes `pravo.#SecretEnv` only when the value never has to reach state.

File `secret/main.cue`:

```cue
package secret

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

db: pravo.#SecretEnv & {name: "DB_PASSWORD"}

note: $local.#Resource.local_file & {
	filename: "out/note.txt"
	content:  db.value
}
```

```sh
pravo -C secret init > /dev/null
pravo -C secret validate
pravo -C secret plan
DB_PASSWORD=x pravo -C secret plan
```

Output:

```text
Success! 1 resources, 0 data sources, 1 ephemeral, 0 function calls, 0 outputs across 1 providers.

Error: db: the environment variable DB_PASSWORD is not set

Error: note.content: references db, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

### A secret in the wrong place

`validate` accepts the file. The plan names the variable first (`the environment variable DB_PASSWORD is not set`) and the attribute second. An ephemeral value may go to provider configuration, to write-only attributes and to other ephemeral resources. An output that reads it is refused in the same way:

File `secretout/main.cue`:

```cue
package secretout

import "pravo.dev/pravo"

db: pravo.#SecretEnv & {name: "DB_PASSWORD"}

leak: pravo.#Output & {value: db.value}
```

```sh
pravo -C secretout init > /dev/null
DB_PASSWORD=x pravo -C secretout plan
```

Output:

```text
Error: output leak references db; outputs are recorded in state, so ephemeral values cannot be used in them
```

Fix: if you accept that the value is recorded in state, as OpenTofu recorded it, declare the variable as `pravo.#Env`. Its value may go anywhere. If the value must stay out of state, send it to a provider setting or to a write-only attribute. A tag (`-t`) is recorded in saved plans, so it is for configuration and not for credentials.

## Providers and versions

### Two versions of one provider

File `versions/main.cue`:

```cue
package versions

import rnd "pravo.dev/providers/hashicorp/random"

$a: rnd.#Provider & {$version: "~> 3.6"}
$b: rnd.#Provider & {$version: "~> 3.5"}

x: $a.#Resource.random_pet & {length: 1}
y: $b.#Resource.random_pet & {length: 1}
```

```sh
pravo -C versions init > /dev/null 2>&1
pravo -C versions validate
```

Output:

```text
Error: $b: $version "~> 3.5" conflicts with "~> 3.6" on another hashicorp/random provider; one version of a provider is used
```

Cause: a root uses one version of a provider. Two structs of one source may differ in configuration and not in `$version`. Fix: give both the same constraint, or constrain one of them.

### A version that does not exist

File `missing/main.cue`:

```cue
package missing

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 99.0"}

pet: $random.#Resource.random_pet & {length: 1}
```

```sh
pravo -C missing init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.

Error: no version of hashicorp/random matches "~> 99.0" for <platform> with plugin protocol 5 or 6
```

Cause: no version in the registry satisfies the constraint for this platform and the plugin protocol. Fix: use a constraint that a published version satisfies. A state from legacy Terraform names its providers on `registry.terraform.io`. pravo reads each as the same provider on the OpenTofu registry. A provider that exists only on another registry has no version there. `$path` points at a local build.

## Policies

### A policy that does not load

File `pol/main.cue`:

```cue
package pol

import "pravo.dev/pravo"

scope: pravo.#Policy & {
	rule: {
		changes: [...{
			addr: string
			if mode == "managed" {
				error("\(addr): no")
			}
		}]
	}
}
```

```sh
pravo -C pol plan
```

Output:

```text
Error: evaluating ./pol:
scope.rule.changes: reference "mode" not found:
    ./pol/main.cue:9:7
```

Cause: CUE resolves a name by scope, and `mode` is not declared in the template. Fix: declare every field the guard reads: `mode: string`. [M6](m6-rewrite-plan-checks.md) shows the four rules, and the refusals for the other mistakes in a rule.

## Recap

- A message names the path, the values and the position. Read the path first, then the file and line.
- `validate` checks structure. `plan` finds a name that refers to itself, a missing tag and a secret in the wrong place.
- A fix is a change to the specification, and never a flag that skips a check.

Next: [M10 Not in pravo](m10-not-in-pravo.md) lists the features that have no counterpart, with what to use instead.
