---
title: pravo discover
description: Run the discoveries of the specification, show what they found against the state, acknowledge the objects that are meant to stay unmanaged and write the resource that adopts one.
order: 20
group: Commands
keywords: "list unmanaged inventory orphans import audit"
capture: false
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo discover [NAME...]`, `pravo discover show [NAME...]`, `pravo discover ack NAME`, `pravo discover acks [NAME...]`, `pravo discover unack ID...`, `pravo discover adopt NAME ID` |
| Changes infrastructure | No. A discovery lists. |
| Starts providers | `discover` and `adopt --read`. The other subcommands read the state and start nothing. |
| Writes | `discover`: one object for each discovery it runs, `discovery/runs/<name>.json`. `ack` and `unack`: one object, `discovery/ack/<id>.json`. No record, no transaction and no lock. |
| Requires | A discovery in the specification, built from a provider that implements list resources. [Discovery](../concepts/discovery.md) explains what one is. |
| Taught in | [Find what nothing manages](../how-to/find-what-nothing-manages.md). |

A discovery is written `name: $provider.#Discovery.<type> & {scope...}` and may set `$limit`, the most objects that one run keeps. `NAME` is the address of the struct, such as `servers` or `network.vpcs`. A name that no discovery has is an error that lists the names that exist. A discovery cannot be called `show`, `ack`, `acks`, `unack` or `adopt`: a first argument that is one of them is the subcommand.

## pravo discover

Runs discoveries. Without names it runs the ones that have not run yet and the ones whose scope changed since they ran. With names it runs those, and with `--all` every one. For each it starts the provider, converts the scope against the schema of the list resource, lists, and stores the result. It then prints what the run found, as `show` prints it.

A discovery that fails does not stop the others. Its error is printed, its last run stays in the state, and the exit code is 1.

| Flag | Default | Meaning |
|---|---|---|
| `--all` | off | Run every discovery. Not with names. |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE. |

With `--json` it prints one document, the one of `show --json` for the discoveries it ran.

## pravo discover show

Prints what the last run of each discovery found, set against the records of the state and the declarations of the specification. It reads and writes nothing and starts no provider. In a sealed state it needs the identity of a reader, as `state show` does, since it compares attributes of records.

For each discovery it prints the name, the type and the provider instance, the scope, the time of the last run and the counts. A discovery that has not run, whose scope cannot be listed with, whose scope changed since it ran, or that the specification does not declare says so, and `Run: pravo discover NAME` is the command that fixes the first two kinds. Then it lists the objects in groups: `UNMANAGED` with the command that adopts each, `ADOPTING` with the resource that adopts it, `ACKNOWLEDGED` with the record and its reason. A group lists 20 objects, then says how many more there are; `--json` lists them all. The managed objects are counted.

| Flag | Default | Meaning |
|---|---|---|
| `--exit-code` | off | Exit 2 when an object that is shown is unmanaged. |
| `--managed` | off | List the managed objects too. |

## pravo discover ack

`pravo discover ack NAME [--id PATTERN]... [--name PATTERN]... [--unmanaged]` writes an acknowledgement: a record that the objects it covers are known and are meant to stay as they are. The objects are still listed and stop asking to be adopted.

`--id` is a pattern for the id of an object, or its whole identity, and `--name` one for the name that the provider gave it. Both repeat. In a pattern `*` stands for any run of characters and `?` for one. `--unmanaged` covers the objects that are unmanaged in the last run, each by its id, and none that appear later. The record holds the patterns and the reason, which is the global `--reason`, with who wrote it (`--actor`, else the user) and when. Writing the same patterns again replaces the record, with the new reason. `NAME` may be a discovery that the specification does not declare, when the state holds a run of it.

A pattern that is empty, longer than 512 bytes or made of `*` alone is refused. So is a record with more than 10000 patterns.

## pravo discover acks, unack

`acks` lists the records, optionally of the named discoveries: the id, the discovery, who and when, the patterns and the reason. A record of a discovery that the specification does not have is marked. `unack ID...` deletes the records with these ids, and exits 1 for an id that no record has.

## pravo discover adopt

`pravo discover adopt NAME ID [--read] [--import-id ID]` prints the CUE of a resource that adopts one object of the last run: a struct of the type of the discovery, built through its provider, with `$import` set to the id of the object. `ID` is the id that the listing shows, or the whole identity. Nothing is written to the specification or the state.

Without `--read` the resource has the import and nothing else. With it the provider is asked for the object, as a plan of an `$import` asks, and the attributes it has are written into the resource: the ones that are not computed alone, no null, no write-only attribute. A sensitive attribute is named in a comment and its value is not written. The result is laid out as `pravo fmt` does.

An object whose identity is not an id, and is made of more than one attribute, has `$import: "<id>"` in the snippet. `--import-id` gives the id for `--read`. An object that is managed is refused. An object that a resource already adopts is printed with a note.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | The command did what it was asked. |
| 1 | A discovery failed to run: the provider returned an error, the scope has no value, or the provider cannot list the type. An argument that is wrong. An id that no acknowledgement has. |
| 2 | `show --exit-code` found an unmanaged object. |
| 6 | The specification does not load, or `NAME` is not a discovery, or the object is not in the last run, or the object is managed, or the acknowledgement is refused. |

The [global flags](global-flags.md) apply.

## The state

| Object | Holds |
|---|---|
| `discovery/runs/<name>.json` | The last run of one discovery: the type, the provider, the scope and its digest, the limit, when and by whom, `complete` or `truncated`, the warnings, and for each object its key, id, display name, identity and import id. The name is the address, with the characters that a file name cannot hold written as `%XXXX`. |
| `discovery/ack/<id>.json` | One acknowledgement: `ack-` and eight hex digits of the digest of the discovery and the patterns, the discovery, the patterns, the reason, who and when. |

Neither is sealed. `pravo state pull` and `pravo state push` copy them with the records.

## The viewer

[`pravo serve`](serve.md#the-discovery-section) shows the same data in the Discovery section: each discovery as a card, the unmanaged objects first. It reads the two kinds of object and writes none. It offers the commands above as text.
