---
title: "Drift: find and reconcile changes"
description: Change a resource outside pravo, find the change with pravo drift, and reconcile it in both directions.
order: 39
group: Track B · pravo
minutes: 12
needs: B8
learn: What is drift? How do you find it, and how do you reconcile it?
done_when: "`pravo drift` exits 2 after a file is deleted and exits 0 after the specification and the world agree again."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: '"time": "[^"]*"'
    replace: '"time": "<time>"'
---

A colleague deletes a file, a bucket or a rule from the console. Which command tells you, and how soon?

Drift is a difference between the state and the real world that no pravo command caused. OpenTofu reports it with a refresh-only plan. pravo has a command for it. A plan reads the resources it reaches and the resources they read. It reads a resource only when its record is older than the refresh age. This lesson shows how that can hide a deletion.

:::compare
```sh title="OpenTofu"
tofu plan -refresh-only -detailed-exitcode
```

```sh title="pravo"
pravo drift
```
:::

`pravo drift` reads the real state of every resource, whatever its refresh age, and reports the ones that changed. This lesson causes drift with a shell and reads it. No cloud account is involved: the resources are files.

:::figure Plan compares the specification with the state. Drift compares the state with the world.
<svg font-size="13" viewBox="0 0 560 170" role="img" aria-label="Three boxes in a row: the specification, the state and the world. A bracket under the first two is labelled pravo plan. A bracket under the last two is labelled pravo drift.">
<g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="6" y="20" width="150" height="56"/><rect x="205" y="20" width="150" height="56"/><rect x="404" y="20" width="150" height="56"/><path d="M156 48H205M355 48H404"/><path d="M81 90V100H274V90"/></g>
<g fill="none" style="stroke: var(--change)" stroke-width="1.5"><path d="M286 90V100H479V90"/></g>
<g fill="currentColor"><path d="M197 44l8 4-8 4zM396 44l8 4-8 4z"/></g>
<text x="24" y="53" style="fill: var(--text)">specification</text>
<text x="241" y="53" style="fill: var(--text)">state</text>
<text x="441" y="53" style="fill: var(--text)">world</text>
<text x="81" y="124">pravo plan</text>
<text x="81" y="142">the specification</text>
<text x="81" y="160">against the state</text>
<text x="286" y="124" style="fill: var(--change)">pravo drift</text>
<text x="286" y="142">the state against</text>
<text x="286" y="160">the world, exit 2</text>
</svg>
:::

## Set a refresh age

Work in an empty directory. A plan reads a resource from its provider only when it looks at the resource, which means that it reaches it or reaches a resource that reads it, and its record is older than the **refresh age**. The default is zero, so a plan reads every resource it looks at. This root sets one hour. A longer refresh age makes a plan read fewer records and trust older ones.

```cue file=main.cue title="main.cue" hl="4,8"
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

```sh run
pravo init
pravo apply --auto-approve
pravo drift
```

```text out
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

```sh run
rm motd.txt
```

:::predict Does pravo plan notice that the file is gone?
The records are a few seconds old and the refresh age is one hour.

::reveal
No. The plan trusts a record that is within the refresh age and does not ask the provider.

```sh run
pravo plan
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 0 · skipped 2 · within refresh age 2
```

The `Graph:` line says `2 within refresh age`. Neither record was read.
:::

`--refresh=all` reads every resource, whatever its age. The other values are `age`, the default, `expired` and `false`. `expired` reads every record that is past its age, and `false` reads nothing.

```sh run
pravo plan --refresh=all
```

```text out
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

```sh run exit=2
pravo drift
```

```text out
Drift. 1 of 2 resources changed outside pravo:
  motd: create
    - refresh found the real state differs from the state (drift)
```

Exit code 2 means drift was found. Exit code 0 means none. A CI job reads the code and the JSON.

```sh run exit=2
pravo drift --json
```

```text out
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

:::figure `motd` after `pravo drift`: the record says that the object is gone, and the state still holds what the file had.
<a class="shot shot-dark" href="../img/serve/b09-drift-dark.png"><img src="../img/serve/b09-drift-dark.png" width="1440" height="900" alt="The viewer with two files, banner and motd, of type local_file. motd is selected and drawn with a red dashed outline and the mark of a broken link. The inspector shows motd, a local_file, with the record gone, in red, the revision 2 and refreshed just now, and the History tab counts 2. The State tab lists the configured attributes: content, welcome, content_base64, null, directory_permission and file_permission, 0777 and computed, filename, motd.txt and required, and source, null. Under Sensitive, sensitive_content is withheld." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/b09-drift-light.png"><img src="../img/serve/b09-drift-light.png" width="1440" height="900" alt="The viewer with two files, banner and motd, of type local_file. motd is selected and drawn with a red dashed outline and the mark of a broken link. The inspector shows motd, a local_file, with the record gone, in red, the revision 2 and refreshed just now, and the History tab counts 2. The State tab lists the configured attributes: content, welcome, content_base64, null, directory_permission and file_permission, 0777 and computed, filename, motd.txt and required, and source, null. Under Sensitive, sensitive_content is withheld." loading="lazy"></a>
:::

## Reconcile: restore

The specification is right and the world is wrong. `pravo drift` left a mark on `motd`: the record says that the file is gone, and the plan acts on that without reading the file again.

```sh run
pravo plan | grep -E '^(Plan|Graph)'
pravo apply --auto-approve | tail -n 1
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 1 · within refresh age 2
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

```sh run
pravo drift
```

```text out
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

Before `pravo drift` read the world, the same apply would have printed `No changes`: the records said that the file exists, and they were young. The mark is what makes the plan act. It stays until an apply writes the resource.

## Not every change is drift

Drift is what the provider reports when it reads the resource. The local provider reads whether the file exists and what it contains. It does not read the file mode.

```sh run
chmod 600 banner.txt
pravo drift
```

```text out
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

A change that the provider cannot see is not drift. That includes this one.

## Exercise

:::exercise Reconcile by accepting
Someone rewrote `banner.txt`. Make the specification say what the file says, and bring the state in line without losing the edit.

```sh title="the edit"
echo "release 2" > banner.txt
```

::hint
Run `pravo drift` first. The provider treats a file whose content differs as gone, so the report says `create`.

::hint
Change `content` of `banner` to the new text. Then apply with `--refresh=all`, and run `pravo drift` again.

::solution
```sh run
echo "release 2" > banner.txt
```

```cue file=main.cue title="main.cue"
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
	content:  "release 2\n"
}
```

```sh run
pravo apply --refresh=all --auto-approve
cat banner.txt
```

```text out
...
banner: no longer exists; it will be created again
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
release 2
```

```sh run check
pravo drift
test "$(cat banner.txt)" = "release 2"
```
:::

Both reconciliations end the same way: the specification, the state and the world agree, and `pravo drift` exits 0.

## Clean up

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- Drift is a change to a resource that no pravo command made. `pravo drift` reads every resource and exits 2 when it finds one.
- A plan reads the resources it reaches, and the resources they read, when their records are older than the refresh age. `--refresh=expired` reads every record that is past its age, and `--refresh=all` reads everything.
- A read that finds drift leaves a mark on the record. The next plan, saved plan and apply act on it until an apply writes the resource.
- `pravo drift --due --limit N` reads the oldest records that are past their refresh age, so a scheduled job covers a large estate in turns.
- Reconcile by applying the specification, or by changing the specification to match the world.

:::check
What exit codes does pravo drift return?
::answer
0 when it finds no drift and 2 when at least one resource changed outside pravo.

Why does pravo plan print No changes after a file is deleted?
::answer
The plan does not reach the resource, and the record is within the refresh age, so the plan does not read the file.
:::

The guide [CI](../how-to/ci.md) shows how a pipeline reads these exit codes. [B10](b10-find-what-nothing-manages.md) finds the objects of the cloud that no specification names.
