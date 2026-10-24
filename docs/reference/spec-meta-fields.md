---
title: Meta fields
description: The $-prefixed fields that resources, data sources and ephemeral resources accept in addition to the provider schema.
order: 27
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[a-z]+(-[a-z]+)+\]'
    replace: '[id=<pet>]'
  - pattern: '"[a-z]+-[a-z]+" -> \(known after apply\)'
    replace: '"<pet>" -> (known after apply)'
keywords: "lifecycle prevent_destroy ignore_changes create_before_destroy depends_on"
---

Meta fields are not part of the provider schema. They all start with `$`, so they never collide with an attribute. A function call has none. The meta fields of a provider struct are described in [Providers](spec-providers.md).

| Field | Type | Offered by | Meaning |
|---|---|---|---|
| `$depends_on` | list of references | resource, data, ephemeral | Dependencies that have no data flow. |
| `$lifecycle` | `#Lifecycle` | resource | `create_before_destroy`, `prevent_destroy`, `ignore_changes`, `refresh`. |
| `$from` | string | resource, data; a group of resources | The address the record had before. See [Moves and imports](spec-moves-and-imports.md). |
| `$import` | string or struct | resource | The id of an existing object to adopt, or its identity. See [Moves and imports](spec-moves-and-imports.md#import). |

```cue
#Lifecycle: {
	create_before_destroy?: bool
	prevent_destroy?:       bool
	ignore_changes?:        [...string] // attribute names, or ["*"] for all
	refresh?:               "always" | "never" | =~"^[0-9]+(ms|s|m|h|d)$"
}
```

Work in an empty directory. This root uses all of `$depends_on` and `$lifecycle`.

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {
	length: 2
	$lifecycle: create_before_destroy: true
}

note: $local.#Resource.local_file & {
	filename:        "note.txt"
	content:         "note"
	file_permission: "0644"
	$depends_on: [pet]
	$lifecycle: {
		ignore_changes: ["file_permission"]
		refresh:        "15m"
	}
}
```

```sh run
pravo init > /dev/null
pravo apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## depends_on

Dependencies are implicit: any reference to another resource in a body makes it a dependency. `$depends_on` adds references for an ordering constraint that has no data flow. `note` does not read `pet`, and still waits for it.

```sh run
pravo state graph
```

```text out
note -> pet
```

The state records the dependency: `note -> pet` reads "`note` depends on `pet`".

The references in `$depends_on` are not counted as a nested resource.

## Lifecycle

| Field | Default | Effect |
|---|---|---|
| `create_before_destroy` | `false` | A replacement creates the new object first, then destroys the old one. |
| `prevent_destroy` | `false` | A plan that replaces the resource is an error. `pravo destroy` and `pravo plan --destroy` refuse while the resource is in the state. |
| `ignore_changes` | none | Changes to the listed attributes, or to all with `["*"]`, are not planned. |
| `refresh` | the [default refresh age](spec-settings.md#refresh) | How old the record may be before a plan reads the resource again. A resource that sets it is read once its record is older than the age, whether or not the plan reaches it. |

Change the length of the pet, which forces a replacement, and the permission of the file, which `ignore_changes` leaves alone:

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {
	length: 3
	$lifecycle: create_before_destroy: true
}

note: $local.#Resource.local_file & {
	filename:        "note.txt"
	content:         "note"
	file_permission: "0600"
	$depends_on: [pet]
	$lifecycle: {
		ignore_changes: ["file_permission"]
		refresh:        "15m"
	}
}
```

```sh run
pravo plan 2>/dev/null | grep -E '^  #|^Plan|^Graph'
```

```text out
  # pet (random_pet) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 2 · refreshed 1 · within refresh age 1
```

The plan replaces `pet` and leaves `note` alone. On the `Graph:` line, `within refresh age` counts `note`, whose `refresh` age of 15 minutes has not run out. `pet` has the default age, `always`, and is read again.

### create_before_destroy

With `create_before_destroy: true` the apply creates the replacement first and destroys the previous instance afterwards:

```sh run
pravo apply --auto-approve 2>&1 | grep -E '^pet: (Creating|Destroying|Replacement)|^Apply'
```

```text out
pet: Creating replacement...
pet: Destroying previous instance... [id=<pet>]
pet: Replacement complete after 0s [id=<pet>]
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

### prevent_destroy

With `prevent_destroy: true`, a plan that replaces the resource stops with an error. The attribute change is not made.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {
	length: 4
	$lifecycle: prevent_destroy: true
}
```

```sh run exit=1
pravo plan
```

```text out
pet: Refreshing state... [id=<pet>]

Error: pet: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set
```

`pravo destroy` and `pravo plan --destroy` refuse as well, before anything is deleted:

```sh run exit=1
pravo plan --destroy
pravo destroy -auto-approve
```

```text out
Error: destroy refused: $lifecycle.prevent_destroy is set on pet; remove the setting from the specification to destroy the resource

Error: destroy refused: $lifecycle.prevent_destroy is set on pet; remove the setting from the specification to destroy the resource
```

`prevent_destroy` is read from the specification. A plan that deletes a resource because it left the specification is not stopped by it: the specification does not hold the field. To refuse deletions of any kind, name a plan policy with `--policy`. See [Writing policies](policies-writing.md#prohibit-with-a-guard).

### ignore_changes

`ignore_changes` takes attribute names. A change to an ignored attribute in the specification is not planned and does not force a replacement. `["*"]` ignores every attribute.

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 3}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "a different text"
	$lifecycle: ignore_changes: ["*"]
}
```

```sh run
pravo plan 2>/dev/null | tail -n 2
```

```text out
No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.
Graph: planned 1 · skipped 1 · refreshed 1 · not read 1
```

### refresh

`refresh` is the refresh age of one resource: `"always"`, `"never"` or a number with a unit (`ms`, `s`, `m`, `h`, `d`). A record within its age is not read from the provider again. A resource that sets the age is read by every plan once its record is older, reached or not; `"always"` makes every plan read it. A value that is not a refresh age is rejected when the specification is evaluated. The command line overrides every age for one run with `-refresh=expired`, `-refresh=all` or `-refresh=false`.

## See also

- [Plans](../concepts/plans.md#refresh-age) explains the refresh age.
- [`pravo destroy`](destroy.md) shows `prevent_destroy` refusing a destroy.
- [Moves and imports](spec-moves-and-imports.md) describes `$from` and `$import`.
