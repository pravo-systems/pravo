<!-- Generated from docs/reference/spec-overview.md by tools/skillgen. Do not edit. -->

# The specification

What a root is, which constructs pravo reads from it, and the page that describes each one.

A root is a directory that holds one CUE package. pravo unifies every `.cue` file of the package into one value and reads the constructs below from it. Everything else in the value is yours.

| Construct | Written as | Page |
|---|---|---|
| Settings | `$settings: pravo.#Settings & {...}` | [Settings](spec-settings.md) |
| Provider | `$aws: awsp.#Provider & {...}` | [Providers](spec-providers.md) |
| Managed resource | `$aws.#Resource.<type> & {...}` | [Resources](spec-resources.md) |
| Data source | `$aws.#Data.<type> & {...}` | [Resources](spec-resources.md#data-sources) |
| Ephemeral resource | `$aws.#Ephemeral.<type> & {...}` | [Resources](spec-resources.md#ephemeral-resources) |
| Function call | `$aws.#Function.<name> & {args: [...]}` | [Resources](spec-resources.md#function-calls) |
| Output | `pravo.#Output & {value: ...}` | [Outputs](spec-outputs.md) |
| Environment variable | `pravo.#Env`, `pravo.#SecretEnv` | [Environment and remote outputs](spec-env.md) |
| Outputs of another root | `pravo.#RemoteOutputs` | [Environment and remote outputs](spec-env.md#remote-outputs) |
| Rename, move, adopt | `$from`, `$import` | [Moves and imports](spec-moves-and-imports.md) |
| Dependencies, lifecycle | `$depends_on`, `$lifecycle` | [Meta fields](spec-meta-fields.md) |
| Input | a field with `@tag(name)` | [Tags and variables](spec-tags-and-variables.md) |
| Module | a definition or a package | [Modules](spec-modules.md) |
| Provider and `pravo` packages | `cue.mod/gen/` | [Generated packages](spec-generated-packages.md) |

## A complete root

Work in an empty directory. The root below uses two providers and publishes one output.

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {state: path: "pravo.state"}

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

hello: $local.#Resource.local_file & {
	filename: "hello.txt"
	content:  "Hello, \(pet.id)!\n"
}

file: pravo.#Output & {value: hello.filename}
```

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
```

`pravo validate` counts what it found: two resources, one output and two providers. The resources have the addresses `pet` and `hello`. The output has the name `file`.

```sh
pravo plan | grep '^  #'
```

Output:

```text
  # pet (random_pet) will be created
  # hello (local_file) will be created
```

## What makes a root

| Rule | Effect |
|---|---|
| The directory holds `.cue` files with one package clause. | The files are unified into one value. A conflict between files is an evaluation error. |
| A file without a package clause is an error. | Nothing is evaluated. The error names the file and the package of the other files. `pravo.lock.cue` and the files whose names start with `.` or `_` are not part of the package. |
| Two package names in one directory are an error. | Nothing is evaluated. The error names the file and the package of the other files. |
| `$settings` is the one reserved top-level field. | pravo reads it and never searches it for resources. |

Two files of one package merge:

File `base.cue`:

```cue
package demo

size: 3
```

File `more.cue`:

```cue
package demo

name: "web\(size)"
```

```sh
pravo eval -e name
pravo eval -e size
```

Output:

```text
"web3"
3
```

A file without a package clause stops the load. pravo names the file:

File `notes.cue`:

```cue
size: 99
```

```sh
pravo eval
```

Output:

```text
Error: loading .:
notes.cue has no package clause; the other files are package demo
```

Remove the file. A file with another package name stops the load in the same way:

```sh
rm notes.cue
```

File `more.cue`:

```cue
package other

name: "web"
```

```sh
pravo eval
```

Output:

```text
Error: loading .:
more.cue is package other; the other files are package demo
```

## What pravo reads

pravo walks the evaluated value and collects every struct that carries the identity fields `$kind`, `$type` and `$source` of a generated definition. Those fields come from the definition, so a struct built through a provider has them without you writing them. [Resources](spec-resources.md#how-pravo-finds-resources) lists what the walk enters and what it skips.

A struct that does not carry them is data. It can hold any value, and it is evaluated with the rest of the package.

## Files in a root

| Path | Written by | Contents |
|---|---|---|
| `cue.mod/module.cue` | `pravo init` | The CUE module of the root. |
| `cue.mod/gen/` | `pravo init`, `pravo gen` | One CUE package per provider and the `pravo.dev/pravo` package. See [Generated packages](spec-generated-packages.md). |
| `pravo.lock.cue` | `pravo init` | The pinned provider versions and archive hashes. Commit it. |
| `pravo.state/` | any command that writes the state | The state of the root. See [State format](state-format.md). The location is set in [Settings](spec-settings.md#state). |

## See also

- [Evaluation](../concepts/evaluation.md) explains how the files of a root become one value.
- [Resources and addresses](../concepts/resources-and-addresses.md) explains how pravo finds a resource and names it.
- [`pravo validate`](validate-eval-fmt-gen.md) checks a specification and [`pravo init`](init.md) writes the packages it imports.
