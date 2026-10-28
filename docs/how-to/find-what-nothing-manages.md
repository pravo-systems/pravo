---
title: Find what nothing manages
description: Declare a discovery, run it, read the objects that no record accounts for, acknowledge the ones that are meant to stay, adopt the rest and run it again when the scope changes.
order: 7
group: Operate
minutes: 15
needs: A root that you have initialised, and a provider that can list the objects of a type. The commands are for a POSIX shell.
done_when: "A discovery lists the objects of a scope, the ones you know are acknowledged, one is adopted by a resource of the specification, and the Discovery section of the viewer shows the rest."
level: intermediate
keywords: "list inventory unmanaged orphans import shadow resources console drift audit cloud"
capture: false
---

A cloud holds objects that no specification names. A discovery lists them, sets them against the state and shows the ones that nothing accounts for. [Discovery](../concepts/discovery.md) explains how an object is told from the others. This page declares one, runs it, reads it, acknowledges and adopts objects, and runs it again after a change of scope.

The examples use `demo_server`, the one type of `tools/fakeprovider` in the pravo source tree. It is a provider that can list, and it needs no cloud account. The output on this page was written from it by hand, so `capture: false` keeps `tools/capture` from running the page. To follow along, build it and keep its objects in a directory:

```sh
go build -o terraform-provider-demo ./tools/fakeprovider
mkdir objects
cat > objects/catalog.json <<'JSON'
[{"id":"srv-web-old","name":"web-old","region":"eu"},
 {"id":"srv-batch-1","name":"batch-1","region":"eu"},
 {"id":"srv-batch-2","name":"batch-2","region":"eu"},
 {"id":"srv-us-1","name":"us-one","region":"us"}]
JSON
export FAKEPROVIDER_DIR=$PWD/objects
```

With a real provider you skip all of this. A provider that has list resources for a type offers `#Discovery.<type>`, and `pravo schema --discovery TYPE` prints its scope.

## 1. Declare a discovery

A discovery is built through a provider, as a resource is. Its fields are the scope.

```cue file=main.cue title="main.cue"
package estate

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

known: $demo.#Resource.demo_server & {name: "known"}

servers: $demo.#Discovery.demo_server & {region: "eu"}
```

Initialise the root, and create the resource that the specification manages. Its object is one that the discovery will count and leave alone.

```sh
pravo init
pravo apply -auto-approve
```

## 2. Run it

```sh
pravo discover
```

```text
servers: listing demo_server...
servers  demo_server via demo
  scope: region=eu
  last run 0s ago: 4 objects
  3 unmanaged · 0 adopting · 0 acknowledged · 1 managed

  UNMANAGED
    srv-batch-1                   batch-1  pravo discover adopt servers srv-batch-1
    srv-batch-2                   batch-2  pravo discover adopt servers srv-batch-2
    srv-web-old                   web-old  pravo discover adopt servers srv-web-old
```

The provider listed four objects in the region `eu`. One is the object of `known`, which has a record, so it is counted as managed and not listed. The other three are unmanaged. The object in `us` is outside the scope and is not listed.

Run it again and nothing happens. A discovery that has run, with the scope it has now, is current.

```sh
pravo discover
```

```text
Every discovery is current. Run pravo discover --all to list them again.
```

## 3. Acknowledge what you know

`batch-1` and `batch-2` are started by a nightly job and removed by it. Say so once, for both, with a pattern for the name. The acknowledgement is code: an entry of the `$ack` list of the discovery, written in the specification.

```cue
servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [{
		names: ["batch-*"]
		reason: "started and removed by the nightly job"
	}]
}
```

A pattern in `names` is matched against the display name the provider gave, and one in `ids` against the id of the object or its whole identity. `*` stands for any run of characters and `?` for one. The viewer writes an entry for any object, with its id in place and the reason left to give; [step 6](#6-see-it-in-the-viewer) shows where.

The entry also covers a `batch-3` that the job starts tomorrow. To withdraw it, take it out of the list. The entries are not part of the scope, so the discovery is still current and nothing runs again.

```sh
pravo discover show
```

```text
servers  demo_server via demo
  scope: region=eu
  last run 1m ago: 4 objects
  1 unmanaged · 0 adopting · 2 acknowledged · 1 managed

  UNMANAGED
    srv-web-old                   web-old  pravo discover adopt servers srv-web-old

  ACKNOWLEDGED
    srv-batch-1                   batch-1  $ack[0] “started and removed by the nightly job”
    srv-batch-2                   batch-2  $ack[0] “started and removed by the nightly job”
```

To acknowledge one object, write its id in `ids`.

## 4. Adopt one

`web-old` is a server that the specification should manage. `pravo discover adopt` writes the resource that adopts it. With `--read` the provider is asked for the object, and its attributes are written in.

```sh
pravo discover adopt servers srv-web-old --read
```

```cue
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

Put it in the specification. Before the next plan reads the object, the discovery already knows that something is about to adopt it:

```sh
pravo discover show
```

```text
  ADOPTING
    srv-web-old                   web-old  by web_old
```

Plan and apply as for any `$import`. [Rename, move and adopt resources](importing-and-moving.md) covers what the plan shows. Once the resource has a record, the object is managed.

```sh
pravo plan
pravo apply -auto-approve
pravo discover --all
```

An object is adopted by its identity, whatever it is made of. A bucket that a provider names by an account, a name and a region has no id to look up: the snippet has `$import: {account_id: …, bucket: …, region: …}`, and the plan imports it by those attributes.

## 5. Change the scope

Change `region: "eu"` to `region: "us"` in `main.cue`. The last run answers the old scope, and `pravo discover show` says so.

```text
servers  demo_server via demo
  scope: region=us
  the scope changed since the last run, which answers the old one. Run: pravo discover servers
  last run 3m ago: 4 objects
...
```

`pravo discover` with no name runs it again, since a scope that changed makes a discovery stale. The viewer shows the same notice on the card of the discovery, with the command to copy.

## 6. See it in the viewer

```sh
pravo serve
```

Open the Discovery section, or press `d`. Each discovery is a card, closed at first, with the count of its objects by status; open it with its name. It shows its scope, when it ran and who ran it, and the count of objects by status. The unmanaged objects come first, with an amber edge, an Adopt button and an Acknowledge button. Adopt opens the resource that adopts the object, and the command that reads the object into it. Acknowledge opens the `$ack` entry that says it is known, to copy. One of them is open at a time. The acknowledged objects are listed below in grey with their reasons, and under them the `$ack` entries of the specification with the number of objects each covers, and the managed objects are a number. The number on the tab is the objects that ask to be adopted.

To narrow what a long list shows, type in the filter box, as in the Inventory: `status:unmanaged` leaves what asks to be adopted, `type:aws_s3*` the buckets, `field:region=eu` the objects of a region, `-status:acknowledged` leaves out what is known. The menu offers the columns and the values the page holds, and a click on the type, the provider or a count of a card adds the term. [The reference](../reference/viewer-sections.md#filtering-the-objects) lists the columns.

The page runs nothing and writes nothing. It shows each command, and a button copies it.

## 7. Check it from a schedule

`pravo discover` lists, and `pravo discover show --exit-code` exits with 2 when any object is unmanaged. Together they make a check for a pipeline that fails when something is created outside the specification:

```sh
pravo discover --all && pravo discover show --exit-code
```

Acknowledge what is allowed, and the check passes. [Run pravo in a pipeline](ci.md) shows how to run a command from one.
