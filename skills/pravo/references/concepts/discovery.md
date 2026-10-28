<!-- Generated from docs/concepts/discovery.md by tools/skillgen. Do not edit. -->

# Discovery

How a discovery lists the objects that exist in a scope, what it keeps in the state, how the unmanaged ones are told from the rest, and when to run it again.

A specification names the objects that pravo manages. A cloud holds others: a bucket made in a console, an instance that a script started, a resource that a former team left. None of them is in the state, so no plan mentions them. A discovery finds them. It asks a provider to list the objects of one type in a scope you write, and pravo sets the answer against the state.

A discovery manages nothing. It is not a node of the graph, no plan or apply reads it, and it cannot create, change or destroy an object. [Find what nothing manages](../how-to/find-what-nothing-manages.md) shows the steps, and [`pravo discover`](../reference/discover.md) lists the flags.

## A discovery is a struct

A provider that can list an object type offers a discovery of it, next to its resources:

```cue
servers: $demo.#Discovery.demo_server & {region: "eu"}
```

`#Discovery.<type>` is built from the list resource type of the provider. Its fields are the scope: the attributes that the provider lets you filter a list by, typed as the provider declares them. `pravo schema --discovery demo/demo_server` prints them. `$limit` is the most objects that one run keeps. `$ack` lists what is known and left alone, as [Acknowledging](#acknowledging) says.

The scope is written out in the specification. It cannot read an attribute of a resource, because nothing has been applied when a discovery runs. A scope that has a field without a value, or reads a value that a run would produce, fails when the discovery runs, and nothing else in the specification is affected.

The address of the struct, `servers` above, names the discovery. A discovery in a group has the address of its path, such as `network.vpcs`.

## What a run keeps

`pravo discover` starts the provider of the discovery, asks it to list, and writes what came back as one object in the state, `discovery/runs/<name>.json`. A later run of the same discovery replaces it. The object holds the scope, the digest of the scope, the time, the actor and one entry for each object found: its identity as the provider gave it, its display name and, when the identity gives one, the id that `$import` takes.

It holds no attribute of an object. It is not sealed: it holds the identities and the names that a provider lists, and an identity is not a secret. It is the only thing a discovery puts in the state. A run that fails keeps nothing and leaves the last run as it was.

## What each object is

The viewer and `pravo discover show` set the objects of the last run against the state and the specification, every time they are read. Nothing about this is stored.

| Status | It means | How it shows |
|---|---|---|
| Managed | A record of the state is this object. | Counted. Never listed. |
| Adopting | A resource of the specification names it in `$import`, and no record exists yet. | Listed, quietly, with the address of the resource. |
| Acknowledged | An entry of `$ack` in the specification covers it. | Listed, quietly, with its reason. |
| Unmanaged | None of the above. | Listed first, in colour, with the means to adopt it. |

An object is **managed** when a record of the same type has the same value for every attribute of the identity that the record also carries, and at least one of those attributes says which object it is. The attributes `region`, `account_id`, `project`, `zone` and the like say where an object is and not which one, so two objects of a region are never taken for each other. A record that does not carry the attributes of an identity cannot be matched, and the object is listed as unmanaged. A mistake in this direction shows an object that is managed. The other direction would hide one that is not. A record that cannot be opened, in a sealed state, is counted, and the page says that objects it holds may be listed as unmanaged.

The status of the objects is where the weight of the page falls. The unmanaged ones are the only objects that ask for anything.

## Acknowledging

An acknowledgement says that an object is known and is meant to stay as it is: the default VPC of an account, the instances that a nightly job starts and removes. It is written in the specification, in the `$ack` list of the discovery, so it is reviewed, versioned and blamed like the rest of the code:

```cue
servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [
		{names: ["batch-*"], reason: "started and removed by the nightly job"},
		{ids: ["srv-web-old"], reason: "retired in the autumn"},
	]
}
```

An entry has `ids`, `names` or both, and a `reason`. A pattern in `ids` is matched against the id of an object, or its whole identity, A pattern in `names` is matched against the display name the provider gave. `*` stands for any run of characters and `?` for one. One entry can cover many objects, and the objects that appear later and match it. An entry with no pattern, or a pattern that matches everything, is an error when the specification loads.

The viewer writes the entry for an object, or for all that are unmanaged, with the identity filled in and the reason left for you to give. You copy it into the list. pravo writes nothing, and the state holds no acknowledgement.

An acknowledged object is still listed. It stops asking to be adopted, and it does not count for the number on the tab of the viewer, for `pravo discover show --exit-code` or for the sentence at the head of the page. Take the entry out of the specification and it asks again. The entries are not part of the scope: changing them does not make the last run stale, since they change what the objects mean and not what was listed.

## The scope and when to run again

A run answers one question: which objects of this type are in this scope, through this provider. When you change the scope, the limit or the type, the last run answers another question. pravo keeps a digest of what a run was asked and compares it with the specification each time the page or the command reads it. A discovery whose digest differs is **stale**. The viewer and `discover show` say so, put the command that runs it again beside it, and go on showing what the old run found, since it is still the last thing that was learned.

`pravo discover` with no name runs the discoveries that have not run and the ones that are stale. `--all` runs every one. The digest covers the provider instance, the type, the scope and the limit. It does not cover the configuration of the provider itself: a change of the profile or the endpoint that the provider uses changes where it looks without changing the digest, so run `pravo discover --all` after one.

A run that stops at its limit is marked `truncated`, and the page says that there may be more. The state keeps at most 100000 objects of one discovery.

## Adopting

An unmanaged object becomes managed when a resource of the specification takes it over. `pravo discover adopt` prints that resource: a struct of the type, built through the provider of the discovery, with `$import` set to the identity of the object. The viewer shows the same text with a button that copies it. With `--read` pravo asks the provider for the object and writes its attributes into the resource, the way a plan would show them, so that the plan of the new resource has little to say. A sensitive attribute is named in a comment and its value is never written.

A provider lists an object by its identity, which is a set of attributes: an id, or an account, a name and a region. `$import` takes that identity as a struct, and the provider imports the object by it, so no id has to be found. An identity is not an id: the id that a provider documents for an import is one string, and a provider cannot always make it from the identity, so pravo does not try. The attributes that the provider can fill in from its configuration may be left out, and a null one is not written. [Moves and imports](../reference/spec-moves-and-imports.md#import) describes both forms.

Nothing writes the specification for you. pravo does not edit your `.cue` files.

## Where it shows

The unmanaged objects are shown in the Discovery section of [the viewer](../reference/viewer-sections.md#the-discovery-section), and by `pravo discover show`. They are not in the graph or in the list of the Inventory, which show what the specification declares and the state records. The tab carries the number of objects that ask to be adopted.

## Providers

A discovery needs a provider that implements list resources of the plugin protocol. A provider that does not has no `#Discovery` types, and a `$kind: "discovery"` struct cannot be built from it. `pravo init` generates the `#Discovery` definitions of the providers that have them, from the schema of the list resource, so the scope is checked when the specification is evaluated.
