---
title: Required, optional, regular
description: Decide for each field whether it must be supplied, may be supplied, or is a plain field.
order: 7
group: Track A · CUE from zero
minutes: 8
needs: A6
learn: What happens when a field is never set? How do the marks `!` and `?` change the answer?
done_when: "`pravo eval -c` names the missing required field and passes once you set it."
level: beginner
---

HCL answers "what if nobody sets it?" with three different mechanisms.

```hcl title="variables.tf"
variable "bucket" {
  type = object({
    name        = string
    description = optional(string)
    versioning  = optional(bool, false)
  })
}
```

An attribute without `optional()` must be supplied. An omitted `description` becomes `null`. An omitted `versioning` becomes `false`.

In CUE, a field is one of three kinds. A [regular field](../reference/glossary.md#regular-field) has no mark. A field written `name!` is a [required field](../reference/glossary.md#required-field): someone must supply a value. A field written `name?` is an [optional field](../reference/glossary.md#optional-field): it may stay absent.

## Warm-up

:::check From earlier lessons
A bucket name must match `=~"^[a-z0-9-]{3,63}$"`, and a second line sets it to `"Logs"`. Is the value accepted?
::answer
No. The capital letter does not match the regular expression ([A5](a05-constraints-narrow-a-value.md)).

A field is `name: string` and nothing else is written. Does `pravo eval` fail?
::answer
No. A type is a value that is not finished yet ([A2](a02-a-value-can-be-a-type.md)). The flag `-c` asks for finished values and fails.
:::

## One bucket, three kinds

Work in an empty directory. Write `estate.cue`. Each kind appears once, and no field has a value.

```cue file=estate.cue hl="4-6"
package estate

bucket: {
	versioning:   bool
	name!:        string
	description?: string
}
```

:::predict Which of the three fields does `pravo eval` print?
Decide for each field, then open the answer.

::reveal
Two. The plain field and the required field are printed. The optional field is left out.

```sh run
pravo eval
```

```text out
bucket: {
	versioning: bool
	name!:      string
}
```

:::

The optional field exists in the specification. `pravo eval` hides it while it has no value. The flag `-O` prints it.

```sh run
pravo eval -O
```

```text out
bucket: {
	versioning:   bool
	name!:        string
	description?: string
}
```

The marks are part of the output. `name!` and `description?` keep their marks until a value arrives.

## Break it

Ask for finished values with `-c`.

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
bucket.name: field is required but not present:
    ./estate.cue:5:2
bucket.versioning: incomplete value bool:
    ./estate.cue:4:16
```

Two fields are reported. The optional field is not.

- `bucket.name: field is required but not present` is the required field. The location is the line that declares `name!`. It marks the demand, not the place where the value belongs.
- `bucket.versioning: incomplete value bool` is the plain field. It is still a type ([A2](a02-a-value-can-be-a-type.md)).
- `description?` is absent. An absent optional field is not an error.

Here is the table that answers the question for all three kinds.

| Never set | Plain | Required | Optional |
|---|---|---|---|
| Written as | `x: string` | `x!: string` | `x?: string` |
| `pravo eval` prints | `x: string` | `x!: string` | nothing |
| `pravo eval -O` prints | `x: string` | `x!: string` | `x?: string` |
| `pravo eval -c` | fails: incomplete value | fails: required but not present | no error |

Once a value is set, all three kinds print `x: "a"`. [Why is my value incomplete?](why-is-my-value-incomplete.md) lists every cause of an incomplete value.

## Supply the values

Write a value for each field that needs one.

```cue file=estate.cue status=valid hl="9-12"
package estate

bucket: {
	versioning:   bool
	name!:        string
	description?: string
}

bucket: {
	name:       "logs"
	versioning: true
}
```

```sh run
pravo eval -c
```

```text out
bucket: {
	versioning: true
	name:       "logs"
}
```

```sh run
pravo eval -O
```

```text out
bucket: {
	versioning:   true
	name:         "logs"
	description?: string
}
```

The flag `-c` passes. The optional field stays absent, because nobody set it. Writing `description: "Access logs"` makes it appear in `pravo eval`.

## Which kind to write

| You want | Write | In HCL |
|---|---|---|
| The caller must supply it | `name!: string` | an attribute without `optional()` |
| The caller may supply it | `description?: string` | `optional(string)`, which gives `null` |
| A value in every case | `versioning: *false \| bool` | `optional(bool, false)` |

The third row is a plain field with a default ([A6](a06-choices-and-defaults.md)). It always has a value, so `-c` never fails on it.

## Exercise

:::exercise Require an owner
Replace `estate.cue` with this file. Both `owner` and `description` are plain fields, so `-c` reports both.

```cue file=estate.cue
package estate

bucket: {
	name:        "logs"
	owner:       string
	description: string
}
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
bucket.description: incomplete value string:
    ./estate.cue:6:15
bucket.owner: incomplete value string:
    ./estate.cue:5:15
```

Mark `owner` as required and `description` as optional. Then `pravo eval -c` fails only for `owner`, and it passes once `owner` has a value.

The check runs the file as it is, and again with an owner supplied from a second file.

```sh file=check.sh
tmp=$(mktemp -d); cp estate.cue "$tmp/"
printf 'package estate\n\nbucket: owner: "platform"\n' > "$tmp/fixture.cue"
pravo eval -c 2>&1 | grep -q 'owner: field is required but not present' || { echo "owner should be required"; exit 1; }
if pravo eval -c 2>&1 | grep -q 'description'; then echo "description should be optional"; exit 1; fi
pravo -C "$tmp" eval -c > /dev/null 2>&1 || { echo "with an owner, -c should pass"; exit 1; }
pravo -C "$tmp" eval -O | grep -q 'description?: string' || { echo "description should stay optional"; exit 1; }
rm -rf "$tmp"
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
owner should be required
```

::hint
Put `!` after the field name for a required field. Put `?` after the field name for an optional one. The colon stays.

::hint
See [required field](../reference/glossary.md#required-field) and [optional field](../reference/glossary.md#optional-field). Neither mark changes the type after the colon.

::solution
```cue file=estate.cue
package estate

bucket: {
	name:         "logs"
	owner!:       string
	description?: string
}
```

```sh run check
sh check.sh
```

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
bucket.owner: field is required but not present:
    ./estate.cue:5:2
```

:::

## Recap

- A plain field may stay a type, and `-c` reports it as incomplete.
- A required field `name!` must be supplied, and `-c` reports it if it is not.
- An optional field `name?` may stay absent, and `-O` prints it.

:::check
Which fields does `pravo eval -c` report when nothing sets them?
::answer
Plain fields that are still types, and required fields. Optional fields are never reported.

Where does an optional field appear in `pravo eval` output when it has no value?
::answer
Nowhere. Only `pravo eval -O` prints it, as `description?: string`.
:::

The next lesson makes a shape that refuses fields it does not declare.
