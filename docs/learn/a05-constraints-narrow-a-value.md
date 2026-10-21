---
title: Constraints narrow a value
description: A rule in CUE is a value written on the field. Bounds and regular expressions narrow what the field may hold.
order: 5
group: Track A · CUE from zero
minutes: 9
needs: A4
learn: How do you restrict a value to a range or to text of a given shape?
done_when: "`pravo eval` rejects a port of 70000 and a bucket name of `Logs`. It prints the data when the values are inside their bounds."
level: beginner
keywords: "validation constraint bound regex pattern"
---

In HCL, every rule is a `validation` block with its own message. The value arrives from another file, and OpenTofu tests it when you plan.

```hcl title="variables.tf"
variable "bucket_name" {
  type = string
  validation {
    condition     = can(regex("^[a-z0-9-]{3,63}$", var.bucket_name))
    error_message = "Use 3 to 63 lowercase letters, digits or hyphens."
  }
}

variable "port" {
  type = number
  validation {
    condition     = var.port >= 1 && var.port <= 65535
    error_message = "Use a port from 1 to 65535."
  }
}
```

The same two rules in CUE are two fields.

```cue title="the same two rules"
rules: https: port: int & >=1 & <=65535
buckets: audit: name: =~"^[a-z0-9-]{3,63}$"
```

The HCL takes 15 lines and the CUE takes 2. In CUE the rule is a value that you write on the field, next to the value it judges. The CUE has no `error_message`: pravo builds the message from the rule that failed, as the next sections show.

The operator `&` joins two values into one value that satisfies both. `>=1`, `<=65535` and `=~"^[a-z0-9-]{3,63}$"` are values too. Each one is a **[bound](../reference/glossary.md#bound)**: a value that allows only some numbers or some text. A field that carries a bound allows only what the bound allows.

:::widget unify
```json
{
  "title": "Unification",
  "left": {"tag": "a type", "label": "int"},
  "right": {"tag": "a range", "label": ">=1 & <=10"},
  "merged": "int & >=1 & <=10",
  "chips": [
    {"value": "3", "ok": true, "result": "3"},
    {"value": "0", "ok": false, "result": "invalid value 0 (out of bound >=1)"},
    {"value": "11", "ok": false, "result": "invalid value 11 (out of bound <=10)"},
    {"value": "\"3\"", "ok": false, "result": "conflicting values int and \"3\" (mismatched types int and string)"}
  ],
  "alt": "The type int and the range >=1 & <=10 join into int & >=1 & <=10. The value 3 is accepted. The value 0 is rejected by the bound >=1, and 11 by the bound <=10. The string \"3\" is rejected by the type."
}
```
:::

:::deep-dive Run the four values
The widget shows results that `pravo eval -e` printed. The flag evaluates an expression inside a package. The runs below use an empty package in a directory of its own. The two numbers at the end of each location are the line and the column in the expression. They point at the bound or the type that rejected the value, then at the value.

```cue file=probe/probe.cue
package estate
```

```sh run exit=1
pravo -C probe eval -e 'int & >=1 & <=10 & 3'
pravo -C probe eval -e 'int & >=1 & <=10 & 0'
pravo -C probe eval -e 'int & >=1 & <=10 & 11'
pravo -C probe eval -e 'int & >=1 & <=10 & "3"'
```

```text out
3

Error: -e "int & >=1 & <=10 & 0": invalid value 0 (out of bound >=1):
    1:7
    1:20


Error: -e "int & >=1 & <=10 & 11": invalid value 11 (out of bound <=10):
    1:13
    1:20


Error: -e "int & >=1 & <=10 & \"3\"": conflicting values int and "3" (mismatched types int and string):
    1:1
    1:20
```

:::

## Warm-up

:::check From earlier lessons
From A2: what does `pravo eval -c` do?
::answer
It fails unless every value is concrete. A constraint such as `int` or `>=1` is not concrete.

From A3: two files give `retention_days` the values 30 and 90. What does pravo print?
::answer
A conflict that names the path, the two values and both locations. It prints no data.
:::

## Start from the estate

Work in the directory from A4. If you start here, write these three files.

```cue file=buckets.cue
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
```

```cue file=rules.cue
package estate

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

```cue file=team.cue
package estate

buckets: uploads: tags: cost_center: "4711"

buckets: uploads: tags: env: "prod"
```

Save the output.

```sh run
pravo eval > before.txt
```

## A bound on a number

The platform team limits every port to the integers from 1 to 65535. It keeps its limits in a file of its own.

```cue file=limits.cue status=valid
package estate

rules: https: port: int & >=1 & <=65535

rules: ssh: port: int & >=1 & <=65535
```

Both ports are inside the bound, so the data does not change.

```sh run
pravo eval | diff before.txt - && echo same
```

```text out
same
```

A bound changes nothing for a value that meets it. It matters when a value does not.

## Break it

Someone sets the port of the `ssh` rule to 70000. Replace `rules.cue`.

```cue file=rules.cue status=rejected hl="9"
package estate

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     70000
		protocol: "tcp"
	}
}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
rules.ssh.port: invalid value 70000 (out of bound <=65535):
    ./limits.cue:5:31
    ./rules.cue:9:13
```

Read the error from left to right.

- **Path.** `rules.ssh.port` is the field that failed.
- **Message.** `invalid value 70000 (out of bound <=65535)` names the value and the bound it breaks. The bound is named, so you know which of `>=1` and `<=65535` rejected it.
- **Locations.** The first location is the bound that rejected the value: `limits.cue`, line 5, column 31. The second is the value: `rules.cue`, line 9.

Put the port back.

```cue file=rules.cue status=valid
package estate

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

## A bound is not a type

A bound allows every number in its range. Write a scratch file with a bound and a value of 22.5.

```cue file=float/f.cue status=unintended hl="4"
package float

port: >=1 & <=65535
port: 22.5
```

```sh run
pravo -C float eval
```

```text out
port: 22.5
```

The file is valid and the value is not a port. Add `int`.

```cue file=float/f.cue status=rejected hl="3"
package float

port: int & >=1 & <=65535
port: 22.5
```

```sh run exit=6
pravo -C float eval
```

```text out
Error: evaluating ./float:
port: conflicting values int and 22.5 (mismatched types int and float):
    ./float/f.cue:3:7
    ./float/f.cue:4:7
```

The message names both sides of the clash: the type `int` and the value `22.5`. The reason follows in brackets, `mismatched types int and float`. This is why the limits above start with `int &`.

## Bounds on text

A regular expression is a bound too. The operator `=~` accepts only text that matches it. Write a scratch file with a bucket name that does not match.

```cue file=names/n.cue status=rejected hl="3"
package names

name: =~"^[a-z0-9-]{3,63}$"
name: "Logs_Bucket"
```

```sh run exit=6
pravo -C names eval
```

```text out
Error: evaluating ./names:
name: invalid value "Logs_Bucket" (out of bound =~"^[a-z0-9-]{3,63}$"):
    ./names/n.cue:3:7
    ./names/n.cue:4:7
```

The message reads as it did for the port. The bound is named, with its regular expression, and the first location is the bound. The expression means 3 to 63 characters, each a lowercase letter, a digit or a hyphen.

| Bound | Allows | A value that passes |
|---|---|---|
| `>=1` | numbers of at least 1 | `5` |
| `>4` | numbers above 4 | `5` |
| `<=5` | numbers of at most 5 | `5` |
| `<6` | numbers below 6 | `5` |
| `!=4` | anything except 4 | `5` |
| `=~"^[a-z][0-9]$"` | text that matches | `"a1"` |
| `!~"^b"` | text that does not match | `"a1"` |

:::deep-dive Run each bound
```sh run
pravo eval -e '5 & >=1'
pravo eval -e '5 & >4'
pravo eval -e '5 & <=5'
pravo eval -e '5 & <6'
pravo eval -e '5 & !=4'
pravo eval -e '"a1" & =~"^[a-z][0-9]$"'
pravo eval -e '"a1" & !~"^b"'
```

```text out
5
5
5
5
5
"a1"
"a1"
```

:::

The two rules for ports repeat. Every bucket name needs the same rule. A8 and A9 write each rule once.

## Exercise

:::exercise Write the bucket-name rule
A third bucket, `audit`, has no name yet. Its name must follow the rule from the HCL at the top of this page.

```cue file=audit.cue
package estate

buckets: audit: {
	name:       string
	versioning: true
}
```

Add one line to `limits.cue` that bounds `buckets.audit.name`. The check joins three valid and three invalid names to that field with `&`.

```sh file=check.sh
fixture() {
  if pravo eval -e "buckets.audit.name & \"$1\"" > /dev/null 2>&1; then got=valid; else got=invalid; fi
  [ "$got" = "$2" ] || { echo "$1 should be $2"; exit 1; }
}
fixture logs valid
fixture my-bucket-01 valid
fixture abc valid
fixture ab invalid
fixture Logs invalid
fixture my_bucket invalid
echo ok
```

Run the check on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
ab should be invalid
```

::hint
Write the bound with `=~` and a regular expression. A name has 3 to 63 characters, each a lowercase letter, a digit or a hyphen. Add the line to `limits.cue`.

::hint
The table in [Bounds on text](#bounds-on-text) lists `=~`. See [bound](../reference/glossary.md#bound).

::solution
```cue file=limits.cue
package estate

rules: https: port: int & >=1 & <=65535

rules: ssh: port: int & >=1 & <=65535

buckets: audit: name: =~"^[a-z0-9-]{3,63}$"
```

```sh run check
sh check.sh
```
:::

## Recap

- `&` joins two values into one that satisfies both. A bound such as `>=1` is a value, so a rule is a value written on the field.
- A bound that rejects a value is named in the error. The first location is the bound, and the second is the value.
- A numeric bound allows decimals. Add `int` when the field is a whole number.

:::check
What do the two locations in `invalid value 70000 (out of bound <=65535)` point to?
::answer
The first is the bound that rejected the value. The second is the value.

Does `>=1 & <=65535` reject `22.5`?
::answer
No. A numeric bound allows every number in its range. `int & >=1 & <=65535` rejects it.
:::

The next lesson offers choices and a default.
