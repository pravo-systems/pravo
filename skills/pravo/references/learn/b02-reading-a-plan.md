<!-- Generated from docs/learn/b02-reading-a-plan.md by tools/skillgen. Do not edit. -->

# Reading a plan

A plan is a diff with a legend. Learn the glyphs, unknown values, the replacement marker and the Graph line, then predict three plans.

## Warm-up

Two questions from [B1](b01-providers-and-resources.md). Decide, then open the answer.

## The problem

You change one number in a file. Which resources does the plan touch? Here is the change in HCL.

```hcl
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

File `main.cue`:

```cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider
```

File `port.cue`:

```cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
```

File `web.cue`:

```cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

## Read a first plan

Nothing exists yet, so the plan creates both resources.

```sh
pravo plan
```

Output:

```text
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

```sh
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Plan again, and then again with `--refresh=false`, which reads no record at all. Keep the `Graph:` line:

```sh
pravo plan | grep '^Graph'
pravo plan --refresh=false | grep '^Graph'
```

Output:

```text
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

File `port.cue`:

```cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  20999
	seed: "web"
}
```

```sh
pravo plan
```

Output:

```text
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

```sh
pravo why port web
```

Output:

```text
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

```sh
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
```

## Break it: a rename

The next file is valid and is not what you mean. You rename `web` to `site` and change nothing else.

File `web.cue`:

```cue
package estate

site: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

```sh
pravo plan
```

Output:

```text
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

File `web.cue`:

```cue
package estate

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

## Predict three plans

Each edit starts from the files as they stand now. Before you open an answer, decide which resources appear in the plan and what the `Plan:` line says.

This filter keeps the lines that matter: the headers, the attribute that forces a replacement, and the summary.

```sh
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
```

## Exercise

**Two glyphs without an example on the path**

The resources in this track are replaced when a configuration attribute changes, and none of them reads existing data. Two glyphs of the table therefore do not appear above. This runs in a second root, `glyphs`, with its own state.

`~` marks an in-place update. `tls_self_signed_cert` changes `early_renewal_hours` without replacement:

File `glyphs/main.cue`:

```cue
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

```sh
pravo -C glyphs init | tail -n 1
pravo -C glyphs apply --auto-approve | tail -n 1
sed 's/early_renewal_hours:   1/early_renewal_hours:   2/' glyphs/main.cue > glyphs/next.cue
mv glyphs/next.cue glyphs/main.cue
pravo -C glyphs plan | grep -E '^(  #|  ~|Plan)'
```

Output:

```text
pravo has been initialised. Run "pravo plan" to see what it would do.
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
  # cert (tls_self_signed_cert) will be updated in-place
  ~ cert: {
  ~     early_renewal_hours:        1 -> 2
Plan: 0 to add, 1 to change, 0 to destroy.
```

The header says `will be updated in-place`, the glyph is `~`, and the object keeps its identity. The summary counts one change.

`<=` marks a data source. A data source reads an object that exists. When its arguments are unknown until apply, the plan cannot read it yet and defers the read:

File `glyphs/read.cue`:

```cue
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

```sh
pravo -C glyphs plan | grep -E 'will be read| <= '
```

Output:

```text
  # lookup (local_file) will be read (configuration depends on values known after apply)
 <= lookup: (known after apply)
```

The file name of `lookup` reads `slot.result`, which does not exist yet. The plan defers the read, and prints `<=` and `(known after apply)`.

## Recap

- A plan has one change per resource: a glyph, an address and an attribute as the reason.
- `(known after apply)` spreads along references, so one replacement can replace the resources that read it.
- `Graph:` counts what the plan reached, skipped, read and did not read, and `pravo why` states the reasons in words.

Reference: [Plans](../concepts/plans.md), [`pravo plan`](../reference/plan.md) and [`pravo why`](../reference/why-and-drift.md).

[B3](b03-apply-outputs-state.md) applies a specification, reads its outputs and opens the state.
