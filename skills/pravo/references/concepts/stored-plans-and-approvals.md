<!-- Generated from docs/concepts/stored-plans-and-approvals.md by tools/skillgen. Do not edit. -->

# Stored plans and approvals

How a plan lives in the state: what pravo plan --store writes, how a listing judges a stored plan, how an apply claims it and records its progress, how it is collected, and how a policy makes a plan wait for the approval of other people.

A plan is computed in one checkout and printed on one screen. A team needs it to travel: to the colleague who reviews it, to the job that applies it tonight, to the person whose approval a policy asks for. pravo keeps such a plan in the state, next to the records it is about, where everyone who can read the state finds it. This page explains what a stored plan is, how it moves from pending to applied, and how approvals attach to it. [Plans](plans.md) explains what a plan contains; this page starts where a plan leaves the terminal.

## A plan in the state

A saved plan is a file that `pravo plan --out` writes. A [stored plan](../reference/glossary.md#stored-plan) is the same plan written into the state by `pravo plan --store`, as three objects below `plans/`.

| Object | Holds | Changes |
|---|---|---|
| `plans/ID.plan.json` | The plan: [the saved plan file](../reference/plan-document.md#the-saved-plan-file). | Never. |
| `plans/ID.meta.json` | The summary that a listing reads: who made the plan, when it expires, what it changes and which records it expects. | Never. |
| `plans/ID.progress.json` | What was applied from the plan and by which transaction, who is applying or removing it, and who approved it. | By a conditional replace, and only this one. |

The id is `plan-` and the first eight hexadecimal digits of the SHA-256 digest of the plan object, so the same bytes are always the same plan. Any unique prefix of six digits or more finds a plan. [State format](../reference/state-format.md#plans) lists every field.

Work in an empty directory. The root `store` has two resources that do not depend on each other. Store a plan for it:

File `store/main.cue`:

```cue
package store

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_integer & {min: 1, max: 9, seed: "a"}
b: $random.#Resource.random_integer & {min: 1, max: 9, seed: "b"}
```

```sh
pravo -C store init > /dev/null
pravo -C store plan --store --actor alice | tail -n 3
```

Output:

```text
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

The plan prints as usual, then pravo says where it went. Nothing is applied, and a plan with no changes is not stored. The listing shows who made it and where it stands:

```sh
pravo -C store plan list
```

Output:

```text
plan-<id>  pending  alice  just now  +2 ~0 -0  expires in 7 days
```

The author of a stored plan is the `--actor` label, or the operating system user when there is none, and pravo does not verify it. In a sealed state the plan is also signed by the identity that stored it, and that signature is verified: [Signatures and roles](signatures-and-roles.md) says what it binds.

## Where a stored plan stands

A listing reads the summaries and the progress objects, and compares each plan with the state. It does not read the plan objects, and it does not compare the configuration or the providers: those belong to a checkout, and the people who share a state have several. The command that shows or applies the plan compares them.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | Some changes were applied, by `apply --only` or by an apply that failed after it finished them. The rest can be applied. |
| `partly-stale` | A record that some changes not applied yet write has another revision or generation, and the other changes can still be applied. The whole plan is refused. `apply --only` with the changes that can be applied is not. |
| `stale` | No part of the plan can be applied: the state has another lineage, a record that the plan only reads moved, or every change not applied yet conflicts. |
| `applied` | Every change was applied. |
| `expired` | The time to live ended before the plan was applied. |

A plan is judged change by change. A change conflicts when the record it writes has another revision or generation than the plan expected. A change that has to be applied with one that conflicts conflicts too: the summary lists, for each change, the changes that an apply of it needs, which are the rules by which `--only` closes a selection. The changes that do not conflict, with the changes they need, are the live part. A change that is applied is not judged. The lineage of the state and a record that the plan only reads are not judged change by change: the plan was worked out from them, and when one moved, the whole plan is stale.

A listing of a partly stale plan names the conflicts and the `--only` set that applies the live part. The apply of the whole plan is refused with exit code 3, and so is an `--only` that holds a conflict. [`pravo apply`](../reference/apply.md#a-partly-stale-plan) runs it. A collection neither marks nor removes a partly stale plan: it stays until its time to live ends. When the live part is applied, nothing that can be applied is left, the plan is stale, and a collection marks it and removes it after the grace like any stale plan. The rest is planned again.

*Figure: What happens to a stored plan, from the plan that is applied to the plan that nobody applies. A plan that is in use is never removed.*

## Two plans for one resource

Stored plans do not exclude each other. Two people can plan the same resource. The first plan applied changes the record, and the other conflicts there: it is stale, or partly stale when it has changes that do not touch that record. A plan that finds an open plan changing one of its resources prints a warning that names the other plan, its author and the resources, each once. A change that the other plan has applied already does not count. The warning informs and blocks nothing. A reservation, made with `plan --reserve`, is what keeps another actor from planning or applying a node. [The viewer](the-viewer.md#who-plans-what) draws the overlap: each resource carries a disc for every plan that still has a change on it, and a ring when two do.

## Applying a stored plan

`pravo apply ID` reads the plan from the state and then does what `pravo apply FILE` does, with the same checks. [`pravo apply`](../reference/apply.md#apply-a-stored-plan) lists them. Before it reads the plan, apply writes a claim into the progress object, so that no collection removes the plan while the apply runs. The claim is that apply's own, so applies of separate parts of one plan run at the same time, and each ends only its own. An apply fails with exit code 3 when a collection has claimed the plan to remove it, and when the plan was not applied and has expired and no collection has removed it yet. Any other failure to claim the plan is exit code 1, and so is a plan that a collection removed. A plan that was applied completely has nothing left to apply, also after it expired.

An apply records what it did in the progress object: the changes applied and the transaction. The plan object stays as it was written. An apply that fails or is interrupted records the changes it finished, as an apply from a plan file marks them in the file. A change that failed, one whose result is not known and one that was never started are not recorded. The apply prints how many changes it recorded and the command that applies the rest, the plan lists as `partial`, and the next apply does what is left. A change that is recorded is not judged against the state again, so the records that the apply wrote for it do not make the plan stale. The plan is `applied` when every change that was pending has been applied, whichever apply finishes last. Apply part of the plan and the listing shows it:

```sh
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id --only a | tail -n 2
pravo -C store plan list
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
1 change remains in plan-<id>; apply with "pravo apply plan-<id>".
plan-<id>  partial  alice  just now  +2 ~0 -0  1 of 2 changes applied; expires in 7 days
```

The rest of the plan can still be applied. When the last change is applied the plan is `applied`:

```sh
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id | tail -n 1
pravo -C store plan list --all
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
plan-<id>  applied  alice  just now  +2 ~0 -0  applied just now
```

A stored plan applies to the files it was made from. It records the `-t` and `-T` of the command that made it. `plan show` and `apply` need the same, because the tags select the state, and refuse others with the command to run. `plan list` reads the state that its own `-t` and `-T` select. A stored plan holds the same values as a plan file, sensitive values included, and the objects are as readable as the rest of the state: whoever can read the state can read its plans, exactly as they can read the records, and a sealed state seals its plans. [The viewer](the-viewer.md#stored-plans) lists stored plans and withholds their sensitive values as it does for a plan file.

## Collection

`pravo plan gc` removes the plans that nobody needs, and `pravo plan --store` removes a few on its way. The rules are in [`pravo plan`](../reference/plan.md#collect-stored-plans). A collection claims a plan in its progress object before it deletes anything. The deletes are conditional: pravo removes an object only if it is what its name says it is: a plan whose digest gives its id, or a summary that names the id. Two collections and an apply that run at once cannot damage each other, on a directory or in a bucket. Only the objects below `plans/` that have the names of plan objects are ever removed. A plan whose summary cannot be read is left alone, and a progress object that cannot be parsed is made empty once the plan is older than an hour. The small collection of `plan --store` never removes the plan that it has just stored.

## Approvals

A policy judges a plan and refuses it or lets it through. A policy can also say that a plan is fine as far as its rule goes, and still has to wait: for a second pair of eyes, or for someone with more authority than its author. That is an approval, and it belongs to a stored plan, because a stored plan is the one object that stays put while people look at it and that an apply can find again by its id.

A rule asks for approvals by setting `approvals` on the plans it matches: `min` is how many distinct keys must approve, `role` the weakest role that counts, `"writer"` by default or `"admin"`, and `self` whether the key that stored the plan may approve it, `false` by default. A rule picks out the plans it asks for with a disjunction, one branch for the plans that need nothing and one for the plans that do. When several policies ask, the strongest wins: the largest `min`, the highest `role`, and the author counts only if every one of them allows it. [Operating policies](../reference/policies-operations.md#ask-for-approvals) lists the rules.

*Figure: A plan that a policy makes wait. The writer who stored it cannot apply it alone; another writer approves it; then the apply goes through, and the transaction records who approved. An admin applies without waiting.*

Approvals are signatures, so they need a sealed state with identities, and roles say who counts. The root `team` is sealed to three people: ada, an admin, and dan and eve, two writers.

The specification has one pet and a policy that lets any plan through, and asks for one approval by a writer other than the author when the plan destroys something:

File `team/main.cue`:

```cue
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}

careful_destroys: pravo.#Policy & {
	description: "A plan that destroys a resource is applied once another writer has approved it."
	rule: {
		{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1}}
	}
}
```

ada seals the state and applies. The first apply writes the policy as version 1 and creates the pet; a policy that is added is not judged.

```sh
pravo -C team init > /dev/null
pravo -C team protect init --encrypt --yes --allow-local > /dev/null
pravo -C team apply --auto-approve | grep -E '^(Policies|Apply complete)'
```

Output:

```text
Policies: +1 new, ~0 changed, -0 removed.
Policies: +1 new, ~0 changed, -0 removed.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

dan removes the pet and stores the plan. The policy is satisfied, since the second branch of the rule matches a plan that destroys, and the plan is stored with the approvals it needs. His own apply is refused: nobody has approved the plan, and the key that stored it does not count.

```sh
sed -i.bak '/^pet:/d' team/main.cue
PRAVO_CONFIG_DIR=people/dan pravo -C team plan --store --actor dan 2>&1 | grep -E '^(Policies|Stored)'
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve > apply.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.out
```

Output:

```text
Policies: 1 satisfied.
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
exit=5
Error: the plan plan-<id> needs 1 approval by a writer or an admin (careful_destroys@1) and has 0. The key that stored the plan does not count.
```

eve approves the plan. `plan show` names who approved, and dan's apply goes through:

```sh
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/eve pravo -C team plan approve "$ID"
pravo -C team plan show "$ID" | grep -E '^(Stored plan|  approved)'
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
```

Output:

```text
Approved plan-<id> as eve.
Stored plan plan-<id> by dan, just now (pending)
  approved: eve (writer)
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
```

An approval is a signature over the digest of the plan, so it holds for that plan alone, and a plan that is made again needs its own. Whether the key may approve is decided at the apply, from the readers file: a key that was demoted, or that is not in the file any more, stops counting. `pravo apply` of a plan file, or of no plan at all, refuses a plan that a policy asks approvals for, because a file has nowhere to keep one. An admin needs no approval, and a readers file without roles makes every key an admin, so a state with one member applies as it always did. The signed finish of the apply names the approvals it carried, so [`pravo state log`](../reference/state.md) and the History tab of [the viewer](the-viewer.md#the-inspector) say who approved what, long after the plan is collected.

One rule closes the loop. A writer may edit a policy, and a plan that changes a policy has no resource change, so no rule judges it. A plan that edits or removes a policy that asks for approvals therefore needs one admin's approval of its own, from a key other than the one that stored it: a writer cannot end the requirement with a plan that nothing judges. Adding a policy needs none.

## What a stored plan does not do

The viewer, `plan list` and the listing of an agent judge a plan by the state alone. They do not know whether the checkout that applies it has the same files. `plan list` and the agent do not count approvals either; the viewer does, against its own readers file, and says on the row of a plan what it still needs. A plan is `pending` until an apply records otherwise, and `pravo apply` makes both checks. A plan is not a lock. Storing it reserves nothing, and another plan on the same resources is a warning, not a refusal; `plan --reserve` is the reservation. And a plan is not a review. It says what the apply will do, exactly, and nothing about whether that is wanted: a policy says that, and an approval says that someone looked.

## Recap

- A stored plan is a plan in the state, in three objects: the plan, its summary and its progress. Its id is the digest of its bytes.
- A listing judges a plan by the state, change by change: pending, partial, partly stale, stale, applied or expired. The checkout and the approvals are judged by the apply.
- An apply claims the plan, records each change it finishes, and leaves the rest for the next apply. A collection removes what nobody needs, with conditional deletes.
- A policy asks for approvals with `approvals: {min, role, self}` on the plans it matches. Approvals are signatures on a stored plan, by other keys of the roles asked for, and an admin needs none.

Next: [Constraints and policies](policies.md) explains the rules that judge every plan.
