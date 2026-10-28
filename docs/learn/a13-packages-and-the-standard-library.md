---
title: Packages and the standard library
description: Import validators and functions from the standard library, and share a definition between directories with your own package.
order: 13
group: Track A · CUE from zero
minutes: 12
needs: A12
learn: Which checks and functions does the standard library offer? How does one directory use a definition from another?
done_when: "`pravo eval` accepts the CIDR `10.0.1.0/24` and refuses `10.0.2.0/33`."
level: intermediate
---

HCL's built-in functions need no import. You call them anywhere. To check a value, you put a function call in a `validation` block.

```hcl title="main.tf"
variable "cidr" {
  type = string
  validation {
    condition     = can(cidrhost(var.cidr, 0))
    error_message = "cidr must be a network in CIDR notation."
  }
}

locals {
  subnet = cidrsubnet(var.cidr, 8, 1)
  name   = upper(var.name)
  policy = jsonencode({ cidrs = [local.subnet] })
}
```

To share a definition between directories, HCL uses a module with its own variables and outputs. CUE has no module of that kind. A package is shared as it is, and a definition in it is used by name.

In CUE, a check is a value you join to the field. A function is a name in a package. A [package](../reference/glossary.md#package) is a directory of `.cue` files that share a `package` clause. You have written one since [A1](a01-your-data-is-already-cue.md). An [import](../reference/glossary.md#import) makes another package available under its last name. After `import "strings"`, you write `strings.ToUpper`.

The standard library ships with pravo. Your own directories can be packages too.

## Warm-up

:::check From earlier lessons
A comprehension uses the zone name as the key, and two zones have the same name. What does `pravo eval` print?
::answer
A conflict at that key, with the locations of both entries ([A10](a10-comprehensions.md)).

A file has a `let`, a hidden field and a regular field that reads both. What does `pravo eval` print?
::answer
Only the regular field. A `let` and a hidden field stay out of the output ([A11](a11-references-let-hidden.md)).
:::

## Check values with validators

Many names in the standard library are validators. A validator is a value that accepts or refuses, and you combine it with `&` like `int` or `>=1` ([A5](a05-constraints-narrow-a-value.md)). The value being checked is its first argument, so you write the others.

Work in an empty directory. Write `estate.cue`.

```cue file=estate.cue hl="10-11,14"
package estate

import (
	"list"
	"net"
	"strings"
)

subnet: {
	zone: strings.HasPrefix("eu-") & "eu-west-1a"
	cidr: net.IPCIDR & "10.0.1.0/24"
}

zones: list.MinItems(1) & ["a", "b"]
```

```sh run
pravo eval
```

```text out
subnet: {
	zone: "eu-west-1a"
	cidr: "10.0.1.0/24"
}
zones: ["a", "b"]
```

`net.IPCIDR` accepts a network in CIDR notation. `strings.HasPrefix("eu-")` accepts a string that starts with `eu-`. `list.MinItems(1)` accepts a list with at least one item.

A file imports only what it uses. Each file lists its own imports.

## Compute values with functions

The other names are functions. A function takes values and returns a value. Add three fields to the file. The block shows the whole file.

```cue file=estate.cue hl="4,17-19"
package estate

import (
	"encoding/json"
	"list"
	"net"
	"strings"
)

subnet: {
	zone: strings.HasPrefix("eu-") & "eu-west-1a"
	cidr: net.IPCIDR & "10.0.1.0/24"
}

zones: list.MinItems(1) & ["a", "b"]

label:  strings.ToUpper(subnet.zone)
total:  list.Sum([30, 60, 90])
policy: json.Marshal({cidrs: [subnet.cidr]})
```

```sh run
pravo eval
```

```text out
subnet: {
	zone: "eu-west-1a"
	cidr: "10.0.1.0/24"
}
zones: ["a", "b"]
label:  "EU-WEST-1A"
total:  180
policy: "{\"cidrs\":[\"10.0.1.0/24\"]}"
```

`json.Marshal` turns a value into JSON text. The result is an ordinary string, so pravo prints its quotes escaped. `json.Unmarshal` does the reverse.

## Try a function without a file

`pravo eval -e` accepts standard library names without an import. It works as a calculator. These rows pair common HCL functions with their CUE side.

| HCL | CUE |
|---|---|
| `cidrsubnet("10.0.0.0/16", 8, n)` | `net.AddIPCIDR("10.0.0.0/24", n)` |
| `contains(list, x)` | `list.Contains(list, x)` |
| `upper(s)` | `strings.ToUpper(s)` |
| `join("-", list)` | `strings.Join(list, "-")` |
| `jsonencode(v)` | `json.Marshal(v)` |
| `jsondecode(s)` | `json.Unmarshal(s)` |
| `length(list)` | `len(list)` |
| `can(regex(p, s))` | `regexp.Match(p, s)` |

`join` and `strings.Join` take their arguments in a different order. `net.AddIPCIDR` adds `n` whole subnets of the size you give it.

```sh run
pravo eval -e 'net.AddIPCIDR("10.0.0.0/24", 3)'
pravo eval -e 'list.Contains(["a", "b"], "b")'
pravo eval -e 'strings.ToUpper("logs")'
pravo eval -e 'strings.Join(["a", "b"], "-")'
pravo eval -e 'json.Marshal({a: 1})'
pravo eval -e 'json.Unmarshal("{\"a\": 1}")'
pravo eval -e 'len([1, 2, 3])'
pravo eval -e 'regexp.Match("^eu-", "eu-west-1")'
```

```text out
"10.0.3.0/24"
true
"LOGS"
"a-b"
"{\"a\":1}"
a: 1
3
true
```

The [function map](../migrate/function-map.md) lists every HCL function.

| Package | Holds | Names on this page |
|---|---|---|
| `strings` | text functions and checks | `HasPrefix`, `ToUpper`, `Join` |
| `list` | list functions and checks | `MinItems`, `Sum`, `Contains` |
| `net` | address checks and address arithmetic | `IPCIDR`, `AddIPCIDR` |
| `encoding/json` | JSON text | `Marshal`, `Unmarshal` |
| `regexp` | regular expression matching | `Match` |

:::deep-dive More names to try
The standard library has many more. Each line below runs as written.

```sh run
pravo eval -e 'strings.Split("a,b,c", ",")'
pravo eval -e 'strings.TrimPrefix("estate-logs", "estate-")'
pravo eval -e 'strings.Replace("a-b-c", "-", "_", -1)'
pravo eval -e 'list.Max([3, 9, 4])'
pravo eval -e 'list.SortStrings(["b", "a"])'
pravo eval -e 'list.Concat([[1], [2, 3]])'
pravo eval -e 'net.InCIDR("10.0.1.5", "10.0.0.0/16")'
pravo eval -e 'net.IPv4 & "10.0.0.2"'
pravo eval -e 'json.Valid("{")'
pravo eval -e 'math.Pow(2, 10)'
pravo eval -e 'strconv.Atoi("42")'
pravo eval -e 'path.Base("/a/b/c.txt")'
pravo eval -e 'base64.Encode(null, "hi")'
```

```text out
["a", "b", "c"]
"logs"
"a_b_c"
9
["a", "b"]
[1, 2, 3]
true
"10.0.0.2"
false
1024
42
"c.txt"
"aGk="
```

The packages `math`, `strconv`, `path`, `encoding/yaml`, `encoding/base64` and `time` ship with pravo as well.
:::

## Share a definition in your own package

A directory below the top of the CUE module is a package of its own. To import it, the root needs a file `cue.mod/module.cue`. Its `module` line names the CUE module, the tree of packages that import paths start with.

`pravo init` writes that file and names the CUE module after the directory, as in `pravo.local/estate`. This lesson writes the file itself, so the import path is the same on every machine.

```cue file=cue.mod/module.cue
module: "estate.example"
language: version: "v0.14.0"
```

Put the definition in a directory `platform`. Its `package` clause is the directory name.

```cue file=platform/shapes.cue
package platform

import "net"

#Subnet: {
	zone!: string
	cidr!: net.IPCIDR
}
```

Replace `estate.cue`. It imports the package by the path of the CUE module and the directory.

```cue file=estate.cue hl="3,6-7"
package estate

import "estate.example/platform"

subnets: {
	a: platform.#Subnet & {zone: "eu-west-1a", cidr: "10.0.1.0/24"}
	b: platform.#Subnet & {zone: "eu-west-1b", cidr: "10.0.2.0/24"}
}
```

```sh run
find . -name '*.cue' | sort
pravo eval
```

```text out
./cue.mod/module.cue
./estate.cue
./platform/shapes.cue
subnets: {
	a: {
		zone: "eu-west-1a"
		cidr: "10.0.1.0/24"
	}
	b: {
		zone: "eu-west-1b"
		cidr: "10.0.2.0/24"
	}
}
```

A definition is visible from other packages, and so is a regular field. A hidden field is not.

## Break it

Give the second subnet a prefix length of 33. An IPv4 prefix has at most 32 bits.

```cue file=estate.cue status=rejected hl="7"
package estate

import "estate.example/platform"

subnets: {
	a: platform.#Subnet & {zone: "eu-west-1a", cidr: "10.0.1.0/24"}
	b: platform.#Subnet & {zone: "eu-west-1b", cidr: "10.0.2.0/33"}
}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
subnets.b.cidr: invalid value "10.0.2.0/33" (does not satisfy net.IPCIDR): netip.ParsePrefix("10.0.2.0/33"): prefix length out of range:
    ./estate.cue:7:51
    ./platform/shapes.cue:7:9
```

Read the error from the top.

- The path is `subnets.b.cidr`.
- The message names the value, `"10.0.2.0/33"`, and the rule it fails: `net.IPCIDR`.
- The reason follows: `prefix length out of range`.
- The first location is the value, in `estate.cue`. The second is the rule, in `platform/shapes.cue`.

The error points into two files, in two packages. You do not search for the rule.

Restore the valid subnet.

```cue file=estate.cue hl="7"
package estate

import "estate.example/platform"

subnets: {
	a: platform.#Subnet & {zone: "eu-west-1a", cidr: "10.0.1.0/24"}
	b: platform.#Subnet & {zone: "eu-west-1b", cidr: "10.0.2.0/24"}
}
```

:::predict A second file imports `strings` and never uses it. Is that accepted?
```cue file=unused.cue
package estate

import "strings"

x: 1
```

::reveal
No. An import that nothing uses is an error.

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
imported and not used: "strings":
    ./unused.cue:3:8
```

:::

Remove the file. The exercise starts from a clean directory.

```sh run
rm unused.cue
```

## Exercise

:::exercise A CIDR check on every subnet
Start from three subnets in one file.

```cue file=estate.cue
package estate

subnets: {
	a: {zone: "eu-west-1a", cidr: "10.0.1.0/24"}
	b: {zone: "eu-west-1b", cidr: "10.0.2.0/24"}
	c: {zone: "eu-west-1c", cidr: "10.0.3.0/24"}
}
```

Add one line so that every present and future subnet must have a `cidr` that satisfies `net.IPCIDR`. The check adds a fourth subnet with a bad address to a copy of the directory and expects a refusal. It also adds one with a good address and expects it to pass.

```sh file=check.sh
tmp=$(mktemp -d); cp estate.cue "$tmp/"
pravo eval > /dev/null 2>&1 || { echo "the three subnets should pass"; exit 1; }
printf 'package estate\n\nsubnets: d: {zone: "x", cidr: "10.0.4.0/33"}\n' > "$tmp/fixture.cue"
if pravo -C "$tmp" eval > /dev/null 2>&1; then echo "a bad cidr should be refused"; exit 1; fi
printf 'package estate\n\nsubnets: d: {zone: "x", cidr: "10.0.4.0/24"}\n' > "$tmp/fixture.cue"
pravo -C "$tmp" eval > /dev/null 2>&1 || { echo "a good cidr should pass"; exit 1; }
rm -rf "$tmp"
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
a bad cidr should be refused
```

::hint
A [pattern](../reference/glossary.md#pattern) applies a constraint to every entry of a struct. Import `net` at the top of the file.

::hint
Read [A9](a09-patterns.md). The pattern is `[string]: ...` and the constraint goes after the field name.

::solution
```cue file=estate.cue
package estate

import "net"

subnets: [string]: cidr: net.IPCIDR

subnets: {
	a: {zone: "eu-west-1a", cidr: "10.0.1.0/24"}
	b: {zone: "eu-west-1b", cidr: "10.0.2.0/24"}
	c: {zone: "eu-west-1c", cidr: "10.0.3.0/24"}
}
```

```sh run check
sh check.sh
```

A bad address in a second file is refused, and the error points at both files.

```sh run exit=6
printf 'package estate\n\nsubnets: d: {zone: "x", cidr: "10.0.4.0/33"}\n' > fixture.cue
pravo eval -e subnets.d
```

```text out
Error: evaluating .:
subnets.d.cidr: invalid value "10.0.4.0/33" (does not satisfy net.IPCIDR): netip.ParsePrefix("10.0.4.0/33"): prefix length out of range:
    ./estate.cue:5:26
    ./fixture.cue:3:31
```

:::

## Recap

- `import "name"` brings in a package. A validator joins a value with `&`. A function computes a value.
- `pravo eval -e` accepts standard library names without an import.
- A directory below the top of the CUE module is a package. A definition in it is checked wherever it is used.

:::check
Which argument of a validator do you leave out when you write it?
::answer
The first one. The value being checked is supplied by `&`, so `strings.HasPrefix("eu-") & x` checks `x`.

What happens to an import that no line uses?
::answer
The file is refused with `imported and not used`.
:::

The next lesson uses all of it in one specification.
