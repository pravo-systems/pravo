<!-- Generated from docs/concepts/resources-and-addresses.md by tools/skillgen. Do not edit. -->

# Resources and addresses

What makes a struct a resource, how its address is its path, and what renaming, moving or reordering does to the record of an object.

A resource is a struct that carries the identity of a provider type. Its address is the path at which it sits in the specification. The address is the only name that pravo uses to find the record of the object in the state.

## The address is the path

pravo finds a resource by its identity and names it by its path. The path of `api.config` is the field `config` inside the field `api`. Work in an empty directory. The files below declare one resource of each shape.

File `providers.cue`:

```cue
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

File `port.cue`:

```cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
```

File `service.cue`:

```cue
package estate

// A definition that contains resources. Its instance is a module.
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

File `keyed.cue`:

```cue
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

File `readme.cue`:

```cue
package estate

readme: $local.#Data.local_file & {filename: "port.cue"}
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

The plan header of each change shows the address.

```sh
pravo plan | grep '^  # '
```

Output:

```text
  # api.secret (random_integer) will be created
  # api.config (local_file) will be created
  # port (random_integer) will be created
  # zones.eu (random_integer) will be created
  # zones."us-east" (random_integer) will be created
  # replicas[0] (random_integer) will be created
  # replicas[1] (random_integer) will be created
```

After an apply, the state holds one record for each address.

```sh
pravo apply -auto-approve | tail -1
```

Output:

```text
Apply complete! Resources: 7 added, 0 changed, 0 destroyed. [txn <id>]
```

```sh
pravo state list
```

Output:

```text
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

```sh
pravo state show 'zones."us-east"' | head -1
```

Output:

```text
// zones."us-east" (provider registry.opentofu.org/hashicorp/random v<v> as random, schema version 0, revision 1)
```

A data source, a resource and an ephemeral resource share one namespace of paths. There is no `data.` prefix. The record says what it is.

```sh
pravo state query 'resources.readme.mode'
```

Output:

```text
"data"
```

These commands take an address: `pravo state show`, `pravo state rm`, `pravo state mv`, `pravo state dependents`, `pravo plan -replace`, `pravo why` and `pravo import`.

## An address stands for one object

The state keeps one record for each address. The record holds the provider, the type and the attributes of the object. The type is a field of the record. It is not part of the name.

pravo matches the specification and the state by address and by nothing else.

- An address in the specification with no record is a create.
- A record with no address in the specification is a delete.
- An address with a record is compared by its configuration.

*Figure: Without $from, a renamed resource is a create and a delete. With $from, the record changes its address and the object is untouched.*

## What renaming does

Rename `port` to `listen` and keep everything else.

File `port.cue`:

```cue
package estate

listen: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
```

```sh
pravo plan
```

Output:

```text
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
Graph: planned 1 of 8, 7 unchanged and skipped, 1 removed from the specification, 8 within refresh age.
```

The plan destroys the object at the old address and creates one at the new address. It also prints a hint, because the two have the same type and the same configuration. After the apply, the object at `listen` is a new object.

`$from` says that the record at another address is the record of this resource.

File `port.cue`:

```cue
package estate

listen: $random.#Resource.random_integer & {
	$from: "port"
	min:   20000
	max:   29999
	seed:  "web"
}
```

```sh
pravo plan
```

Output:

```text
port: moved to listen

pravo will perform the following actions:

  # port was moved to listen (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 0 of 8, 8 unchanged and skipped, 8 within refresh age, 1 moved.
```

The plan renames the record and changes nothing else. A plan makes the move when it runs, so the state already holds `listen`.

```sh
pravo state list
```

Output:

```text
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

File `service.cue`:

```cue
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

```sh
pravo plan
```

Output:

```text
api.config: moved to backend.config
api.secret: moved to backend.secret

pravo will perform the following actions:

  # api.config was moved to backend.config (record renamed; no change to the object)

  # api.secret was moved to backend.secret (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 0 of 8, 8 unchanged and skipped, 8 within refresh age, 2 moved.
```

### Position and key

A key is a name. A position is a number. Reordering the elements of a list changes which element sits at which address, so it changes which object each address stands for.

File `keyed.cue`:

```cue
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

```sh
pravo plan | grep -E '^(  # |Plan:)'
```

Output:

```text
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
