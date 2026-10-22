<!-- Generated from docs/migrate/m8-rollback.md by tools/skillgen. Do not edit. -->

# Roll back

Where the migration can be undone by deleting the pravo files, and what to do after the first apply that changes an object.

Write the rollback before you start, and rehearse it before you need it. The rollback depends on one fact: which commands have changed a real object.

## The boundary

pravo writes to its own state directory, `pravo.state`, and to the files of its own root. It reads the objects through their providers. It never writes the `.tfstate` file or the OpenTofu backend.

Until the first `pravo apply` that creates, updates, replaces or deletes an object, no object has changed. These write records and nothing else: `import-tfstate`, `plan` (moves included), `import`, `drift` and the apply of an empty plan. Deleting the pravo files undoes everything, and the OpenTofu state and the objects are what they were.

From the first apply that changes an object, the OpenTofu state is out of date. pravo does not write it, and pravo has no command that converts its state back into an OpenTofu state. Rolling back means making the OpenTofu state agree with the objects, or making the objects agree with the OpenTofu state.

*Figure: The rollback depends on the first apply that changes an object.*

## Rehearse the first rollback

The workspace holds an OpenTofu root that OpenTofu 1.13.1 applied. It has the state file `terraform.tfstate` that OpenTofu wrote, the objects, and the pravo files of [M4](m4-translate-the-specification.md) and [M5](m5-convert-the-state.md). The page does not run OpenTofu. Rehearse in a copy, so that nothing is lost.

Take a checksum of the state file and of every object. Then run the migration up to the first apply, which is empty.

```sh
rm -rf r1 && mkdir r1 && cp -R cue.mod banner.txt out terraform.tfstate *.cue r1/
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > before.txt
pravo -C r1 state import-tfstate terraform.tfstate > /dev/null
pravo -C r1 plan > /dev/null 2>&1
pravo -C r1 apply 2>&1 | grep -E '^(No changes|Apply complete)'
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > after.txt
cmp before.txt after.txt && echo "unchanged"
```

Output:

```text
No changes. Your infrastructure matches the configuration.
unchanged
```

The state file and the objects have the same checksums after the import, a plan with moves and an apply. Now roll back. Remove what pravo made and nothing else:

1. Stop every pravo job for the root.
2. Delete `pravo.state`.
3. Delete the pravo files: the `.cue` files, `cue.mod` and `pravo.lock.cue`.
4. Run `tofu plan -detailed-exitcode`. It is empty, because nothing it owns has changed.

```sh
rm -rf r1/pravo.state r1/pravo.lock.cue r1/cue.mod r1/*.cue
ls r1
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > rolled.txt
cmp before.txt rolled.txt && echo "unchanged"
```

Output:

```text
banner.txt
out
terraform.tfstate
unchanged
```

What remains is the OpenTofu root with its state and its objects. OpenTofu 1.13.1 agrees:

```text
...
No changes. Your infrastructure matches the configuration.
```

The provider cache in `$PRAVO_HOME` is shared by every project and holds no state. Leave it.

## After the boundary

Add a third region and apply it, as in [M5](m5-convert-the-state.md). The apply creates a real object. Convert the state in a second copy first.

```sh
rm -rf r2 && mkdir r2 && cp -R cue.mod banner.txt out terraform.tfstate *.cue r2/
pravo -C r2 state import-tfstate terraform.tfstate > /dev/null
pravo -C r2 plan > /dev/null 2>&1
```

File `r2/variables.cue`:

```cue
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us", "ap"]
```

```sh
pravo -C r2 apply --auto-approve 2>&1 | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The transaction log says what the applies changed. Each apply lists its records with the action:

```sh
pravo -C r2 state log | grep -E '^    ([0-9]+ added|(create|update|replace|delete) )'
```

Output:

```text
    1 added, 0 changed, 0 destroyed
    create   region.ap  r0 -> r1
```

The OpenTofu state does not know the new object:

```sh
grep -c '"ap"' r2/terraform.tfstate
pravo -C r2 state list | grep ap
```

Output:

```text
0
region.ap
```

The count is 0, and pravo lists `region.ap`. The `.tfstate` file is the one you imported. Neither tool can see the other's changes.

Choose how to go back.

| What pravo did | Where the object is now | Undo with pravo | Reconcile OpenTofu |
|---|---|---|---|
| Created an object | It exists, and the OpenTofu state has no record. | Remove it from the specification, plan, and apply. | `tofu import <address> <id>` when the HCL has a block for it. |
| Updated an object in place | It has the new values. | Restore the old values in the specification, plan, and apply. | `tofu apply -refresh-only`, then edit the HCL to the new values. |
| Replaced an object | It has a new id. The old object is destroyed. | Not possible. | `tofu state rm <address>`, then `tofu import <address> <new id>`. |
| Deleted an object | It is gone. | Not possible. | `tofu apply` creates a new object. The data of the old one is lost. |

The id for `tofu import` is in `pravo state show <address>`. The import id of a type is in the provider's documentation. For many types it equals the `id` attribute. The `tofu` commands are not run by this page.

A provider may not implement import for a type. The `random_pet` of this sample is one. With `ap` added to the HCL, OpenTofu 1.13.1 plans to create `random_pet.region["ap"]`, and the import stops:

```text
...
Error: Resource Import Not Implemented

This resource does not support import. Please contact the provider developer
for additional information.
```

For such a type, undo the change with pravo, as below.

Work through the changes in this order:

1. Stop the pravo pipeline and the OpenTofu jobs for the root.
2. List what the applies changed: `pravo state log`, as above.
3. Choose a row of the table for each object.
4. Undo with pravo, or reconcile OpenTofu. Do not mix the two for one object.
5. Run `tofu plan` and `pravo plan`. Both are empty.
6. Delete the pravo files, if the rollback is complete.

Undo a created object with pravo, so that the objects match the OpenTofu state again. Restore the old default of `regions`:

File `r2/variables.cue`:

```cue
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us"]
```

```sh
pravo -C r2 plan --out undo.plan 2>&1 | grep -E '^(  # |Plan:)'
pravo -C r2 apply undo.plan 2>&1 | grep -E '^(Apply complete)'
pravo -C r2 plan --detailed-exitcode > /dev/null 2>&1
echo "exit=$?"
```

Output:

```text
  # region.ap (random_pet) will be destroyed
Plan: 0 to add, 0 to change, 1 to destroy.
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
exit=0
```

The plan deletes the object that pravo made, and the next plan is empty. The objects are again the ones the OpenTofu state describes, so you can delete the pravo files as before.

## After the cut over

[M7](m7-cut-over.md) removes the addresses from the OpenTofu state. The rollback then has one more step at the front.

1. Stop the pravo pipeline for the root.
2. If an apply since the cut over changed an object, reconcile as in the table above and stop here. The archived state is older than the objects.
3. If no apply changed an object, restore the archived state: `tofu state push -force archive/platform-final.tfstate`.
4. Check out the HCL from the tag `opentofu-final`.
5. Run `tofu plan`. It is empty.
6. Re-enable the OpenTofu jobs. Delete the pravo files, and the pravo state in the bucket if M7 moved it there.

The archived file has a lower serial than the state that `tofu state rm` wrote, so a push without `-force` is refused. The page does not run these commands. The output is from OpenTofu 1.13.1 on the `platform` root of [M3](m3-worked-example.md), after the removal of [M7](m7-cut-over.md):

```text

Error: Failed to write the imported state

cannot import state with serial 2 over newer state with serial 9
```

`tofu state push -force` writes the archived file over the current OpenTofu state, even when its serial is lower. The state lists the seven resources again, and the plan is empty:

```text
...
No changes. Your infrastructure matches the configuration.
```

## Recap

- Until the first `pravo apply` that changes an object, a rollback is deleting the pravo files. The OpenTofu state is never written.
- After it, the OpenTofu state is out of date. Undo a created or updated object with pravo, and reconcile a replaced or deleted object in OpenTofu.
- After the cut over, restore the archived state only when no apply has changed an object since.

Next: [M9 Pitfalls](m9-pitfalls.md) lists the symptoms you may meet, with the messages and the fixes.
