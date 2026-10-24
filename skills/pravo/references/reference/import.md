<!-- Generated from docs/reference/import.md by tools/skillgen. Do not edit. -->

# pravo import

Bring an existing object under management from the command line, by the address of a declared resource and the provider's import id.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo import ADDR ID` |
| Changes infrastructure | No. The provider reads the object. |
| Writes | One node in the state, in a transaction of kind `import`. |
| Requires | `ADDR` is a managed resource declared in the specification and not yet in the state. The provider supports import for its type. |
| Taught in | [B8, Renames, moves, imports](../learn/b08-renames-moves-imports.md). |

`ADDR` is the address of the resource. `ID` is the import id of the provider, which its documentation gives for each type.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |

The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The object was recorded. |
| 1 | The resource is not declared, is not a managed resource, or is already in the state. The provider returned nothing or reports that the object does not exist. The number of arguments is not two. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. Write the resource first. `random_integer` imports an id of the form `result,min,max`.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

dice: $random.#Resource.random_integer & {min: 1, max: 6}
```

A wrong id is the provider's error, printed as it arrives. Nothing is recorded.

```sh
pravo init > /dev/null
pravo import dice nonsense
```

Output:

```text
Error: Import Random Integer Error: Invalid import usage: expecting {result},{min},{max} or {result},{min},{max},{seed}
```

The right id records the node.

```sh
pravo import dice 4,1,6
```

Output:

```text
dice: Importing from ID "4,1,6"...
dice: Import complete. Run "pravo plan" to compare it with the configuration.
```

The node now holds the attributes that the provider read.

```sh
pravo state show dice
```

Output:

```text
// dice (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 1)
// refreshed <age> ago, applied never, config hash none
{
	id:      "4"
	keepers: null
	max:     6
	min:     1
	result:  4
	seed:    null
}
```

The next plan compares the specification with the object. A plan that proposes a change shows where they differ.

```sh
pravo plan
```

Output:

```text
dice: Refreshing state... [id=4]

No changes. Your infrastructure matches the configuration.
Graph: planned 1 · refreshed 1
```

### Refusals

An address that the specification does not declare, and an address that is already in the state, are errors:

```sh
pravo import nope 4,1,6
```

Output:

```text
Error: nope is not declared in the configuration; add it before importing
```

```sh
pravo import dice 4,1,6
```

Output:

```text
Error: dice is already in state
```

### The declarative form

`$import: "ID"` on the resource adopts the object at the next plan and needs no command:

```cue
dice: $random.#Resource.random_integer & {min: 1, max: 6, $import: "4,1,6"}
```

The plan reports `dice was imported from id "4,1,6"`. Leave the field in place afterwards. See [Moves and imports](spec-moves-and-imports.md) and [Importing and moving](../how-to/importing-and-moving.md).

## See also

- [`pravo state`](state.md) moves, forgets and queries nodes.
- [`pravo plan`](plan.md) adopts `$import` objects.
