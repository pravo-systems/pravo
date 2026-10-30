---
title: Readers, writers, admins
description: Everyone who can read a sealed state can change it, until the readers file says otherwise. Give each line a role, pin the roles in the state so that nobody promotes themselves in a copy of the file, and see what a reader, a writer and an admin may do.
order: 64
group: Track C · A team on one state
minutes: 15
needs: C3
learn: Who may change a sealed state? What stops a reader from editing their own role? What does a writer's signature say that a reader's cannot?
done_when: "A reader's apply is refused with exit code 5, the same reader stores a plan that a writer applies, and a copy of the readers file with a better role changes nothing."
level: intermediate
services:
  - fakes3:9000
env:
  AWS_ACCESS_KEY_ID: test
  AWS_SECRET_ACCESS_KEY: test
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'AAAAC3NzaC1lZDI1NTE5AAAA[A-Za-z0-9+/]{44}'
    replace: '<ssh-key>'
  - pattern: 'x25519:[A-Za-z0-9_-]{43}'
    replace: 'x25519:<key>'
  - pattern: 'roles-genesis [0-9a-f]{64}'
    replace: 'roles-genesis <digest>'
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}"
    replace: "<txn>"
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "\\[id=[^\\]]+\\]"
    replace: "[id=<id>]"
---

C3 sealed the state to a list of readers. Every one of them may also apply, destroy, move and remove, because a reader key opens the records and nothing else was said. OpenTofu has no word for this at all: whoever has the credentials of the bucket has the state, and whoever has the state and the cloud credentials can change anything. Teams put the difference in IAM, in a pipeline that is the only thing allowed to apply, or in a convention.

```text title="who may apply, in OpenTofu"
the people who hold the bucket credentials and the cloud credentials
```

pravo puts it in the readers file. A fifth word on a line gives the key a role: `reader`, `writer` or `admin`. A reader opens the state, plans and stores a plan. A writer changes the state. An admin changes who reads, changes the roles and removes policies. The file is in the repository, so a role is granted the way read access is: by merging a line. And because the file could be edited in a checkout by the person it is about, the roles are pinned in the state as a signed version, and from then on the version in the state decides.

| Role | May |
|---|---|
| `reader` | Open the state, read it, plan, ask `why`, and store a plan with `plan --store`. Read a secret that the state keeps. |
| `writer` | What a reader may, and change the state: `apply`, `destroy`, `import`, `state mv`, `state rm`, `discover adopt`. Add and edit a policy. Set a secret. Approve a plan. |
| `admin` | What a writer may, and change who reads the state, change the roles, remove a policy, remove a secret, collect garbage and take the seal off. Apply a plan without the approvals a policy asks for. |

## A sealed state, three readers

Work in an empty directory. The root is `team/`, one resource in the in-memory bucket, sealed to Ada, Dan and Eve as C3 did it, and applied once by Ada. The setup is the steps of C3, run for you.

```cue file=team/main.cue title="team/main.cue"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "northwind-state"
		prefix:     "prod"
		region:     "eu-west-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {length: 2}
```

```sh run hidden
mkdir -p team/.pravo/access
pravo -C team id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo -C team id new --name dan > /dev/null
PRAVO_CONFIG_DIR=people/eve pravo -C team id new --name eve > /dev/null
for p in ada dan eve; do PRAVO_CONFIG_DIR=people/$p pravo -C team id show | tail -n 1 | sed 's/^ *//'; done > team/.pravo/access/readers.txt
pravo -C team init > /dev/null
pravo -C team protect init --encrypt --yes > /dev/null
pravo -C team apply --auto-approve > /dev/null
```

```sh run
cat team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Sealed|Roles)'
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
eve ssh-ed25519 <ssh-key> x25519:<key>
Sealed       on · format 3 · 3 readers (ada dan eve) · recovery none
Roles        off
```

## A fifth word

Ada is the admin, Dan writes, Eve reads. As soon as one line has a role, a line without one is a reader, so every line gets one.

```sh run
sed -i.bak -e '/^ada /s/$/ role:admin/' -e '/^dan /s/$/ role:writer/' -e '/^eve /s/$/ role:reader/' team/.pravo/access/readers.txt && rm team/.pravo/access/readers.txt.bak
cat team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Roles|Records)'
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:writer
eve ssh-ed25519 <ssh-key> x25519:<key> role:reader
Roles        on · 1 admin · 1 writer · 1 reader · from the readers file, not pinned (pravo protect roles) · you are an admin
Records      0 bound to a transaction of a writer · 1 from before roles
```

The roles are in force from this line on, for every pravo that reads this checkout. No `rewrap` is needed: a role is not a key, and the data keys stay as they are. The status says `from the readers file, not pinned`, which is the gap that the next step closes.

## Pin them

:::predict Eve has a checkout of her own. She edits her line there to `role:admin`. What does pravo in her checkout say she is?
Ask where the roles come from at this point.

::reveal
An admin, in that checkout, for as long as the checkout lasts. The file is the only source of the roles until they are pinned. Nothing in the bucket says otherwise yet.
:::

`pravo protect roles`, run by an admin, writes the roles to the state as a version signed by the admin's key, and adds one line to the readers file: the digest of that first version. From then on the roles in the state decide, a new version has to be signed by an admin of the version before it, and a state whose chain was replaced or removed is refused with exit code 7.

```sh run
pravo -C team protect roles --yes
tail -n 1 team/.pravo/access/readers.txt
pravo -C team protect status | grep '^Roles'
```

```text out
  ada: admin
  dan: writer
  eve: reader
Stored the roles as version 1.
Added roles-genesis <digest> to .pravo/access/readers.txt. Commit it: until it is in the repository the roles of the file decide.
roles-genesis <digest>
Roles        on · 1 admin · 1 writer · 1 reader · pinned, version 1 in the state · you are an admin
```

Commit the file with its new line. The line is a pin: a checkout that has it refuses a state whose roles chain does not start from that version.

## Break it: a better role in a copy of the file

Eve makes a copy of the checkout, gives herself `role:writer` in the copy, changes the resource and applies. The block carries `status=unintended`: the file is well formed, and it is not the file that decides.

```sh run
cp -r team eve-team
sed -i.bak 's/^\(eve .*\) role:reader$/\1 role:writer/' eve-team/.pravo/access/readers.txt && rm eve-team/.pravo/access/readers.txt.bak
sed -i.bak 's/length: 2/length: 3/' eve-team/main.cue && rm eve-team/main.cue.bak
PRAVO_CONFIG_DIR=people/eve pravo -C eve-team apply --auto-approve; echo "exit=$?"
PRAVO_CONFIG_DIR=people/eve pravo -C eve-team protect status | grep '^Roles'
```

```text out
Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
exit=5
Roles        on · 1 admin · 1 writer · 1 reader · pinned, version 1 in the state (the readers file asks for other roles: pravo protect roles) · you are a reader
```

The roles in the state say reader, and the apply is refused with exit code 5. The status reports the difference between the file and the state, and names the command that would resolve it, which only an admin can run. The copy carried the identity directories along, which is how the command runs as Eve.

```sh run
rm -r eve-team
```

## What a reader does

Eve adds a resource and tries to apply it from the real checkout.

```cue file=team/second.cue title="team/second.cue"
package team

second: $random.#Resource.random_pet & {length: 2, prefix: "eve"}
```

```sh run
PRAVO_CONFIG_DIR=people/eve pravo -C team apply --auto-approve; echo "exit=$?"
```

```text out

Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
exit=5
```

A reader plans, and the plan stays in memory: the records it refreshed are not written back. The one thing a reader writes is a stored plan, from [C2](c02-store-a-plan.md), and a writer applies it:

```sh run
PRAVO_CONFIG_DIR=people/eve pravo -C team plan --store | grep '^Stored'
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
pravo -C team state log | grep 'signed by'
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
    signed by dan (verified)
    signed by ada (verified)
```

The transaction is signed by Dan, who applied, and the stored plan keeps Eve's signature as the key that stored it. That is the shape of a review: one person asks, another runs, and the state says which was which.

## What a writer may not do

Dan may change the state and not who reads it, nor the roles:

```sh run
PRAVO_CONFIG_DIR=people/dan pravo -C team protect rewrap --yes; echo "exit=$?"
PRAVO_CONFIG_DIR=people/dan pravo -C team protect roles --yes; echo "exit=$?"
```

```text out

Error: your identity is a writer in .pravo/access/readers.txt, and an admin is needed to change who reads the state.
exit=5

Error: your identity is a writer in the roles in the state, and an admin is needed to store the roles.
exit=5
```

With roles, the signature of a writer's run names each record the run wrote. `protect status` checks every record against the transactions, and reports one that no writer's run accounts for as untrusted. That is what the `Records` line counts, and it is why a writer's signature says more than a reader's: it vouches for records, and a reader has none to vouch for.

```sh run
pravo -C team protect status | grep '^Records'
```

```text out
Records      1 bound to a transaction of a writer · 1 from before roles
```

## Exercise

:::exercise Promote Eve
Make Eve a writer, as Ada, and let Eve apply a change of her own. Which two things have to happen, and which command needs no `rewrap`?

::hint
A promotion is an edit of Eve's line and `pravo protect roles --yes` again, run by an admin. The command prints what changed and the number of the new version.

::hint
No `rewrap`: Eve is a reader already, so she holds the data key. A role is not a key.

::solution
```sh run
sed -i.bak 's/^\(eve .*\) role:reader$/\1 role:writer/' team/.pravo/access/readers.txt && rm team/.pravo/access/readers.txt.bak
pravo -C team protect roles --yes
sed -i.bak 's/prefix: "eve"/prefix: "eve", separator: "_"/' team/second.cue && rm team/second.cue.bak
PRAVO_CONFIG_DIR=people/eve pravo -C team apply --auto-approve | grep '^Apply complete'
pravo -C team state log | grep -m1 'signed by'
```

```text out
  eve: reader -> writer
Stored the roles as version 2.
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
    signed by eve (verified)
```

A demotion is the same edit the other way. What a demoted writer wrote earlier stays valid, because the admin's version vouches for it; the how-to page [Give people roles](../how-to/give-people-roles.md#6-take-a-member-off) takes a member off the team altogether.

```sh run check
pravo -C team protect status | grep -q 'version 2 in the state'
```
:::

## Clean up

```sh run
pravo -C team destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A role is a fifth word on a line of the readers file: `reader`, `writer` or `admin`. A reader reads, plans and stores a plan; a writer changes the state and approves plans; an admin changes who reads and the roles.
- The file is a request. `pravo protect roles`, run by an admin, pins the roles in the state as a signed version, and a better role in a copy of the file changes nothing.
- A writer's signature names the records the run wrote, so the state can tell a record that a writer wrote from one that was put in the bucket.

:::check
Eve, a reader, has a change ready. How does it reach the state?
::answer
She stores the plan with `pravo plan --store`, and a writer applies it by its id. The transaction is signed by the writer, and the plan keeps Eve's signature as the key that stored it.

Why did Eve's copy of the readers file, with `role:writer` on her line, not make her a writer?
::answer
The roles were pinned. The version in the state decides, and a new version has to be signed by an admin of the version before it. The copy of the file asks for something the state does not say.
:::

The how-to page [Give people roles](../how-to/give-people-roles.md) takes a member off, gives a CI job a role and lists the credentials each role needs on the bucket. [Signatures and roles](../concepts/signatures-and-roles.md) says what a role rests on and what it does not do. The next lesson, [Write a plan policy](c05-write-a-plan-policy.md), writes the rules that every writer's plan has to fit.
