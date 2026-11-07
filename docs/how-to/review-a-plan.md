---
title: Review a plan as an approver
description: One page for the person who reads and approves plans and does not write the specification. What to read, what to run, what an approval does and does not do, and what to commit.
order: 7
minutes: 5
needs: A sealed state whose members have roles, and a key of your own that is a writer or an admin in it.
done_when: "You can list the stored plans, read one, approve it with your key and see your name on it."
level: beginner
group: Share and protect
capture: false
keywords: "reviewer approver approve plan read role writer admin sign"
---

You review plans and you do not write the specification. This page lists what you read, what you run and what you commit. It describes how pravo behaves today and sets nothing up: the people who run the state do that in [Give people roles](give-people-roles.md) and [Approve a plan](approve-a-plan.md), which script three people on one machine. The terms are in the [glossary](../reference/glossary.md).

## What you need

- A checkout of the repository, and the command `pravo` on your `PATH`.
- A key of your own. `pravo id new --name YOURNAME` makes one and prints a line. Hand that line to an admin, who adds it to `.pravo/access/readers.txt` with a role and commits it. Until then you cannot read a sealed state.
- The role `writer` or `admin` to approve. A reader can read and plan, and a reader's approval does not count.

## What you run

1. List the stored plans of the state.

   ```sh
   pravo plan list
   ```

   Each row has the id, the status, the author, the age and the counts of resources to add, change and destroy. `pending` means that the plan can still be applied.

2. Read one. The id can be shortened to six digits.

   ```sh
   pravo plan show plan-2f9c1a
   ```

   The header names the author, the reason, the command that made the plan, the expiry and what has been approved so far. Below it the plan lists each action. `pravo serve` shows the same plans over the graph of the estate: [Explore an estate in a browser](explore-your-estate.md#8-see-what-your-team-has-planned).

3. Approve it, if you agree.

   ```sh
   pravo plan approve plan-2f9c1a
   ```

   Run `pravo plan show` again: your name and role appear on the line `approved:`.

You do not apply. The author, or a pipeline, applies by id: `pravo apply plan-2f9c1a`. An apply with too few approvals stops with exit code 5 and says how many the plan has and how many it needs.

## What to read in a plan

- The counts of destroys and replacements first. A replacement destroys and creates the object.
- The reason in the header. A plan stored without `--reason` has none, so ask the author.
- Values marked sensitive, which the plan shows as `(sensitive)`. You approve the action, not the value.

## What an approval does

It is a signature of your key over the digest of this plan, kept in the state beside the plan. It holds for this plan alone: a plan made again from the same change needs approvals of its own. The key that stored the plan does not count, so the author cannot approve their own plan. Whether your key counts is decided at the apply, from the readers file of the checkout, so a key that was demoted or taken off stops counting.

An approval does not check that the plan is wise, and it does not change what the plan does. An admin's apply needs no approval, so give the role `admin` to few people.

## What you commit

Almost nothing. You commit your line in `.pravo/access/readers.txt` only when you are being added, and an admin commits it. A change to a rule, such as the number of approvals it asks for, is a change to the specification. It needs its own plan, and the rule that is in force when the plan is applied decides.

To see afterwards who asked, who agreed and who ran a plan, read `pravo plan show ID` and `pravo state log`: the transaction is signed by the key that applied.

## What next

- [Approve a plan](approve-a-plan.md) writes the rule and runs the whole round with three people.
- [Give people roles](give-people-roles.md) adds and takes off members.
- [Stored plans and approvals](../concepts/stored-plans-and-approvals.md#approvals) explains why an approval is a signature.
