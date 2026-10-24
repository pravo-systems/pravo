---
title: Changing live infrastructure
description: Save a plan, apply exactly that plan or nothing, and apply it in stages. Read the refusal when the plan is stale.
order: 37
group: Track B · pravo
minutes: 15
needs: B6
learn: How do you apply exactly the plan that was reviewed? What happens when the world moves before you apply it?
done_when: A saved plan applies in two stages, and a plan whose files have changed is refused with exit code 3.
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "created [0-9-]+ [0-9:]+"
    replace: "created <time>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
keywords: "saved plan plan -out apply stale plan -target"
---

Two questions about a change to live infrastructure. Does the apply run exactly the plan that was reviewed? Can part of the plan go out today and the rest tomorrow?

OpenTofu answers the second with `-target`, which narrows the plan when it is made. pravo has no `-target`. It saves the whole plan and applies a selection of that file.

:::compare
```sh title="OpenTofu"
tofu plan -target=aws_db_instance.main -out=tfplan
tofu apply tfplan
```

```sh title="pravo"
pravo plan --out change.plan
pravo apply change.plan --only db
```
:::

What runs is always part of a plan that was reviewed as a whole. This lesson saves a plan, applies it in stages and reads the refusals.

## Save a plan

Work in an empty directory. This root has a release number and a manifest file that reads it. The `env` tag from [B4](b04-inputs-tags-environments.md) names the file.

```cue file=main.cue title="main.cue"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

env: *"dev" | "prod" @tag(env)

release: $random.#Resource.random_integer & {min: 1, max: 99, seed: "v1"}
manifest: $local.#Resource.local_file & {
	filename: "manifest-\(env).txt"
	content:  "release \(release.result)\n"
}
```

```sh run
pravo init
pravo apply --auto-approve
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Change the seed. The release number changes, and the manifest that reads it changes with it.

```cue file=main.cue title="main.cue" hl="13"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

env: *"dev" | "prod" @tag(env)

release: $random.#Resource.random_integer & {min: 1, max: 99, seed: "v2"}
manifest: $local.#Resource.local_file & {
	filename: "manifest-\(env).txt"
	content:  "release \(release.result)\n"
}
```

`--out` writes the plan to a file instead of only printing it.

```sh run
pravo plan --out change.plan
```

```text out
...
pravo will perform the following actions:

  # release (random_integer) must be replaced
-/+ release: {
  ~     id:      "15" -> (known after apply)
        max:     99
        min:     1
  ~     result:  15 -> (known after apply)
  ~     seed:    "v1" -> "v2" # forces replacement
    }

  # manifest (local_file) must be replaced
-/+ manifest: {
  ~     content:              "release 15\n" -> (known after apply) # forces replacement
...
    }

Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · refreshed 2

Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

The file is the plan. `pravo show` prints it again with the time and the command that made it.

```sh run
pravo show change.plan
```

```text out
Saved plan created <time> by "pravo plan --out change.plan"

pravo will perform the following actions:

  # release (random_integer) must be replaced
-/+ release: {
  ~     id:      "15" -> (known after apply)
        max:     99
        min:     1
  ~     result:  15 -> (known after apply)
  ~     seed:    "v1" -> "v2" # forces replacement
    }
...
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · refreshed 2
```

The plan carries the tags it was made with, so a tag cannot change at apply time.

```sh run exit=1
pravo apply change.plan -t env=prod
```

```text out
Error: -t and -T cannot be combined with a saved plan; the plan carries its own tags
```

## A plan applies only to what it was made for

:::predict You add a comment to main.cue after saving the plan. Does pravo apply change.plan still run?
The plan records a fingerprint of the files it was made from. Decide whether a comment counts.

::reveal
No. The fingerprint covers every file that was loaded for the root, so a comment is a change. The run is refused with exit code 3 and nothing is applied.

```sh run
echo "// a comment" >> main.cue
```

```sh run exit=3
pravo apply change.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
```

The reason is listed under the error. `pravo show` prints the same reason as a warning above the plan. Use it to check a plan before you apply it.

```sh run
pravo show change.plan | head -n 2
```

```text out
Warning: this plan can no longer be applied: the configuration changed since the plan was made (main.cue changed)
Saved plan created <time> by "pravo plan --out change.plan"
```

Take the comment out again. The plan is valid when the files are the files it was made from.

```sh run
grep -v '^// a comment$' main.cue > main.next && mv main.next main.cue
pravo show change.plan | head -n 2
```

```text out
Saved plan created <time> by "pravo plan --out change.plan"

```
:::

The fingerprint also covers packages elsewhere in the module that the root imports. A package the root does not import has no effect on it.

## Apply in stages

You can apply a part of the plan. The part must be closed: it must include everything it depends on that is still pending. The manifest reads the release, so the manifest alone is refused.

```sh run exit=1
pravo apply change.plan --only manifest
```

```text out
Error: the selection is not closed under dependencies; it also needs release
  (the smallest closed selection is -only manifest,release)
```

The message gives the smallest selection that works. Nothing ran. Select the release.

```sh run
pravo apply change.plan --only release
```

```text out
Applying 1 of 2 remaining changes from change.plan.
release: Destroying... [id=15]
release: Creating replacement...
release: Replacement complete after 0s [id=24]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
1 change remains in change.plan; apply with "pravo apply change.plan".
```

The release is replaced and the manifest is not. The plan file keeps the rest, and `pravo show` marks the part that is applied.

```sh run
pravo show change.plan
```

```text out
Saved plan created <time> by "pravo plan --out change.plan"

pravo will perform the following actions:

  # release (random_integer) must be replaced (already applied in txn <txn>)
-/+ release: {
...
    }

  # manifest (local_file) must be replaced
...
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · refreshed 2
```

Check the new release. The manifest still holds the old number.

```sh run
cat manifest-dev.txt
pravo state show release | grep -o 'result:.*'
```

```text out
release 15
result:  24
```

Review whatever you need. Then apply what remains.

```sh run
pravo apply change.plan
cat manifest-dev.txt
```

```text out
manifest: Destroying... [id=5fc55c580ff743f43f93b9139b115b7bbfe8610c]
manifest: Creating replacement...
manifest: Replacement complete after 0s [id=9a8ecf4b7af5fb7483b46efde91bb596bfaa94a6]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
release 24
```

Each resource is planned again by its provider immediately before it is applied. A provider that plans something different at that point stops the apply.

## Exercise

:::exercise Let the world move
Add a second file to the root and save a plan for it. Then apply the specification without the plan, the way a colleague applies it. Apply your saved plan and read the refusal.

```cue title="audit"
audit: $local.#Resource.local_file & {
	filename: "audit-\(env).txt"
	content:  "audit of release \(release.result)\n"
}
```

::hint
Save the plan with `pravo plan --out audit.plan`. Apply the specification with `pravo apply --auto-approve`. The plan file is not an argument of that command.

::hint
The refusal names a record, the revision it has now, and the revision the plan expected. A record that did not exist when the plan was made has revision 0.

::solution
```cue file=main.cue title="main.cue"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

env: *"dev" | "prod" @tag(env)

release: $random.#Resource.random_integer & {min: 1, max: 99, seed: "v2"}
manifest: $local.#Resource.local_file & {
	filename: "manifest-\(env).txt"
	content:  "release \(release.result)\n"
}

audit: $local.#Resource.local_file & {
	filename: "audit-\(env).txt"
	content:  "audit of release \(release.result)\n"
}
```

```sh run
pravo plan --out audit.plan
```

```text out
...
  # audit (local_file) will be created
  + audit: {
  +     content:              "audit of release 24\n"
...
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 1

Saved the plan to audit.plan. To apply exactly these actions, run:
  pravo apply audit.plan
```

```sh run
pravo apply --auto-approve
```

```text out
...
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 1
...
audit: Creating...
audit: Creation complete after 0s [id=3fe13eb17d7ee300fd5f4871245bcc6e47abed5a]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

```sh run exit=3
pravo apply audit.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - audit changed in state (revision 1, plan expected 0)
```

```sh run check
pravo apply audit.plan || test $? -eq 3
```
:::

The first apply created `audit`. The saved plan expected to create it, found it, and refused. Plan again to get a plan that matches the world.

## Clean up

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

If you added `audit` in the exercise, the summary says 3.

## Recap

- A saved plan is a file. `pravo apply <file>` runs exactly its changes or refuses.
- A plan is refused, with exit code 3, when the files or a record it touches have changed.
- `--only` applies a closed part of a plan and leaves the rest in the file.

:::check
Why is `--only manifest` refused when the release is pending?
::answer
The manifest reads the release. A selection must include every pending change it depends on.

Which exit code means a saved plan is stale?
::answer
Exit code 3. The error lists each reason.
:::

Next, [B8](b08-renames-moves-imports.md) changes what an address means.
