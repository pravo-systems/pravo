<!-- Generated from docs/learn/b09-drift.md by tools/skillgen. Do not edit. -->

# Drift: find and reconcile changes

Change a resource outside pravo, find the change with pravo drift, and reconcile it in both directions.

A colleague deletes a file, a bucket or a rule from the console. Which command tells you, and how soon?

Drift is a difference between the state and the real world that no pravo command caused. OpenTofu reports it with a refresh-only plan. pravo has a command for it. A plan reads the resources it reaches and the resources they read. It reads a resource only when its record is older than the refresh age. This lesson shows how that can hide a deletion.

```sh
tofu plan -refresh-only -detailed-exitcode
```

```sh
pravo drift
```

`pravo drift` reads the real state of every resource, whatever its refresh age, and reports the ones that changed. This lesson causes drift with a shell and reads it. No cloud account is involved: the resources are files.

*Figure: Plan compares the specification with the state. Drift compares the state with the world.*

## Set a refresh age

Work in an empty directory. A plan reads a resource from its provider only when it looks at the resource, which means that it reaches it or reaches a resource that reads it, and its record is older than the **refresh age**. The default is zero, so a plan reads every resource it looks at. This root sets one hour. A longer refresh age makes a plan read fewer records and trust older ones.

File `main.cue`:

```cue
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$local: loc.#Provider

motd: $local.#Resource.local_file & {
	filename: "motd.txt"
	content:  "welcome\n"
}

banner: $local.#Resource.local_file & {
	filename: "banner.txt"
	content:  "release 1\n"
}
```

```sh
pravo init
pravo apply --auto-approve
pravo drift
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

`No drift` and exit code 0. The state and the files agree.

## Break the world

Delete `motd.txt`, as someone with a shell does.

```sh
rm motd.txt
```

`--refresh=all` reads every resource, whatever its age. The other values are `age`, the default, `expired` and `false`. `expired` reads every record that is past its age, and `false` reads nothing.

```sh
pravo plan --refresh=all
```

Output:

```text
...
motd: no longer exists; it will be created again
...
  # motd (local_file) will be created
  + motd: {
  +     content:              "welcome\n"
...
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2 · drifted 1
```

The plan creates `motd`, and the `Graph:` line counts one drifted resource. `pravo drift` always reads everything and answers with an exit code.

```sh
pravo drift
```

Output:

```text
Drift. 1 of 2 resources changed outside pravo:
  motd: create
    - refresh found the real state differs from the state (drift)
```

Exit code 2 means drift was found. Exit code 0 means none. A CI job reads the code and the JSON.

```sh
pravo drift --json
```

Output:

```text
{
  "time": "<time>",
  "root": ".",
  "drifted": [
    {
      "addr": "motd",
      "action": "create",
      "reasons": [
        "refresh found the real state differs from the state (drift)"
      ],
      "gone": true
    }
  ],
  "pending_changes": 1,
  "summary": {
    "add": 1,
    "change": 0,
    "destroy": 0,
    "read": 0,
    "blast_radius": 1
  }
}
```

`gone` means the provider found no object. `pending_changes` is the number of changes in the plan.

The viewer reads the same mark. `pravo serve` draws `motd` with the outline of a record whose object is gone, and the inspector says gone where it said in sync, over the file name and content that the state still holds.

*Figure: motd after pravo drift: the record says that the object is gone, and the state still holds what the file had.*

## Reconcile: restore

The specification is right and the world is wrong. `pravo drift` left a mark on `motd`: the record says that the file is gone, and the plan acts on that without reading the file again.

```sh
pravo plan | grep -E '^(Plan|Graph)'
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 1 · within refresh age 2
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

```sh
pravo drift
```

Output:

```text
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

Before `pravo drift` read the world, the same apply would have printed `No changes`: the records said that the file exists, and they were young. The mark is what makes the plan act. It stays until an apply writes the resource.

## Not every change is drift

Drift is what the provider reports when it reads the resource. The local provider reads whether the file exists and what it contains. It does not read the file mode.

```sh
chmod 600 banner.txt
pravo drift
```

Output:

```text
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

A change that the provider cannot see is not drift. That includes this one.

## Exercise

Both reconciliations end the same way: the specification, the state and the world agree, and `pravo drift` exits 0.

## Clean up

```sh
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- Drift is a change to a resource that no pravo command made. `pravo drift` reads every resource and exits 2 when it finds one.
- A plan reads the resources it reaches, and the resources they read, when their records are older than the refresh age. `--refresh=expired` reads every record that is past its age, and `--refresh=all` reads everything.
- A read that finds drift leaves a mark on the record. The next plan, saved plan and apply act on it until an apply writes the resource.
- `pravo drift --due --limit N` reads the oldest records that are past their refresh age, so a scheduled job covers a large estate in turns.
- Reconcile by applying the specification, or by changing the specification to match the world.

The guide [CI](../how-to/ci.md) shows how a pipeline reads these exit codes. [B10](b10-policy.md) turns to rules about what a plan may do.
