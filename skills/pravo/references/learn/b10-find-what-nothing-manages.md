<!-- Generated from docs/learn/b10-find-what-nothing-manages.md by tools/skillgen. Do not edit. -->

# Find what nothing manages

Declare a discovery over a region, run it, acknowledge the objects that are meant to stay, adopt one into the specification and check the rest from a pipeline.

Someone made a server from the console a year ago, and a job starts two more every night. No specification names them. Which command lists them, and how do you take one over?

A specification names what pravo manages. A cloud holds more: objects made by hand, by a job, or by a specification that nobody runs. In OpenTofu you learn the id of such an object elsewhere, in the console or from the command line of the cloud, and write an `import` block for it. pravo has a **discovery**: a struct of the specification that lists the objects of one type in a scope, sets them against the state and shows the ones that no record accounts for. [Discovery](../concepts/discovery.md) explains how an object is told from the others.

```sh
tofu import demo_server.web_old srv-web-old
```

```sh
pravo discover
pravo discover adopt servers srv-web-old --read
```

A discovery needs a provider that can list. This lesson uses one that stands in for a cloud: `tools/fakeprovider` in the source tree of pravo, which keeps its objects in a JSON file. Its type `demo_server` has a name and a size, and the provider can list the servers of a region. Nothing on this page needs an account.

## Build the provider

Work in an empty directory. The provider is built from a checkout of pravo with the Go tool, into the directory of this lesson, which the command below calls `~/b10`.

```sh
go build -o ~/b10/terraform-provider-demo ./tools/fakeprovider
```

The provider reads its cloud from a file. Write four servers into it, as if three had been made by hand in the region `eu` and one in `us`, and tell the provider where the file is. A real provider takes credentials in the same place.

File `objects/catalog.json`:

```json
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

File `main.cue`:

```cue
package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {region: "eu"}
```

`$path` names a provider by its binary, where [B1](b01-providers-and-resources.md) named one by a registry and a version; [Provider declarations](../reference/spec-providers.md) has the rules. A discovery is not a resource. No plan reads it, and an apply makes nothing from it.

```sh
pravo init
pravo apply --auto-approve
```

Output:

```text
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The apply made `web` in `eu`, and the provider added it to its file. The cloud now holds five servers.

## Run it

```sh
pravo discover
```

Output:

```text
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

```sh
pravo discover
pravo discover show | head -n 4
```

Output:

```text
Every discovery is current. Run pravo discover --all to list them again.
servers  demo_server via demo
  scope: region=eu
  last run <age> ago: 4 objects
  3 unmanaged · 0 adopting · 0 acknowledged · 1 managed
```

`--exit-code` makes `show` answer with a code, for a pipeline: 2 while any object is unmanaged, 0 otherwise.

```sh
pravo discover show --exit-code >/dev/null
```

Exit code 2. Three objects are unmanaged.

## Acknowledge what you know

`batch-1` and `batch-2` are started by a nightly job and removed by it. The specification should not manage them, and the listing should not ask about them every morning. Say so once, for both, in the specification: an entry of the `$ack` list of the discovery, with a pattern for the name and a reason.

File `main.cue`:

```cue
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

```sh
pravo discover show
```

Output:

```text
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

*Figure: The Discovery section after the acknowledgement: one object asks to be adopted, two are known, and the server of the specification is a number.*

## Adopt one

`web-old` is a server that the specification should manage. `pravo discover adopt` writes the resource that adopts it: a `$import` with the identity of the object, as [B8](b08-renames-moves-imports.md) wrote one by hand. With `--read` the provider is asked for the object, and its attributes are written in. `tee -a` shows the resource and appends it to `main.cue`.

```sh
pravo discover adopt servers srv-web-old --read | tee -a main.cue
```

Output:

```text
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

```sh
pravo discover show
```

Output:

```text
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

```sh
pravo plan
```

Output:

```text
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

File `main.cue`:

```cue
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

```sh
pravo plan | grep -E '^(  #|Plan)'
pravo apply --auto-approve | tail -n 1
```

Output:

```text
  # web_old will be imported from the identity "id=srv-web-old,region=eu" and matches the specification
Plan: 0 to add, 0 to change, 0 to destroy, 1 to import.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
```

The object matches the specification, and the apply wrote its record. `pravo discover --all` runs every discovery again, current or not. The object is managed now.

```sh
pravo discover --all
```

Output:

```text
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

```sh
pravo discover show --exit-code >/dev/null
```

Exit code 0. With `pravo discover --all` before it, this is the check that a pipeline runs: it fails when something was made outside the specification and nobody acknowledged it. [Run pravo in a pipeline](../how-to/ci.md) shows where to put it.

## Exercise

## Clean up

```sh
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A discovery lists the objects of one type in a scope and sets them against the state. It manages nothing: no plan reads it, and no apply makes anything from it.
- `pravo discover` runs the discoveries that have not run or whose scope changed. `pravo discover show` prints the last run, and `--exit-code` makes it exit 2 while an object is unmanaged.
- An object is managed when a record holds its identity, acknowledged when an entry of `$ack` matches its name or id, and adopting when a resource names it in `$import` and has no record yet. The rest is unmanaged.
- `pravo discover adopt NAME ID --read` writes the resource that adopts an object, with the attributes the provider returns. Plan and apply as for any `$import`.
- The Discovery section of the viewer, `d`, shows the same run, with the resource that adopts each object and the entry that acknowledges it, to copy.

The guide [Find what nothing manages](../how-to/find-what-nothing-manages.md) adds a change of scope and the filter of the Discovery section. [B11](https://pravo.systems/learn/b11-policy.html) turns to rules about what a plan may do.
