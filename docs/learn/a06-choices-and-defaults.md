---
title: Choices and defaults
description: Offer a short list of values, mark one as the default, and select it from the command line.
order: 6
group: Track A · CUE from zero
minutes: 9
needs: A5
learn: How does a field offer a choice? Which value does it take when nobody chooses?
done_when: "`pravo eval -t env=prod` prints `env: \"prod\"`, and `pravo eval -t env=qa` is refused."
level: beginner
---

In HCL, a default and the list of allowed values are two separate statements.

```hcl title="variables.tf"
variable "class" {
  type    = string
  default = "standard"
  validation {
    condition     = contains(["standard", "ia", "glacier"], var.class)
    error_message = "class must be standard, ia or glacier."
  }
}
```

Change the list and the default does not follow. A `lookup(var.bucket, "class", "standard")` elsewhere repeats the default a third time.

In CUE, a `|` between values offers a choice. A `*` in front of one choice marks the [default](../reference/glossary.md#default): the value the field takes when nothing else is written.

## Warm-up

:::check From earlier lessons
Two lines give `name` the values `"logs"` and `"media"`. What does `pravo eval` print?
::answer
An error that names both values and both lines. A field written twice must agree ([A3](a03-writing-a-field-twice.md)).

A port is `>=1 & <=65535` and a second line sets it to 70000. Is the value accepted?
::answer
No. A bound is a constraint, and 70000 is outside it ([A5](a05-constraints-narrow-a-value.md)).
:::

## Offer a choice with a default

Work in an empty directory. Write `estate.cue`. The bucket has a class with three choices.

```cue file=estate.cue hl="5"
package estate

bucket: {
	name:  "logs"
	class: *"standard" | "ia" | "glacier"
}
```

```sh run
pravo eval
```

```text out
bucket: {
	name:  "logs"
	class: "standard"
}
```

Nothing sets `class`, so it takes the default, `"standard"`. The other two choices stay allowed. The eight lines of HCL above say what this one line says.

Write the field again with one of the other choices. A field written twice adds a constraint.

```cue file=estate.cue hl="8"
package estate

bucket: {
	name:  "logs"
	class: *"standard" | "ia" | "glacier"
}

bucket: class: "ia"
```

```sh run
pravo eval
```

```text out
bucket: {
	name:  "logs"
	class: "ia"
}
```

The new value matches one choice, so the default is not used.

## Break it

Write a value that is not one of the choices.

```cue file=estate.cue status=rejected hl="8"
package estate

bucket: {
	name:  "logs"
	class: *"standard" | "ia" | "glacier"
}

bucket: class: "cold"
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
bucket.class: 3 errors in empty disjunction:
bucket.class: conflicting values "glacier" and "cold":
    ./estate.cue:5:30
    ./estate.cue:8:16
bucket.class: conflicting values "ia" and "cold":
    ./estate.cue:5:23
    ./estate.cue:8:16
bucket.class: conflicting values "standard" and "cold":
    ./estate.cue:5:10
    ./estate.cue:8:16
```

Read the error from the top.

- The path is `bucket.class`.
- "3 errors in empty disjunction" means pravo tested `"cold"` against each of the three choices. No choice is left.
- Each error pairs one choice with your value.
- Each error gives two locations. Line 5 wrote the choice. Line 8 wrote `"cold"`.

## Take a value from the command line

pravo has no workspaces. Select the environment with `-t env=prod`.

A [tag](../reference/glossary.md#tag) is a field marked `@tag(name)`. The flag `-t name=value` supplies its value. Add an `env` field with three choices and a default.

```cue file=estate.cue hl="3,8"
package estate

env: *"dev" | "staging" | "prod" @tag(env)

bucket: {
	name:  "logs"
	class: *"standard" | "ia" | "glacier"
	stage: env
}
```

`stage: env` copies the value of `env` into the bucket. [A11](a11-references-let-hidden.md) covers this.

```sh run
pravo eval
pravo eval -t env=prod
```

```text out
env: "dev"
bucket: {
	name:  "logs"
	class: "standard"
	stage: "dev"
}
env: "prod"
bucket: {
	name:  "logs"
	class: "standard"
	stage: "prod"
}
```

Without a tag, `env` takes its default. With `-t env=prod`, the value is `"prod"`. Now ask for an environment that does not exist.

```sh run exit=6
pravo eval -t env=qa
```

```text out
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./estate.cue:3:7
env: conflicting values "prod" and "qa":
    ./estate.cue:3:27
env: conflicting values "staging" and "qa":
    ./estate.cue:3:15
```

The flag does not skip the rules. `-t env=qa` is the same as writing `env: "qa"` in a file. It meets the same three choices, and none matches. The refusal happens during evaluation, before any provider starts.

## Two defaults cancel each other

A second file marks a different choice as the default for `class`. Each file marks one choice with `*`, and they disagree.

```cue file=prod.cue status=unintended hl="3"
package estate

bucket: class: *"ia" | "standard" | "glacier"
```

:::predict Which default wins?
Decide before you open the answer.

::reveal
Neither. The two defaults cannot both hold, so no default remains. The field keeps all three choices.

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
env: "dev"
bucket: {
	name:  "logs"
	class: "standard" | "ia" | "glacier"
	stage: "dev"
}

Error: the value is not concrete:
bucket.class: incomplete value "standard" | "ia" | "glacier"
```

:::

`pravo eval` succeeds and prints the three choices without a default. The flag `-c` fails unless every value is [concrete](../reference/glossary.md#concrete). It reports `incomplete value`, because pravo cannot pick one choice. [C2](c2-why-is-my-value-incomplete.md) lists every cause of an incomplete value.

> [!PITFALL]
> Mark the default once per field, in one place. Every other file writes a plain value or a plain list of choices.

Remove the second file. The default works again.

```sh run
rm prod.cue
pravo eval
```

```text out
env: "dev"
bucket: {
	name:  "logs"
	class: "standard"
	stage: "dev"
}
```

## Exercise

:::exercise Add a region
Add a top-level field `region` to `estate.cue`. It offers `eu-west-1`, `eu-central-1` and `us-east-1`. The default is `eu-west-1`. A tag named `region` fills it.

The check runs `pravo eval -e region` with no tag and with `-t region=us-east-1`. It also runs `-t region=ap-south-1`, which must be refused.

```sh file=check.sh
test "$(pravo eval -e region 2>/dev/null)" = '"eu-west-1"' || { echo "with no tag, region should be eu-west-1"; exit 1; }
test "$(pravo eval -t region=us-east-1 -e region 2>/dev/null)" = '"us-east-1"' || { echo "-t region=us-east-1 should select us-east-1"; exit 1; }
if pravo eval -t region=ap-south-1 > /dev/null 2>&1; then echo "ap-south-1 should be refused"; exit 1; fi
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
with no tag, region should be eu-west-1
```

::hint
Follow the `env` field. Put `*` in front of one choice and `@tag(region)` after the last choice.

::hint
Compare with the [default](../reference/glossary.md#default) and [tag](../reference/glossary.md#tag) definitions. The field needs no other line.

::solution
```cue file=estate.cue hl="5"
package estate

env: *"dev" | "staging" | "prod" @tag(env)

region: *"eu-west-1" | "eu-central-1" | "us-east-1" @tag(region)

bucket: {
	name:  "logs"
	class: *"standard" | "ia" | "glacier"
	stage: env
}
```

```sh run check
sh check.sh
```

The refusal reads like the one for `env`.

```sh run exit=6
pravo eval -t region=ap-south-1 -e region
```

```text out
Error: evaluating .:
region: 3 errors in empty disjunction:
region: conflicting values "eu-central-1" and "ap-south-1":
    ./estate.cue:5:24
region: conflicting values "eu-west-1" and "ap-south-1":
    ./estate.cue:5:10
region: conflicting values "us-east-1" and "ap-south-1":
    ./estate.cue:5:41
```

:::

:::deep-dive What `|` and `*` do
A list of values joined by `|` is a [disjunction](../reference/glossary.md#disjunction). The field can be any one of them. Writing a value narrows the list to the choices that value matches.

A default is selected only when nothing narrows the list further. Four fields show the cases.

```cue file=deep/estate.cue
package estate

a: *1 | 2 | 3
b: *1 | 2 | 3
b: 2
c: *1 | *2 | 3
d: 1 | 2 | 3
```

```sh run
pravo -C deep eval
```

```text out
a: 1
b: 2
c: 1 | 2
d: 1 | 2 | 3
```

Field `a` takes its default. Field `b` takes the value written, because that value is one of the choices. Field `c` marks two defaults, so both stay as choices. Field `d` has no default and stays open.
:::

## Recap

- A `|` offers choices. The value must be one of them.
- A `*` marks the default. Mark it once per field.
- A `@tag(name)` field takes its value from `-t name=value`. The choices still apply.

:::check
What does `class: *"standard" | "ia"` hold when nothing else is written?
::answer
The default, `"standard"`.

Why does `-t env=qa` fail when `env` offers `dev`, `staging` and `prod`?
::answer
A tag value is [unified](../reference/glossary.md#unification) with the field like any other value. The value `"qa"` matches none of the choices.
:::

The next lesson decides what happens to a field that nobody sets.
