---
title: Apply, outputs, state
description: Apply a plan, read the outputs and open the state. A plan with no changes shows that a refactor changed nothing.
order: 33
group: Track B · pravo
minutes: 10
needs: B2
learn: What does apply record, and how does a plan prove that a refactor changed nothing?
done_when: "`pravo plan` reports `No changes.` after you replace a literal with a field whose default equals it."
level: beginner
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<id>'
  - pattern: 'after [0-9.]+(ms|s|m)\b'
    replace: 'after 0s'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} [0-9.]+m?s'
    replace: '<time>'
  - pattern: 'v[0-9]+\.[0-9]+\.[0-9]+ as '
    replace: 'v<version> as '
  - pattern: 'refreshed [0-9a-z]+ ago, applied [0-9a-z]+ ago, config hash [0-9a-f]+'
    replace: 'refreshed <age> ago, applied <age> ago, config hash <hash>'
---

## Warm-up

Two questions from [B2](b02-reading-a-plan.md). Decide, then open the answer.

:::predict A plan shows `-/+ web: {`. What does the glyph say?
::reveal
The provider destroys the object and creates a new one. The plan counts it once under add and once under destroy.
:::

:::predict `web.content` reads `port.result`. Why does the plan print `(known after apply)` for it?
::reveal
The provider produces `result` when it creates `port`. An unknown value makes everything that reads it unknown.
:::

## The problem

You replace a literal with a variable, rename a local, split a file. You mean to change the text of the specification and not the infrastructure. How do you know that you did not?

In HCL, a literal becomes a variable that defaults to the same value. If the default is one digit off, nothing in the file complains. The next plan shows a replacement, and only a person who reads it stops it.

## The idea

In CUE, the literal becomes a field with a default. If the default equals the literal, the value is the same and the plan has nothing to do. If it does not, the plan shows the difference before anything runs.

:::compare
```hcl title="main.tf" hl="1-3,7"
variable "limit" {
  default = 29999
}

resource "random_integer" "port" {
  min  = 20000
  max  = var.limit # was 29999
  seed = "web"
}
```

```cue title="CUE" hl="1,5"
limit: *29999 | int

port: $random.#Resource.random_integer & {
	min:  20000
	max:  limit // was 29999
	seed: "web"
}
```
:::

This lesson applies a specification first. Then it does the refactor, makes one mistake in it and reads what stops the mistake.

## Apply the specification

Work in an empty directory. The specification is the one from [B2](b02-reading-a-plan.md), with two outputs. An output is a struct built from `pravo.#Output`. It holds a value that `pravo output` prints after apply.

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

```cue file=outputs.cue
package estate

import "pravo.dev/pravo"

web_port: pravo.#Output & {value: port.result}
web_file: pravo.#Output & {value: web.filename}
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

`pravo apply` makes the plan, prints it and asks for confirmation. Only `yes` is accepted. In a terminal you type `yes`. Here a pipe types it.

```sh run
echo yes | pravo apply
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

Outputs:
  web_file: "web.conf"
  web_port: (known after apply)

Do you want to perform these actions?
  Only 'yes' will be accepted to approve.

  Enter a value:
port: Creating...
port: Creation complete after 0s [id=26720]
web: Creating...
web: Creation complete after 0s [id=d79ddeb22063b83d015e9cd3e32402a02f16849e]

Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

Outputs:
  web_file: "web.conf"
  web_port: 26720
```

Three things happened, in this order.

1. pravo printed the plan. The output `web_port` was `(known after apply)`, because `port.result` did not exist yet.
2. After the confirmation, pravo created `port` and then `web`. It created `port` first because `web` reads it.
3. pravo printed the outputs again. `web_port` now has a value.

The result is a file:

```sh run
cat web.conf
```

```text out
port = 26720
```

`--auto-approve` skips the prompt. A reviewed plan is a better input for a script, and [B7](b07-changing-live-infrastructure.md) covers saved plans.

## Read the outputs

```sh run
pravo output
pravo output web_port
pravo output --json
```

```text out
web_file: "web.conf"
web_port: 26720
26720
{
  "web_file": "web.conf",
  "web_port": 26720
}
```

`pravo output` reads the values that the last apply recorded. It starts no provider. A name prints one value as CUE, so a string keeps its quotes. `--json` prints JSON, which is the form for a script.

## Open the state

State is what apply leaves behind. List the addresses in it:

```sh run
pravo state list
```

```text out
port
web
```

Each address is the path of a resource. Show one record:

```sh run
pravo state show web | head -n 3
```

```text out
// web (provider registry.opentofu.org/hashicorp/local v<version> as local, schema version 0, revision 1)
// refreshed <age> ago, applied <age> ago, config hash <hash>
// depends on port
```

The record holds the attributes that the provider reported and a revision. It also holds a hash of the configuration that the provider last saw. The comment line `depends on port` is the edge. `pravo state graph` prints all edges, from a resource to the ones it reads:

```sh run
pravo state graph
```

```text out
web -> port
```

State is a directory with one file per resource and one per transaction:

```sh run
find pravo.state -type f | sort
```

```text out
pravo.state/meta.json
pravo.state/outputs.json
pravo.state/resources/port.json
pravo.state/resources/web.json
pravo.state/txn/<id>.json
```

The files are plain JSON. They hold every attribute that a provider reported, including the ones it marks sensitive. Whoever can read the directory can read them. Keep `pravo.state` out of version control, or move it to a bucket with [Keep the state in a shared bucket](../how-to/remote-state.md). Whoever can read the bucket can read them too. Sealing a state in a bucket encrypts the attributes to a list of readers. A copy of the bucket then shows no value. [What sealing protects](../concepts/sealed-state.md) says what it covers. [Lab 3](lab3-the-leaked-bucket.md) comes after B7 and copies a bucket before and after sealing. [Seal the state to a list of readers](../how-to/seal-the-state.md) does it step by step.

`pravo state log` lists the applies and plans, newest first:

```sh run
pravo state log
```

```text out
<id>  apply    applied   <time>
    pravo apply
    2 added, 0 changed, 0 destroyed
    create   port  r0 -> r1
    create   web  r0 -> r1
```

The hash is how the next plan knows that `web` is unchanged. pravo evaluates the specification and hashes the configuration for the provider. It compares the hash with the record. This is the reason for `planned 0 of 2` in B2.

## Refactor with a plan of zero changes

Replace the literal `29999` with a field. Put the field in its own file, so that the next step edits three lines.

```cue file=limits.cue
package estate

limit: *29999 | int
```

```cue file=port.cue hl="5"
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  limit
	seed: "web"
}
```

Plan the refactor. The default equals the literal, so the plan has nothing to do:

```sh run
pravo plan | grep -E '^(No changes|Plan|Graph)'
pravo plan --detailed-exitcode >/dev/null
echo "exit status $?"
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
exit status 0
```

`--detailed-exitcode` makes the exit status say what the plan found: 0 for no changes and 2 for changes. A script uses it as the check. The plan shows that the refactor changed nothing.

## Break it: a default that is one digit off

The next file is valid CUE and is not the refactor you meant. The default is `29990`.

```cue file=limits.cue status=unintended hl="3"
package estate

limit: *29990 | int
```

```sh run
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
pravo plan --detailed-exitcode >/dev/null
echo "exit status $?"
```

```text out
  # port (random_integer) must be replaced
  ~     max:     29999 -> 29990 # forces replacement
  # web (local_file) must be replaced
  ~     content:              "port = 26720\n" -> (known after apply) # forces replacement
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 2, 2 refreshed.
exit status 2
```

pravo accepts the file, because nothing in it is wrong. The plan shows the cost: `max` changed from `29999` to `29990`, which forces the replacement of `port`, and `web` follows. The exit status is 2.

> [!WARNING]
> Do not apply a refactor whose plan has changes you did not intend. Fix the default and plan again.

Restore the correct default:

```cue file=limits.cue
package estate

limit: *29999 | int
```

```sh run
pravo plan | grep -E '^(No changes|Plan|Graph)'
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
```

## Exercise

:::exercise Refactor the file name
Replace the literal `"web.conf"` in `web.cue` with a name built from a field. Add the field `service` with the default `"web"`, and write the file name as `"\(service).conf"`. The plan must report no changes.

When you are done, this must print `ok`:

```sh title="check"
pravo plan --detailed-exitcode >/dev/null && pravo eval -e service | grep -q '"web"' && echo ok
```

::hint
The default must equal the part of the literal that you replace. The file name is `web.conf`, so `service` defaults to `"web"`.

::hint
A string with a value inside it uses `\(service)`. [A12](a12-conditions-and-strings.md) covers it.

::solution
```cue file=service.cue
package estate

service: *"web" | string
```

```cue file=web.cue hl="4"
package estate

web: $local.#Resource.local_file & {
	filename: "\(service).conf"
	content:  "port = \(port.result)\n"
}
```

```sh run check
pravo plan --detailed-exitcode >/dev/null
pravo eval -e service | grep -q '"web"'
```
:::

## Clean up

`pravo destroy` removes every resource in the state, in reverse order of dependency.

```sh run
pravo destroy --auto-approve | tail -n 8
test -e web.conf || echo "web.conf is gone"
```

```text out
Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state, 2 refreshed.
web: Destroying... [id=d79ddeb22063b83d015e9cd3e32402a02f16849e]
web: Destruction complete after 0s
port: Destroying... [id=26720]
port: Destruction complete after 0s

Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
web.conf is gone
```

## Recap

- `apply` plans, asks, creates in dependency order and records one node per resource.
- `output` and `state` read what apply recorded. They start no provider.
- A refactor changes nothing when its plan reports `No changes.`, and `--detailed-exitcode` turns that into an exit status of 0.

:::check
Why is `web_port` unknown in the plan and known after the apply?
::answer
It reads `port.result`, which the provider produces when it creates `port`.

What does the hash in a state record let pravo skip?
::answer
The planning of a resource whose configuration has not changed since the last apply.
:::

Reference: [State](../concepts/state.md), [`pravo apply`](../reference/apply.md), [`pravo output`](../reference/output.md), [`pravo state`](../reference/state.md) and [Outputs](../reference/spec-outputs.md).

[B4](b04-inputs-tags-environments.md) turns a field with a default into an input and runs one specification in several environments.
