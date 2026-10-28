<!-- Generated from docs/migrate/m4-translate-the-specification.md by tools/skillgen. Do not edit. -->

# Translate the specification

Rewrite each OpenTofu construct as CUE in a fixed order, and check every step with pravo validate.

This is step 4 of the runbook. You write a pravo root by hand, one construct at a time, next to your OpenTofu root. The HCL files stay where they are.

Nothing on this page changes your infrastructure or your OpenTofu state. `pravo init` writes `cue.mod`, `pravo.lock.cue` and the provider packages into the new directory. `pravo validate` reads the provider schemas that `init` stored and starts no provider.

The example is a small estate of `random` and `local` resources, so every command runs without an account. Work in an empty directory. Each step adds or replaces one file. The file names carry no meaning, because pravo unifies every `.cue` file of the package.

> [!NOTE]
> Imports are scoped to the file that writes them. A file that uses `loc.#Provider` needs its own `import`. The error for a missing one is in [Pitfalls](m9-pitfalls.md).

## The order

| Step | OpenTofu | CUE | Learn more |
|---|---|---|---|
| 1 | `required_providers` | a provider struct | [B1](../learn/b01-providers-and-resources.md) |
| 2 | `variable` | a tagged field with a constraint | [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html), [A6](https://pravo.systems/learn/a06-choices-and-defaults.html) |
| 3 | `locals` | a hidden field | [A11](https://pravo.systems/learn/a11-references-let-hidden.html) |
| 4 | `resource`, `data` | a struct built through a provider | [B1](../learn/b01-providers-and-resources.md) |
| 5 | `count`, `for_each` | a comprehension | [A10](https://pravo.systems/learn/a10-comprehensions.html), [B5](../learn/b05-many-resources-from-one-rule.md) |
| 6 | `dynamic` | a comprehension inside the body | [A10](https://pravo.systems/learn/a10-comprehensions.html) |
| 7 | `module` | an instance of a definition with resources | [A8](https://pravo.systems/learn/a08-definitions-reject-typos.html), [B6](../learn/b06-reuse-providers-credentials.md) |
| 8 | `provider`, `depends_on`, `lifecycle` | provider structs and `$` fields | [B6](../learn/b06-reuse-providers-credentials.md) |
| 9 | `output` | `pravo.#Output` | [B3](../learn/b03-apply-outputs-state.md) |

Do the steps in this order. Each one ends with `pravo validate`, and the counts it prints grow with the specification. Fix a step before you start the next.

## Step 1. Providers

Every resource is built through a provider struct, so the providers come first. One struct holds the source, the version constraint and the provider's own settings.

```hcl
terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
    local  = { source = "hashicorp/local", version = "~> 2.5" }
  }
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
```

The import path is `pravo.dev/providers/<namespace>/<type>`. The name before the colon starts with `$` by convention, so it never collides with an attribute. `pravo init` installs the providers and generates the packages the imports name.

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

The constraint from the HCL is a range. pravo installs the newest version inside it, and OpenTofu applied with the version in `.terraform.lock.hcl`. A newer provider can plan changes that your translation did not cause, and the first plan cannot tell the two apart. While you migrate, write the locked version as the constraint:

```cue
$random: rnd.#Provider & {$version: "= 3.6.1"}
```

Relax the pin after the cut over, in its own commit ([M11](m11-after-the-migration.md)).

A provider whose source is not `hashicorp/` is imported by its path: `integrations/github` becomes `pravo.dev/providers/integrations/github`. A source on another registry host carries the host in the path.

## Step 2. Variables

A variable is a field. The type and the `validation` block become one constraint, and `default` becomes a disjunction with `*` on the default branch.

```hcl
variable "env" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.env)
    error_message = "env must be dev, staging or prod."
  }
}

variable "regions" {
  type    = list(string)
  default = ["eu", "us"]
}
```

File `variables.cue`:

```cue
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us"]
```

| OpenTofu | CUE |
|---|---|
| `var.env` | `env` |
| `type = string`, `number`, `bool` | `string`, `number`, `bool` |
| `type = list(string)`, `map(string)` | `[...string]`, `[string]: string` |
| `type = object({a = string, b = optional(number)})` | `{a: string, b?: number}` |
| `default = x` | `*x \| type` |
| `validation { condition = ... }` | a constraint on the field |
| `-var env=prod` | `-t env=prod` for a field with `@tag(env)` |
| `terraform.tfvars` | `-t` flags, or a `.cue` file that sets the field |
| `TF_VAR_x` that holds a value from the environment | `x: pravo.#Env & {name: "X"}`, used as `x.value` |
| `sensitive = true` | `x: pravo.#SecretEnv & {name: "X"}`, with restrictions |

`-t` injects strings, numbers and booleans. A list or a map is a field that you edit, or that a second file sets. `validation` has no `error_message`. CUE names the value, the violated constraint and the file position, as the run below shows.

A secret variable is an ephemeral value. It may reach provider configuration and write-only attributes, and no attribute that is recorded in state. [Pitfalls](m9-pitfalls.md) has the messages. Use `pravo.#Env` for a value that OpenTofu recorded in state as sensitive.

```sh
pravo validate
pravo validate -t env=prod
```

Output:

```text
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

A value outside the choices is refused before any provider starts.

Status: rejected.

```sh
pravo validate -t env=qa
```

Output:

```text
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./variables.cue:3:11
env: conflicting values "prod" and "qa":
    ./variables.cue:3:31
env: conflicting values "staging" and "qa":
    ./variables.cue:3:19
```

The path is `env`, the two values disagree, and the position names the choice that failed. A name that you give to a variable and to an attribute of a resource needs care. See [Step 4](#step-4-resources-and-data-sources).

## Step 3. Locals

A local is a field. Start its name with `_` and it is hidden: it is not exported and it never reaches a provider.

```hcl
locals {
  prefix = "acme-${var.env}"
}
```

File `locals.cue`:

```cue
package estate

_prefix: "acme-\(env)"
```

`local.prefix` becomes `_prefix`, and `"${...}"` becomes `"\(...)"`. A string that keeps `${env}` is valid CUE. It holds those characters as text. Search the translated files for `${` before you go on.

```sh
pravo validate
```

Output:

```text
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Step 4. Resources and data sources

A resource is a struct built through a provider: `$local.#Resource.local_file & {...}`. The field name replaces the `type.name` pair, so it becomes the address. A data source is built from `#Data`.

```hcl
data "local_file" "banner" {
  filename = "banner.txt"
}

resource "random_pet" "server" {
  prefix = local.prefix
  length = 2
}

resource "local_file" "hello" {
  filename = "out/hello.txt"
  content  = "${data.local_file.banner.content}Hello, ${random_pet.server.id}!\n"
}
```

File `resources.cue`:

```cue
package estate

banner: $local.#Data.local_file & {filename: "banner.txt"}

server: $random.#Resource.random_pet & {
	prefix: _prefix
	length: 2
}

hello: $local.#Resource.local_file & {
	filename: "out/hello.txt"
	content:  "\(banner.content)Hello, \(server.id)!\n"
}
```

| OpenTofu | CUE |
|---|---|
| `resource "T" "N" { ... }` | `n: $p.#Resource.T & { ... }` |
| `data "T" "N" { ... }` | `n: $p.#Data.T & { ... }` |
| `attr = value` | `attr: value` |
| `"${expr}"` | `"\(expr)"` |
| a nested block | a struct; a repeated block is a list of structs |
| an argument set to `null` | leave the field out |
| `random_pet.server.id` | `server.id` |
| `data.local_file.banner.content` | `banner.content` |

Choose the field name with care. It is the address, and renaming it later is a move. A name inside a struct resolves to the nearest field of that name. So `prefix: prefix` in a resource body refers to the attribute itself. Give the variable a different name, or alias it outside the body (`_p: prefix`). [Pitfalls](m9-pitfalls.md) shows the plan that results.

In a root that declares the provider, `pravo schema <type>` prints the arguments a type accepts, with the provider's documentation as comments. `pravo schema <type> -required` prints only those that must be set.

The file `banner.txt` is the input of the data source. Validation does not read it. The plan does.

File `banner.txt`:

```text
estate
```

```sh
pravo validate
```

Output:

```text
Success! 2 resources, 1 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

The first figure counts resources and the second counts data sources. The data source is not in the first.

## Step 5. count and for_each

A comprehension builds the instances. `for_each` becomes a struct comprehension whose keys are the last element of the address. `count` becomes a list comprehension, and the addresses carry the index.

```hcl
resource "random_integer" "port" {
  count = 2
  min   = 8000
  max   = 8999
}

resource "random_pet" "region" {
  for_each = toset(var.regions)
  prefix   = each.key
  length   = 2
}

variable "create_extra" {
  type    = bool
  default = false
}

resource "random_pet" "extra" {
  count  = var.create_extra ? 1 : 0
  length = 1
}
```

File `repeat.cue`:

```cue
package estate

import "list"

ports: [
	for i in list.Range(0, 2, 1) {
		$random.#Resource.random_integer & {min: 8000, max: 8999}
	},
]

region: {
	for r in regions {
		"\(r)": $random.#Resource.random_pet & {prefix: r, length: 2}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: $random.#Resource.random_pet & {length: 1}
}
```

| OpenTofu | CUE | Address |
|---|---|---|
| `count = 2` | a list comprehension | `ports[0]`, `ports[1]` |
| `count.index` | the loop variable of the comprehension | |
| `for_each = toset(list)` | a struct comprehension over the list | `region.eu` |
| `for_each = map`, `each.key`, `each.value` | `for k, v in map { "\(k)": ... }` | `region.eu` |
| `count = cond ? 1 : 0` | an `if` clause | `extra` |
| `random_pet.region["eu"].id` | `region.eu.id` | |
| `random_integer.port[0].result` | `ports[0].result` | |

Keep the shape of the instances. A `for_each` becomes a struct, a `count` becomes a list, and the keys stay the keys. A change of shape is a change of address, and the plan destroys and creates. A conditional resource loses its index: `random_pet.extra[0]` is `extra`.

```sh
pravo validate
pravo validate -t extra=true
```

Output:

```text
Success! 6 resources, 1 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Success! 7 resources, 1 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Step 6. dynamic blocks

A repeated nested block is a list of structs, so `dynamic` becomes a list comprehension in the body. This step uses a second root with the AWS provider, because the `random` and `local` providers have no repeated blocks. `pravo validate` checks it against the cached provider schema and needs no credentials.

```hcl
provider "aws" {
  region = "eu-north-1"
}

variable "ports" {
  type    = list(number)
  default = [80, 443]
}

resource "aws_security_group" "web" {
  name = "web"

  dynamic "ingress" {
    for_each = var.ports
    content {
      from_port   = ingress.value
      to_port     = ingress.value
      protocol    = "tcp"
      cidr_blocks = ["0.0.0.0/0"]
    }
  }
}
```

File `network/main.cue`:

```cue
package network

import awsp "pravo.dev/providers/hashicorp/aws"

$aws: awsp.#Provider & {$version: "~> 5.0", region: "eu-north-1"}

ports: [...int] | *[80, 443]

web: $aws.#Resource.aws_security_group & {
	name: "web"
	ingress: [
		for port in ports {
			from_port:   port
			to_port:     port
			protocol:    "tcp"
			cidr_blocks: ["0.0.0.0/0"]
		},
	]
}
```

`ingress.value` becomes the loop variable `port`, and `content` becomes the struct. A `dynamic` block over a one-element list, set only by a variable, becomes an `if` clause on the field.

```sh
pravo -C network init
pravo -C network validate
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  aws = hashicorp/aws v<version>
Using the module at ..
...
pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

The `network` directory sits inside the CUE module of the first root. `init` reuses that module, prints its path, and writes the AWS package under the module's `cue.mod/gen`.

## Step 7. Module blocks

pravo has no module block. A `module "notes" {...}` block becomes an instance of a definition that contains resources: one field that unifies the definition with the inputs. The inputs are fields of the definition. The definition builds its resources through a provider that the caller passes in, so one definition serves every root.

```hcl
variable "dir" { type = string }
variable "names" { type = list(string) }

resource "local_file" "note" {
  for_each = toset(var.names)
  filename = "${var.dir}/${each.key}.txt"
  content  = "${each.key}\n"
}

output "paths" {
  value = [for n in var.names : local_file.note[n].filename]
}
```

File `notes.cue`:

```cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

#Notes: {
	provider!: loc.#Provider
	dir:       string
	names:     [...string]

	files: {
		for n in names {
			"\(n)": provider.#Resource.local_file & {
				filename: "\(dir)/\(n).txt"
				content:  "\(n)\n"
			}
		}
	}
	paths: [for n in names {files[n].filename}]
}
```

```hcl
module "notes" {
  source = "./modules/notes"
  dir    = "out/notes"
  names  = ["alpha", "beta"]
}
```

File `instances.cue`:

```cue
package estate

notes: #Notes & {
	provider: $local
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

| OpenTofu | CUE |
|---|---|
| `source = "./modules/x"` | a definition in the same package, or an `import` of a package |
| `variable` in the module | a field of the definition |
| `output` in the module | another field of the definition |
| `module.notes.paths` | `notes.paths` |
| `module.notes.local_file.note["alpha"]` | `notes.files.alpha` |
| `providers = { local = local.archive }` | a required input `provider!: loc.#Provider`, filled by the caller |

Instantiating the definition is the whole wiring. The resources inside `notes` have the addresses `notes.files.alpha` and `notes.files.beta`. A definition that is shared between roots is a package, and it takes every provider as an input. [B6](../learn/b06-reuse-providers-credentials.md) shows the package form. A module from a registry becomes a package in a CUE module, fetched with `pravo mod get`: [Use a published CUE module](../how-to/using-a-module.md).

```sh
pravo validate
```

Output:

```text
Success! 8 resources, 1 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Step 8. Providers and meta-arguments

A `provider` block becomes the fields of a provider struct, beside `$version`. Each alias is a further struct, and the resource chooses the alias by the struct it is built through. There is no `provider = aws.west` argument.

The estate has a second `local` provider for the notes. Its HCL is `provider "local" { alias = "archive" }` and `providers = { local = local.archive }` on the module call. In CUE the second struct is a second line, and the caller passes it as the `provider` input of the definition.

File `providers.cue`:

```cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:  rnd.#Provider & {$version: "~> 3.6"}
$local:   loc.#Provider & {$version: "~> 2.5"}
$archive: loc.#Provider
```

File `instances.cue`:

```cue
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

Meta-arguments become `$` fields on the resource. They never collide with provider attributes.

```hcl
resource "local_file" "hello" {
  filename = "out/hello.txt"
  content  = "${data.local_file.banner.content}Hello, ${random_pet.server.id}!\n"

  depends_on = [random_integer.port]

  lifecycle {
    ignore_changes = [file_permission]
  }
}
```

File `hello.cue`:

```cue
package estate

hello: {
	$depends_on: [ports[0], ports[1]]
	$lifecycle: ignore_changes: ["file_permission"]
}
```

`hello.cue` adds fields to the `hello` of `resources.cue`. CUE unifies the two declarations, so a meta-argument can sit in its own file or in the resource.

| OpenTofu | CUE |
|---|---|
| `provider = aws.west` | build the resource through `$west.#Resource...` |
| `depends_on = [a, b]` | `$depends_on: [a, b]` |
| `lifecycle { create_before_destroy = true }` | `$lifecycle: create_before_destroy: true` |
| `lifecycle { prevent_destroy = true }` | `$lifecycle: prevent_destroy: true` |
| `lifecycle { ignore_changes = [tags] }` | `$lifecycle: ignore_changes: ["tags"]`; `all` is `["*"]` |
| `lifecycle { precondition ... }` | a constraint on the field; see [M10](m10-not-in-pravo.md) |

`$depends_on` names resources. An entry names one resource, such as `ports[0]`, or a list or a struct of resources, such as `ports`, and then depends on every resource in it. Every reference in a body is already a dependency, so most `depends_on` arguments can go.

The AWS root shows the same rules with a region alias:

```hcl
provider "aws" {
  alias  = "west"
  region = "us-west-2"
}

resource "aws_s3_bucket" "archive" {
  provider = aws.west
  bucket   = "acme-archive"
}
```

File `network/main.cue`:

```cue
package network

import awsp "pravo.dev/providers/hashicorp/aws"

$aws: awsp.#Provider & {$version: "~> 5.0", region: "eu-north-1"}

$west: awsp.#Provider & {region: "us-west-2"}

ports: [...int] | *[80, 443]

web: $aws.#Resource.aws_security_group & {
	name: "web"
	ingress: [
		for port in ports {
			from_port:   port
			to_port:     port
			protocol:    "tcp"
			cidr_blocks: ["0.0.0.0/0"]
		},
	]
}

archive: $west.#Resource.aws_s3_bucket & {bucket: "acme-archive"}
```

Two structs of one source must not give different `$version` values.

```sh
pravo validate
pravo -C network validate
```

Output:

```text
Success! 8 resources, 1 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

## Step 9. Outputs

An output is a struct built from `pravo.#Output`. Its name is its path. `sensitive` hides the value where outputs are listed.

```hcl
output "server_name" {
  value = random_pet.server.id
}

output "note_paths" {
  value = module.notes.paths
}

output "port_numbers" {
  value     = random_integer.port[*].result
  sensitive = true
}
```

File `outputs.cue`:

```cue
package estate

import "pravo.dev/pravo"

server_name: pravo.#Output & {value: server.id}
note_paths:  pravo.#Output & {value: notes.paths}

port_numbers: pravo.#Output & {
	value:     [for p in ports {p.result}]
	sensitive: true
}
```

`random_integer.port[*].result` becomes a list comprehension over the instances. An output may not read an ephemeral value. A `precondition` on an output has no counterpart.

```sh
pravo validate
```

Output:

```text
Success! 8 resources, 1 data sources, 0 ephemeral, 0 function calls, 3 outputs across 2 providers.
```

## Check the whole root

The root validates, and so does the second root. Two more runs cover the paths a tag changes.

```sh
pravo validate -t env=prod -t extra=true
pravo -C network validate
```

Output:

```text
Success! 9 resources, 1 data sources, 0 ephemeral, 0 function calls, 3 outputs across 2 providers.
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

> [!WARNING]
> `pravo validate` checks the structure against the provider schema. It does not call the provider, so rules such as "exactly one of `content` and `source`" are checked by `pravo plan`. The plan in the next step is the full check.

## Recap

- Translate in order: providers, variables, locals, resources, loops, `dynamic`, module blocks, provider settings and meta-arguments, outputs.
- Keep the shape and the keys of every `count` and `for_each`, because the key is part of the address.
- `pravo validate` prints `Success!` with counts that grow with each step, and it reads only the provider schemas.

Next: [M5 Convert the state](m5-convert-the-state.md) turns the OpenTofu state into pravo records and points each record at its new address.
