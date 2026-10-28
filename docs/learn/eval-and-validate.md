---
title: eval and validate
description: What pravo eval and pravo validate check, what they print, and what the flags -c, -e and -O change.
order: 23
group: Track A · Companions
minutes: 8
needs: A7
learn: Which command answers which question? What do -c, -e and -O change?
done_when: "You can say whether a given file passes `pravo eval`, `pravo eval -c` and `pravo validate`."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
  - pattern: '"[a-z]+-[a-z]+-[a-z]+"'
    replace: '"quiet-river-fox"'
  - pattern: 'random = hashicorp/random v[0-9][0-9.]*'
    replace: 'random = hashicorp/random v3.9.1'
  - pattern: '\([0-9]+ resource types, [0-9]+ data sources, [0-9]+ ephemeral, [0-9]+ functions\)'
    replace: '(10 resource types, 0 data sources, 2 ephemeral, 0 functions)'
keywords: "cue vet cue eval cue validate"
---

Two commands read a specification without running it. `pravo eval` evaluates it and prints the result. `pravo validate` evaluates it and checks every resource against its provider's schema.

pravo has no `vet` command. To ask whether every value is final, use `pravo eval -c`.

Work in an empty directory. Neither command needs a provider for a file that declares none.

## Quick reference

| Command | Use it to | Exit status on failure |
|---|---|---|
| `pravo eval` | Print the evaluated specification. | 6 |
| `pravo eval -e EXPR` | Print one path or one expression. | 6, or 1 when the path does not exist |
| `pravo eval -c` | Fail unless every value is concrete. | 1 for a value that is not concrete, 6 otherwise |
| `pravo eval -O` | Also print optional fields. | 6 |
| `pravo eval -t key=value` | Give an `@tag` field its value. | 6 |
| `pravo validate` | Check the specification against the provider schemas. | 6 |
| `pravo validate --json` | The same, as one JSON document. | 6 |

Status 6 means the specification does not load, evaluate or validate. Status 1 means a value is missing or not final.

The flags `-t` and `-e` combine with `-c` and `-O`. `-t` also works with `validate`.

## What eval prints

`pravo eval` prints the value of the whole package: [references](../reference/glossary.md#reference) resolved, comprehensions expanded, constraints unified and defaults selected. It prints CUE, in the order the fields were written.

```cue file=estate.cue
package estate

#Bucket: {
	name!:      string
	versioning: *false | bool
	size?:      int
}

let prefix = "estate"
_zones: ["a", "b"]

class: *"standard" | "ia"
names: [for z in _zones {"\(prefix)-\(z)"}]
logs:  #Bucket & {name: names[0]}
```

```sh run
pravo eval
```

```text out
class: "standard"
names: ["estate-a", "estate-b"]
logs: {
	name:       "estate-a"
	versioning: false
}
```

The output holds the result of each step.

- `class` shows its default, `"standard"`.
- `names` shows the comprehension expanded, with `prefix` and `_zones` already used.
- `logs` shows the fields of the definition, with the default `false` selected.

What it leaves out: the definition `#Bucket`, the `let` and the hidden field `_zones`. A definition, a `let` and a hidden field are not data. Name a definition with `-e` to see it.

```sh run
pravo eval -e '#Bucket'
```

```text out
name!:      string
versioning: false
```

`pravo eval` reads the state of the directory so that it can show recorded values. A directory without one is read as an empty state, and `eval` creates nothing.

```sh run
test -e pravo.state || echo "no state directory"
```

```text out
no state directory
```

## Concrete values with -c

A value is [concrete](../reference/glossary.md#concrete) when it is one final value: `"logs"`, `3`, `true`. A type such as `string`, a range such as `>=1` and a choice such as `"standard" | "ia"` are not concrete.

`pravo eval` prints a value that is not concrete as it is written. The flag `-c` makes the same value an error. It does not change what is printed. It changes which input fails.

```cue file=estate.cue status=unintended
package estate

#Bucket: {
	name:       string
	class:      "standard" | "ia"
	versioning: *false | bool
	size?:      int
	owner!:     string
}

bucket: #Bucket
```

```sh run
pravo eval
```

```text out
bucket: {
	name:       string
	class:      "standard" | "ia"
	versioning: false
	owner!:     string
}
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
bucket.class: incomplete value "standard" | "ia":
    ./estate.cue:11:9
bucket.name: incomplete value string:
    ./estate.cue:4:14
    ./estate.cue:11:9
bucket.owner: field is required but not present:
    ./estate.cue:8:2
    ./estate.cue:11:9
```

Each error names a path and a reason.

- `incomplete value string` means the field is still a type. Write a value.
- `incomplete value "standard" | "ia"` means no choice is selected and no default marks one. Write a value or mark a default.
- `field is required but not present` means a field declared with `!` has no value.

The optional field `size?` and the defaulted field `versioning` do not appear. An optional field may stay unset. A default is a value.

Write the missing values and run `-c` again.

```cue file=estate.cue status=valid
package estate

#Bucket: {
	name:       string
	class:      "standard" | "ia"
	versioning: *false | bool
	size?:      int
	owner!:     string
}

bucket: #Bucket & {name: "logs", class: "ia", owner: "platform"}
```

```sh run
pravo eval -c
```

```text out
bucket: {
	name:       "logs"
	class:      "ia"
	versioning: false
	owner:      "platform"
}
```

The exit status is 0, and the output is the value. [Why is my value incomplete?](why-is-my-value-incomplete.md) lists every cause of an incomplete value.

## One expression with -e

`-e` evaluates one path or one CUE expression in the scope of the specification and prints only that.

```sh run
pravo eval -e bucket.class
pravo eval -e 'len(bucket.name)'
pravo eval -e 'bucket.owner + "/" + bucket.name'
pravo eval -e 'strings.ToUpper(bucket.name)'
```

```text out
"ia"
4
"platform/logs"
"LOGS"
```

- A path prints the value at that path.
- An expression may call functions of the standard library without an import.
- `-e` sees regular fields and definitions. A hidden field or a `let` is not visible to it. Copy the value into a regular field to look at it.
- With `-c`, only the selected value must be concrete.

A path that does not exist is an error.

```sh run exit=1
pravo eval -e bucket.colour
```

```text out
Error: -e "bucket.colour": undefined field: colour:
    1:8
```

## Optional fields with -O

Without `-O`, `pravo eval` leaves out a field declared with `?`. With `-O` it prints the field and its type. A field declared with `!` is always printed.

```sh run
pravo eval -O -e bucket
```

```text out
name:       "logs"
class:      "ia"
versioning: false
size?:      int
owner:      "platform"
```

The flag shows what a definition offers. Combine it with `-e` to read the whole shape.

```sh run
pravo eval -O -e '#Bucket'
```

```text out
name:       string
class:      "standard" | "ia"
versioning: false
size?:      int
owner!:     string
```

## Tags with -t and -T

A tag is a field marked `@tag(name)`. The flag `-t name=value` gives it a value. A name that no field declares is refused.

```cue file=estate.cue
package estate

env: *"dev" | "staging" | "prod" @tag(env)
```

```sh run exit=6
pravo eval -t env=prod
pravo eval -t stage=prod
```

```text out
env: "prod"

Error: loading .:
no tag for "stage"
```

The flag `-T` fills tags declared with `var=`, such as `@tag(os,var=os)`, from the system. The names include `os`, `arch` and `cwd`.

## What validate checks

`pravo validate` loads and evaluates the specification. It checks each resource, data source and call against the schema of its provider. It prints one line when everything holds.

```sh run
pravo validate
```

```text out
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.
```

For a file without providers, `validate` loads and evaluates the file as `eval` does. It prints a count instead of the value. It does not require concrete values.

The counts matter when providers are declared. Declare one and generate its schema with `pravo init`.

```cue file=estate.cue
package estate

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {
	length:    3
	separator: "-"
}
```

```sh run
pravo init
pravo validate
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

`pravo validate` accepts the resource. It checks the field names, the types and the constraints of the generated definition. Misspell a field.

```cue file=estate.cue status=rejected hl="8"
package estate

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {
	lenght:    3
	separator: "-"
}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
pet.lenght: field not allowed:
    ./estate.cue:8:2
```

`pravo validate` exits with 6 when the specification does not evaluate or validate. `pravo eval` exits with 6 for the same file.

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
pet.lenght: field not allowed:
    ./estate.cue:8:2
```

Restore the field name. A resource has attributes that the provider computes, such as `id`. They are unknown before an apply, so `-c` over the whole file fails, and `validate` passes.

```cue file=estate.cue
package estate

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {
	length:    3
	separator: "-"
}
```

```sh run exit=1
pravo validate
pravo eval -c
```

```text out
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.

Error: the value is not concrete:
pet.id: incomplete value string | null
```

Select a value that is known, and `-c` passes.

```sh run
pravo eval -c -e pet.length
```

```text out
3
```

:::deep-dive After an apply
An apply records the computed attributes in the state. `pravo eval` fills them in, so `-c` passes for the whole file. [B3](b03-apply-outputs-state.md) covers the apply.

```sh run
pravo apply --auto-approve > /dev/null
pravo eval -c -e pet.id
```

```text out
"quiet-river-fox"
```

The id is random, so your output shows another name.
:::

## Which command, when

| Question | Command |
|---|---|
| What does my specification evaluate to? | `pravo eval` |
| What is this one value? | `pravo eval -e path` |
| Is every value final? | `pravo eval -c`, with `-e` for the part you care about |
| What fields can I still set? | `pravo eval -O -e '#Shape'` |
| Does each resource match its provider's schema? | `pravo validate` |
| Should a script stop? | The exit status: 6 for an error in the specification, 1 for a value that is missing or not final |

A short gate for a script runs both.

```sh
pravo validate && pravo eval -c -e buckets
```

## Recap

- `pravo eval` prints the evaluated value. It leaves out definitions, `let` and hidden fields.
- `-c` makes a value that is not concrete an error. `-e` selects one expression. `-O` adds optional fields.
- `pravo validate` checks resources against provider schemas and does not require concrete values. It exits with 6.

The full list of flags is in the [command reference](../reference/validate-eval-fmt-gen.md).

:::check
A file contains `size: int`. Does `pravo eval` accept it? Does `pravo eval -c`?
::answer
`pravo eval` accepts it and prints `size: int`. `pravo eval -c` fails with `incomplete value int`.

Which command refuses a misspelt attribute of a resource, and with which exit status?
::answer
`pravo validate`, with exit status 6. It reports `field not allowed`.
:::
