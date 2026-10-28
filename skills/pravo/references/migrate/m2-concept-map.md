<!-- Generated from docs/migrate/m2-concept-map.md by tools/skillgen. Do not edit. -->

# Concept map

Each OpenTofu construct, command and function beside its pravo equivalent, with one example for each group and the rows that have none.

Each group below shows one OpenTofu construct beside the CUE that replaces it, runs the CUE, and lists the rest of the group in a table. The left column of a table is what you write today. A row that says No equivalent names its replacement in the note, or links to the page that does. The annexes hold the complete lists: [constructs](construct-matrix.md), [commands](command-map.md) and [functions](function-map.md).

In the CUE column, `$p` stands for a provider struct such as `$aws`, and `pkg` for its generated package. [M3](m3-worked-example.md) shows the forms in a working stack.

### Providers
`required_providers`, `provider` and `alias` become a provider struct.

[Providers](#providers)

### Resources
`resource`, `data` and `ephemeral` become structs built through a provider.

[Resources](#resources-and-data-sources)

### Inputs
`variable`, `.tfvars` and `TF_VAR_x` become a tagged field, `-t` and `pravo.#Env`.

[Inputs](#inputs)

### Names and results
`locals` become hidden fields and `output` becomes `pravo.#Output`.

[Names](#names-and-results)

### Repetition
`count`, `for_each` and `dynamic` become comprehensions.

[Repetition](#repetition)

### Reuse
A `module` becomes a definition, and a provider becomes an input.

[Reuse](#reuse)

### Order and lifecycle
`depends_on` and `lifecycle` become `$depends_on` and `$lifecycle`.

[Order](#order-and-lifecycle)

### Addresses
`moved` and `import` become `$from` and `$import`.

[Addresses](#addresses-and-adoption)

### State
`backend`, workspaces and `terraform_remote_state` become `$settings`, a tag and `pravo.#RemoteOutputs`.

[State](#state-and-backends)

Work in an empty directory. The examples share one package: each group writes a file of its own, and a later file may use what an earlier one declares. A file that uses a package imports it, because an import belongs to the file that writes it.

## Providers

A provider is a struct built from a generated definition. The version constraint and the settings of the provider sit in the same struct. A second configuration of one provider is a second struct.

```hcl
terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
    local  = { source = "hashicorp/local", version = "~> 2.5" }
  }
}

provider "local" {
  alias = "backup"
}
```

File `providers.cue`:

```cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider & {$version: "~> 2.5"}
$backup: loc.#Provider
```

`pravo init` installs the providers and writes the CUE packages that the imports name. `pravo validate` checks the specification against their schemas and starts no provider.

```sh
pravo init
pravo validate
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v<version>
  random = hashicorp/random v<version>
...
pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

| OpenTofu | pravo | Note |
|---|---|---|
| `required_providers`, `provider`, `alias` | `$p: pkg.#Provider & {$version: "…"}` | One struct per configuration. |
| `provider = aws.west` | `$west.#Resource.T & {…}` | The struct the resource is built through. |
| `providers = {…}` on a module call | A required module input `provider!: pkg.#Provider` | The caller writes `provider: $west`. |

[M4, step 1](m4-translate-the-specification.md#step-1-providers) covers the version constraint and the lock file.

## Resources and data sources

A resource is a struct built through a provider. Its address is its path in the specification, so `random_pet.name` becomes `name`.

```hcl
resource "random_pet" "name" {
  length = 2
}

resource "local_file" "greeting" {
  filename = "greeting.txt"
  content  = "hello ${random_pet.name.id}\n"
}
```

File `resources.cue`:

```cue
package estate

name: $random.#Resource.random_pet & {length: 2}

greeting: $local.#Resource.local_file & {
	filename: "greeting.txt"
	content:  "hello \(name.id)\n"
}
```

The reference `name.id` is the dependency: `greeting` waits for `name`, and there is no `depends_on`.

```sh
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

| OpenTofu | pravo | Note |
|---|---|---|
| `resource "T" "N"`, `data "T" "N"` | `n: $p.#Resource.T & {…}`, `n: $p.#Data.T & {…}` | The address is the path `n`, not `T.N`. |
| `ephemeral "T" "N"` | `n: $p.#Ephemeral.T & {…}` | Opened for the run. Never written to state. |

## Inputs

A variable is a field. The type, the default and the validation are one constraint, and `-t` sets the value.

```hcl
variable "env" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.env)
    error_message = "env must be dev, staging or prod."
  }
}
```

File `inputs.cue`:

```cue
package estate

env: *"dev" | "staging" | "prod" @tag(env)
```

The `*` marks the default. The three strings are the allowed values, and a fourth is an error.

```sh
pravo eval -e env
pravo eval -t env=prod -e env
```

Output:

```text
"dev"
"prod"
```

```sh
pravo eval -t env=qa -e env
```

Output:

```text
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./inputs.cue:3:7
env: conflicting values "prod" and "qa":
    ./inputs.cue:3:27
env: conflicting values "staging" and "qa":
    ./inputs.cue:3:15
```

| OpenTofu | pravo | Note |
|---|---|---|
| `variable` with `type`, `default`, `validation` | `x: string @tag(x)` with a constraint | The constraint is the type. Set it with `-t x=value`. |
| `TF_VAR_x` | `x: pravo.#Env & {name: "X"}` | Read when the run starts. |
| `variable` with `sensitive = true`, fed by `TF_VAR_x` | `x: pravo.#SecretEnv & {name: "X"}` | Ephemeral. Never written to state or to a saved plan. Use `x.value`. |
| `.tfvars`, `-var-file` | No equivalent | Pass each value with `-t key=value`. |

[M4, step 2](m4-translate-the-specification.md#step-2-variables) translates variables with lists, maps and defaults, and [Environment and remote outputs](../reference/spec-env.md) describes `#Env` and `#SecretEnv`.

## Names and results

A local is a hidden field: a name that starts with an underscore is not an output. An output is a struct built from `pravo.#Output`.

```hcl
locals {
  prefix = "app-${var.env}"
}

output "bucket_name" {
  value = "${local.prefix}-logs"
}
```

File `names.cue`:

```cue
package estate

import "pravo.dev/pravo"

_prefix: "app-\(env)"

bucket_name: pravo.#Output & {value: "\(_prefix)-logs"}
```

```sh
pravo eval -e bucket_name.value
pravo eval -t env=prod -e bucket_name.value
```

Output:

```text
"app-dev-logs"
"app-prod-logs"
```

| OpenTofu | pravo | Note |
|---|---|---|
| `locals` | A field. `_x` hides it. | There is no `local.` prefix. |
| `output` | `x: pravo.#Output & {value: …}` | `sensitive: true` hides the value in listings. |

## Repetition

A comprehension builds the instances. The key of a `for_each` is the last part of the address, and the index of a `count` is its index.

```hcl
resource "local_file" "note" {
  for_each = toset(["alpha", "beta"])
  filename = "${each.key}.txt"
  content  = "${each.key}\n"
}

resource "random_integer" "slot" {
  count = 2
  min   = 8000
  max   = 8999
}
```

File `repeat.cue`:

```cue
package estate

import "list"

note: {
	for k in ["alpha", "beta"] {
		(k): $local.#Resource.local_file & {
			filename: "\(k).txt"
			content:  "\(k)\n"
		}
	}
}

slot: [
	for i in list.Range(0, 2, 1) {
		$random.#Resource.random_integer & {min: 8000, max: 8999}
	},
]
```

```sh
pravo validate
pravo plan | grep -E '^  # (note|slot)'
```

Output:

```text
Success! 6 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
  # note.alpha (local_file) will be created
  # note.beta (local_file) will be created
  # slot[0] (random_integer) will be created
  # slot[1] (random_integer) will be created
```

Keep the shape of the instances. A struct stays a struct and a list stays a list, because a change of shape is a change of address.

| OpenTofu | pravo | Note |
|---|---|---|
| `count` | A list comprehension | The address ends in an index: `r[0]`. |
| `for_each` | A struct comprehension | The key is the last part of the address: `r.key`. |
| `dynamic` | A comprehension in the body of the resource | A repeated block is a list of structs. |

[M4, steps 5 and 6](m4-translate-the-specification.md#step-5-count-and-for-each) translate `count`, `for_each` and `dynamic` in full.

## Reuse

A module is a definition that holds resources. An instance of it is one more field, and its resources take the address `instance.resource`. A caller may set any field of an instance, and the definition still checks the result.

```hcl
variable "name" {
  type = string
}

variable "port" {
  type = number

  validation {
    condition     = var.port >= 1024
    error_message = "port must be 1024 or more."
  }
}

resource "local_file" "file" {
  filename = "${var.name}.env"
  content  = "PORT=${var.port}\n"
}
```

```hcl
module "api" {
  source = "./modules/service"
  name   = "api"
  port   = 8080
}

module "web" {
  source = "./modules/service"
  name   = "web"
  port   = 8081
}
```

File `module.cue`:

```cue
package estate

#Service: {
	name: string
	port: int & >=1024
	file: $local.#Resource.local_file & {
		filename: "\(name).env"
		content:  "PORT=\(port)\n"
	}
}

api: #Service & {name: "api", port: 8080}

web: #Service & {
	name: "web"
	port: 8081
	file: file_permission: "0600"
}
```

The second instance sets `file_permission` on the resource inside the definition. The definition declares no input for it, and the type of the attribute still applies.

```sh
pravo validate
pravo plan | grep -E '^  # (api|web)'
```

Output:

```text
Success! 8 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
  # api.file (local_file) will be created
  # web.file (local_file) will be created
```

| OpenTofu | pravo | Note |
|---|---|---|
| `module` | A definition that holds resources | An instance `m: #M & {…}` gives its resources the address `m.res`. |
| `providers = {…}` on a module call | A required module input `provider!: pkg.#Provider` | The caller writes `provider: $west`. |

[B6](../learn/b06-modules-providers-credentials.md) teaches modules with providers, and [M4, step 7](m4-translate-the-specification.md#step-7-modules) translates a module call.

## Order and lifecycle

A reference is a dependency already. `$depends_on` names a dependency that no reference shows, and `$lifecycle` holds the lifecycle settings.

```hcl
resource "local_file" "first" {
  filename = "first.txt"
  content  = "1\n"
}

resource "local_file" "second" {
  filename   = "second.txt"
  content    = "2\n"
  depends_on = [local_file.first]

  lifecycle {
    prevent_destroy = true
  }
}
```

File `order.cue`:

```cue
package estate

first: $local.#Resource.local_file & {
	filename: "first.txt"
	content:  "1\n"
}

second: $local.#Resource.local_file & {
	filename: "second.txt"
	content:  "2\n"
	$depends_on: [first]
	$lifecycle: prevent_destroy: true
}
```

```sh
pravo validate
```

Output:

```text
Success! 10 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
```

| OpenTofu | pravo | Note |
|---|---|---|
| `depends_on` | `$depends_on: [r]` | A reference is a dependency already. |
| `lifecycle` | `$lifecycle: {…}` | Fields: `create_before_destroy`, `prevent_destroy`, `ignore_changes`, `refresh`. |
| `precondition`, `postcondition`, `replace_triggered_by` | No equivalent | A constraint on the field fails evaluation. |
| `provisioner` | No equivalent | See [M10](m10-not-in-pravo.md). |
| `check` | No equivalent | See [M10](m10-not-in-pravo.md). |

## Addresses and adoption

The address of a resource is its identity. `$from` names the old address on the resource that now holds it, and `$import` names the object that an address adopts.

```hcl
moved {
  from = local_file.old
  to   = local_file.renamed
}

resource "local_file" "renamed" {
  filename = "renamed.txt"
  content  = "x\n"
}
```

File `moved.cue`:

```cue
package estate

renamed: $local.#Resource.local_file & {
	$from:    "old"
	filename: "renamed.txt"
	content:  "x\n"
}
```

```sh
pravo validate
```

Output:

```text
Success! 11 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
```

| OpenTofu | pravo | Note |
|---|---|---|
| `moved` | `$from: "old.address"` | On the resource that now holds the address. |
| `import` | `$import: "id"` | Or `pravo import ADDR ID`. |

[M5](m5-convert-the-state.md#point-each-record-at-its-address) uses `$from` to point each converted record at its address.

## State and backends

The state is a directory of records, or a bucket. The settings sit in `$settings`, and they may depend on a tag, which is how an environment gets its own state without a workspace.

```hcl
terraform {
  backend "s3" {
    bucket = "acme-state"
    key    = "prod/terraform.tfstate"
    region = "eu-west-1"
  }
}
```

File `state/main.cue`:

```cue
package state

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: {
		backend: "s3"
		bucket:  "acme-state"
		prefix:  "prod"
		region:  "eu-west-1"
	}
}
```

The block sits in its own directory, because it is a root of its own. `validate` reads the settings and opens no bucket.

```sh
pravo -C state validate
```

Output:

```text
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.
```

| OpenTofu | pravo | Note |
|---|---|---|
| `backend` | `$settings: pravo.#Settings & {state: {…}}` | A local directory, an S3-compatible bucket or an HTTP server of your own: [Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md). |
| Workspaces, `terraform.workspace` | No equivalent | `@tag(env)` selects the environment. The state path can depend on it. |
| `terraform_remote_state` | `pravo.#RemoteOutputs` | Reads the other root's `outputs.json`. Sensitive outputs are left out. |

## Commands

A pravo command takes the place of the tofu command in a pipeline. The loop that every root runs is the same three steps, and `-t` replaces `-var`.

```sh
tofu init
tofu plan -var env=prod -out=change.plan
tofu apply change.plan
```

```sh
pravo init
pravo plan -t env=prod --out change.plan
pravo apply change.plan
```

The commands on the right run in the directory of this page. `apply` of a saved plan asks nothing.

```sh
pravo plan -t env=prod --out change.plan | grep '^Plan:'
pravo apply change.plan | grep '^Apply complete'
pravo state list | head -n 3
```

Output:

```text
Plan: 11 to add, 0 to change, 0 to destroy.
Apply complete! Resources: 11 added, 0 changed, 0 destroyed. [txn <txn>]
api.file
first
greeting
```

### Set up and check

| OpenTofu | pravo | Note |
|---|---|---|
| `tofu init` | `pravo init` | Installs providers, writes `pravo.lock.cue` and `cue.mod/gen`. |
| `tofu init -upgrade` | `pravo init --upgrade` | |
| `tofu validate` | `pravo validate` | Needs no credentials. |
| `tofu fmt` | `pravo fmt` | Formats the CUE files in the current directory. |
| `tofu console` | `pravo eval -e EXPR` | |
| `tofu providers` | `pravo providers` | Also `providers lock` and `providers schema`. |

### Plan and apply

| OpenTofu | pravo | Note |
|---|---|---|
| `tofu plan` | `pravo plan` | |
| `-var k=v` | `-t k=v` | |
| `-out=FILE` | `--out FILE` | |
| `-detailed-exitcode` | `--detailed-exitcode` | Exit code 2 means changes among the resources the plan looked at. `tofu plan` reads every resource; `pravo drift` reads every record. |
| `-refresh=false` | `--refresh=false` | The values are `age` (default), `expired`, `false` and `all`. `tofu plan` reads every resource; `pravo plan` reads the resources it reaches and the ones they read, and `--refresh=expired` reads every record past its refresh age. |
| `-replace=ADDR` | `--replace ADDR` | |
| `-target=ADDR` | No equivalent | A plan reaches only what changed. `pravo apply PLANFILE --only ADDR` applies part of a saved plan. |
| `tofu apply [PLANFILE]` | `pravo apply [PLANFILE]` | `--auto-approve` skips the prompt. |
| `tofu destroy` | `pravo destroy` | |
| `tofu show PLANFILE` | `pravo show PLANFILE` | `--json` prints JSON. |

### State and outputs

| OpenTofu | pravo | Note |
|---|---|---|
| `tofu import ADDR ID` | `pravo import ADDR ID` | |
| `tofu state list`, `show`, `rm`, `mv` | `pravo state list`, `show`, `rm`, `mv` | |
| `tofu state pull`, `push` | `pravo state pull DIR`, `pravo state push DIR` | Copies pravo's state to or from a directory. |
| `tofu output [NAME]` | `pravo output [NAME]` | |
| `tofu refresh` | `pravo drift` | Reads every object, writes the refreshed state and a mark on each record that differs, exits 2 on drift. `pravo drift --due --limit N` reads only the oldest records past their refresh age. |
| `tofu force-unlock ID` | `pravo state unlock ADDR` | Locks are per resource. |
| `tofu graph` | `pravo state graph --dot` | Prints the dependency edges of the state. |

### Without a counterpart

| OpenTofu | pravo | Note |
|---|---|---|
| `tofu taint`, `untaint` | No equivalent | Use `pravo plan --replace ADDR`. |
| `tofu workspace` | No equivalent | Use a tag. |
| `tofu test` | No equivalent | |
| No OpenTofu command | `pravo why ADDR`, `pravo schema TYPE`, `pravo mcp` | Explain a plan entry, print a resource definition, serve the commands to an agent. |
| No OpenTofu command | `pravo plan --store`, `pravo plan list`, `pravo plan approve ID` | A plan in the state, which the team lists, reads, approves and applies by its id. |
| No OpenTofu command | `pravo id`, `pravo protect` | An identity per person, a state sealed to a list of readers, a role per reader. |
| No OpenTofu command | `pravo policy test` | A rule held against saved plans or the stored plans, with nothing applied. |
| No OpenTofu command | `pravo discover` | The objects of a scope that nothing manages, to acknowledge or adopt. |
| No OpenTofu command | `pravo serve` | The viewer: the estate as a graph, its history, the team's plans, drift, the policies and the recorded runs. |

The [command map](command-map.md) lists the flags, the environment variables and the files as well.

## Functions

Import the package that holds the function: `strings`, `list`, `math`, `regexp`, `strconv`, `net` or an `encoding` package. Interpolation replaces `format`, and a comprehension replaces most of what a function builds.

### Strings

| OpenTofu | CUE | Note |
|---|---|---|
| `upper`, `lower`, `title` | `strings.ToUpper`, `strings.ToLower`, `strings.ToTitle` | |
| `trimspace` | `strings.TrimSpace` | |
| `replace(s, a, b)` | `strings.Replace(s, a, b, -1)` | `-1` replaces every match. |
| `split(sep, s)`, `join(sep, l)` | `strings.Split(s, sep)`, `strings.Join(l, sep)` | The arguments change places. |
| `format("%s-%d", a, b)` | `"\(a)-\(b)"` | Interpolation. |
| `substr(s, o, n)` | `strings.SliceRunes(s, o, o+n)` | The last argument is the end, not the length. |
| `startswith`, `endswith` | `strings.HasPrefix`, `strings.HasSuffix` | |
| `regex`, `regexall` | `regexp.Find(re, s)`, `regexp.FindAll(re, s, -1)` | `s =~ re` tests a match. |

### Lists and maps

| OpenTofu | CUE | Note |
|---|---|---|
| `length` | `len(x)` | |
| `contains(l, v)` | `list.Contains(l, v)` | |
| `concat`, `flatten` | `list.Concat`, `list.FlattenN(l, depth)` | |
| `sort`, `reverse`, `slice` | `list.SortStrings`, `list.Reverse`, `list.Slice` | |
| `keys`, `values` | `[for k, _ in m {k}]`, `[for _, v in m {v}]` | |
| `merge(a, b)` | `a & b` | A conflict is an error. It is not an override. |
| `zipmap(ks, vs)` | `{for i, k in ks {(k): vs[i]}}` | |
| `range` | `list.Range(start, end, step)` | |

### Fallbacks and conversion

| OpenTofu | CUE | Note |
|---|---|---|
| `lookup(m, k, d)`, `coalesce(a, b)` | `*m[k] \| d`, `*a \| b` | The `*` marks the default. |
| `try(x, d)`, `can(x)` | `*x \| d`, `(x != _\|_)` | A constraint that fails stops evaluation. A default supplies a fallback. The [function map](function-map.md) shows both with results. |
| `tonumber`, `tostring` | `strconv.Atoi`, `strconv.FormatInt(n, 10)` | |
| `jsonencode`, `jsondecode` | `json.Marshal`, `json.Unmarshal` | |
| `yamlencode`, `yamldecode` | `yaml.Marshal`, `yaml.Unmarshal` | |
| `base64encode`, `base64decode` | `base64.Encode(null, s)`, `string(base64.Decode(null, s))` | |
| `sha256`, `md5` | `hex.Encode(sha256.Sum256(s))`, `hex.Encode(md5.Sum(s))` | |

### Numbers and networks

| OpenTofu | CUE | Note |
|---|---|---|
| `min`, `max`, `abs`, `ceil`, `floor` | `list.Min`, `list.Max`, `math.Abs`, `math.Ceil`, `math.Floor` | `list.Min` and `list.Max` take a list. |
| `cidrhost`, `cidrsubnet` | `net.AddIP`, `net.AddIPCIDR` | `net.IPCIDR` checks that a string is a CIDR. The [function map](function-map.md) holds the full expressions. |
| `timestamp`, `uuid` | No equivalent | |
| `sensitive`, `nonsensitive` | No equivalent | The provider schema and `sensitive: true` on an output decide. |
| `provider::name::fn(args)` | `$p.#Function.fn & {args: […]}` | The answer is in `.result`. |

### Check a mapping

A top-level field cannot have the name of a package that the same file imports. A field called `json` next to `import "encoding/json"` fails evaluation. The check below uses three rows of the tables: a tag in place of a variable, `upper` and `lookup`. It replaces `inputs.cue` with a file that holds them.

File `inputs.cue`:

```cue
package estate

import (
	"list"
	"strings"
)

env: *"dev" | "staging" | "prod" @tag(env)

_sizes: {dev: 1, prod: 3}
_names: ["web", "api", "db"]

shout:  strings.ToUpper(env)
joined: strings.Join(list.SortStrings(_names), ",")
size:   *_sizes[env] | 2
```

```sh
pravo eval -e '[env, shout, joined, size]'
pravo eval -t env=staging -e '[env, shout, joined, size]'
```

Output:

```text
["dev", "DEV", "api,db,web", 1]
["staging", "STAGING", "api,db,web", 2]
```

`staging` has no entry in `_sizes`, so `size` takes its default, 2.

## Recap

- Every construct maps to a field, a definition, a comprehension or a `$` field; the rows with no equivalent name their replacement.
- Commands keep their names, and flags move from `-var` to `-t`; `-target`, workspaces and taint have no counterpart. Stored plans, sealing, roles, discovery and the viewer have no OpenTofu side, and [Track D](../learn/d01-one-state-two-people.md) teaches them.
- Functions come from CUE packages; `merge` is `&`, and a conflict is an error.

Next: [the worked example](m3-worked-example.md) runs the forms on a real stack.
