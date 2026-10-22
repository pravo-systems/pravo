<!-- Generated from docs/learn/b07-changing-live-infrastructure.md by tools/skillgen. Do not edit. -->

# Changing live infrastructure

Save a plan, apply exactly that plan or nothing, and apply it in stages. Read the refusal when the plan is stale.

Two questions about a change to live infrastructure. Does the apply run exactly the plan that was reviewed? Can part of the plan go out today and the rest tomorrow?

OpenTofu answers the second with `-target`, which narrows the plan when it is made. pravo has no `-target`. It saves the whole plan and applies a selection of that file.

```sh
tofu plan -target=aws_db_instance.main -out=tfplan
tofu apply tfplan
```

```sh
pravo plan --out change.plan
pravo apply change.plan --only db
```

What runs is always part of a plan that was reviewed as a whole. This lesson saves a plan, applies it in stages and reads the refusals.

## Save a plan

Work in an empty directory. This root has a release number and a manifest file that reads it. The `env` tag from [B4](b04-inputs-tags-environments.md) names the file.

File `main.cue`:

```cue
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

```sh
pravo init
pravo apply --auto-approve
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Change the seed. The release number changes, and the manifest that reads it changes with it.

File `main.cue`:

```cue
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

```sh
pravo plan --out change.plan
```

Output:

```text
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
Graph: planned 2 of 2, 2 refreshed.

Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

The file is the plan. `pravo show` prints it again with the time and the command that made it.

```sh
pravo show change.plan
```

Output:

```text
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
Graph: planned 2 of 2, 2 refreshed.
```

The plan carries the tags it was made with, so a tag cannot change at apply time.

```sh
pravo apply change.plan -t env=prod
```

Output:

```text
Error: -t and -T cannot be combined with a saved plan; the plan carries its own tags
```

## A plan applies only to what it was made for

The fingerprint also covers packages elsewhere in the module that the root imports. A package the root does not import has no effect on it.

## Apply in stages

You can apply a part of the plan. The part must be closed: it must include everything it depends on that is still pending. The manifest reads the release, so the manifest alone is refused.

```sh
pravo apply change.plan --only manifest
```

Output:

```text
Error: the selection is not closed under dependencies; it also needs release
  (the smallest closed selection is -only manifest,release)
```

The message gives the smallest selection that works. Nothing ran. Select the release.

```sh
pravo apply change.plan --only release
```

Output:

```text
Applying 1 of 2 remaining changes from change.plan.
release: Destroying... [id=15]
release: Creating replacement...
release: Replacement complete after 0s [id=24]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
1 change remains in change.plan; apply with "pravo apply change.plan".
```

The release is replaced and the manifest is not. The plan file keeps the rest, and `pravo show` marks the part that is applied.

```sh
pravo show change.plan
```

Output:

```text
Saved plan created <time> by "pravo plan --out change.plan"

pravo will perform the following actions:

  # release (random_integer) must be replaced (already applied in txn <txn>)
-/+ release: {
...
    }

  # manifest (local_file) must be replaced
...
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 2, 2 refreshed.
```

Check the new release. The manifest still holds the old number.

```sh
cat manifest-dev.txt
pravo state show release | grep -o 'result:.*'
```

Output:

```text
release 15
result:  24
```

Review whatever you need. Then apply what remains.

```sh
pravo apply change.plan
cat manifest-dev.txt
```

Output:

```text
manifest: Destroying... [id=5fc55c580ff743f43f93b9139b115b7bbfe8610c]
manifest: Creating replacement...
manifest: Replacement complete after 0s [id=9a8ecf4b7af5fb7483b46efde91bb596bfaa94a6]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
release 24
```

Each resource is planned again by its provider immediately before it is applied. A provider that plans something different at that point stops the apply.

## Exercise

The first apply created `audit`. The saved plan expected to create it, found it, and refused. Plan again to get a plan that matches the world.

## Clean up

```sh
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

If you added `audit` in the exercise, the summary says 3.

## Recap

- A saved plan is a file. `pravo apply <file>` runs exactly its changes or refuses.
- A plan is refused, with exit code 3, when the files or a record it touches have changed.
- `--only` applies a closed part of a plan and leaves the rest in the file.

Next, [B8](b08-renames-moves-imports.md) changes what an address means.
