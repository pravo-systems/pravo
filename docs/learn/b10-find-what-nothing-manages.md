---
title: Find what nothing manages
description: Declare a discovery over a region, run it, acknowledge the objects that are meant to stay, adopt one into the specification and check the rest from a pipeline.
order: 40
group: Track B · pravo
minutes: 15
needs: B9
learn: Which objects of the cloud does no specification name? How do you say that one is known, and how do you bring one under management?
done_when: "`pravo discover show --exit-code` exits 2 while an object is unmanaged, and 0 once the last one is adopted or acknowledged."
level: intermediate
services:
  - fakeprovider
normalize:
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "last run [0-9]+[smh] ago"
    replace: "last run <age> ago"
---

Someone made a server from the console a year ago, and a job starts two more every night. No specification names them. Which command lists them, and how do you take one over?

A specification names what pravo manages. A cloud holds more: objects made by hand, by a job, or by a specification that nobody runs. In OpenTofu you learn the id of such an object elsewhere, in the console or from the command line of the cloud, and write an `import` block for it. pravo has a **discovery**: a struct of the specification that lists the objects of one type in a scope, sets them against the state and shows the ones that no record accounts for. [Discovery](../concepts/discovery.md) explains how an object is told from the others.

:::compare
```sh title="OpenTofu"
tofu import demo_server.web_old srv-web-old
```

```sh title="pravo"
pravo discover
pravo discover adopt servers srv-web-old --read
```
:::

A discovery needs a provider that can list. This lesson uses one that stands in for a cloud: `tools/fakeprovider` in the source tree of pravo, which keeps its objects in a JSON file. Its type `demo_server` has a name and a size, and the provider can list the servers of a region. Nothing on this page needs an account.

## Build the provider

Work in an empty directory. The provider is built from a checkout of pravo with the Go tool, into the directory of this lesson, which the command below calls `~/b10`.

```sh title="in a checkout of pravo"
go build -o ~/b10/terraform-provider-demo ./tools/fakeprovider
```

```sh hidden
ln -s "$FAKEPROVIDER_BIN" terraform-provider-demo
```

The provider reads its cloud from a file. Write four servers into it, as if three had been made by hand in the region `eu` and one in `us`, and tell the provider where the file is. A real provider takes credentials in the same place.

```json file=objects/catalog.json title="objects/catalog.json"
[
  {"id": "srv-web-old", "name": "web-old", "region": "eu"},
  {"id": "srv-batch-1", "name": "batch-1", "region": "eu"},
  {"id": "srv-batch-2", "name": "batch-2", "region": "eu"},
  {"id": "srv-us-1", "name": "us-one", "region": "us"}
]
```

```sh
export FAKEPROVIDER_DIR=$PWD/objects
```

## Declare a discovery

A discovery is built through a provider, as a resource is, from `#Discovery.<type>` in place of `#Resource.<type>`. Its fields are the scope: what the provider lists. This specification manages one server, and declares a discovery of the servers of `eu`.

```cue file=main.cue title="main.cue" hl="9"
package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {region: "eu"}
```

`$path` names a provider by its binary, where [B1](b01-providers-and-resources.md) named one by a registry and a version; [Provider declarations](../reference/spec-providers.md) has the rules. A discovery is not a resource. No plan reads it, and an apply makes nothing from it.

```sh run
pravo init
pravo apply --auto-approve
```

```text out
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The apply made `web` in `eu`, and the provider added it to its file. The cloud now holds five servers.

:::predict How many objects does the first run of servers list, and how many of them does it show?
The scope is the region `eu`.

::reveal
It lists four: the three of the file that are in `eu`, and `web`. `us-one` is outside the scope. `web` has a record, so it is counted as managed and not shown. The other three are shown, each with the command that adopts it.
:::

## Run it

```sh run
pravo discover
```

```text out
servers: listing demo_server...
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  3 unmanaged · 0 adopting · 0 acknowledged · 1 managed

  UNMANAGED
    srv-batch-1                   batch-1  pravo discover adopt servers srv-batch-1
    srv-batch-2                   batch-2  pravo discover adopt servers srv-batch-2
    srv-web-old                   web-old  pravo discover adopt servers srv-web-old
```

Each object the provider listed is set against the state and gets a status:

| Status | Meaning |
|---|---|
| Managed | A record of the state holds its identity. Counted, not listed. |
| Unmanaged | No record and no acknowledgement. Listed, with the command that adopts it. |
| Acknowledged | An entry of the `$ack` list of the discovery matches it. Known, and left alone. |
| Adopting | A resource of the specification has its identity in `$import` and no record yet. |

Run it again and nothing happens. A discovery that has run, with the scope it has now, is current. `pravo discover show` prints the last run without asking the provider.

```sh run
pravo discover
pravo discover show | head -n 4
```

```text out
Every discovery is current. Run pravo discover --all to list them again.
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  3 unmanaged · 0 adopting · 0 acknowledged · 1 managed
```

`--exit-code` makes `show` answer with a code, for a pipeline: 2 while any object is unmanaged, 0 otherwise.

```sh run exit=2
pravo discover show --exit-code >/dev/null
```

Exit code 2. Three objects are unmanaged.

## Acknowledge what you know

`batch-1` and `batch-2` are started by a nightly job and removed by it. The specification should not manage them, and the listing should not ask about them every morning. Say so once, for both, in the specification: an entry of the `$ack` list of the discovery, with a pattern for the name and a reason.

```cue file=main.cue title="main.cue" hl="9-15"
package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [{
		names:  ["batch-*"]
		reason: "started and removed by the nightly job"
	}]
}
```

A pattern in `names` is matched against the name the provider shows, and one in `ids` against the id of the object. `*` stands for any run of characters and `?` for one. The entry also covers a `batch-3` that the job starts tomorrow. The entries are not part of the scope, so the discovery is still current, and `show` reads the acknowledgement from the specification against the last run:

```sh run
pravo discover show
```

```text out
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  1 unmanaged · 0 adopting · 2 acknowledged · 1 managed

  UNMANAGED
    srv-web-old                   web-old  pravo discover adopt servers srv-web-old

  ACKNOWLEDGED
    srv-batch-1                   batch-1  $ack[0] “started and removed by the nightly job”
    srv-batch-2                   batch-2  $ack[0] “started and removed by the nightly job”
```

The viewer shows the same run. Run `pravo serve` in this directory and press `d` for the Discovery section: the discovery is a card with its scope, when it ran, and the count of objects by status. The object that nothing accounts for comes first, with a button that opens the resource to adopt it and one that opens the `$ack` entry to copy. The acknowledged objects follow in grey with their reasons, and the managed one is a number.

:::figure The Discovery section after the acknowledgement: one object asks to be adopted, two are known, and the server of the specification is a number.
<a class="shot shot-dark" href="../img/serve/b10-discovery-dark.png"><img src="../img/serve/b10-discovery-dark.png" width="1440" height="900" alt="The Discovery section of the viewer. The head says 1 unmanaged, and that 1 object that nothing manages asks to be adopted. One card, servers, of type demo_server via demo and marked current, is open: the scope region=eu, the last run just now, written at main.cue line 9, and the counts 1 unmanaged, 0 adopting, 2 acknowledged and 1 managed, of 4 found. Under Unmanaged, web-old, srv-web-old, in the region eu, has the buttons Acknowledge and Adopt. Under Acknowledged, batch-1 and batch-2 carry the reason started and removed by the nightly job and a link to adopt anyway, and a folded row counts 1 entry acknowledged in the specification. A footer says that the page suggests commands and runs nothing." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/b10-discovery-light.png"><img src="../img/serve/b10-discovery-light.png" width="1440" height="900" alt="The Discovery section of the viewer. The head says 1 unmanaged, and that 1 object that nothing manages asks to be adopted. One card, servers, of type demo_server via demo and marked current, is open: the scope region=eu, the last run just now, written at main.cue line 9, and the counts 1 unmanaged, 0 adopting, 2 acknowledged and 1 managed, of 4 found. Under Unmanaged, web-old, srv-web-old, in the region eu, has the buttons Acknowledge and Adopt. Under Acknowledged, batch-1 and batch-2 carry the reason started and removed by the nightly job and a link to adopt anyway, and a folded row counts 1 entry acknowledged in the specification. A footer says that the page suggests commands and runs nothing." loading="lazy"></a>
:::

## Adopt one

`web-old` is a server that the specification should manage. `pravo discover adopt` writes the resource that adopts it: a `$import` with the identity of the object, as [B8](b08-renames-moves-imports.md) wrote one by hand. With `--read` the provider is asked for the object, and its attributes are written in. `tee -a` shows the resource and appends it to `main.cue`.

```sh run
pravo discover adopt servers srv-web-old --read | tee -a main.cue
```

```text out
// Adopts the demo_server "srv-web-old" (web-old) that the discovery servers found.
web_old: $demo.#Resource.demo_server & {
	$import: {
		id:     "srv-web-old"
		region: "eu"
	}
	name: "web-old"
	// secret is sensitive: write its value yourself.
	size: "large"
}
```

The provider gave out the name and the size. It did not give out `secret`, which is sensitive, and the comment says so. Before any plan reads the object, the discovery knows that something is about to adopt it:

```sh run
pravo discover show
```

```text out
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  0 unmanaged · 1 adopting · 2 acknowledged · 1 managed

  ADOPTING
    srv-web-old                   web-old  by web_old

  ACKNOWLEDGED
    srv-batch-1                   batch-1  $ack[0] “started and removed by the nightly job”
    srv-batch-2                   batch-2  $ack[0] “started and removed by the nightly job”
```

Plan, as for any `$import`. The plan reads the object and sets it against the resource:

```sh run
pravo plan
```

```text out
web_old: Reading the object of the identity "id=srv-web-old,region=eu"...

pravo will perform the following actions:

  # web_old will be imported from the identity "id=srv-web-old,region=eu" and will be updated in-place
  ~ web_old: {
        id:     "srv-web-old"
        name:   "web-old"
  -     secret: (sensitive)
        size:   "large"
    }

Plan: 0 to add, 1 to change, 0 to destroy, 1 to import.
Graph: planned 1 · skipped 1 · not read 1 · to import 1
```

The server has a secret, and the resource says nothing about one, so the apply would take it away: a resource says everything about its object. Write the value in place of the comment. Every server of the demo provider was made with `hunter2`.

```cue file=main.cue title="main.cue" hl="24"
package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [{
		names:  ["batch-*"]
		reason: "started and removed by the nightly job"
	}]
}

// Adopts the demo_server "srv-web-old" (web-old) that the discovery servers found.
web_old: $demo.#Resource.demo_server & {
	$import: {
		id:     "srv-web-old"
		region: "eu"
	}
	name:   "web-old"
	secret: "hunter2"
	size:   "large"
}
```

```sh run
pravo plan | grep -E '^(  #|Plan)'
pravo apply --auto-approve | tail -n 1
```

```text out
  # web_old will be imported from the identity "id=srv-web-old,region=eu" and matches the specification
Plan: 0 to add, 0 to change, 0 to destroy, 1 to import.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
```

The object matches the specification, and the apply wrote its record. `pravo discover --all` runs every discovery again, current or not. The object is managed now.

```sh run
pravo discover --all
```

```text out
servers: listing demo_server...
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  0 unmanaged · 0 adopting · 2 acknowledged · 2 managed

  ACKNOWLEDGED
    srv-batch-1                   batch-1  $ack[0] “started and removed by the nightly job”
    srv-batch-2                   batch-2  $ack[0] “started and removed by the nightly job”
```

## Check it from a pipeline

Nothing is unmanaged, so the check passes:

```sh run
pravo discover show --exit-code >/dev/null
```

Exit code 0. With `pravo discover --all` before it, this is the check that a pipeline runs: it fails when something was made outside the specification and nobody acknowledged it. [Run pravo in a pipeline](../how-to/ci.md) shows where to put it.

## Exercise

:::exercise A second region
`us-one` is in `us`, outside the scope of `servers`. Declare a second discovery, `servers_us`, over the region `us`, run it, and acknowledge `us-one` by its id with a reason of your own, so that `pravo discover show --exit-code` exits 0 again.

::hint
A root holds any number of discoveries. `pravo discover` with no name runs the ones that have not run, or whose scope changed, and leaves the current ones alone.

::hint
An entry with `ids: ["srv-us-1"]` matches one object by its id. Patterns work in `ids` as in `names`.

::solution
```cue file=main.cue title="main.cue"
package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [{
		names:  ["batch-*"]
		reason: "started and removed by the nightly job"
	}]
}

// Adopts the demo_server "srv-web-old" (web-old) that the discovery servers found.
web_old: $demo.#Resource.demo_server & {
	$import: {
		id:     "srv-web-old"
		region: "eu"
	}
	name:   "web-old"
	secret: "hunter2"
	size:   "large"
}

servers_us: $demo.#Discovery.demo_server & {
	region: "us"
	$ack: [{
		ids:    ["srv-us-1"]
		reason: "the archive of the old region, kept until the audit"
	}]
}
```

```sh run
pravo discover
```

```text out
servers_us: listing demo_server...
servers_us  demo_server via demo
  scope: region=us
  last run <age> ago: 1 object
  0 unmanaged · 0 adopting · 1 acknowledged · 0 managed

  ACKNOWLEDGED
    srv-us-1                      us-one  $ack[0] “the archive of the old region, kept until the audit”
```

`servers` is current and did not run.

```sh run check
pravo discover show --exit-code >/dev/null
```
:::

## Clean up

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A discovery lists the objects of one type in a scope and sets them against the state. It manages nothing: no plan reads it, and no apply makes anything from it.
- `pravo discover` runs the discoveries that have not run or whose scope changed. `pravo discover show` prints the last run, and `--exit-code` makes it exit 2 while an object is unmanaged.
- An object is managed when a record holds its identity, acknowledged when an entry of `$ack` matches its name or id, and adopting when a resource names it in `$import` and has no record yet. The rest is unmanaged.
- `pravo discover adopt NAME ID --read` writes the resource that adopts an object, with the attributes the provider returns. Plan and apply as for any `$import`.
- The Discovery section of the viewer, `d`, shows the same run, with the resource that adopts each object and the entry that acknowledges it, to copy.

:::check
A discovery ran yesterday. You add an entry to its $ack list today. Does pravo discover run it again?
::answer
No. The entries are not part of the scope, so the discovery is current. pravo discover show reads the acknowledgement from the specification against the last run.

Why is the object of web counted and not listed?
::answer
A record of the state holds its identity, so it is managed. The listing shows what asks for a decision: the unmanaged objects, and the ones being adopted.
:::

The guide [Find what nothing manages](../how-to/find-what-nothing-manages.md) adds a change of scope and the filter of the Discovery section. [B11](b11-policy.md) turns to rules about what a plan may do.
