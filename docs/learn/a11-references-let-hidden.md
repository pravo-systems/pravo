---
title: Naming a value once
description: Use the value of one field in another. Keep helper values out of the output with let. Share them across files with hidden fields.
order: 11
group: Track A · CUE from zero
minutes: 8
needs: A10
learn: How does one field use the value of another? Where does a helper value live so that it stays out of the output?
done_when: "`pravo eval` prints the same data from a shorter file, and a misspelt name is refused."
level: beginner
---

HCL keeps a named value in a `locals` block. You read it with the prefix `local.`.

```hcl title="main.tf"
locals {
  base = "10.0"
}

resource "aws_subnet" "a" {
  vpc_id     = "vpc-0abc1234"
  cidr_block = "${local.base}.1.0/24"
}
```

The prefix says where the name lives. It also means a value that is not in `locals` needs another prefix: `var.`, `data.`, `module.`.

CUE has no prefix. A [field](../reference/glossary.md#field) is a name, and you write the name where you need the value. This is a [reference](../reference/glossary.md#reference).

## Warm-up

:::check From earlier lessons
::question
A pattern gives every bucket a name built from its key:

```cue
buckets: [Name=string]: {name: "estate-\(Name)"}
buckets: logs: {}
```

What does `pravo eval` print for `logs`?
::answer
The name `estate-logs`. The pattern from [A9](a09-patterns.md) applies to every entry, and `Name` stands for the key.

A bucket is built from a definition that declares `name`. What happens when a second field is spelled `nmae`?
::answer
The file is refused with `field not allowed`. A definition is [closed](../reference/glossary.md#closed) ([A8](a08-definitions-reject-typos.md)).
:::

## Refer to a field

Work in an empty directory. Write `estate.cue`. The prefix `10.0` is written once, in `base`.

```cue file=estate.cue hl="3,6-7,10"
package estate

vpc: cidr: "\(base).0.0/16"

subnets: {
	a: cidr: "\(base).1.0/24"
	b: cidr: "\(base).2.0/24"
}

base: "10.0"
```

```sh run
pravo eval
```

```text out
vpc: {
	cidr: "10.0.0.0/16"
}
subnets: {
	a: {
		cidr: "10.0.1.0/24"
	}
	b: {
		cidr: "10.0.2.0/24"
	}
}
base: "10.0"
```

Inside a string, `\(base)` stands for the value of `base`. [A12](a12-conditions-and-strings.md) covers strings.

`base` is written last and used first. Order does not matter ([A4](a04-order-does-not-matter.md)). There is no evaluation order to manage.

`pravo eval` also prints `base`. A regular field is data, and all data is output.

## Keep a helper out of the output

A value that only helps you build other values should not be output. A [let](../reference/glossary.md#let) names a value for use in the file. It is not a field.

```cue file=estate.cue hl="3"
package estate

let base = "10.0"

vpc: cidr: "\(base).0.0/16"

subnets: {
	a: cidr: "\(base).1.0/24"
	b: cidr: "\(base).2.0/24"
}
```

```sh run
pravo eval
```

```text out
vpc: {
	cidr: "10.0.0.0/16"
}
subnets: {
	a: {
		cidr: "10.0.1.0/24"
	}
	b: {
		cidr: "10.0.2.0/24"
	}
}
```

The data is the same, and `base` is gone from the output.

## Share a helper between files

A `let` at the top of a file belongs to that file. A [hidden field](../reference/glossary.md#hidden-field) belongs to the package. Its name starts with `_`. Every file of the package can read it, and `pravo eval` does not print it.

Write a second file.

```cue file=zone.cue
package estate

_region: "eu-west-1"
```

Read `_region` from `estate.cue`.

```cue file=estate.cue hl="8-9"
package estate

let base = "10.0"

vpc: cidr: "\(base).0.0/16"

subnets: {
	a: {cidr: "\(base).1.0/24", zone: "\(_region)a"}
	b: {cidr: "\(base).2.0/24", zone: "\(_region)b"}
}
```

```sh run
pravo eval
```

```text out
vpc: {
	cidr: "10.0.0.0/16"
}
subnets: {
	a: {
		cidr: "10.0.1.0/24"
		zone: "eu-west-1a"
	}
	b: {
		cidr: "10.0.2.0/24"
		zone: "eu-west-1b"
	}
}
```

The three kinds of name differ in two ways.

| Kind | Written as | In the output | Readable from |
|---|---|---|---|
| Regular field | `base: "10.0"` | Yes | Every file of the package, and importing packages |
| `let` | `let base = "10.0"` | No | The file, or the struct where it is written |
| Hidden field | `_region: "eu-west-1"` | No | Every file of the package |

`pravo eval` omits hidden fields, and `-e` cannot name one.

```sh run exit=1
pravo eval -e _region
```

```text out
Error: -e "_region": reference "_region" not found:
    1:1
```

To look at a hidden value, copy it into a regular field.

## Break it

Misspell a name.

```cue file=estate.cue status=rejected hl="5"
package estate

let base = "10.0"

vpc: cidr: "\(bse).0.0/16"

subnets: {
	a: {cidr: "\(base).1.0/24", zone: "\(_region)a"}
	b: {cidr: "\(base).2.0/24", zone: "\(_region)b"}
}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
vpc.cidr: reference "bse" not found:
    ./estate.cue:5:15
```

Read the error from the top.

- The path is `vpc.cidr`: the field that holds the reference.
- The message is `reference "bse" not found`. No struct around the reference has a field or `let` named `bse`.
- The location is line 5, column 15: the first letter of `bse`.

The same error appears when the name exists but is not visible from the file. Correct the typo, then read `base` from a second file.

```cue file=estate.cue hl="5"
package estate

let base = "10.0"

vpc: cidr: "\(base).0.0/16"

subnets: {
	a: {cidr: "\(base).1.0/24", zone: "\(_region)a"}
	b: {cidr: "\(base).2.0/24", zone: "\(_region)b"}
}
```

```cue file=other.cue status=rejected
package estate

other: cidr: "\(base).9.0/24"
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
other.cidr: reference "base" not found:
    ./other.cue:3:17
```

The `let` is private to `estate.cue`, so `other.cue` cannot see it.

A `let` that nothing reads is refused too. Replace `other.cue`.

```cue file=other.cue status=rejected
package estate

let spare = "unused"
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
unreferenced alias or let clause spare:
    ./other.cue:3:1
```

Remove the file.

```sh run
rm other.cue
```

## A field cannot read its own name

In HCL you write `region = local.region`. The same shape in CUE, `region: region`, does not work.

A name looks in the struct where it is written, then in each struct around it. It uses the first match. The field you are writing is a match.

```cue file=estate.cue status=unintended hl="6"
package estate

region: "eu-west-1"

bucket: {
	region: region
}
```

:::predict A top-level `region` exists. What does `bucket.region` hold?
Decide before you open the answer.

::reveal
It holds `_`, which means any value. The `region` on the right is the field being written, so the field refers to itself.

```sh run exit=1
pravo eval
pravo eval -c
```

```text out
region: "eu-west-1"
bucket: {
	region: _
}

Error: the value is not concrete:
bucket.region: incomplete value _
```

:::

`pravo eval` does not fail here. The flag `-c` does, because `_` is not [concrete](../reference/glossary.md#concrete).

> [!PITFALL]
> A field must not share a name with the value it reads. Give the helper another name: `let prefix`, or `_region` for a field `region`.

## Exercise

:::exercise Write each value once
Start clean.

```sh run
rm zone.cue
```

This file has 24 lines. It repeats `10.0` and `eu-west-1`.

```cue file=estate.cue
package estate

vpc: cidr: "10.0.0.0/16"

subnets: {
	a: {cidr: "10.0.1.0/24", zone: "eu-west-1a"}
	b: {cidr: "10.0.2.0/24", zone: "eu-west-1b"}
	c: {cidr: "10.0.3.0/24", zone: "eu-west-1c"}
}

buckets: {
	logs: {
		name:   "estate-logs-eu-west-1"
		region: "eu-west-1"
	}
	assets: {
		name:   "estate-assets-eu-west-1"
		region: "eu-west-1"
	}
	backups: {
		name:   "estate-backups-eu-west-1"
		region: "eu-west-1"
	}
}
```

Save the output first.

```sh run
pravo eval > expected.txt
wc -l < estate.cue | tr -d ' '
```

```text out
24
```

Rewrite `estate.cue` so that `10.0` and `eu-west-1` each appear once. The output must match `expected.txt`, and the file must be shorter.

```sh file=check.sh
pravo eval | diff expected.txt - > /dev/null || { echo "the output changed"; exit 1; }
[ "$(grep -o '10\.0' estate.cue | wc -l | tr -d ' ')" = 1 ] || { echo "10.0 should appear once"; exit 1; }
[ "$(grep -o 'eu-west-1' estate.cue | wc -l | tr -d ' ')" = 1 ] || { echo "eu-west-1 should appear once"; exit 1; }
[ "$(wc -l < estate.cue | tr -d ' ')" -lt 24 ] || { echo "the file should be shorter than 24 lines"; exit 1; }
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
10.0 should appear once
```

::hint
Use a `let` for each repeated value. The field `region` needs a helper with a different name.

::hint
Read [let](../reference/glossary.md#let) and the pitfall above. A helper named `region` clashes with the field `region`.

::solution
```cue file=estate.cue
package estate

let prefix = "10.0"
let zoneName = "eu-west-1"

vpc: cidr: "\(prefix).0.0/16"

subnets: {
	a: {cidr: "\(prefix).1.0/24", zone: "\(zoneName)a"}
	b: {cidr: "\(prefix).2.0/24", zone: "\(zoneName)b"}
	c: {cidr: "\(prefix).3.0/24", zone: "\(zoneName)c"}
}

buckets: {
	logs: {name: "estate-logs-\(zoneName)", region: zoneName}
	assets: {name: "estate-assets-\(zoneName)", region: zoneName}
	backups: {name: "estate-backups-\(zoneName)", region: zoneName}
}
```

```sh run check
sh check.sh
```

```sh run
wc -l < estate.cue | tr -d ' '
```

```text out
18
```

:::

## Recap

- A field reads another field by its name. Order does not matter.
- A `let` or a `_hidden` field names a helper and stays out of the output. A `let` belongs to its file. A hidden field belongs to the package.
- A name uses the first match, searching outward from where it is written. The field you are writing is a match.

:::check
Which two kinds of name stay out of `pravo eval` output?
::answer
A `let` and a hidden field. A regular field is data and is always printed.

Why does `region: region` leave `region` as `_`?
::answer
The right-hand `region` finds the field being written, which is the first match. The field refers to itself. Name the helper differently.
:::

The next lesson makes a field exist only when a condition holds.
