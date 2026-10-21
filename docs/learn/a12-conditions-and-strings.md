---
title: Conditions and strings
description: Include a field only when a condition holds. Build strings from values. See why a condition needs a concrete value.
order: 12
group: Track A · CUE from zero
minutes: 8
needs: A11
learn: How does a field exist only in some environments? How does a string use the value of a field?
done_when: "`pravo eval -t env=prod` prints a `kms_key` that `-t env=dev` leaves out."
level: beginner
---

HCL says "only in prod" with a ternary. A resource that exists only in prod takes a count of one or zero.

```hcl title="main.tf"
resource "aws_kms_key" "logs" {
  count = var.env == "prod" ? 1 : 0
}

locals {
  retention = var.env == "prod" ? 90 : 7
}
```

A nested block that exists only in prod needs a `dynamic` block over a list of one or zero items.

In CUE, an `if` inside a struct includes the fields in its braces when the condition is true. When the condition is false, the fields are not there at all.

## Warm-up

:::check From earlier lessons
One line sets `retention` to 7 and a second line sets it to 90. What does `pravo eval` print?
::answer
An error that names both values. Two different concrete values cannot both hold ([A3](a03-writing-a-field-twice.md)).

A field offers `*7 | int`, and a second line sets it to 90. What does `pravo eval` print?
::answer
The value 90. A default gives way to any value that matches the other choice ([A6](a06-choices-and-defaults.md)).
:::

## Include a field only in prod

Work in an empty directory. Write `estate.cue`. The `env` field is the tag from [A6](a06-choices-and-defaults.md).

```cue file=estate.cue hl="8-11"
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

```sh run
pravo eval
pravo eval -t env=prod
```

```text out
env: "dev"
bucket: {
	name:      "estate-logs-dev"
	retention: 7
}
env: "prod"
bucket: {
	name:      "estate-logs-prod"
	retention: 90
	kms_key:   "alias/estate-logs"
}
```

In `dev` the `if` adds nothing, and the default keeps `retention` at 7. In `prod` the `if` adds `kms_key` and sets `retention` to 90.

The condition `env == "prod"` is a comparison. You can also write `!=`, `<`, `>`, `&&`, `||` and `!`. There is no `else`. To cover the other case, write a second `if` with the opposite condition.

## Break it

A condition adds fields. It never replaces a value. Give `retention` a plain 7 instead of a default.

```cue file=estate.cue status=conflict hl="7"
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:      "estate-logs-\(env)"
	retention: 7
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

```sh run
pravo eval
```

```text out
env: "dev"
bucket: {
	name:      "estate-logs-dev"
	retention: 7
}
```

```sh run exit=6
pravo eval -t env=prod
```

```text out
Error: evaluating .:
bucket.retention: conflicting values 90 and 7:
    ./estate.cue:5:9
    ./estate.cue:7:13
    ./estate.cue:8:2
    ./estate.cue:9:14
```

`dev` works, because the `if` is not included. In `prod` the `if` adds 90 to a field that already holds 7. That is the conflict from [A3](a03-writing-a-field-twice.md). Read the error from the top.

- The path is `bucket.retention`.
- The values are `90` and `7`.
- The four locations are line 5 (the struct), line 7 (`7`), line 8 (the `if`) and line 9 (`90`).

Restore the default, so the value 7 can give way.

```cue file=estate.cue hl="7"
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

## Build strings from values

A string can contain `\(` and `)`. The expression between them is replaced by its value. This is interpolation. You have used it since [A11](a11-references-let-hidden.md).

```cue file=strings.cue
package estate

region: "eu-west-1"
port:   8443

url:  "https://\(env).example.com:\(port + 1)"
arn:  "arn:aws:s3:::\(bucket.name)"
note: """
	bucket: \(bucket.name)
	region: \(region)
	"""
```

```sh run
pravo eval -e url
pravo eval -e arn
pravo eval -e note
```

```text out
"https://dev.example.com:8444"
"arn:aws:s3:::estate-logs-dev"
"""
	bucket: estate-logs-dev
	region: eu-west-1
	"""
```

The expression can be a field, a sum or any other value. Numbers and booleans become text. Three quotes open a string that spans several lines. Interpolation works inside it.

## Choose a value by name

An `if` adds fields. To choose one value out of several, index a struct by the key.

```cue file=strings.cue
package estate

days: {dev: 7, staging: 30, prod: 90}[env]
```

```sh run
pravo eval -e days
pravo eval -t env=staging -e days
```

```text out
7
30
```

An `if` suits a field that may be absent. A lookup suits a value that always exists and changes.

Remove `strings.cue`. The rest of the lesson uses `estate.cue` only.

```sh run
rm strings.cue
```

## A condition needs a concrete value

An `if` can only decide when its condition is [concrete](../reference/glossary.md#concrete). Remove the tag and leave `env` as a type.

```cue file=estate.cue status=unintended hl="3"
package estate

env: string

bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

```sh run
pravo eval
```

```text out
env: string
bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

`pravo eval` does not fail. It prints the `if` as it is written, because `env` is still a type. This is the difference between a type and a value from [A2](a02-a-value-can-be-a-type.md). The flag `-c` makes the gap an error.

```sh run exit=1
pravo eval -c
```

```text out
Error: the value is not concrete:
env: incomplete value string:
    ./estate.cue:3:6
bucket: non-concrete value string in operand to ==:
    ./estate.cue:8:5
    ./estate.cue:3:6
```

The first error says `env` is incomplete: it is still `string`. The second says the `if` cannot compare a `string` with `"prod"`. Fix both by giving `env` a value: a default, a tag or a line in a file.

## Exercise

:::exercise Two conditions
Restore the tag. Then change `estate.cue` so that these three runs print what is listed.

| Command | `bucket.retention` | `bucket.kms_key` |
|---|---|---|
| `pravo eval` | 7 | absent |
| `pravo eval -t env=staging` | 30 | absent |
| `pravo eval -t env=prod` | 90 | `"alias/estate-logs"` |

```cue file=estate.cue
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
}
```

The check runs each command of the table. A field that is absent is an error: `pravo eval -e bucket.kms_key` fails in `dev`.

```sh file=check.sh
want() { [ "$(pravo eval $1 2>/dev/null)" = "$2" ] || { echo "pravo eval $1 should print $2"; exit 1; }; }
absent() { if pravo eval $1 > /dev/null 2>&1; then echo "pravo eval $1 should fail"; exit 1; fi; }
want "-e bucket.retention" 7
want "-t env=staging -e bucket.retention" 30
want "-t env=prod -e bucket.retention" 90
want "-t env=prod -e bucket.kms_key" '"alias/estate-logs"'
absent "-e bucket.kms_key"
absent "-t env=staging -e bucket.kms_key"
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
pravo eval -t env=staging -e bucket.retention should print 30
```

::hint
Write one `if` for `staging` and one for `prod`. Put `kms_key` only in the second.

::hint
Compare the first example on this page. The default `*7 | int` lets both conditions set a different value.

::solution
```cue file=estate.cue
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:      "estate-logs-\(env)"
	retention: *7 | int
	if env == "staging" {
		retention: 30
	}
	if env == "prod" {
		retention: 90
		kms_key:   "alias/estate-logs"
	}
}
```

```sh run check
sh check.sh
```

```sh run
pravo eval -t env=staging
pravo eval -t env=prod -e bucket.kms_key
```

```text out
env: "staging"
bucket: {
	name:      "estate-logs-staging"
	retention: 30
}
"alias/estate-logs"
```

:::

## Recap

- An `if` includes its fields when the condition is true. There is no `else`.
- A condition adds to a field and never replaces it. Use a default so a later value can win.
- A condition needs a concrete value. `pravo eval -c` finds the ones that are still types.

:::check
What does `retention: 7` plus `if env == "prod" { retention: 90 }` do in `prod`?
::answer
It fails with `conflicting values 90 and 7`. Make the first value a default: `*7 | int`.

Why does `pravo eval` succeed when `env` is `string`, and `pravo eval -c` fail?
::answer
Without `-c`, an undecided `if` is printed as written. The flag `-c` requires every value to be concrete.
:::

The next lesson imports the standard library and shares a definition between directories.
