---
title: A value can be a type
description: In CUE, int and 30 are both values. One allows every integer and the other allows one.
order: 2
group: Track A · CUE from zero
minutes: 8
needs: A1
learn: What is a type in CUE? What does it mean for a value to be incomplete?
done_when: "`pravo eval -c` prints the whole estate with no type left in it."
level: beginner
---

In HCL, a type and a value are separate declarations. They sit in separate files and meet by name.

:::compare
```hcl title="variables.tf"
variable "retention_days" {
  type = number
}
```

```hcl title="terraform.tfvars"
retention_days = 30
```
:::

OpenTofu checks the second file against the first when it plans. The type has no value, and the value has no type.

In CUE, `int` is a value. So is `30`. The first stands for every integer. The second stands for one. A field can hold either, and a bound such as `>=1` sits between them.

A value that stands for one thing is **[concrete](../reference/glossary.md#concrete)**. A value that stands for many things is a **[constraint](../reference/glossary.md#constraint)**. `30`, `"web"` and `true` are concrete. `int`, `string`, `bool` and `>=1` are constraints.

| You write | It allows | Kind |
|---|---|---|
| `int` | every integer | constraint |
| `>=1` | every number from 1 upward | constraint |
| `30` | exactly 30 | concrete |

The more specific the value, the fewer values it allows.

## Leave some values open

Work in the directory from A1. The platform team knows everything about `logs`. The web team has answered only part of the questions about `uploads`. Record what is known and leave the rest as constraints. Replace `estate.cue`. Each bucket gains `encrypted` and `retention_days`.

```cue file=estate.cue status=valid hl="7-8,14-16"
package estate

buckets: {
	logs: {
		name:           "logs"
		versioning:     true // keep old versions
		encrypted:      true
		retention_days: 365
		tags: owner: "platform"
	}
	uploads: {
		name:           "uploads"
		versioning:     false
		encrypted:      bool
		retention_days: >=1
		tags: owner: string
	}
}

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     22
		protocol: "tcp"
	}
}
```

Ask for the `uploads` bucket only. The `-e` flag evaluates one field, given by its path.

```sh run
pravo eval -e buckets.uploads
```

```text out
name:           "uploads"
versioning:     false
encrypted:      bool
retention_days: >=1
tags: {
	owner: string
}
```

`pravo eval` prints a constraint as it is. An unfinished value is not an error.

:::predict Which fields of the uploads bucket are concrete?
`uploads` has five fields: `name`, `versioning`, `encrypted`, `retention_days` and `tags.owner`. Decide before you open the answer.

::reveal
`name` and `versioning`. They hold `"uploads"` and `false`, each one value. The other three hold `bool`, `>=1` and `string`. The flag `-c` asks `pravo eval` to fail unless the value is concrete, so these two commands pass.

```sh run
pravo eval -c -e buckets.uploads.name
pravo eval -c -e buckets.uploads.versioning
```

```text out
"uploads"
false
```

:::

## Break it

The flag `-c` makes `pravo eval` fail unless every value is concrete. Run it on the file you wrote. The file is valid. The command is refused.

```sh run exit=1 status=rejected
pravo eval -c
```

```text out
Error: the value is not concrete:
buckets.uploads.encrypted: incomplete value bool:
    ./estate.cue:14:19
buckets.uploads.retention_days: incomplete value >=1:
    ./estate.cue:15:19
buckets.uploads.tags.owner: incomplete value string:
    ./estate.cue:16:16
```

Read the error from left to right.

- **Stage.** `the value is not concrete` says that `-c` refused the file. The CUE is valid.
- **Path.** Each message starts with the path of a field: `buckets.uploads.encrypted`. There are three, one for each constraint left in `uploads`.
- **Message.** `incomplete value bool` says that the field holds the constraint `bool`. It names the constraint after the word `value`.
- **Location.** `./estate.cue:14:19` is the file, the line and the column of the constraint.

[Incomplete](../reference/glossary.md#incomplete) does not mean wrong. It means not finished. [Why is my value incomplete?](c2-why-is-my-value-incomplete.md) lists the causes.

## Exercise

:::exercise Finish the uploads bucket
The web team replies: encrypted, kept for 30 days, owned by `web`. Replace the three constraints in `uploads` with these concrete values.

The check passes when `pravo eval -c` succeeds.

```sh file=check.sh
pravo eval -c > /dev/null 2>&1 || { echo "a value is still not concrete"; exit 1; }
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
a value is still not concrete
```

::hint
Replace each constraint with one value of the same kind. Use a boolean, a whole number from 1 upward and a string.

::hint
`pravo eval -c` lists every field that is still open. See [concrete](../reference/glossary.md#concrete) for the definition.

::solution
```cue file=estate.cue
package estate

buckets: {
	logs: {
		name:           "logs"
		versioning:     true // keep old versions
		encrypted:      true
		retention_days: 365
		tags: owner: "platform"
	}
	uploads: {
		name:           "uploads"
		versioning:     false
		encrypted:      true
		retention_days: 30
		tags: owner: "web"
	}
}

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     22
		protocol: "tcp"
	}
}
```

```sh run check
sh check.sh
```
:::

## Recap

- A type such as `int` is a value that stands for every value of its kind. `30` stands for one.
- A constraint allows many values. A concrete value allows one.
- `pravo eval` prints constraints as they are. `pravo eval -c` fails while any remain.

:::check
Is `string` a value?
::answer
Yes. It is the value that stands for every string. `"web"` is a narrower value that stands for one.

What does `incomplete value bool` tell you?
::answer
The field holds the constraint `bool` and not a concrete value. It is not wrong. It is not finished.
:::

The next lesson writes `encrypted: bool` and `encrypted: true` for the same field.
