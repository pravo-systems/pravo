---
title: Writing a field twice
description: Writing a field twice adds detail. The copies must agree, and a disagreement is an error, not an override.
order: 3
group: Track A · CUE from zero
minutes: 10
needs: A2
learn: What happens when two values for one field disagree?
done_when: "`pravo eval` prints one merged value for the fields you wrote twice, and an error for the one conflict."
level: beginner
keywords: "conflicting values conflict unification default override"
---

A bucket module declares `encrypted` with a default of `true`. A caller sets it to `false` while debugging an upload failure. OpenTofu uses the caller's value.

:::compare
```hcl title="envs/prod/main.tf" hl="4"
module "logs" {
  source    = "../../modules/bucket"
  name      = "logs"
  encrypted = false
}
```

```cue title="two files, one field" hl="2,4"
// estate.cue
buckets: logs: encrypted: true
// team.cue
buckets: logs: encrypted: false
```
:::

The module declares `variable "encrypted" { default = true }`, and the caller's line replaces it. The same rule appears across HCL.

- `merge({ encrypted = true }, { encrypted = false })` returns a map in which `encrypted` is `false`.
- A variable that is set in `terraform.tfvars` and again with `-var` takes the value of the last source.
- A `validation` block can refuse `false`, but only when the module author wrote one.

In HCL the last writer wins. CUE has no last writer. A field is a constraint, and writing it twice adds a second constraint. The value must satisfy both. That is **[unification](../reference/glossary.md#unification)**. When no value can satisfy both, you have a **[conflict](../reference/glossary.md#conflict)**, and pravo stops. You caused one in Start, step 8. This lesson shows why it happens.

The widget below applies the rule to a type and a range. Pick a value to write against them. The four programs under it are the ones that `pravo eval` ran for this page.

:::demo idea
:::

## Copies that agree

Work in the directory from A2. If you start here, write this file.

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

A second file can say more about the same fields. Both files start with `package estate`, so pravo reads them as one specification. [A4](a04-order-does-not-matter.md) explains how. Write `team.cue`.

```cue file=team.cue status=valid
package estate

// The same value, said again.
buckets: logs: encrypted: true

// A bound that the value already meets.
buckets: logs: retention_days: >=90

// A new field in a struct that already exists.
buckets: logs: tags: cost_center: "4711"
```

> [!PITFALL]
> Start every file with `package estate`. A file without a package line, or with another one, stops the load, and the error names the file. [A4](a04-order-does-not-matter.md#break-it) shows the case.

Ask for the `logs` bucket.

```sh run
pravo eval -e buckets.logs
```

```text out
name:           "logs"
versioning:     true
encrypted:      true
retention_days: 365
tags: {
	cost_center: "4711"
	owner:       "platform"
}
```

Each pair of copies became one value.

- `encrypted` appears twice with the same value. The result is that value.
- `retention_days` is `365` in one file and `>=90` in the other. The value meets the bound, so the result is `365`.
- `tags` is a struct in one file and a struct in the other. The result is one struct with the fields of both.

In each case the result is the more specific copy, or the details of both. Nothing was replaced.

:::deep-dive The operator behind it
Writing a field twice is the same as joining its values with `&`. The value `int & 3` is `3`, in either order.

```sh run
pravo eval -e 'int & 3'
pravo eval -e '3 & int'
```

```text out
3
3
```

:::

## A copy that disagrees

Someone debugs an upload failure and adds one line to `team.cue`. Replace the file.

```cue file=team.cue status=conflict hl="4"
package estate

// Added while debugging uploads.
buckets: logs: encrypted: false
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.logs.encrypted: conflicting values false and true:
    ./estate.cue:7:19
    ./team.cue:4:27
```

In HCL, the same two statements give a plan that uses the caller's value. In CUE they give no plan until someone decides which line is wrong.

Read the error from left to right.

- **Stage.** `evaluating` says that both files are valid CUE and that their values do not unify.
- **Path.** `buckets.logs.encrypted` is the field in dispute.
- **Values.** `false and true` are the two values that cannot both hold. The message does not say which came from where. Open both locations.
- **Locations.** Each `file:line:column` points at one side. Line 7 of `estate.cue` holds `true`. Line 4 of `team.cue` holds `false`. The column is the start of the value.

pravo stops at evaluation and prints no data. It reports one conflict at a time: fix this one and run the command again to see the next. [Reading a CUE error](reading-a-cue-error.md) lists the messages you meet.

Every pair of copies falls into one of these rows.

| First copy | Second copy | Result |
|---|---|---|
| `true` | `true` | `true` |
| `365` | `>=90` | `365` |
| `int` | `30` | `30` |
| a struct with `owner` | a struct with `cost_center` | one struct with both fields |
| `true` | `false` | a conflict |

To let a caller change a value, mark the value as a default. That is [A6](a06-choices-and-defaults.md). A plain value is a rule.

## Predict

Write the four pairs below into a scratch directory. Each pair is one field written twice.

```cue file=predict/p.cue
package predict

a: 3
a: 3

b: int
b: 3

c: owner: "web"
c: stage: "prod"

d: 3
d: "3"
```

:::predict Which pair makes pravo stop?
Decide before you open the answer. Then run `pravo -C predict eval`, which runs pravo in the directory `predict`.

::reveal
Only `d`. The values `3` and `"3"` are different values of different types, so they conflict.

```sh run exit=6
pravo -C predict eval
```

```text out
Error: evaluating ./predict:
d: conflicting values 3 and "3" (mismatched types int and string):
    ./predict/p.cue:12:4
    ./predict/p.cue:13:4
```

pravo stops at the first conflict and prints no data. The other three pairs print only after `d` is gone. Remove it and run again.

```cue file=predict/p.cue
package predict

a: 3
a: 3

b: int
b: 3

c: owner: "web"
c: stage: "prod"
```

```sh run
pravo -C predict eval
```

```text out
a: 3
b: 3
c: {
	owner: "web"
	stage: "prod"
}
```

:::

## Exercise

:::exercise Clear the conflict, then merge two structs
`team.cue` still holds the conflicting line.

```cue file=team.cue status=conflict
package estate

// Added while debugging uploads.
buckets: logs: encrypted: false
```

Make two changes. First, clear the conflict by deleting the line that breaks the rule. Second, add two separate lines. Together they give `buckets.uploads.tags` the field `cost_center` with the value `"4711"` and the field `env` with the value `"prod"`.

```sh file=check.sh
want() { [ "$(pravo eval -e "$1" 2>/dev/null)" = "$2" ] || { echo "$1 should be $2"; exit 1; }; }
want buckets.logs.encrypted true
want buckets.uploads.tags.owner '"web"'
want buckets.uploads.tags.cost_center '"4711"'
want buckets.uploads.tags.env '"prod"'
echo ok
```

Run the check on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
buckets.logs.encrypted should be true
```

::hint
`estate.cue` states the rule: `logs` is encrypted. Delete the line in `team.cue` that breaks it. Then add one line for `cost_center` and one for `env` under `buckets: uploads: tags`.

::hint
Two structs for the same field merge into one. See [unification](../reference/glossary.md#unification).

::solution
```cue file=team.cue
package estate

buckets: uploads: tags: cost_center: "4711"

buckets: uploads: tags: env: "prod"
```

```sh run check
sh check.sh
```
:::

## Recap

- Writing a field twice adds a second constraint. The value must satisfy both, so the result is the more specific copy or the details of both.
- Two concrete values that differ are a conflict. pravo stops, prints the path, the values and both locations, and reports no data.
- HCL replaces an earlier value with a later one. CUE never replaces. A value that may change is a default, which A6 introduces.

:::check
What does pravo print when two files give a field different concrete values?
::answer
An error that names the path of the field, the two values and the file, line and column of each. It prints no data.

A field is `int` in one file and `30` in another. What is the result?
::answer
`30`. The value satisfies the constraint, so the more specific copy is the result.
:::

The next lesson shows that the order of lines and files does not matter.
