<!-- Generated from docs/learn/b03-apply-outputs-state.md by tools/skillgen. Do not edit. -->

# Apply, outputs, state

Apply a plan, read the outputs and open the state. A plan with no changes shows that a refactor changed nothing.

## Warm-up

Two questions from [B2](b02-reading-a-plan.md). Decide, then open the answer.

## The problem

You replace a literal with a variable, rename a local, split a file. You mean to change the text of the specification and not the infrastructure. How do you know that you did not?

In HCL, a literal becomes a variable that defaults to the same value. If the default is one digit off, nothing in the file complains. The next plan shows a replacement, and only a person who reads it stops it.

## The idea

In CUE, the literal becomes a field with a default. If the default equals the literal, the value is the same and the plan has nothing to do. If it does not, the plan shows the difference before anything runs.

```hcl
variable "limit" {
  default = 29999
}

resource "random_integer" "port" {
  min  = 20000
  max  = var.limit # was 29999
  seed = "web"
}
```

```cue
limit: *29999 | int

port: $random.#Resource.random_integer & {
	min:  20000
	max:  limit // was 29999
	seed: "web"
}
```

This lesson applies a specification first. Then it does the refactor, makes one mistake in it and reads what stops the mistake.

## Apply the specification

Work in an empty directory. The specification is the one from [B2](b02-reading-a-plan.md), with two outputs. An output is a struct built from `pravo.#Output`. It holds a value that `pravo output` prints after apply.

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

File `outputs.cue`:

```cue
package estate

import "pravo.dev/pravo"

web_port: pravo.#Output & {value: port.result}
web_file: pravo.#Output & {value: web.filename}
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

`pravo apply` makes the plan, prints it and asks for confirmation. Only `yes` is accepted. In a terminal you type `yes`. Here a pipe types it.

```sh
echo yes | pravo apply
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

```sh
cat web.conf
```

Output:

```text
port = 26720
```

`--auto-approve` skips the prompt. A reviewed plan is a better input for a script, and [B7](b07-changing-live-infrastructure.md) covers saved plans.

## Read the outputs

```sh
pravo output
pravo output web_port
pravo output --json
```

Output:

```text
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

```sh
pravo state list
```

Output:

```text
port
web
```

Each address is the path of a resource. Show one record:

```sh
pravo state show web | head -n 3
```

Output:

```text
// web (provider registry.opentofu.org/hashicorp/local v<version> as local, schema version 0, revision 1)
// refreshed <age> ago, applied <age> ago, config hash <hash>
// depends on port
```

The record holds the attributes that the provider reported and a revision. It also holds a hash of the configuration that the provider last saw. The comment line `depends on port` is the edge. `pravo state graph` prints all edges, from a resource to the ones it reads:

```sh
pravo state graph
```

Output:

```text
web -> port
```

State is a directory with one file per resource and one per transaction:

```sh
find pravo.state -type f | sort
```

Output:

```text
pravo.state/meta.json
pravo.state/outputs.json
pravo.state/resources/port.json
pravo.state/resources/web.json
pravo.state/txn/<id>.json
```

The files are plain JSON. They hold every attribute that a provider reported, including the ones it marks sensitive. Keep `pravo.state` out of version control, or move it to a bucket with [Keep the state in a shared bucket](../how-to/remote-state.md).

`pravo state log` lists the applies and plans, newest first:

```sh
pravo state log
```

Output:

```text
<id>  apply    applied   <time>
    pravo apply
    2 added, 0 changed, 0 destroyed
    create   port  r0 -> r1
    create   web  r0 -> r1
```

The hash is how the next plan knows that `web` is unchanged. pravo evaluates the specification and hashes the configuration for the provider. It compares the hash with the record. This is the reason for `planned 0 of 2` in B2.

## Refactor with a plan of zero changes

Replace the literal `29999` with a field. Put the field in its own file, so that the next step edits three lines.

File `limits.cue`:

```cue
package estate

limit: *29999 | int
```

File `port.cue`:

```cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  limit
	seed: "web"
}
```

Plan the refactor. The default equals the literal, so the plan has nothing to do:

```sh
pravo plan | grep -E '^(No changes|Plan|Graph)'
pravo plan --detailed-exitcode >/dev/null
echo "exit status $?"
```

Output:

```text
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
exit status 0
```

`--detailed-exitcode` makes the exit status say what the plan found: 0 for no changes and 2 for changes. A script uses it as the check. The plan shows that the refactor changed nothing.

## Break it: a default that is one digit off

The next file is valid CUE and is not the refactor you meant. The default is `29990`.

File `limits.cue`:

```cue
package estate

limit: *29990 | int
```

```sh
pravo plan | grep -E '^(  #|Plan|Hint|Graph|No changes)|forces replacement'
pravo plan --detailed-exitcode >/dev/null
echo "exit status $?"
```

Output:

```text
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

File `limits.cue`:

```cue
package estate

limit: *29999 | int
```

```sh
pravo plan | grep -E '^(No changes|Plan|Graph)'
```

Output:

```text
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.
```

## Exercise

## Clean up

`pravo destroy` removes every resource in the state, in reverse order of dependency.

```sh
pravo destroy --auto-approve | tail -n 8
test -e web.conf || echo "web.conf is gone"
```

Output:

```text
Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state, 2 refreshed.
web: Destroying... [id=d79ddeb22063b83d015e9cd3e32402a02f16849e]
web: Destruction complete after 0s
port: Destroying... [id=26720]
port: Destruction complete after 0s

Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
web.conf is gone
```

## Who can read the state

The files of the state hold every attribute that a provider reported, secrets included. Whoever can read the directory or the bucket can read them. Sealing encrypts the attributes to a list of readers, so that a copy of the bucket shows addresses and types and no value. [What sealing protects](../concepts/sealed-state.md) says what it covers. [Seal the state to a list of readers](../how-to/seal-the-state.md) does it step by step. [Lab 3](https://pravo.systems/learn/lab3-the-leaked-bucket.html) copies a bucket before and after.

## Recap

- `apply` plans, asks, creates in dependency order and records one node per resource.
- `output` and `state` read what apply recorded. They start no provider.
- A refactor changes nothing when its plan reports `No changes.`, and `--detailed-exitcode` turns that into an exit status of 0.

Reference: [State](../concepts/state.md), [`pravo apply`](../reference/apply.md), [`pravo output`](../reference/output.md), [`pravo state`](../reference/state.md) and [Outputs](../reference/spec-outputs.md).

[B4](b04-inputs-tags-environments.md) turns a field with a default into an input and runs one specification in several environments.
