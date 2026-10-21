---
title: Reading a plan
description: A plan is a diff with a legend. Learn the glyphs, unknown values, the replacement marker and the Graph line, then predict three plans.
order: 32
group: Track B · pravo
minutes: 12
needs: B1
learn: How do you tell from a plan what changes, what is skipped, and why?
done_when: "You predict the `Plan:` line of three edits before you run them, and `score.sh` reports 3 of 3."
level: beginner
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: '\(hash [0-9a-f]+ -> [0-9a-f]+\)'
    replace: '(hash <old> -> <new>)'
---

## Warm-up

Two questions from [B1](b01-providers-and-resources.md). Decide, then open the answer.

:::predict `pravo validate` rejects a misspelt attribute. Does it start the provider to find out?
::reveal
No. It reads the definitions that `pravo init` wrote, and a provider process never starts.
:::

:::predict Which field of a struct tells pravo that the struct is a resource?
::reveal
`$kind`, with the value `"resource"`. `$type` and `$source` name the type and the provider.
:::

## The problem

You change one number in a file. Which resources does the plan touch? Here is the change in HCL.

```hcl title="main.tf" hl="3"
resource "random_integer" "port" {
  min  = 20000
  max  = 20999 # was 29999
  seed = "web"
}

resource "local_file" "web" {
  filename = "web.conf"
  content  = "port = ${random_integer.port.result}\n"
}
```

Does the plan replace `port` only, or `web` as well? This lesson gives you the rules to answer before the plan does.

## The idea

A plan is a diff with a legend. Each change has a glyph, an address and a reason. The reason is an attribute: one you edited, or one that reads a value that does not exist yet.

## Write the specification

Work in an empty directory. The specification is the one from [B1](b01-providers-and-resources.md), split into three files. Each edit in this lesson then touches one short file.

```cue file=main.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider
```

```cue file=port.cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
```

```cue file=web.cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

## Read a first plan

Nothing exists yet, so the plan creates both resources.

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # port (random_integer) will be created
  + port: {
  +     id:     (known after apply)
  +     max:    29999
  +     min:    20000
  +     result: (known after apply)
  +     seed:   "web"
    }

  # web (local_file) will be created
  + web: {
  +     content:              (known after apply)
...
  +     filename:             "web.conf"
  +     id:                   (known after apply)
    }

Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.
```

The `...` stands for the hash and permission lines of `web`, which are cut here. Read the plan from the top.

- `# port (random_integer) will be created` is the header. It holds the address, the type and the action.
- `+ port: {` repeats the address as a CUE path. A nested address such as `conf.api` prints as `conf: api: {`. [B5](b05-many-resources-from-one-rule.md) produces one.
- `min: 20000` is a value pravo knows now.
- `id: (known after apply)` is a value that only the provider can produce. Attributes with a null value are left out.
- `content` of `web` is unknown too. It reads `port.result`, and an unknown value makes everything computed from it unknown.
- `Plan:` counts the actions. `Graph:` says how much of the specification the plan reached.

The glyph opens each change, and the header above it says the same in words:

| Glyph | The header says | Meaning |
|---|---|---|
| `+` | will be created | The resource is new. |
| `~` | will be updated in-place | The provider changes the object and keeps it. |
| `-/+` | must be replaced | The provider destroys the object and creates a new one. |
| `-` | will be destroyed | The resource leaves the specification. |
| `<=` | will be read | A data source whose arguments are unknown until apply. |

Inside a change, `~` also marks each attribute that differs. `# forces replacement` marks the attribute that makes the replacement necessary. Both appear below.

## Apply, then plan again

[B3](b03-apply-outputs-state.md) covers apply. Here it gives the plan a state to compare with.

```sh run
pravo apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Plan again, and then again with `--refresh=false`, which reads no record at all. Keep the `Graph:` line:

```sh run
pravo plan | grep '^Graph'
pravo plan --refresh=false | grep '^Graph'
```

```text out
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
Graph: planned 0 of 2, 2 unchanged and skipped.
```

| Part of the line | Meaning |
|---|---|
| `planned 0 of 2` | The plan reached no resource out of the two in the specification. |
| `2 unchanged and skipped` | Nothing changed for these two, so no provider planned them. |
| `2 not read` | The plan did not reach them, so it did not read their real state from the providers either. |

A plan reads the resources it reaches, and the resources they read, before it compares, so it sees a change made outside pravo in those. `--refresh=false` skips every read. [B9](b09-drift.md) shows what a read finds, which resources a plan does not read and how a refresh age limits it.

## Change one number

Narrow the range in `port.cue`, as in the HCL above:

```cue file=port.cue hl="4"
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  20999
	seed: "web"
}
```

```sh run
pravo plan
```

```text out
port: Refreshing state... [id=26720]
web: Refreshing state... [id=d79ddeb22063b83d015e9cd3e32402a02f16849e]

pravo will perform the following actions:

  # port (random_integer) must be replaced
-/+ port: {
  ~     id:      "26720" -> (known after apply)
  ~     max:     29999 -> 20999 # forces replacement
        min:     20000
  ~     result:  26720 -> (known after apply)
        seed:    "web"
    }

  # web (local_file) must be replaced
-/+ web: {
  ~     content:              "port = 26720\n" -> (known after apply) # forces replacement
...
        directory_permission: "0777"
        file_permission:      "0777"
        filename:             "web.conf"
  ~     id:                   "d79ddeb22063b83d015e9cd3e32402a02f16849e" -> (known after apply)
    }

Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 2, 2 refreshed.
```

This answers the opening question: both. Follow the cause.

- `max: 29999 -> 20999 # forces replacement` is the cause. The provider cannot change `max` in place, so `port` is replaced.
- The replacement of `port` gives `result` a new value, which does not exist yet.
- `web` reads `port.result`. Its `content` becomes `(known after apply)`, and the provider cannot change `content` in place either.
- A replacement counts once under add and once under destroy, so two replacements make `2 to add` and `2 to destroy`.

`pravo why` gives the same account in words, for any address:

```sh run
pravo why port web
```

```text out
port: replace
  - the specification changed since the last apply (hash <old> -> <new>)
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change max in place; replacement required
  reaches: web
web: replace
  - inputs depend on values known only after apply: port
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change content in place; replacement required
```

`reaches: web` lists the resources that the change spreads to. For `web` the reason is `inputs depend on values known only after apply: port`. Apply the change:

```sh run
pravo apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
```

## Break it: a rename

The next file is valid and is not what you mean. You rename `web` to `site` and change nothing else.

```cue file=web.cue status=unintended hl="3"
package estate

site: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

```sh run
pravo plan
```

```text out
port: Refreshing state... [id=20720]
web: Refreshing state... [id=6089a5ab722262bb4cbcfb8dcc46d4e517ea8fc7]

pravo will perform the following actions:

  # site (local_file) will be created
  + site: {
  +     content:              "port = 20720\n"
...
  +     directory_permission: "0777"
  +     file_permission:      "0777"
  +     filename:             "web.conf"
  +     id:                   (known after apply)
    }

  # web (local_file) will be destroyed
  - web: {
  -     content:              "port = 20720\n"
...
  -     directory_permission: "0777"
  -     file_permission:      "0777"
  -     filename:             "web.conf"
  -     id:                   "6089a5ab722262bb4cbcfb8dcc46d4e517ea8fc7"
    }

Plan: 1 to add, 0 to change, 1 to destroy.
Hint: site looks like web under a new address; to keep the existing object, add $from: "web" to site
Graph: planned 1 of 2, 1 unchanged and skipped, 1 removed from the specification, 2 refreshed.
```

The plan creates one resource and destroys another. The address is the identity of a resource. `site` is a new address, so pravo creates it. `web` has left the file, so pravo destroys it. `-` marks the destroyed one, and every attribute of it is printed with `-`.

The `Hint:` line sees that `site` and `web` look alike. It names the field that says they are one object: `$from`. [B8](b08-renames-moves-imports.md) covers it. Do not apply this plan. Restore the name:

```cue file=web.cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

## Predict three plans

Each edit starts from the files as they stand now. Before you open an answer, decide which resources appear in the plan and what the `Plan:` line says.

This filter keeps the lines that matter: the headers, the attribute that forces a replacement, and the summary.

```sh title="digest"
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
```

:::predict Edit A: you change filename in web.cue to "site.conf". Which resources does the plan replace?
`web.cue` has `filename: "web.conf"`. You write `filename: "site.conf"` and run the plan.

::reveal
```cue file=web.cue hl="4"
package estate

web: $local.#Resource.local_file & {
	filename: "site.conf"
	content:  "port = \(port.result)\n"
}
```

```sh run
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
```

```text out
  # web (local_file) must be replaced
  ~     filename:             "web.conf" -> "site.conf" # forces replacement
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 of 2, 1 unchanged and skipped, 2 refreshed.
```

Only `web`. The provider cannot change `filename` in place. Nothing that `port` reads has changed, so `port` is not reached: `planned 1 of 2`.

Write the original file back before the next edit.

```cue file=web.cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```
:::

:::predict Edit B: you write max as 20000 + 999 in port.cue. What does the plan say?
The sum is 20999, the value that `max` has now.

::reveal
```cue file=port.cue hl="5"
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  20000 + 999
	seed: "web"
}
```

```sh run
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
```

The plan compares evaluated values, not text. An expression that evaluates to the same value is not a change.

Write the original file back before the next edit.

```cue file=port.cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  20999
	seed: "web"
}
```
:::

:::predict Edit C: you delete web.cue. What does the plan say?
`port.cue` and `main.cue` stay as they are.

::reveal
```sh run
rm web.cue
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
```

```text out
  # web (local_file) will be destroyed
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 of 1, 1 unchanged and skipped, 1 removed from the specification, 1 refreshed, 1 not read.
```

A resource that leaves the specification is destroyed. Nothing else is touched. The `Graph:` line counts one resource in the specification now, `port`, and it was skipped and not read. The state still holds the record of `web`. The plan read it to plan the destroy, so the line counts it as `removed from the specification` and as `refreshed`.

Write the file back.

```cue file=web.cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```
:::

## Exercise

:::exercise Predict, run, score
Three more edits follow. For each one, predict the three numbers of the `Plan:` line: resources to add, to change and to destroy.

1. In `port.cue`, change `min` from `20000` to `20500`.
2. In `web.cue`, change the text `port = ` in `content` to `listen = `.
3. Add a file `admin.cue` with a third `local_file` named `admin`.

Write your predictions in `predictions.txt`, one line per edit, as `add change destroy`. Then run the script below. It makes each edit in a copy of the directory, plans the copy and compares the result with your line. Your own files do not change.

```sh file=score.sh
# Usage: sh score.sh predictions.txt
edit1() { sed 's/min:  20000/min:  20500/' port.cue >"$1/port.cue"; }
edit2() { sed 's/port = /listen = /' web.cue >"$1/web.cue"; }
edit3() { printf 'package estate\n\nadmin: $local.#Resource.local_file & {filename: "admin.conf", content: "x\\n"}\n' >"$1/admin.cue"; }

score=0
for n in 1 2 3; do
	tmp=$(mktemp -d)
	cp -R . "$tmp"
	edit$n "$tmp"
	actual=$(pravo -C "$tmp" plan | sed -n 's/^Plan: \([0-9]*\) to add, \([0-9]*\) to change, \([0-9]*\) to destroy\./\1 \2 \3/p')
	rm -rf "$tmp"
	said=$(sed -n "${n}p" predictions.txt)
	if [ "$said" = "$actual" ]; then
		score=$((score + 1))
		echo "edit $n: correct, $actual"
	else
		echo "edit $n: you said $said; the plan says $actual"
	fi
done
echo "score: $score of 3"
[ "$score" -eq 3 ]
```

::hint
A replacement counts once under add and once under destroy. For each edit, ask which attribute changes, and whether another resource reads it.

::hint
Edit 2 changes a resource that nothing reads. Edit 3 adds a resource and leaves the others alone. [Change one number](#change-one-number) shows the cascade.

::solution
```sh run check
printf '2 0 2\n1 0 1\n1 0 0\n' > predictions.txt
sh score.sh predictions.txt
```
:::

:::deep-dive Two glyphs without an example on the path
The resources in this track are replaced when a configuration attribute changes, and none of them reads existing data. Two glyphs of the table therefore do not appear above. This runs in a second root, `glyphs`, with its own state.

`~` marks an in-place update. `tls_self_signed_cert` changes `early_renewal_hours` without replacement:

```cue file=glyphs/main.cue
package glyphs

import tls "pravo.dev/providers/hashicorp/tls"

$tls: tls.#Provider

key: $tls.#Resource.tls_private_key & {algorithm: "ED25519"}

cert: $tls.#Resource.tls_self_signed_cert & {
	private_key_pem: key.private_key_pem
	allowed_uses: ["server_auth"]
	validity_period_hours: 24
	early_renewal_hours:   1
	subject: [{common_name: "web.example"}]
}
```

```sh run
pravo -C glyphs init | tail -n 1
pravo -C glyphs apply --auto-approve | tail -n 1
sed 's/early_renewal_hours:   1/early_renewal_hours:   2/' glyphs/main.cue > glyphs/next.cue
mv glyphs/next.cue glyphs/main.cue
pravo -C glyphs plan | grep -E '^(  #|  ~|Plan)'
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
  # cert (tls_self_signed_cert) will be updated in-place
  ~ cert: {
  ~     early_renewal_hours:        1 -> 2
Plan: 0 to add, 1 to change, 0 to destroy.
```

The header says `will be updated in-place`, the glyph is `~`, and the object keeps its identity. The summary counts one change.

`<=` marks a data source. A data source reads an object that exists. When its arguments are unknown until apply, the plan cannot read it yet and defers the read:

```cue file=glyphs/read.cue
package glyphs

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

slot: $random.#Resource.random_integer & {min: 1, max: 9, seed: "x"}

lookup: $local.#Data.local_file & {filename: "conf-\(slot.result).txt"}
```

```sh run
pravo -C glyphs plan | grep -E 'will be read| <= '
```

```text out
  # lookup (local_file) will be read (configuration depends on values known after apply)
 <= lookup: (known after apply)
```

The file name of `lookup` reads `slot.result`, which does not exist yet. The plan defers the read, and prints `<=` and `(known after apply)`.
:::

## Recap

- A plan has one change per resource: a glyph, an address and an attribute as the reason.
- `(known after apply)` spreads along references, so one replacement can replace the resources that read it.
- `Graph:` counts what the plan reached, skipped, read and did not read, and `pravo why` states the reasons in words.

:::check
What does `# forces replacement` mark?
::answer
The attribute that the provider cannot change in place. Changing it replaces the resource.

Why does a replacement of `port` also replace `web`?
::answer
`web.content` reads `port.result`. The new result does not exist yet, so `content` is unknown, and `content` cannot change in place.
:::

Reference: [Plans](../concepts/plans.md), [`pravo plan`](../reference/plan.md) and [`pravo why`](../reference/why-and-drift.md).

[B3](b03-apply-outputs-state.md) applies a specification, reads its outputs and opens the state.
