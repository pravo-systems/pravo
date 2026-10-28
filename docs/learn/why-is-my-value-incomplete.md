---
title: Why is my value incomplete?
description: The three causes of an incomplete value, each with the error pravo prints, how to read it and the fix.
order: 21
group: Track A · Companions
minutes: 12
needs: A7
learn: Which of three causes left a value incomplete? Which line of the specification fixes it?
done_when: "You can name the cause behind each `incomplete value` line and `pravo eval -c` passes."
level: intermediate
normalize:
  - pattern: '\[txn [0-9A-Za-z-]+\]'
    replace: '[txn <id>]'
---

A value is [concrete](../reference/glossary.md#concrete) when it is one plain value, such as `"logs"`, `3` or `true`. A value is [incomplete](../reference/glossary.md#incomplete) when it still allows more than one value and nothing has chosen. An incomplete value is not wrong. It is unfinished, and more information can finish it. A [conflict](../reference/glossary.md#conflict) is different: no information can repair it.

Work in an empty directory. Every case below replaces `estate.cue`.

## Where it shows

Write a field with two choices and no default.

```cue file=estate.cue hl="3"
package estate

class: "standard" | "ia"
```

```sh run exit=1
pravo eval
pravo validate
pravo eval -c
```

```text out
class: "standard" | "ia"
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.

Error: the value is not concrete:
class: incomplete value "standard" | "ia"
```

The commands treat an incomplete value in three ways.

| Command | What it does with an incomplete value |
|---|---|
| `pravo eval` | Prints the value as it stands and succeeds. |
| `pravo eval -e PATH` | Prints the value at that path as it stands and succeeds. |
| `pravo validate` | Does not fail for it. |
| `pravo eval -c` | Fails and lists every incomplete value. |

Anything that needs a plain value fails in the same way. Examples are a string built from the field and a condition on it. Cases 1.3 and 1.4 show both.

## Find the cause

1. Run `pravo eval -c`. Every reported line names a path.
2. Print the value at that path with `pravo eval -e PATH`.
3. Read what is printed.

- A type or a bound, such as `int`, `string` or `>1024`: cause 1.
- A required marker, `name!: string`: cause 1.
- Several choices joined by `|`, and no `*` anywhere in the source: cause 2.
- Several choices, and a `*` in two places or twice in one list: cause 3.
- A bound where you wrote a default, such as `int & <=2`: cause 3.

## Cause 1: nothing gave the field a value

The field is a type, a bound or a required field, and no line writes a concrete value. This is the most common cause.

### 1.1 A type or a bound

A type such as `int` and a [bound](../reference/glossary.md#bound) such as `>1024` allow many values. Neither has chosen one.

```cue file=estate.cue hl="5-6"
package estate

bucket: {
	name: "logs"
	size: int
	port: >1024
}
```

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
bucket: {
	name: "logs"
	size: int
	port: >1024
}

Error: the value is not concrete:
bucket.port: incomplete value >1024:
    ./estate.cue:6:8
bucket.size: incomplete value int:
    ./estate.cue:5:8
```

`pravo eval` prints both fields as written. `pravo eval -c` lists them. Each location is the line that wrote the type or the bound. Write a value that satisfies it, in any file of the package.

```cue file=estate.cue status=valid hl="9-12"
package estate

bucket: {
	name: "logs"
	size: int
	port: >1024
}

bucket: {
	size: 20
	port: 8080
}
```

```sh run
pravo eval -c
```

```text out
bucket: {
	name: "logs"
	size: 20
	port: 8080
}
```

### 1.2 A required field

```cue file=estate.cue hl="4"
package estate

bucket: {
	name!: string
	size:  20
}
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
bucket.name: field is required but not present:
    ./estate.cue:4:2
```

The message is `field is required but not present`. The location is the line that declares `name!`. It points at the demand, not at the place where the value belongs. Write the value in any file of the package.

```cue file=estate.cue status=valid hl="8"
package estate

bucket: {
	name!: string
	size:  20
}

bucket: name: "logs"
```

```sh run
pravo eval -c
```

```text out
bucket: {
	name: "logs"
	size: 20
}
```

### 1.3 A value used inside a string

```cue file=estate.cue hl="4-5"
package estate

bucket: {
	name:  string
	label: "bucket-\(name)"
}
```

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
bucket: {
	name:  string
	label: "bucket-\(name)"
}

Error: the value is not concrete:
bucket.name: incomplete value string:
    ./estate.cue:4:9
bucket.label: invalid interpolation: non-concrete value string (type string):
    ./estate.cue:5:9
    ./estate.cue:4:9
```

`pravo eval` prints the string as written. `pravo eval -c` reports two lines. The first is `name`, which is incomplete. The second is `label`, whose string needs `name`. Its two locations are the string (line 5) and the field it needs (line 4). Fixing `name` fixes both.

```cue file=estate.cue status=valid hl="8"
package estate

bucket: {
	name:  string
	label: "bucket-\(name)"
}

bucket: name: "logs"
```

```sh run
pravo eval -c
```

```text out
bucket: {
	name:  "logs"
	label: "bucket-logs"
}
```

### 1.4 A value used in a condition

```cue file=estate.cue hl="4-5"
package estate

bucket: {
	size: int
	if size > 10 {
		large: true
	}
}
```

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
bucket: {
	size: int
	if size > 10 {
		large: true
	}
}

Error: the value is not concrete:
bucket: non-concrete value int in operand to >:
    ./estate.cue:5:5
    ./estate.cue:4:8
```

An `if` needs a plain value to decide. `pravo eval` leaves the condition in place. `pravo eval -c` reports `non-concrete value int in operand to >`. The two locations are the condition (line 5) and the field it reads (line 4).

```cue file=estate.cue status=valid hl="10"
package estate

bucket: {
	size: int
	if size > 10 {
		large: true
	}
}

bucket: size: 20
```

```sh run
pravo eval -c
```

```text out
bucket: {
	size:  20
	large: true
}
```

### 1.5 An optional field that was never set

```cue file=estate.cue hl="5,8"
package estate

bucket: {
	name:         "logs"
	description?: string
}

note: bucket.description
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
note: cannot reference optional field: description:
    ./estate.cue:8:14
```

An optional field without a value is absent, and nothing can read an absent field. Set the field, or remove the line that reads it.

```cue file=estate.cue status=valid hl="8"
package estate

bucket: {
	name:         "logs"
	description?: string
}

bucket: description: "Access logs"
note: bucket.description
```

```sh run
pravo eval -c
```

```text out
bucket: {
	name:        "logs"
	description: "Access logs"
}
note: "Access logs"
```

## Cause 2: choices without a default

The field is a [disjunction](../reference/glossary.md#disjunction): a list of choices. No line selects one, and no choice is marked as the [default](../reference/glossary.md#default).

### 2.1 A list of choices

```cue file=estate.cue hl="3"
package estate

class: "standard" | "ia" | "glacier"
```

```sh run exit=1
pravo eval -e class
pravo eval -c
```

```text out
"standard" | "ia" | "glacier"

Error: the value is not concrete:
class: incomplete value "standard" | "ia" | "glacier"
```

The value is the whole list. There are two fixes. Write one choice as a value, for example `class: "ia"`. Or mark one choice with `*` as the default ([A6](a06-choices-and-defaults.md)).

```cue file=estate.cue status=valid hl="3"
package estate

class: *"standard" | "ia" | "glacier"
```

```sh run
pravo eval -c
```

```text out
class: "standard"
```

### 2.2 A tag that was not given

```cue file=estate.cue hl="3"
package estate

env: "dev" | "staging" | "prod" @tag(env)
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
env: incomplete value "dev" | "staging" | "prod"
```

The field waits for `-t env=...`. Pass the tag, or mark a default.

```sh run
pravo eval -c -t env=prod
```

```text out
env: "prod"
```

## Cause 3: defaults that do not agree

The field has a default somewhere, and the defaults cancel each other or are removed. The printed value looks like cause 1 or cause 2. The source shows the `*`.

### 3.1 Two defaults in two places

```cue file=estate.cue
package estate

bucket: class: *"standard" | "ia" | "glacier"
```

```cue file=prod.cue status=unintended hl="3"
package estate

bucket: class: *"ia" | "standard" | "glacier"
```

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
bucket: {
	class: "standard" | "ia" | "glacier"
}

Error: the value is not concrete:
bucket.class: incomplete value "standard" | "ia" | "glacier"
```

Both files agree on the choices and disagree on the default. `"standard"` and `"ia"` cannot both be the default, so no default remains. Two `*` inside one list act the same way. Keep one `*` per field. The second file lists the choices without it.

```cue file=prod.cue status=valid hl="3"
package estate

bucket: class: "ia" | "standard" | "glacier"
```

```sh run
pravo eval -c
```

```text out
bucket: {
	class: "standard"
}
```

The default `"standard"` stays, because only one file marked it.

### 3.2 A default that another constraint removes

Remove the second file from the previous case.

```sh run
rm prod.cue
```

```cue file=estate.cue hl="4,7"
package estate

bucket: {
	size: *3 | int
}

bucket: size: <=2
```

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
bucket: {
	size: int & <=2
}

Error: the value is not concrete:
bucket.size: incomplete value <=2 & int
```

The printed value, `int & <=2`, looks like cause 1. The default is gone. `3` is not `<=2`, so pravo drops it, and a type and a bound remain. Change the default to a value that satisfies every bound.

```cue file=estate.cue status=valid hl="4"
package estate

bucket: {
	size: *2 | int
}

bucket: size: <=2
```

```sh run
pravo eval -c
```

```text out
bucket: {
	size: 2
}
```

## In a specification with resources

Attributes that a provider fills in stay incomplete until the resource exists. `pravo eval -c` lists them before the first apply.

```cue file=estate.cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "hello"
}
```

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run exit=1
pravo eval -c -e note.id
```

```text out
Error: the value is not concrete:
incomplete value string | null
```

The attribute `id` is `string | null` until the provider returns it. This is cause 2: two choices and no default. Apply the resource, then ask again.

```sh run
pravo apply -auto-approve | grep '^Apply complete'
pravo eval -c -e note.id
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
```

After the apply, pravo fills the recorded value in. Track B explains this in [B3](b03-apply-outputs-state.md).

## Recap

- `pravo eval` prints incomplete values. `pravo eval -c` refuses them.
- Cause 1: a type, a bound or a required field that no line fills. Write a value.
- Cause 2: choices with no default. Write a value, mark one default, or pass a tag.
- Cause 3: defaults that cancel or are removed. Keep one default that satisfies every bound.

[Reading a CUE error](reading-a-cue-error.md) explains how to read the errors above. [Closed or open?](closed-or-open.md) covers the errors about fields that are not allowed.
