<!-- Generated from docs/how-to/approve-a-plan.md by tools/skillgen. Do not edit. -->

# Approve a plan

Make a plan wait for the approval of another member before it is applied. Write the rule, store the plan, approve it with a second key, apply it by its id, and see what an admin may skip and what a change to the rule itself needs.

A plan that destroys a database should not run on one person's say-so. A policy can ask that a plan is approved by other members before it is applied, and the apply refuses until it is. An approval is a signature by a key of the readers file, so it needs a sealed state with roles. This page writes the rule, stores a plan that it catches, approves the plan with a second key and applies it, and then shows what an admin may skip and what a change to the rule itself needs.

> [!NOTE]
> The state is a local directory, sealed with `--allow-local` so that the page needs no bucket. The three people, ada, dan and eve, are three identity directories on one machine, and `PRAVO_CONFIG_DIR` picks one. Each command is written as the person who runs it; a command without a prefix is ada's.

## 1. Start from a sealed state with roles

The root is `team/`, with one resource. Its state is sealed to ada, an admin, and to dan and eve, two writers, the roles are pinned, and ada has applied it once. [Give people roles](give-people-roles.md) sets this up step by step.

File `team/main.cue`:

```cue
package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {length: 2}
```

```sh
awk '$5 ~ /^role:/ {print $1, $5}' team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Sealed|Roles)'
```

Output:

```text
ada role:admin
dan role:writer
eve role:writer
Sealed       on · format 3 · 3 readers (ada dan eve) · recovery none
Roles        on · 1 admin · 2 writer · 0 reader · pinned, version 1 in the state · you are an admin
```

## 2. Write the rule

A policy is a node of the root whose `rule` is a CUE value that every plan has to fit, as [Write and roll out policies](policies.md) shows. This one asks for an approval of any plan that destroys something:

File `team/policy.cue`:

```cue
package team

import "pravo.dev/pravo"

second_opinion: pravo.#Policy & {
	description: "A plan that destroys a resource is applied once another writer has approved it."
	rule: {
		{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1}}
	}
}
```

The `|` makes the rule a choice between two shapes, and a plan fits the rule when it fits one of them. The left shape fits a plan whose summary destroys nothing, and asks for nothing more. The right shape fits a plan that destroys one resource or more, and `approvals: {min: 1}` is what it asks for: one approval, by a key whose role is `writer` or above, other than the key that stored the plan. A replacement destroys the old object, so it counts. The rule is written as a choice because a rule cannot say `if` about the document as a whole; [Writing policies](../reference/policies-writing.md) lists the shapes that work.

`approvals` has three fields, and the two that are left out take their defaults:

```cue
approvals: {
	min:  2       // how many distinct keys have to approve
	role: "admin" // the weakest role that counts: "writer", the default, or "admin"
	self: true    // whether the key that stored the plan may approve it; false by default
}
```

When several policies ask, the strongest wins: the largest `min`, the highest `role`, and the author counts only if every one of them allows it.

## 3. Add the policy

Ada applies. The plan that adds a policy is not judged by it, and the policy is in force from this apply on.

```sh
pravo -C team apply --auto-approve | grep -E '^(Policies|Apply complete)'
```

Output:

```text
Policies: +1 new, ~0 changed, -0 removed.
Policies: +1 new, ~0 changed, -0 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
```

## 4. Store a plan that the rule catches

Dan removes the resource and stores the plan. At plan time the policy is satisfied: the approvals are counted at the apply, so that they can be given in between. His own apply is refused, because nobody has approved yet and his own key does not count.

```sh
sed -i.bak '/^pet:/d' team/main.cue && rm team/main.cue.bak
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
Error: the plan plan-<id> needs 1 approval by a writer or an admin (second_opinion@1) and has 0. The key that stored the plan does not count.
```

`ID` in the shell stands for the id that pravo printed. The approvals belong to the stored plan, so a plan that a rule asks approvals for is stored: `pravo apply` of a plan file, or without a plan, refuses such a plan too, because a file has nowhere to keep an approval.

## 5. Approve it with a second key

Eve reads the plan first. `pravo plan show ID` prints it, and the Plans section of [the viewer](explore-your-estate.md#8-see-what-your-team-has-planned) lays it over the graph. Then she approves.

```sh
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/eve pravo -C team plan approve "$ID"
pravo -C team plan show "$ID" | grep -E '^(Stored plan|  approved)'
```

Output:

```text
Approved plan-<id> as eve.
Stored plan plan-<id> by dan, just now (pending)
  approved: eve (writer)
```

An approval is a signature over the digest of the plan, by eve's key, kept in the state next to the plan. It holds for this plan alone: a plan made again, from the same change, needs approvals of its own. Whether eve's key counts is decided at the apply, from the readers file of the checkout: a key that was demoted to a reader, or taken off the file, stops counting.

## 6. Apply it

```sh
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
pravo -C team plan list --all | head -n 1
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
plan-<id>  applied  dan  just now  +0 ~0 -1  applied just now
```

The transaction is signed by dan, who applied, and the plan keeps eve's approval, so the state says who asked, who agreed and who ran it.

## 7. What an admin may skip

An admin needs no approval, so a state with one member works as it did before the rule, and a readers file without roles makes every key an admin. Ada puts the resource back, dan tries the same removal with a plain apply, and ada runs it.

```sh
printf '\npet: $random.#Resource.random_pet & {length: 2}\n' >> team/main.cue
pravo -C team apply --auto-approve | grep '^Apply complete'
sed -i.bak '/^pet:/d' team/main.cue && rm team/main.cue.bak
PRAVO_CONFIG_DIR=people/dan pravo -C team apply --auto-approve > apply.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.out
pravo -C team apply --auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
exit=5
Error: the plan needs 1 approval by a writer or an admin (second_opinion@1), and approvals belong to a stored plan: make it with plan --store, have it approved with "pravo plan approve" and apply it by its id
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
```

## 8. Change the rule

Dan rewrites the rule so that it asks for nothing. A writer may edit a policy, and the plan that carries the edit changes no resource, so no rule judges it. A change to a policy that asks for approvals therefore needs one approval by an admin, from a key other than the one that stored the plan, so that a writer cannot end the rule on their own.

File `team/policy.cue`:

```cue
package team

import "pravo.dev/pravo"

second_opinion: pravo.#Policy & {
	description: "A plan that destroys a resource is applied once another writer has approved it."
	rule: {
		summary: destroy: >=0
	}
}
```

```sh
PRAVO_CONFIG_DIR=people/dan pravo -C team plan --store --actor dan 2>&1 | grep -E '^(Policies|Stored)'
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve > apply.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.out
PRAVO_CONFIG_DIR=people/eve pravo -C team plan approve "$ID"
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve > apply.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.out
```

Output:

```text
Policies: +0 new, ~1 changed, -0 removed.
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
exit=5
Error: the plan plan-<id> needs 1 approval by an admin (a change to second_opinion@1) and has 0. The key that stored the plan does not count.
Approved plan-<id> as eve.
exit=5
Error: the plan plan-<id> needs 1 approval by an admin (a change to second_opinion@1) and has 0. The key that stored the plan does not count.
```

Eve's approval is recorded and does not count, because the rule that is being changed asks for an admin. Ada approves, and dan applies:

```sh
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
pravo -C team plan approve "$ID"
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep -E '^(Policies|Apply complete)'
pravo -C team plan show "$ID" | grep '^  approved'
```

Output:

```text
Approved plan-<id> as ada.
Policies: +0 new, ~1 changed, -0 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
  approved: eve (writer), ada (admin)
```

Removing the policy altogether is an admin's apply: a writer's apply that removes a policy is refused before it changes anything. Adding a policy needs nothing, since a new rule can only ask for more.

## 9. In a pipeline

The plan job stores the plan and prints its id: `pravo plan --store --json`, and the field `stored.id` of the document. A person reads it, with `pravo plan show ID` or in the viewer, and approves it from their own machine with `pravo plan approve ID`. The apply job applies by id, and exit code 5 with `needs N approvals` means that the job runs again later, or that the pipeline waits for the person. The identity of the apply job is a writer. No job is an admin, because an admin's apply skips the approvals. [Run pravo in a pipeline](ci.md) has the jobs.

## What an approval does not do

- There is no command that withdraws an approval. A plan that should not be applied after all is left to expire, and a plan made again from the same change needs approvals of its own.
- `min` counts distinct keys, not people. A member who has two identities on the readers file, as [Seal the state](seal-the-state.md#1-make-an-identity) allows, could approve twice. Where approvals matter, give each person one line, or set `min` with that in mind.
- The viewer shows a plan as pending until it is applied, and does not count approvals: `pravo apply` does, under the locks.
- A state that is not sealed has no approvals, because it has no signatures. A readers file without roles makes every key an admin, and an admin needs none.

## What next

- [Give people roles](give-people-roles.md) sets the roles that the approvals count.
- [Write and roll out policies](policies.md) tests a rule on fixtures before it is in force.
- [Operating policies](../reference/policies-operations.md#ask-for-approvals) is the reference for `approvals`, and [`pravo plan approve`](../reference/plan.md#approve-a-stored-plan) for the command.
- [Stored plans and approvals](../concepts/stored-plans-and-approvals.md) explains what an approval is and where it lives.
