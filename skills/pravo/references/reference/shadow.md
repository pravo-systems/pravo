<!-- Generated from docs/reference/shadow.md by tools/skillgen. Do not edit. -->

# pravo shadow

Decompile an OpenTofu or Terraform state into a specification that plans clean, keep it beside the Terraform root, hand the objects over, or write them back.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo shadow STATE`, `pravo shadow sync`, `pravo shadow take-over`, `pravo shadow status`, `pravo shadow back` |
| Changes infrastructure | No. The provider reads the objects. |
| Writes | `shadow.cue`, `shadow.lock.json`, and the state of the root: one record for each resource of `STATE`, in a transaction of kind `shadow`, and an apply with no changes that stores the hash of each configuration. In a sealed state also one secret for each sensitive value that the specification leaves out. The state file is read and never written. |
| Requires | A specification that declares the providers of `STATE`, and `pravo init`. |
| Reads | A state file, `-` for standard input, or `s3://BUCKET/KEY` for the state that an S3 backend of OpenTofu or Terraform holds. Format version 4. |
| Taught in | [Migrate](https://pravo.systems/migrate/index.html). A shadow replaces the translation of steps 4 and 5 for a first look. |

`pravo shadow` is the shortest way to see an OpenTofu or Terraform estate planned by pravo. It reads no HCL. Every value in a state is evaluated, so the state is enough to write the resources, and the provider says whether what was written restates the object.

## What it does

For each managed resource of the state, the command:

1. Writes one resource of the specification, in a struct for each module instance, with the address that OpenTofu used as a comment.
2. Reads the object through its provider, so that the specification states what the object is now. A record of the state that was behind its object is brought up to date.
3. Writes the references that the values show. A string that equals the `id`, the `arn` or another computed value of a dependency becomes a reference to it, a long value inside a string becomes an interpolation, and a dependency that no value shows goes to `$depends_on`. The report counts every edge of the state: by reference, by interpolation, implied by those, in `$depends_on`, to data sources and lost.
4. Keeps secrets out of the source. A sensitive attribute is left out and named in `$lifecycle.ignore_changes`, so the object keeps its value. The provider decides which attributes are sensitive. Where it refuses the resource without one, a placeholder stands in its place. In a [sealed state](protect.md) the value is also kept in the vault, as the secret `shadow/ADDRESS/ATTRIBUTE`: see [Sensitive values](#sensitive-values).
5. Asks the provider about every resource: it validates it and plans it against its object. What the provider refuses, or computes in another form, is left out, or left to the provider with `ignore_changes`, until the plan is empty. The command needs no list of cases for a type. A comment in `shadow.cue` says why each attribute was left out.
6. Leaves out what the provider derives anyway, so that the specification holds what a person would write.
7. Writes the records of the objects, plans the root as `pravo plan` does, and applies that plan, which has no changes, so that each record holds the hash of the configuration the provider has seen. The plans after it read only what a change reaches.

The last step is the proof. The command ends with `Verified: the plan of the specification says No changes`, or exits with 1 and lists what the plan wants to change.

Data sources are not part of the specification. Their values are written as constants, and the report counts them.

## Whose objects they are

The root that a shadow wrote stands beside the OpenTofu root, and OpenTofu still owns the objects. Until the objects are handed over, `pravo apply` refuses a plan that creates, changes, replaces or destroys an object, with exit code 5. So do the commands that change what a record says about an object: `pravo import`, `pravo state rm`, `pravo state mv` and `pravo state import-tfstate`. Plans are not refused, so the shadow can be planned as often as you like, from any number of machines, while OpenTofu goes on applying.

The owner is kept in the state, not in a file beside the specification. The transaction that wrote the records has the kind `shadow`, and `pravo shadow take-over` writes one of the kind `handover`; the newest of them says whose the objects are. In a sealed state both are signed, and a hand-over that no reader signed does not count. Deleting `shadow.lock.json` therefore takes nothing over: that file only remembers where the state came from and what the shadow wrote.

`pravo shadow status` says who the owner is, and warns when the state has moved since the shadow was taken: a new serial means that the shadow is behind. [Keep it in step](#keep-it-in-step) takes it again without losing what you edited.

`pravo shadow take-over` hands the objects to pravo. It refuses when the state moved. After it `pravo apply` changes objects. Stop applying the OpenTofu root: remove its backend or lock it. A root that was handed over is not shadowed again, since a shadow would replace the records that pravo changed since with those of the state file.

`pravo shadow back` is the way back. It writes the records of this root into a copy of the OpenTofu state, one instance at a time, with the serial raised by one. What the shadow left out of the state, such as data sources, stays as it was. A resource that was created after the hand-over has no place in the HCL, and the command lists it. OpenTofu then plans against the copy, so that a change made by pravo is in its state.

## Keep it in step

While OpenTofu goes on applying, its state moves and the shadow falls behind. `pravo shadow sync` reads the state again (the one the shadow came from, unless you name another) and brings the records and `shadow.cue` up to date. It takes the same flags as `pravo shadow`.

You may edit `shadow.cue`. Every resource is a block under a comment with its OpenTofu address, and the note keeps the text of each block as it was written. At a sync, each block is merged by that address, not by line:

| The block | The file after the sync |
|---|---|
| is as the shadow wrote it | the text of the new state |
| is edited, and the state did not change it | your text |
| is edited, and the state changed it | your text, and the command names the block |
| is new in the state | a new block |
| is edited, and its resource left the state | gone, and the command names it |

The plan that ends the command may want changes. If every one of them is in a block that you edited, the command says so and exits 0: they are your edits, and the shadow restates everything else. Any other change is a gap, as in a first shadow, and the command exits 1. Only blocks are kept: resources of your own belong in another file of the package. Without `shadow.lock.json` there is no earlier text to tell an edit from a change of the state, so every block that differs is kept as it is and the command counts them.

## Fold what is alike

`pravo shadow --fold` (and `pravo shadow sync --fold`) writes resources that are alike as one definition and small instances. Six buckets that differ in a name and a tag become:

```cue
// #S3Bucket is what 6 resources of type aws_s3_bucket have in common.
#S3Bucket: $aws.#Resource.aws_s3_bucket & {
	#name!: string
	bucket: "acme-\(#name)"
	tags: Name: "acme-\(#name)"
}

// aws_s3_bucket.logs
logs: #S3Bucket & {#name: "logs"}
```

The folding reads the syntax only. It finds what the bodies have in common, turns what differs into parameters, and writes a definition only where that makes the file shorter: at least three resources, at least 15 percent saved and at most eight parameters. Every address stays, so the records do not move. A value that most instances share becomes the default of its parameter. A resource that is not written in the plain form, such as a module's, is left as it is.

The proof is a plan. The command writes the folded file and plans the root again: each resource whose configuration is the one that the provider has seen is skipped, and one that is not is planned. The folded file stays only when that plan says as little as the plan of the file it came from. Otherwise the file is put back as it was, and the command says so. On an estate of 83 resources on AWS, 7 definitions took 67 of them and the file went from 858 lines to 512.

A folded file is a specification that people write: it keeps no blocks for a sync to merge into. A sync of a folded file that was not touched folds the new state again. A folded file that was edited is refused, with the choice between editing it by hand and `pravo shadow --force`.

## Sensitive values

A sensitive attribute is never written to the specification. Its value stays in the records of the state, which a sealed state seals to its readers. In a sealed state the shadow also keeps each sensitive string value in the vault, as the secret `shadow/ADDRESS/ATTRIBUTE` (`shadow/edge.this/secret`, with the characters that a name does not allow turned into underscores), with a note that says which state and serial it came from. `pravo secret get` reads it, `pravo secret exec` hands it to a command, and `pravo secret history` shows who set each version, so the values can leave the Terraform state with the rest of it. The comment in `shadow.cue` names the secret. The first secret of a state needs a key that may write one: where yours may not, the shadow says that the values stay in the records only.

The vault is for what runs, not for what is recorded: [a secret may not go into an attribute that is recorded](../how-to/secrets.md), so the specification cannot read these values back into the resource. An attribute that the provider insists on keeps its placeholder, and the object keeps the value it has.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out FILE` | `shadow.cue` | The file the resources are written to. |
| `--force` | off | Replace the file, the state and the note of an earlier shadow. A run that fails puts the earlier file back. |
| `--offline` | off | Write from the state without asking the provider anything. The specification may then need repair, and the closing plan says what. |
| `--keep-derived` | off | Also write what the provider derives anyway. |
| `--no-guard` | off | Make the root pravo's at once: apply changes objects with no hand-over. |
| `--no-state` | off | Write the file only: no records, no note and no plan. |
| `--fold` | off | Write resources that are alike as one definition and instances, and keep that only if the plan of the result is as clean. |
| `--flat` | off | Write every resource at the top level, with no struct for each module. |
| `--no-references` | off | Write every value as a literal. |
| `--report FILE` | none | Write the numbers as JSON. |
| `--region`, `--endpoint`, `--profile` | the AWS defaults | Where an `s3://` state is read from. The credentials come from the usual AWS chain. |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. Repeatable. |

`pravo shadow sync` takes every flag of `pravo shadow` except `--no-state` and `--no-guard`, and reads the state of the note when it is given none. `pravo shadow back` takes `--out FILE` (default `back.tfstate`) and `--from STATE`, the file to rewrite. It is the file that the shadow was taken from unless the shadow read standard input. The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The specification was written and its plan is empty. |
| 1 | The state could not be read or converted, the provider is not declared or has no schema for a type, `shadow.cue` exists and `--force` is missing, or the plan of what was written is not empty. The specification and the records stay, and the message says what the plan wants. |
| 5 | `pravo apply` refused a change because OpenTofu still owns the objects. |
| 6 | The specification does not load or evaluate. |

## Example

The example uses `demo_bucket` and `demo_server` from `tools/fakeprovider`, a provider that stands in for a cloud. The state is what OpenTofu wrote for a bucket, a server that is named after the bucket's id, and a server of a module whose size the provider refuses.

File `main.cue`:

```cue
package estate

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "{{FAKEPROVIDER_BIN}}"}
```

File `objects/catalog.json`:

```json
[
  {"type": "demo_bucket", "id": "b-1234", "name": "logs", "region": "eu"},
  {"type": "demo_server", "id": "srv-web", "name": "web", "region": "eu"},
  {"type": "demo_server", "id": "srv-edge", "name": "edge", "region": "eu"}
]
```

File `terraform.tfstate`:

```json
{
  "version": 4, "terraform_version": "1.13.1", "serial": 7, "lineage": "0e1f6a40-5b9c-4a43-9d3d-2c5a1d0b7e11",
  "outputs": {},
  "resources": [
    {"mode": "managed", "type": "demo_bucket", "name": "logs", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "b-1234", "bucket": "logs"}}]},
    {"mode": "managed", "type": "demo_server", "name": "web", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-web", "name": "web", "size": "b-1234", "secret": null}, "dependencies": ["demo_bucket.logs"]}]},
    {"module": "module.edge", "mode": "managed", "type": "demo_server", "name": "this", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-edge", "name": "edge", "size": "huge", "secret": "hunter2"}, "sensitive_attributes": [[{"type": "get_attr", "value": "secret"}]]}]}
  ]
}
```

Initialise the root and shadow the state.

```sh
pravo init > /dev/null
pravo shadow terraform.tfstate
```

Output:

```text
Shadowed 3 resources in 1 modules from terraform.tfstate into shadow.cue (24 lines) in <time>
  data sources dropped: 0 (their values are written as constants)
  edges of the state: 1 = 1 by reference + 0 by interpolation + 0 implied by those + 0 in $depends_on + 0 to data sources + 0 lost
  sensitive values kept out of the source: 1 left out, 0 by reference (0 more are computed and never written)
  the provider on the first rendering: 2 clean, 1 invalid, 0 with changes
  the provider on what was written:    3 clean, 0 invalid, 0 with changes (8 questions)
    ignore_changes: 1
    left out (conflict): 1

Verified: the plan of the specification says No changes (3 resources planned, 3 refreshed).

Terraform still owns the objects: an apply here that would change one is refused. Plan as often as you like.
When you are ready to leave Terraform: pravo shadow take-over
```

The specification has the secret nowhere, the refused size is left to the object, and the bucket's id is a reference.

```sh
cat shadow.cue
```

Output:

```text
package estate

// Decompiled from terraform.tfstate by pravo shadow: 3 resources of the state, no HCL read.

// demo_bucket.logs
logs: $demo.#Resource.demo_bucket & {
	bucket: "logs"
}

// demo_server.web
web: $demo.#Resource.demo_server & {
	name: "web"
	size: logs.id
}

edge: {
	// module.edge.demo_server.this
	this: $demo.#Resource.demo_server & {
		name: "edge"
		// secret: sensitive: the value stays in the state and is never written here
		// size: left out: the provider refuses it beside another attribute; ignore_changes keeps the value of the object
		$lifecycle: ignore_changes: ["secret", "size"]
	}
}
```

The next plan reads nothing, because the shadow stored the hash of every configuration.

```sh
pravo plan
```

Output:

```text
No changes. The specification matches the state. 3 records were not read, so a change made outside pravo would not show here; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 3 · not read 3
```

Change the server and apply. OpenTofu still owns the objects, so the apply is refused.

```sh
sed -i 's/size: logs.id/size: "large"/' shadow.cue
pravo apply --auto-approve
```

Output:

```text
pravo will perform the following actions:

  # web (demo_server) will be updated in-place
  ~ web: {
        id:     "srv-web"
        name:   "web"
  ~     size:   "b-1234" -> "large"
    }

Plan: 0 to add, 1 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 2

Error: apply refused: this root is a shadow of the Terraform state ./terraform.tfstate (serial 7), and Terraform still owns its objects. The plan would change a node: web (update). Nothing was changed.
Plan here as often as you like. To change the objects, either apply the change with Terraform and take the shadow again, or hand the objects over to pravo with: pravo shadow take-over
```

Hand the objects over and apply again.

```sh
pravo shadow take-over
pravo apply --auto-approve
```

Output:

```text
The objects are pravo's now. Stop applying ./terraform.tfstate with Terraform: remove its backend or lock it.

pravo will perform the following actions:

  # web (demo_server) will be updated in-place
  ~ web: {
        id:     "srv-web"
        name:   "web"
  ~     size:   "b-1234" -> "large"
    }

Plan: 0 to add, 1 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 2
web: Modifying... [id=srv-web]
web: Modifications complete after 0s [id=srv-web]

Apply complete! Resources: 0 added, 1 changed, 0 destroyed. [txn <txn>]
```

## Limits

- The result is the estate, not the program. Loops, variables and module calls of the HCL are not recovered: each instance is written once. The groups keep the boundaries of the module instances.
- Data sources become constants.
- A state read from an S3 bucket is read once, with the credentials of the caller. Workspaces and other backends are read by pulling the state to a file or to standard input: `tofu state pull | pravo shadow -`.
- A sensitive value that the provider insists on has a placeholder in the source. The vault keeps the value, but a specification cannot read a secret into a recorded attribute, so the placeholder stays and the object keeps the value it has.
- A shadow taken again with `--force` replaces `shadow.cue` and the records. An edit made by hand is lost: use `pravo shadow sync`, which keeps it.
- The guard is in the state. Whoever has a key that may write a hand-over has taken the objects over; deleting `shadow.lock.json` does not.
- Folding works on resources written at the top of the file. Those of a module stay as they are.
