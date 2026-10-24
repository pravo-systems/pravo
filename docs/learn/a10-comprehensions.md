---
title: Generating fields from data
description: Build fields from a list or a struct with for. Choose keys that stay the same when the data is reordered.
order: 10
group: Track A · CUE from zero
minutes: 12
needs: A9
learn: How does for generate fields from data? Why does the key you give each field matter?
done_when: "A swap of two zones replaces the two resources keyed by position and none keyed by zone name. `pravo eval -e subnets` prints one subnet per zone."
level: intermediate
normalize:
  - pattern: '\[txn [0-9A-Za-z-]+\]'
    replace: '[txn <id>]'
keywords: "comprehension comprehensions for_each count for loop dynamic block iterate"
---

In HCL, `count` creates one resource per list entry. The position of the entry becomes the identity of the resource.

```hcl title="main.tf"
resource "aws_subnet" "main" {
  count             = length(var.subnets)
  availability_zone = var.subnets[count.index].zone
  cidr_block        = var.subnets[count.index].cidr
  vpc_id            = aws_vpc.main.id
}
```

Here `var.subnets` is a list of objects with `zone` and `cidr`. The instances are `aws_subnet.main[0]`, `[1]` and `[2]`. Swap the first two entries of the list, and instance 0 now describes what instance 1 described. The plan replaces both subnets.

With `for_each = { for s in var.subnets : s.zone => s }`, the instances are named by zone, and the swap changes nothing.

A [comprehension](../reference/glossary.md#comprehension) builds fields from other data, and you choose the key of each field. `for i, z in zones` walks a list and gives each position `i` and each value `z`. `for k, v in zones` walks a struct and gives each key and value. Write `_` for a value you do not need.

A key can be computed. Parentheses make the key an expression: `(z.name)` uses the value of `z.name` as the key. A string with `\(...)` builds a key from parts: `"subnet\(i)"` joins `subnet` and the value of `i`.

## Warm-up

:::check From earlier lessons
A rule `buckets: [Name=string]: #Bucket & {name: Name}` holds the entry `logs: {name: "other"}`. Is it accepted?
::answer
No. The rule sets `name` to the key `logs`, and `"other"` disagrees ([A9](a09-patterns.md)).

A field `description?: string` has no value. Does `pravo eval` print it?
::answer
No. An optional field without a value is left out. The flag `-O` prints it ([A7](a07-required-optional-regular.md)).
:::

## Generate from a list

Work in an empty directory. The data goes in one file and the comprehensions in another. Write `zones.cue`.

```cue file=zones.cue
package estate

zones: [
	{name: "eu-west-1a", cidr: "10.0.1.0/24"},
	{name: "eu-west-1b", cidr: "10.0.2.0/24"},
	{name: "eu-west-1c", cidr: "10.0.3.0/24"},
]
```

Write `estate.cue`. Two comprehensions turn the list into two structs.

```cue file=estate.cue
package estate

byIndex: {
	for i, z in zones {
		"subnet\(i)": z.cidr
	}
}

byName: {
	for z in zones {
		(z.name): z.cidr
	}
}
```

```sh run
pravo eval -e byIndex
```

```text out
subnet0: "10.0.1.0/24"
subnet1: "10.0.2.0/24"
subnet2: "10.0.3.0/24"
```

```sh run
pravo eval -e byName
```

```text out
"eu-west-1a": "10.0.1.0/24"
"eu-west-1b": "10.0.2.0/24"
"eu-west-1c": "10.0.3.0/24"
```

Both comprehensions read the same list and make three fields. They differ only in the key. `byIndex` takes its key from a position. `byName` takes its key from a property of the data, the zone.

## Reorder the data

:::predict Swap the first two zones in the list. Which lines of the two results change?
Think about each key. Does it still point to the same data?

::reveal
In `byIndex`, the lines `subnet0` and `subnet1` change their values. In `byName`, no line changes its value. Two lines change place.
:::

Swap the first two entries of `zones.cue`. Nothing else changes.

```cue file=zones.cue hl="4-5"
package estate

zones: [
	{name: "eu-west-1b", cidr: "10.0.2.0/24"},
	{name: "eu-west-1a", cidr: "10.0.1.0/24"},
	{name: "eu-west-1c", cidr: "10.0.3.0/24"},
]
```

```sh run
pravo eval -e byIndex
```

```text out
subnet0: "10.0.2.0/24"
subnet1: "10.0.1.0/24"
subnet2: "10.0.3.0/24"
```

```sh run
pravo eval -e byName
```

```text out
"eu-west-1b": "10.0.2.0/24"
"eu-west-1a": "10.0.1.0/24"
"eu-west-1c": "10.0.3.0/24"
```

Read the results by key.

| Field | Before | After |
|---|---|---|
| `byIndex.subnet0` | `10.0.1.0/24` | `10.0.2.0/24` |
| `byIndex.subnet1` | `10.0.2.0/24` | `10.0.1.0/24` |
| `byName."eu-west-1a"` | `10.0.1.0/24` | `10.0.1.0/24` |
| `byName."eu-west-1b"` | `10.0.2.0/24` | `10.0.2.0/24` |

The key `subnet0` held the first zone before the swap and holds the second one after it. The key `"eu-west-1a"` holds the same subnet in both cases.

## See it in a plan

An address is the path of a field from the top of the specification. `byIndex.subnet0` and `byName."eu-west-1a"` are addresses. pravo files one record per resource under its address. A resource made by a comprehension takes your key as the last part of its address.

Make each field a resource, and the same swap becomes a plan. This section uses the `local` provider from Start. Each resource is a file under `out/` whose content is the cidr. `pravo init` needs the network once, to fetch the provider. Track B explains every command.

```cue file=plan/zones.cue
package infra

zones: [
	{name: "eu-west-1a", cidr: "10.0.1.0/24"},
	{name: "eu-west-1b", cidr: "10.0.2.0/24"},
	{name: "eu-west-1c", cidr: "10.0.3.0/24"},
]
```

```cue file=plan/main.cue hl="9,18"
package infra

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

byIndex: {
	for i, z in zones {
		"subnet\(i)": $local.#Resource.local_file & {
			filename: "out/index-\(i).txt"
			content:  z.cidr
		}
	}
}

byName: {
	for z in zones {
		(z.name): $local.#Resource.local_file & {
			filename: "out/\(z.name).txt"
			content:  z.cidr
		}
	}
}
```

```sh run
pravo -C plan init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo -C plan apply -auto-approve | grep '^Apply complete'
pravo -C plan state list
```

```text out
Apply complete! Resources: 6 added, 0 changed, 0 destroyed. [txn <id>]
byIndex.subnet0
byIndex.subnet1
byIndex.subnet2
byName."eu-west-1a"
byName."eu-west-1b"
byName."eu-west-1c"
```

The state holds one record per address. Now swap the first two zones, as before.

```cue file=plan/zones.cue hl="4-5"
package infra

zones: [
	{name: "eu-west-1b", cidr: "10.0.2.0/24"},
	{name: "eu-west-1a", cidr: "10.0.1.0/24"},
	{name: "eu-west-1c", cidr: "10.0.3.0/24"},
]
```

:::predict Which of the six resources does the plan replace?
Think about each address. Does the data at that address change?

::reveal
The two at `byIndex.subnet0` and `byIndex.subnet1`. Their data changed. The four others are skipped.

```sh run
pravo -C plan plan | grep -E '^  #|^Plan:|^Graph:'
```

```text out
  # byIndex.subnet0 (local_file) must be replaced
  # byIndex.subnet1 (local_file) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · skipped 4 · refreshed 2 · not read 4
```

:::

The same data, reordered, replaced two resources keyed by position and touched none keyed by name. `count` in HCL has the first behaviour. `for_each` has the second. In pravo you choose by choosing the key.

> [!TIP]
> Choose a key that names the thing: a zone, a service, a team. A position, or an attribute that may change, is a poor key.

Remove what you made.

```sh run
pravo -C plan destroy -auto-approve | grep '^Destroy complete'
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 6 destroyed. [txn <id>]
```

[B5](b05-many-resources-from-one-rule.md) teaches this in full.

## Break it

Return to the first directory. Give two zones the same name.

```cue file=zones.cue status=conflict hl="6"
package estate

zones: [
	{name: "eu-west-1a", cidr: "10.0.1.0/24"},
	{name: "eu-west-1b", cidr: "10.0.2.0/24"},
	{name: "eu-west-1a", cidr: "10.0.3.0/24"},
]
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
byName."eu-west-1a": conflicting values "10.0.3.0/24" and "10.0.1.0/24":
    ./estate.cue:9:9
    ./estate.cue:10:2
    ./estate.cue:11:13
    ./zones.cue:4:29
    ./zones.cue:6:29
```

Read the error from the top.

- The path is `byName."eu-west-1a"`. Two entries produced the same key.
- The rule is a conflict ([A3](a03-writing-a-field-twice.md)). The key is one field, and it cannot hold two cidrs.
- The first three locations are the comprehension in `estate.cue` that produced the key. The last two are the entries that disagree, lines 4 and 6 of `zones.cue`.

A key taken from the data turns a duplicate into an error. With `count`, a duplicate is one more position.

## Exercise

:::exercise Subnets per zone
In this exercise the zones are a struct. Each key is a zone name. Replace `zones.cue` and `estate.cue`.

```cue file=zones.cue
package estate

zones: {
	"eu-west-1a": netnum: 1
	"eu-west-1b": netnum: 2
	"eu-west-1c": netnum: 3
}
```

```cue file=estate.cue
package estate

subnets: {}
```

Generate `subnets` with one entry per zone. Use the zone name as the key of each entry. Each entry has `zone` and `cidr`. The `cidr` is `10.0.N.0/24`, where `N` is the `netnum` of the zone.

Then reorder the lines of `zones.cue`. Every subnet must stay the same. The check does the reordering on a copy.

```sh file=check.sh
tmp=$(mktemp -d); cp estate.cue "$tmp/"
printf 'package estate\n\nzones: {\n\t"eu-west-1c": netnum: 3\n\t"eu-west-1b": netnum: 2\n\t"eu-west-1a": netnum: 1\n}\n' > "$tmp/zones.cue"
test "$(pravo eval -e 'len(subnets)' 2>/dev/null)" = 3 || { echo "subnets should hold three entries"; exit 1; }
test "$(pravo eval -e 'subnets["eu-west-1b"].cidr' 2>/dev/null)" = '"10.0.2.0/24"' || { echo "eu-west-1b should have 10.0.2.0/24"; exit 1; }
for k in eu-west-1a eu-west-1b eu-west-1c; do
  test "$(pravo eval -e "subnets[\"$k\"]")" = "$(pravo -C "$tmp" eval -e "subnets[\"$k\"]")" || { echo "$k changed when zones.cue was reordered"; exit 1; }
done
rm -rf "$tmp"
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
subnets should hold three entries
```

::hint
`for z, c in zones` gives the key `z` and the value `c`. Write `(z)` as the key. Inside the string for the cidr, write `\(c.netnum)`.

::hint
See [comprehension](../reference/glossary.md#comprehension). The key of the generated field is the only place where the zone name is needed.

::solution
```cue file=estate.cue
package estate

subnets: {
	for z, c in zones {
		(z): {
			zone: z
			cidr: "10.0.\(c.netnum).0/24"
		}
	}
}
```

```sh run check
sh check.sh
```

```sh run
pravo eval -e subnets
```

```text out
"eu-west-1a": {
	zone: "eu-west-1a"
	cidr: "10.0.1.0/24"
}
"eu-west-1b": {
	zone: "eu-west-1b"
	cidr: "10.0.2.0/24"
}
"eu-west-1c": {
	zone: "eu-west-1c"
	cidr: "10.0.3.0/24"
}
```

:::

## Recap

- `for` builds fields from a list or a struct. The key you write names each field.
- An address is the path of a field. pravo files a resource under its address, so a key that moves is a resource that is replaced.
- Name a field after the thing it describes, so that reordering the data changes nothing.

:::check
Which key survives a reorder of the list: `"subnet\(i)"` or `(z.name)`?
::answer
The second one. A zone name stays with its zone. A position moves to whatever entry now stands there.

What is an address?
::answer
The path of a field from the top of the specification, such as `byName."eu-west-1a"`.
:::

The next lesson gives a value a name, so you write it once.
