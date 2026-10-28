---
title: Stored plans and approvals
description: "How a plan lives in the state: what pravo plan --store writes, how a listing judges a stored plan, how an apply claims it and records its progress, how it is collected, and how a policy makes a plan wait for the approval of other people."
order: 8
minutes: 13
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+'
    replace: 'txn <id>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
keywords: "stored plan plan id plan list plan approve approvals review share apply later partial stale expired plan gc claim"
---

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

```cue file=store/main.cue
package store

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_integer & {min: 1, max: 9, seed: "a"}
b: $random.#Resource.random_integer & {min: 1, max: 9, seed: "b"}
```

```sh run
pravo -C store init > /dev/null
pravo -C store plan --store --actor alice | tail -n 3
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

The plan prints as usual, then pravo says where it went. Nothing is applied, and a plan with no changes is not stored. The listing shows who made it and where it stands:

```sh run
pravo -C store plan list
```

```text out
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

:::figure What happens to a stored plan, from the plan that is applied to the plan that nobody applies. A plan that is in use is never removed.
<svg viewBox="0 0 780 236" role="img" aria-label="A timeline with three rows. A plan that is applied is pending, then applied, is kept for 24 hours and is then removed. A plan that the state moved away from is pending, then stale; the first collection that sees it marks it, and a collection that runs an hour later removes it. A plan that nobody applies is pending until its time to live ends, is then expired, and the next collection removes it." style="min-width:680px">
<text x="0" y="62" style="fill:var(--text-2);font-size:12px;font-weight:500">applied</text>
<line x1="120" y1="70" x2="770" y2="70" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="122" style="fill:var(--text-2);font-size:12px;font-weight:500">state moved</text>
<line x1="120" y1="130" x2="770" y2="130" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="182" style="fill:var(--text-2);font-size:12px;font-weight:500">not applied</text>
<line x1="120" y1="190" x2="770" y2="190" style="stroke:var(--line);stroke-width:1"/>
<rect x="135" y="44" width="150" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="62" style="fill:var(--text);font-size:12px">pending</text>
<rect x="285" y="44" width="130" height="26" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="295" y="62" style="fill:var(--text);font-size:12px;font-weight:500">applied</text>
<rect x="415" y="44" width="150" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="425" y="62" style="fill:var(--text-2);font-size:12px">kept for 24 hours</text>
<rect x="565" y="44" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="62" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="285" y="38" style="fill:var(--text-3);font-size:10px">last change applied</text>
<rect x="135" y="104" width="120" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="122" style="fill:var(--text);font-size:12px">pending</text>
<rect x="255" y="104" width="100" height="26" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="265" y="122" style="fill:var(--conflict);font-size:12px;font-weight:600">stale</text>
<rect x="355" y="104" width="210" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="365" y="122" style="fill:var(--text-2);font-size:12px">marked, kept for 1 hour</text>
<rect x="565" y="104" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="122" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="255" y="98" style="fill:var(--text-3);font-size:10px">a record moved</text>
<text x="355" y="98" style="fill:var(--text-3);font-size:10px">a collection sees it</text>
<rect x="135" y="164" width="310" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="182" style="fill:var(--text);font-size:12px">pending until the time to live ends</text>
<rect x="445" y="164" width="120" height="26" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.4"/>
<text x="455" y="182" style="fill:var(--text);font-size:12px;font-weight:500">expired</text>
<rect x="565" y="164" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="182" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="445" y="158" style="fill:var(--text-3);font-size:10px">7 days, or --ttl</text>
<text x="565" y="158" style="fill:var(--text-3);font-size:10px">next collection</text>
<line x1="120" y1="212" x2="770" y2="212" style="stroke:var(--text-3);stroke-width:1.2"/>
<polygon points="770,212 764,208.4 764,215.6" style="fill:var(--text-3)"/>
<text x="770" y="230" text-anchor="end" style="fill:var(--text-3);font-size:10.5px">time</text>
</svg>
:::

## Two plans for one resource

Stored plans do not exclude each other. Two people can plan the same resource. The first plan applied changes the record, and the other conflicts there: it is stale, or partly stale when it has changes that do not touch that record. A plan that finds an open plan changing one of its resources prints a warning that names the other plan, its author and the resources, each once. A change that the other plan has applied already does not count. The warning informs and blocks nothing. A reservation, made with `plan --reserve`, is what keeps another actor from planning or applying a node. [The viewer](the-viewer.md#who-plans-what) draws the overlap: each resource carries a disc for every plan that still has a change on it, and a ring when two do.

## Applying a stored plan

`pravo apply ID` reads the plan from the state and then does what `pravo apply FILE` does, with the same checks. [`pravo apply`](../reference/apply.md#apply-a-stored-plan) lists them. Before it reads the plan, apply writes a claim into the progress object, so that no collection removes the plan while the apply runs. The claim is that apply's own, so applies of separate parts of one plan run at the same time, and each ends only its own. An apply fails with exit code 3 when a collection has claimed the plan to remove it, and when the plan was not applied and has expired and no collection has removed it yet. Any other failure to claim the plan is exit code 1, and so is a plan that a collection removed. A plan that was applied completely has nothing left to apply, also after it expired.

An apply records what it did in the progress object: the changes applied and the transaction. The plan object stays as it was written. An apply that fails or is interrupted records the changes it finished, as an apply from a plan file marks them in the file. A change that failed, one whose result is not known and one that was never started are not recorded. The apply prints how many changes it recorded and the command that applies the rest, the plan lists as `partial`, and the next apply does what is left. A change that is recorded is not judged against the state again, so the records that the apply wrote for it do not make the plan stale. The plan is `applied` when every change that was pending has been applied, whichever apply finishes last. Apply part of the plan and the listing shows it:

```sh run
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id --only a | tail -n 2
pravo -C store plan list
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
1 change remains in plan-<id>; apply with "pravo apply plan-<id>".
plan-<id>  partial  alice  just now  +2 ~0 -0  1 of 2 changes applied; expires in 7 days
```

The rest of the plan can still be applied. When the last change is applied the plan is `applied`:

```sh run
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id | tail -n 1
pravo -C store plan list --all
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
plan-<id>  applied  alice  just now  +2 ~0 -0  applied just now
```

A stored plan applies to the files it was made from. It records the `-t` and `-T` of the command that made it. `plan show` and `apply` need the same, because the tags select the state, and refuse others with the command to run. `plan list` reads the state that its own `-t` and `-T` select. A stored plan holds the same values as a plan file, sensitive values included, and the objects are as readable as the rest of the state: whoever can read the state can read its plans, exactly as they can read the records, and a sealed state seals its plans. [The viewer](the-viewer.md#stored-plans) lists stored plans and withholds their sensitive values as it does for a plan file.

## Collection

`pravo plan gc` removes the plans that nobody needs, and `pravo plan --store` removes a few on its way. The rules are in [`pravo plan`](../reference/plan.md#collect-stored-plans). A collection claims a plan in its progress object before it deletes anything. The deletes are conditional: pravo removes an object only if it is what its name says it is: a plan whose digest gives its id, or a summary that names the id. Two collections and an apply that run at once cannot damage each other, on a directory or in a bucket. Only the objects below `plans/` that have the names of plan objects are ever removed. A plan whose summary cannot be read is left alone, and a progress object that cannot be parsed is made empty once the plan is older than an hour. The small collection of `plan --store` never removes the plan that it has just stored.

## Approvals

A policy judges a plan and refuses it or lets it through. A policy can also say that a plan is fine as far as its rule goes, and still has to wait: for a second pair of eyes, or for someone with more authority than its author. That is an approval, and it belongs to a stored plan, because a stored plan is the one object that stays put while people look at it and that an apply can find again by its id.

A rule asks for approvals by setting `approvals` on the plans it matches: `min` is how many distinct keys must approve, `role` the weakest role that counts, `"writer"` by default or `"admin"`, and `self` whether the key that stored the plan may approve it, `false` by default. A rule picks out the plans it asks for with a disjunction, one branch for the plans that need nothing and one for the plans that do. When several policies ask, the strongest wins: the largest `min`, the highest `role`, and the author counts only if every one of them allows it. [Operating policies](../reference/policies-operations.md#ask-for-approvals) lists the rules.

:::figure A plan that a policy makes wait. The writer who stored it cannot apply it alone; another writer approves it; then the apply goes through, and the transaction records who approved. An admin applies without waiting.
<svg viewBox="0 0 780 170" role="img" aria-label="Four boxes in a row joined by arrows. First, dan stores the plan with pravo plan --store. Second, the policy asks for one approval by a writer, and dan's own apply is refused with exit code 5. Third, eve approves the plan with pravo plan approve. Fourth, dan applies the plan by its id, and the signed finish of the apply names eve. A note under the row says that an admin needs no approval, and that a plan which changes such a policy needs an admin's approval of its own." style="min-width:680px">
<rect x="0" y="30" width="170" height="70" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="85" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">dan stores the plan</text>
<text x="85" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo plan --store</text>
<text x="85" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">a writer</text>
<line x1="172" y1="65" x2="200" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="200,65 194,61.4 194,68.6" style="fill:var(--text-3)"/>
<rect x="202" y="30" width="180" height="70" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="292" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">the policy asks</text>
<text x="292" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">1 approval by a writer</text>
<text x="292" y="88" text-anchor="middle" style="fill:var(--conflict);font-size:10.5px;font-weight:600">dan's apply: exit 5</text>
<line x1="384" y1="65" x2="412" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="412,65 406,61.4 406,68.6" style="fill:var(--text-3)"/>
<rect x="414" y="30" width="170" height="70" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="499" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">eve approves</text>
<text x="499" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo plan approve ID</text>
<text x="499" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">another writer, signed</text>
<line x1="586" y1="65" x2="614" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="614,65 608,61.4 608,68.6" style="fill:var(--text-3)"/>
<rect x="616" y="30" width="164" height="70" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="698" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">dan applies</text>
<text x="698" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo apply ID</text>
<text x="698" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">the finish names eve</text>
<text x="0" y="134" style="fill:var(--text-3);font-size:11.5px">An admin needs no approval. A plan that edits or removes a policy that asks for approvals needs one admin's approval of its own.</text>
<text x="0" y="152" style="fill:var(--text-3);font-size:11.5px">An approval is a signature over the digest of the plan: it holds for that plan alone, and whether the key may approve is read from the readers file at apply.</text>
</svg>
:::

Approvals are signatures, so they need a sealed state with identities, and roles say who counts. The root `team` is sealed to three people: ada, an admin, and dan and eve, two writers.

```sh run hidden
mkdir -p people team/.pravo/access
pravo -C team id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo -C team id new --name dan > /dev/null
PRAVO_CONFIG_DIR=people/eve pravo -C team id new --name eve > /dev/null
{
  echo "$(pravo -C team id show | tail -n 1 | sed 's/^ *//') role:admin"
  echo "$(PRAVO_CONFIG_DIR=people/dan pravo -C team id show | tail -n 1 | sed 's/^ *//') role:writer"
  echo "$(PRAVO_CONFIG_DIR=people/eve pravo -C team id show | tail -n 1 | sed 's/^ *//') role:writer"
} > team/.pravo/access/readers.txt
```

The specification has one pet and a policy that lets any plan through, and asks for one approval by a writer other than the author when the plan destroys something:

```cue file=team/main.cue
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

```sh run
pravo -C team init > /dev/null
pravo -C team protect init --encrypt --yes --allow-local > /dev/null
pravo -C team apply --auto-approve | grep -E '^(Policies|Apply complete)'
```

```text out
Policies: +1 new, ~0 changed, -0 removed.
Policies: +1 new, ~0 changed, -0 removed.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

dan removes the pet and stores the plan. The policy is satisfied, since the second branch of the rule matches a plan that destroys, and the plan is stored with the approvals it needs. His own apply is refused: nobody has approved the plan, and the key that stored it does not count.

```sh run
sed -i.bak '/^pet:/d' team/main.cue
PRAVO_CONFIG_DIR=people/dan pravo -C team plan --store --actor dan 2>&1 | grep -E '^(Policies|Stored)'
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve > apply.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.out
```

```text out
Policies: 1 satisfied.
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
exit=5
Error: the plan plan-<id> needs 1 approval by a writer or an admin (careful_destroys@1) and has 0. The key that stored the plan does not count.
```

eve approves the plan. `plan show` names who approved, and dan's apply goes through:

```sh run
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/eve pravo -C team plan approve "$ID"
pravo -C team plan show "$ID" | grep -E '^(Stored plan|  approved)'
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
```

```text out
Approved plan-<id> as eve.
Stored plan plan-<id> by dan, just now (pending)
  approved: eve (writer)
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
```

An approval is a signature over the digest of the plan, so it holds for that plan alone, and a plan that is made again needs its own. Whether the key may approve is decided at the apply, from the readers file: a key that was demoted, or that is not in the file any more, stops counting. `pravo apply` of a plan file, or of no plan at all, refuses a plan that a policy asks approvals for, because a file has nowhere to keep one. An admin needs no approval, and a readers file without roles makes every key an admin, so a state with one member applies as it always did. The signed finish of the apply names the approvals it carried, so [`pravo state log`](../reference/state.md) and the History tab of [the viewer](the-viewer.md#the-inspector) say who approved what, long after the plan is collected.

One rule closes the loop. A writer may edit a policy, and a plan that changes a policy has no resource change, so no rule judges it. A plan that edits or removes a policy that asks for approvals therefore needs one admin's approval of its own, from a key other than the one that stored it: a writer cannot end the requirement with a plan that nothing judges. Adding a policy needs none.

## What a stored plan does not do

The viewer, `plan list` and the listing of an agent judge a plan by the state alone. They do not know whether the checkout that applies it has the same files, and they do not know whether the plan has its approvals: a plan is `pending` until an apply records otherwise, and `pravo apply` makes both checks. A plan is not a lock. Storing it reserves nothing, and another plan on the same resources is a warning, not a refusal; `plan --reserve` is the reservation. And a plan is not a review. It says what the apply will do, exactly, and nothing about whether that is wanted: a policy says that, and an approval says that someone looked.

## Recap

- A stored plan is a plan in the state, in three objects: the plan, its summary and its progress. Its id is the digest of its bytes.
- A listing judges a plan by the state, change by change: pending, partial, partly stale, stale, applied or expired. The checkout and the approvals are judged by the apply.
- An apply claims the plan, records each change it finishes, and leaves the rest for the next apply. A collection removes what nobody needs, with conditional deletes.
- A policy asks for approvals with `approvals: {min, role, self}` on the plans it matches. Approvals are signatures on a stored plan, by other keys of the roles asked for, and an admin needs none.

Next: [Policies](policies.md) explains the rules that judge every plan.
