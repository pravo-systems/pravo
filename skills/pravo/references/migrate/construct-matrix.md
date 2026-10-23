<!-- Generated from docs/migrate/construct-matrix.md by tools/skillgen. Do not edit. -->

# Construct matrix

Which OpenTofu constructs pravo supports, supports in part, or does not support, with the replacement for each.

Three words state the status of a construct.

| Status | Meaning |
|---|---|
| Supported | The construct has a counterpart that does the same job. The form differs. |
| Partial | Part of the construct has a counterpart. The note says which part. |
| Not supported | There is no counterpart. The note gives the replacement. |

Use the matrix to read your preflight list from [M1](m1-before-you-start.md). The steps that translate the supported constructs are in [M4](m4-translate-the-specification.md). The lookup of absent features is [Not in pravo](m10-not-in-pravo.md).

## Blocks

| Construct | Status | In pravo |
|---|---|---|
| `terraform { required_providers }` | Supported | A provider struct. `$version` holds the constraint. The source is the import path. |
| `terraform { required_version }` | Not supported | Pin `pravo version` in the pipeline image. |
| `terraform { backend }` | Partial | A local directory or an S3-compatible bucket. No other backend. No lock table. |
| `terraform { cloud }` | Not supported | A pipeline that runs `pravo plan` and `pravo apply <planfile>`. |
| `terraform { encryption }` | Not supported | Encrypt the bucket or the volume. |
| `provider`, `alias` | Supported | One struct for each configuration. The resource is built through its struct. |
| `variable` | Partial | A field with a constraint and `@tag`. `-t` takes scalars. No `error_message`. A secret is a restricted ephemeral value. |
| `locals` | Supported | A hidden field. |
| `resource` | Supported | A struct built through a provider. |
| `data` | Supported | A struct built from `#Data`. |
| `ephemeral` | Supported | A struct built from `#Ephemeral`. |
| `output` | Partial | `pravo.#Output` with `value`, `description` and `sensitive`. No `depends_on`, no `precondition`. |
| `module` | Partial | A definition, or a CUE package, that contains resources. No registry or `git::` source. No `depends_on` on a module. |
| `moved` | Supported | `$from` on the resource, or on a group for a whole module. |
| `import` | Supported | `$import: "id"` on the resource. `pravo import` for one object. No generated configuration. |
| `removed` | Not supported | `pravo state rm addr`. |
| `check` | Not supported | A constraint on a field, or a plan policy. |

## Meta-arguments and nested blocks

| Construct | Status | In pravo |
|---|---|---|
| `count` | Supported | A list comprehension. The address has the index. |
| `for_each` | Supported | A struct comprehension. The address has the key. |
| `depends_on` | Partial | `$depends_on` on a resource, a data source or an ephemeral resource. Name each instance. |
| `provider = aws.west` | Supported | Build the resource through the struct `$west`. |
| `lifecycle.create_before_destroy` | Supported | `$lifecycle: create_before_destroy: true`. |
| `lifecycle.prevent_destroy` | Supported | `$lifecycle: prevent_destroy: true`. |
| `lifecycle.ignore_changes` | Supported | `$lifecycle: ignore_changes: ["attr"]`, or `["*"]`. |
| `lifecycle.replace_triggered_by` | Not supported | `-replace addr`, or an attribute that depends on the trigger. |
| `lifecycle.precondition`, `postcondition` | Not supported | A constraint on the field. |
| `provisioner`, `connection` | Not supported | Cloud-init, a shell provider, an SSH provider. |
| `dynamic` | Supported | A list comprehension in the body. |

## Expressions

| Construct | Status | In pravo |
|---|---|---|
| `for` expression | Supported | A comprehension. |
| Splat `[*]` | Supported | A comprehension: `[for p in ports {p.result}]`. |
| `a ? b : c` | Supported | An `if` clause, or `[if a {b}, c][0]`. |
| `"${expr}"` | Supported | `"\(expr)"`. |
| `%{ if }` and `%{ for }` in a string | Not supported | A comprehension and `strings.Join`. |
| Heredoc | Supported | A multi-line string with `"""`. |
| `each.key`, `each.value`, `count.index` | Supported | The loop variables of the comprehension. |
| `var.x`, `local.x`, `module.m.o` | Supported | The field name: `x`, `_x`, `m.o`. |
| Built-in function | Partial | Most have an equivalent. See the [Function map](function-map.md). |
| `provider::name::fn()` | Supported | A declaration built from `#Function`, with `args` and `result`. |
| `terraform.workspace` | Not supported | A tag: `-t env=prod`. |
| `path.module`, `path.root` | Not supported | Paths are relative to the root. `-T` supplies `cwd` as a tag. |
| `optional()` and `object()` types | Supported | Optional fields (`a?: string`) and structs. |
| `validation` | Supported | A constraint on the field. The message is the one CUE prints. |
| `sensitive` | Partial | `sensitive: true` on an output. The provider schema marks attributes. No `sensitive()` function. |

## State and operation

| Construct | Status | In pravo |
|---|---|---|
| State format 4 | Supported | `pravo state import-tfstate`. Taint, deposed objects, check results and records of the built-in provider are not carried over. |
| State locking | Supported | A lock for each resource, on every apply. |
| `terraform_remote_state` | Partial | `pravo.#RemoteOutputs` reads the `outputs.json` of another pravo root. It does not read an OpenTofu state. |
| `terraform_data` | Not supported | `null_resource` of `hashicorp/null`. |
| Workspaces | Not supported | A tag and a state path that depends on it. |
| `-target` | Not supported | Scoped plans, and `apply <planfile> -only addr`. |
| `-refresh-only` | Not supported | `pravo drift`. |
| Taint | Not supported | `-replace addr`. |
| Saved plan | Supported | `pravo plan -out f`, `pravo apply f`. |
| JSON plan | Partial | `pravo plan --json` prints pravo's plan document. It is not the OpenTofu JSON. See [M6](m6-rewrite-plan-checks.md). |
| Providers on the OpenTofu registry | Supported | Plugin protocol 5 and 6. |
| Providers from another registry host | Supported | The host is part of the import path. |
| Providers from a container registry | Supported | `pravo providers copy` publishes a provider version to one. See [providers from a registry](../how-to/providers-from-a-registry.md). |
| Modules from a registry | Not supported | CUE modules: `pravo mod get`, `import`. |
| `tofu test` | Not supported | `pravo validate`, `pravo eval -c`, a policy on a saved plan. |

## Spot check

A few supported rows have no example elsewhere in the runbook. This root declares:

- a resource with every supported `lifecycle` field;
- an ephemeral resource;
- an import;
- a definition, instantiated for each element of a list.

File `spot/main.cue`:

```cue
package spot

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

keep: $random.#Resource.random_pet & {
	length: 1
	$lifecycle: {
		create_before_destroy: true
		prevent_destroy:       true
		ignore_changes: ["prefix"]
		refresh: "1h"
	}
}

secret: $random.#Ephemeral.random_password & {length: 16}

port: $random.#Resource.random_integer & {min: 1, max: 5, $import: "3,1,5"}

#Site: {
	name: string
	file: $local.#Resource.local_file & {
		filename: "out/\(name).txt"
		content:  "\(name)\n"
	}
}

sites: {
	for s in ["a", "b"] {
		"\(s)": #Site & {name: s}
	}
}
```

```sh
pravo -C spot init > /dev/null
pravo -C spot validate
pravo -C spot plan 2>&1 | grep -E '^(  # |Plan:|Graph:)'
```

Output:

```text
Success! 4 resources, 0 data sources, 1 ephemeral, 0 function calls, 0 outputs across 2 providers.
  # keep (random_pet) will be created
  # port was imported from id "3,1,5" and matches the specification
  # sites.a.file (local_file) will be created
  # sites.b.file (local_file) will be created
Plan: 3 to add, 0 to change, 0 to destroy.
Graph: planned 4 of 4, 1 ephemeral opened, 1 imported.
```

The plan creates the pet and the two files, imports `port`, and opens the ephemeral resource without recording it.

## Recap

- A supported construct has the same job and another form. A partial one says which part is missing.
- An unsupported construct has a replacement in the same row.
- The runbook translates the supported constructs in [M4](m4-translate-the-specification.md), and the absent ones are collected in [Not in pravo](m10-not-in-pravo.md).

Back to the [Migrate overview](https://pravo.systems/migrate/index.html).
