<!-- Generated from docs/reference/validate-eval-fmt-gen.md by tools/skillgen. Do not edit. -->

# pravo validate, eval, fmt and gen

Check a specification without planning it, print its evaluated value, format the CUE files and regenerate the provider packages.

## Quick reference

| Command | Does | Starts a provider | Writes |
|---|---|---|---|
| `pravo validate` | Evaluates the specification and checks it against the generated definitions. Prints a summary. | no, once `pravo init` has cached the schema | nothing |
| `pravo eval` | Prints the evaluated value, as `cue eval` does. Fills in the recorded state. | no | nothing |
| `pravo fmt` | Formats the `.cue` files of the current directory in place. | no | the files that change |
| `pravo gen` | Rewrites the packages under `cue.mod/gen` from the cached schemas, and generates a package that an import names and that does not exist yet. | no | `cue.mod/gen` |

`validate` and `eval` also work on plain CUE that declares no provider and imports no generated package. Imports of the CUE standard library are fine. The file needs a `package` clause, and no `pravo init` is needed. Taught in [eval and validate](../learn/eval-and-validate.md), [A1, Your data is already CUE](https://pravo.systems/learn/a01-your-data-is-already-cue.html) and [B1, Providers and resources](../learn/b01-providers-and-resources.md).

### Flags

| Flag | Command | Default | Meaning |
|---|---|---|---|
| `-e`, `--expression EXPR` | `eval` | none | Evaluate `EXPR` in the scope of the specification: a path such as `logs.tags`, or any CUE expression. |
| `-c`, `--concrete` | `eval` | off | Fail unless every value is concrete. |
| `-O`, `--optional` | `eval` | off | Print optional fields too. |
| `--raw` | `eval` | off | Print the value at the path of `-e` as it is written, comprehensions included. A policy rule becomes a file for `cue vet`. |
| `-t`, `--tag key=value` | `validate`, `eval`, `gen` | none | Inject a value for an `@tag()` field. Repeatable. |
| `-T`, `--inject-vars` | `validate`, `eval`, `gen` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |

`fmt` takes no flags of its own. The [global flags](global-flags.md) apply. With `--json`, `validate` prints a document and the other three print text.

### Exit codes

| Code | Command | Meaning |
|---|---|---|
| 0 | all | Done. |
| 1 | `eval` | `-c` found a value that is not concrete, or `-e` is not an expression. |
| 1 | `fmt` | A file does not parse. |
| 1 | all | A flag is unknown, or an argument is given: these commands take none. |
| 6 | `validate`, `eval`, `gen` | The specification does not load or evaluate. For `validate` also: a provider cannot be installed. |

Every command that loads the specification reports the same errors with exit code 6.

## Examples

Work in an empty directory named `demo`. The first file mixes a resource with plain CUE.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

size: int
size: 3
name: *"web" | string
port: int @tag(port,type=int)
tags: [for i in [1, 2, 3] {i * 2}]
settings: {
	region: "eu"
	zone?:  string
}
```

```sh
pravo init > /dev/null
```

### pravo validate

`validate` evaluates the specification and checks every resource against the definitions that `pravo init` generated. It catches a misspelt attribute, a wrong type, a missing reference and a violated constraint, with the file and the line. It also reports resources that depend on each other in a circle (see [Errors](errors.md#resources-and-values)). It does not run the validation of the providers themselves, which `plan` and `apply` do.

```sh
pravo validate
```

Output:

```text
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

The counts are resources, data sources, ephemeral resources, function calls, outputs and providers. A misspelt attribute is rejected before any provider starts. The block carries `status=rejected`.

File `typo.cue`:

```cue
package demo

pet: $random.#Resource.random_pet & {lenght: 2}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
pet.lenght: field not allowed:
    ./typo.cue:3:38
```

The path is `pet.lenght`, the rule is `field not allowed`, and the location is the line of the attribute. Remove the file.

```sh
rm typo.cue
pravo validate --json
```

Output:

```text
{
  "calls": 0,
  "data_sources": 0,
  "ephemeral": 0,
  "outputs": 0,
  "providers": 1,
  "resources": 1,
  "valid": true
}
```

A file without a `package` clause next to files that have one stops the load, and the error names the file:

File `loose/base.cue`:

```cue
package loose

size: 1
```

File `loose/loose.cue`:

```cue
size: 3
```

```sh
pravo -C loose validate
```

Output:

```text
Error: loading ./loose:
loose.cue has no package clause; the other files are package loose
```

When no file has a `package` clause, nothing loads:

```sh
rm loose/base.cue
pravo -C loose validate
```

Output:

```text
Error: loading ./loose:
build constraints exclude all CUE files in .:
    ./loose/loose.cue: no package name
```

### pravo eval

`eval` prints the specification after references are resolved, comprehensions expanded, constraints unified and defaults selected. A struct prints as the fields of a file. The recorded state of each resource is filled in.

```sh
pravo eval | tail -n 9
```

Output:

```text
	seed: "base"
}
size: 3
name: "web"
port: int
tags: [2, 4, 6]
settings: {
	region: "eu"
}
```

A field that is a type, not a value, prints as the type: `port` is `int` until a tag supplies it. `-e` selects a part:

```sh
pravo eval -e size
pravo eval -e name
pravo eval -e 'tags[1]'
pravo eval -e 'len(tags)'
pravo eval -t port=8080 -e port
```

Output:

```text
3
"web"
4
3
8080
```

`-O` prints the optional field `zone?`.

```sh
pravo eval -e settings -O
```

Output:

```text
region: "eu"
zone?:  string
```

`-c` fails when a value is not concrete. `port` has no value yet:

```sh
pravo eval -c -e port
```

Output:

```text
Error: the value is not concrete:
incomplete value int:
    ./main.cue:12:7
```

With the tag, the same command succeeds:

```sh
pravo eval -t port=8080 -c -e port
```

Output:

```text
8080
```

A computed attribute of a resource is incomplete until the resource is applied. After an apply, `eval` prints the recorded value.

```sh
pravo eval -e base.result
pravo apply -auto-approve > /dev/null
pravo eval -e base.result
```

Output:

```text
number | null
9
```

### pravo fmt

`fmt` rewrites the `.cue` files of the current directory in the style of `cue fmt`. It prints the name of each file that changed and prints nothing for a file that is formatted. It does not descend into subdirectories.

File `messy.cue`:

```cue
package demo

x:   1


 y: {a:    2}
```

```sh
pravo fmt
pravo fmt
cat messy.cue
```

Output:

```text
messy.cue
package demo

x: 1

y: {a: 2}
```

### pravo gen

`gen` rewrites every package under `cue.mod/gen` from the cached schemas of the providers in the lock file, and installs a provider that is missing. A package that an import names and that does not exist yet is generated first, as `pravo init` does.

Append a line to a generated file, and `gen` writes the file as it should be:

```sh
echo "// edited" >> cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
pravo gen
pravo gen
```

Output:

```text
Generated cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
```

Each line names a package. `Generated` means the file changed, and `Up to date` means it is as `gen` writes it. Every other command that loads the specification warns when a generated package was made from another provider version than the one selected. `init` and `gen` write the package again and do not warn.

Remove the generated packages of the providers. `gen` generates the package that the import names:

```sh
rm -r cue.mod/gen/pravo.dev/providers
pravo gen
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Generated cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
```

## See also

- [`pravo init`](init.md) creates the CUE module and the generated packages.
- [`pravo schema`](schema.md) prints one type.
- [Reading a CUE error](../learn/reading-a-cue-error.md) explains the shape of an evaluation error.
