---
title: Many resources from one rule
description: A comprehension produces resources. Each key becomes the last element of an address, so reordering the source replaces nothing.
order: 35
group: Track B · pravo
minutes: 10
needs: B4, A10
learn: How does one rule produce many resources, and what keeps their addresses stable?
done_when: "`pravo plan` reports `No changes.` after you reorder the services, and `pravo state list` shows an address per key."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
keywords: "for_each count comprehension many resources loop"
---

## Warm-up

Two questions from earlier lessons. Decide, then open the answer.

:::predict You save a plan with `-t env=prod`. Can you apply it with `-t env=dev`?
::reveal
No. A saved plan carries its own tags, and pravo refuses `-t` with a plan file ([B4](b04-inputs-tags-environments.md)).
:::

:::predict The rule `for name, svc in services { (name): ... }` writes one field per entry. What does the label of each field hold?
::reveal
The key of the entry in `services`. The parentheses make the label an expression ([A10](a10-comprehensions.md)).
:::

## The problem

Three services need a configuration file each, and a port to put in it. You can write six resources, or you can write one rule and a list of services.

In HCL, `count` repeats a block, and each copy is named by its position.

```hcl title="main.tf"
variable "services" {
  default = ["api", "web", "worker"]
}

resource "local_file" "conf" {
  count    = length(var.services)
  filename = "${var.services[count.index]}.conf"
  content  = "name = ${var.services[count.index]}\n"
}
```

The copies are `local_file.conf[0]`, `local_file.conf[1]` and `local_file.conf[2]`. Put `"worker"` first in the list and each position holds a different service. The addresses stay the same, and the services behind them change. `for_each` over a set names the copies by key and avoids this.

## The idea

A comprehension in CUE builds the fields of a struct from another struct. The key of each source field becomes the name of the new field. The address of a resource is its path, so the key is the last element of the address. The order of the source is not part of the value. A struct has keys and no positions, so the stable form is the one you get by default. The unstable form needs a list, and the lesson shows what it costs.

## Write the rule

Work in an empty directory. Three files. `services.cue` holds the data. `rules.cue` holds the rule. `main.cue` holds the providers.

```cue file=main.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider
```

```cue file=services.cue
package estate

services: {
	api: {workers: 2}
	web: {workers: 1}
	worker: {workers: 4}
}
```

```cue file=rules.cue ln
package estate

port: {
	for name, _ in services {
		(name): $random.#Resource.random_integer & {
			min:  20000
			max:  29999
			seed: name
		}
	}
}

conf: {
	for name, svc in services {
		(name): $local.#Resource.local_file & {
			filename: "\(name).conf"
			content:  "port = \(port[name].result)\nworkers = \(svc.workers)\n"
		}
	}
}
```

Read the rule.

- `for name, svc in services` visits each field of `services`. `name` is the key and `svc` is the value. [A10](a10-comprehensions.md) covers the syntax.
- `(name): ...` writes a field whose label is the value of `name`. The parentheses mark the label as an expression.
- Each new field holds a resource built through a provider, as in [B1](b01-providers-and-resources.md).
- `port[name]` selects the port with the same key. That reference is the dependency of each file on its port.

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

## Read the addresses

```sh run
pravo validate
pravo plan | grep '^  #'
```

```text out
Success! 6 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
  # port.api (random_integer) will be created
  # port.web (random_integer) will be created
  # port.worker (random_integer) will be created
  # conf.api (local_file) will be created
  # conf.web (local_file) will be created
  # conf.worker (local_file) will be created
```

Three services and two kinds of resource make six resources. Each address is the name of the field that holds the rule, then the key:

| Key | Ports | Files |
|---|---|---|
| `api` | `port.api` | `conf.api` |
| `web` | `port.web` | `conf.web` |
| `worker` | `port.worker` | `conf.worker` |

The key reaches the resource in CUE as well:

```sh run
pravo eval -e conf.api.filename
```

```text out
"api.conf"
```

Apply the six resources and list what the state holds:

```sh run
pravo apply --auto-approve | tail -n 1
pravo state list
pravo state graph
```

```text out
Apply complete! Resources: 6 added, 0 changed, 0 destroyed. [txn <id>]
conf.api
conf.web
conf.worker
port.api
port.web
port.worker
conf.api -> port.api
conf.web -> port.web
conf.worker -> port.worker
```

The graph has one edge per key. `conf.api` reads `port.api` and no other port.

## Reorder the keys

:::predict You write the services in another order in services.cue. How many resources does the plan replace?
Put `worker` first and `api` second. Nothing else changes.

::reveal
None. The address of each resource is built from its key, and the keys have not changed. The order of the lines is not part of the value that pravo plans.
:::

Check the answer. This file holds the same three services in a new order:

```cue file=services.cue hl="4-6"
package estate

services: {
	worker: {workers: 4}
	api: {workers: 2}
	web: {workers: 1}
}
```

```sh run
pravo plan | grep -E '^(No changes|Plan|Graph)'
```

```text out
No changes. The specification matches the state. 6 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 6 · not read 6
```

The plan reports `No changes.` and reaches no resource. Sort the services, group them or add a comment between them. The infrastructure does not move.

## Break it: a list instead of a struct

A comprehension over a list builds a list. The address of an element is its index, written `conf[0]`. The next two files form a second root in the directory `list`. It has its own state and stays apart from the first.

```cue file=list/names.cue
package list

names: ["api", "web", "worker"]
```

```cue file=list/main.cue
package list

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

conf: [for name in names {
	$local.#Resource.local_file & {
		filename: "list-\(name).conf"
		content:  "name = \(name)\n"
	}
}]
```

```sh run
pravo -C list apply --auto-approve | tail -n 1
pravo -C list state list
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
conf[0]
conf[1]
conf[2]
```

The addresses are `conf[0]`, `conf[1]` and `conf[2]`. Now reorder the list. The file below is valid and is not what you meant:

```cue file=list/names.cue status=unintended hl="3"
package list

names: ["worker", "api", "web"]
```

```sh run
pravo -C list plan | grep -E '^(  #|Plan|Graph|No changes)'
```

```text out
  # conf[0] (local_file) must be replaced
  # conf[1] (local_file) must be replaced
  # conf[2] (local_file) must be replaced
Plan: 3 to add, 0 to change, 3 to destroy.
Graph: planned 3 · refreshed 3
```

Each address now holds a different service. The file names differ, so every file is replaced. The services did not change, and the plan replaces three files.

> [!PITFALL]
> An index is an address. Generate resources from a struct whose keys are stable names. Use a list only when the position is the identity.

## Exercise

:::exercise Change the services
Add the service `cache` with two workers, and remove `worker`. Predict the `Plan:` line first. Then apply, and check that `api` and `web` were not touched.

When you are done, this must print `ok`:

```sh title="check"
pravo plan --detailed-exitcode >/dev/null && pravo state list | grep -qx conf.cache && ! pravo state list | grep -q worker && echo ok
```

::hint
A new key adds two resources, one port and one file. A removed key destroys two.

::hint
Read `Graph:` in the plan. It reaches the two new resources and skips the four that did not change. [A10](a10-comprehensions.md) explains why the keys stay stable.

::solution
```cue file=services.cue
package estate

services: {
	api: {workers: 2}
	web: {workers: 1}
	cache: {workers: 2}
}
```

```sh run
pravo plan | grep -E '^(  #|Plan|Graph)'
```

```text out
  # port.cache (random_integer) will be created
  # conf.cache (local_file) will be created
  # conf.worker (local_file) will be destroyed
  # port.worker (random_integer) will be destroyed
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · skipped 4 · removed 2 · refreshed 2 · not read 4
```

```sh run check
pravo apply --auto-approve
pravo plan --detailed-exitcode >/dev/null
pravo state list | grep -qx conf.cache
! pravo state list | grep -q worker
```
:::

:::deep-dive Keys that are not identifiers
A key such as `my-web` is not a plain CUE identifier. The address quotes it. This runs in a third root, `keys`, with its own state.

```cue file=keys/main.cue
package keys

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

conf: "my-web": $local.#Resource.local_file & {filename: "my-web.conf", content: "web\n"}
```

```sh run
pravo -C keys init > /dev/null
pravo -C keys plan | grep '^  #'
```

```text out
  # conf."my-web" (local_file) will be created
```

The address is `conf."my-web"`. Quote the whole address when you give it to a shell, for example `pravo state show 'conf."my-web"'`. A key is part of the address, so renaming a key creates a new resource and destroys the old one. [B8](b08-renames-moves-imports.md) shows how to keep the object when you rename a key.
:::

## Clean up

```sh run
pravo destroy --auto-approve | tail -n 1
pravo -C list destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 6 destroyed. [txn <id>]
Destroy complete! Resources: 0 added, 0 changed, 3 destroyed. [txn <id>]
```

## Recap

- A comprehension writes one field per key, and a resource in that field has the address `field.key`.
- The order of the source does not change an address. A list uses the index as the address, so its order does.
- Adding or removing a key adds or destroys only the resources of that key.

:::check
What is the address of the file for the service `web` in `rules.cue`?
::answer
`conf.web`. The field `conf` holds the rule, and the key `web` is the last element.

Why does reordering a list replace resources and reordering a struct does not?
::answer
In a list the index is the address. In a struct the key is the address, and the key does not move.
:::

Reference: [Resources and addresses](../concepts/resources-and-addresses.md) and [Resources](../reference/spec-resources.md).

[B6](b06-modules-providers-credentials.md) packs resources into a module and passes providers to it.
