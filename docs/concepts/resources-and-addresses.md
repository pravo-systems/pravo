---
title: Resources and addresses
description: What makes a struct a resource, how its address is its path, and what renaming, moving or reordering does to the record of an object.
order: 3
minutes: 11
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'hashicorp/random v[0-9]+\.[0-9]+\.[0-9]+'
    replace: 'hashicorp/random v<v>'
---

A resource is a struct that carries the identity of a provider type. Its address is the path at which it sits in the specification. The address is the only name that pravo uses to find the record of the object in the state.

## The address is the path

pravo finds a resource by its identity and names it by its path. The path of `api.config` is the field `config` inside the field `api`. Work in an empty directory. The files below declare one resource of each shape.

```cue file=providers.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

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

```cue file=service.cue
package estate

// A definition that contains resources. An instance holds them at its path.
#Service: {
	name: string
	secret: $random.#Resource.random_integer & {min: 1000, max: 9999, seed: name}
	config: $local.#Resource.local_file & {
		filename: "\(name).conf"
		content:  "name = \(name)\n"
	}
}

api: #Service & {name: "api"}
```

```cue file=keyed.cue
package estate

// One resource for each key.
zones: {
	for z in ["eu", "us-east"] {
		"\(z)": $random.#Resource.random_integer & {min: 1, max: 9, seed: z}
	}
}

// One resource for each position.
replicas: [
	for n in ["alpha", "beta"] {
		$random.#Resource.random_integer & {min: 1, max: 9, seed: n}
	},
]
```

```cue file=readme.cue
package estate

readme: $local.#Data.local_file & {filename: "port.cue"}
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

The plan header of each change shows the address.

```sh run
pravo plan | grep '^  # '
```

```text out
  # api.secret (random_integer) will be created
  # api.config (local_file) will be created
  # port (random_integer) will be created
  # zones.eu (random_integer) will be created
  # zones."us-east" (random_integer) will be created
  # replicas[0] (random_integer) will be created
  # replicas[1] (random_integer) will be created
```

After an apply, the state holds one record for each address.

```sh run
pravo apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 7 added, 0 changed, 0 destroyed. [txn <id>]
```

```sh run
pravo state list
```

```text out
api.config
api.secret
port
readme
replicas[0]
replicas[1]
zones."us-east"
zones.eu
```

| Declaration | Address |
|---|---|
| `port: $random.#Resource.random_integer & {...}` | `port` |
| `config` inside the instance `api` of a definition | `api.config` |
| the key `eu` in the struct `zones` | `zones.eu` |
| the key `us-east` in the struct `zones` | `zones."us-east"` |
| the second element of the list `replicas` | `replicas[1]` |
| a data source, `readme: $local.#Data.local_file & {...}` | `readme` |

A label that CUE needs quoted is quoted in the address. Quote the whole address for the shell when you pass it to a command.

```sh run
pravo state show 'zones."us-east"' | head -1
```

```text out
// zones."us-east" (provider registry.opentofu.org/hashicorp/random v<v> as random, schema version 0, revision 1)
```

A data source, a resource and an ephemeral resource share one namespace of paths. There is no `data.` prefix. The record says what it is.

```sh run
pravo state query 'resources.readme.mode'
```

```text out
"data"
```

These commands take an address: `pravo state show`, `pravo state rm`, `pravo state mv`, `pravo state dependents`, `pravo plan -replace`, `pravo why` and `pravo import`.

## An address stands for one object

The state keeps one record for each address. The record holds the provider, the type and the attributes of the object. The type is a field of the record. It is not part of the name.

pravo matches the specification and the state by address and by nothing else.

- An address in the specification with no record is a create.
- A record with no address in the specification is a delete.
- An address with a record is compared by its configuration.

:::figure Without `$from`, a renamed resource is a create and a delete. With `$from`, the record changes its address and the object is untouched.
<svg viewBox="0 0 780 270" role="img" aria-label="Two panels. Without from, the record port has no address in the specification, so it is deleted, and the address listen has no record, so it is created. With from, the record port is renamed to listen and the object is unchanged. The record api.config matches its address in both panels." style="min-width:680px">
<text x="0" y="14" style="fill:var(--text-3);font-size:11px;letter-spacing:0.04em">WITHOUT $from</text>
<text x="0" y="38" style="fill:var(--text-3);font-size:10.5px">state records</text>
<text x="262" y="38" style="fill:var(--text-3);font-size:10.5px">addresses</text>
<rect x="0" y="52" width="108" height="34" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="54.0" y="73.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">port</text>
<rect x="262" y="52" width="108" height="34" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="316.0" y="73.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">listen</text>
<line x1="108" y1="69.0" x2="138" y2="69.0" style="stroke:var(--conflict);stroke-width:1.5"/>
<line x1="138" y1="62.0" x2="138" y2="76.0" style="stroke:var(--conflict);stroke-width:2"/>
<line x1="232" y1="69.0" x2="262" y2="69.0" style="stroke:var(--accent);stroke-width:1.5"/>
<line x1="232" y1="62.0" x2="232" y2="76.0" style="stroke:var(--accent);stroke-width:2"/>
<text x="126" y="57.0" text-anchor="middle" style="fill:var(--conflict);font-size:10.5px;font-weight:600">delete</text>
<text x="244" y="57.0" text-anchor="middle" style="fill:var(--accent);font-size:10.5px;font-weight:600">create</text>
<rect x="0" y="104" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="54.0" y="125.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">api.config</text>
<rect x="262" y="104" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="316.0" y="125.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">api.config</text>
<line x1="108" y1="121.0" x2="262" y2="121.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="262,121.0 256,117.4 256,124.6" style="fill:var(--text-3)"/>
<text x="185" y="112.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">same</text>
<rect x="0" y="156" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="54.0" y="177.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">zones.eu</text>
<rect x="262" y="156" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="316.0" y="177.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">zones.eu</text>
<line x1="108" y1="173.0" x2="262" y2="173.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="262,173.0 256,169.4 256,176.6" style="fill:var(--text-3)"/>
<text x="185" y="164.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">same</text>
<text x="410" y="14" style="fill:var(--text-3);font-size:11px;letter-spacing:0.04em">WITH $from</text>
<text x="410" y="38" style="fill:var(--text-3);font-size:10.5px">state records</text>
<text x="672" y="38" style="fill:var(--text-3);font-size:10.5px">addresses</text>
<rect x="410" y="52" width="108" height="34" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.4"/>
<text x="464.0" y="73.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">port</text>
<rect x="672" y="52" width="108" height="34" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.4"/>
<text x="726.0" y="73.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">listen</text>
<line x1="518" y1="69.0" x2="672" y2="69.0" style="stroke:var(--change);stroke-width:1.5" stroke-dasharray="5 3"/>
<polygon points="672,69.0 666,65.4 666,72.6" style="fill:var(--change)"/>
<text x="595" y="60.0" text-anchor="middle" style="fill:var(--change);font-size:10.5px;font-weight:600">move</text>
<text x="595" y="85.0" text-anchor="middle" style="fill:var(--text-2);font-size:10.5px">$from: "port"</text>
<rect x="410" y="104" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="464.0" y="125.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">api.config</text>
<rect x="672" y="104" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="726.0" y="125.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">api.config</text>
<line x1="518" y1="121.0" x2="672" y2="121.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="672,121.0 666,117.4 666,124.6" style="fill:var(--text-3)"/>
<text x="595" y="112.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">same</text>
<rect x="410" y="156" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="464.0" y="177.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">zones.eu</text>
<rect x="672" y="156" width="108" height="34" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="726.0" y="177.5" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:500">zones.eu</text>
<line x1="518" y1="173.0" x2="672" y2="173.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="672,173.0 666,169.4 666,176.6" style="fill:var(--text-3)"/>
<text x="595" y="164.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">same</text>
<line x1="390" y1="8" x2="390" y2="200" style="stroke:var(--line-strong);stroke-width:1" stroke-dasharray="2 4"/>
<text x="190" y="240" text-anchor="middle" style="fill:var(--text-2);font-size:11.5px">Plan: 1 to add, 0 to change, 1 to destroy.</text>
<text x="600" y="240" text-anchor="middle" style="fill:var(--text-2);font-size:11.5px">Plan: 0 to add, 0 to change, 0 to destroy.</text>
</svg>
:::

## What renaming does

Rename `port` to `listen` and keep everything else.

```cue file=port.cue
package estate

listen: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # listen (random_integer) will be created
  + listen: {
  +     id:     (known after apply)
  +     max:    29999
  +     min:    20000
  +     result: (known after apply)
  +     seed:   "web"
    }

  # port (random_integer) will be destroyed
  - port: {
  -     id:     "26720"
  -     max:    29999
  -     min:    20000
  -     result: 26720
  -     seed:   "web"
    }

Plan: 1 to add, 0 to change, 1 to destroy.
Hint: listen looks like port under a new address; to keep the existing object, add $from: "port" to listen
Graph: planned 1 · skipped 7 · removed 1 · within refresh age 8
```

The plan destroys the object at the old address and creates one at the new address. It also prints a hint, because the two have the same type and the same configuration. After the apply, the object at `listen` is a new object.

`$from` says that the record at another address is the record of this resource.

```cue file=port.cue
package estate

listen: $random.#Resource.random_integer & {
	$from: "port"
	min:   20000
	max:   29999
	seed:  "web"
}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # port will be moved to listen (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
Graph: planned 0 · skipped 8 · within refresh age 8 · to move 1
```

The plan lists the rename and changes nothing else, and it writes nothing: the state holds `port` until the apply makes the move.

```sh run
pravo apply --auto-approve | grep '^Apply complete'
pravo state list
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
api.config
api.secret
listen
readme
replicas[0]
replicas[1]
zones."us-east"
zones.eu
```

A move whose old record is gone is complete and does nothing. Leave `$from` in the file or remove it. If both addresses hold a record, the plan is an error that tells you to remove one with `pravo state rm`. The type must stay the same.

### Moving a group

`$from` on a struct that holds resources is a prefix move. Every record below the old path moves to the same place below the struct. The definition of the struct must accept the field, so it embeds `pravo.#Movable`.

```cue file=service.cue
package estate

import "pravo.dev/pravo"

#Service: {
	pravo.#Movable
	name: string
	secret: $random.#Resource.random_integer & {min: 1000, max: 9999, seed: name}
	config: $local.#Resource.local_file & {
		filename: "\(name).conf"
		content:  "name = \(name)\n"
	}
}

backend: #Service & {$from: "api", name: "api"}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # api.config will be moved to backend.config (record renamed; no change to the object)

  # api.secret will be moved to backend.secret (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 2 to move.
Graph: planned 0 · skipped 8 · within refresh age 8 · to move 2
```

The apply makes both moves:

```sh run
pravo apply --auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
```

### Position and key

A key is a name. A position is a number. Reordering the elements of a list changes which element sits at which address, so it changes which object each address stands for.

```cue file=keyed.cue
package estate

zones: {
	for z in ["us-east", "eu"] {
		"\(z)": $random.#Resource.random_integer & {min: 1, max: 9, seed: z}
	}
}

replicas: [
	for n in ["beta", "alpha"] {
		$random.#Resource.random_integer & {min: 1, max: 9, seed: n}
	},
]
```

```sh run
pravo plan | grep -E '^(  # |Plan:)'
```

```text out
  # replicas[0] (random_integer) must be replaced
  # replicas[1] (random_integer) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
```

The keys of `zones` were reordered and nothing changed. The elements of `replicas` were reordered and both were replaced. Use keys for resources that you create from a collection.

## What does not set an address

No field sets an address by hand. Three things relate an address to an object that already has another name.

- `$from` states that the record of this resource had another address. It is reviewed with the rest of the change and recorded in the state.
- `pravo state mv OLD NEW` renames a record directly. It does not check the new address against the specification. A misspelt address moves the record to a place that nothing declares, and the next plan destroys it.
- `$import` and `pravo import ADDRESS ID` bind an address to an object that exists and has no record.

A state that `pravo state import-tfstate` converts keeps the names of the converted state: `random_pet.logs` for a managed resource and `data.aws_region.current` for a data source. An instance made with `count` or `for_each` gets `<name>_<key>`, and a resource inside a module gets the module path as a prefix. `$from` or `pravo state mv` points each of these records at its address in the specification.

## Recap

- The address of a resource is its path. The state keeps one record for each address.
- A rename, a move into a definition and a change of position change the address. Without `$from`, the plan creates one object and destroys another.
- Keys are stable names. Positions are not.

Next: [Plans](plans.md) explains how pravo compares the specification with the records.
