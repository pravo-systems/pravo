---
title: Give people roles
description: Say which readers of a sealed state may change it and which may only read and plan, pin the roles in the state so that nobody promotes themselves by editing a copy of the readers file, promote a member, take a member off, and give a CI job the role its work needs.
order: 3
minutes: 15
needs: A sealed state, as in [Seal the state](seal-the-state.md). This page uses the in-memory bucket from pravo's source, and the three people are three directories on one machine.
done_when: "`pravo protect status` says `pinned, version 1 in the state`, and `pravo apply` is refused for a reader with exit code 5."
level: intermediate
group: Share and protect
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
  - pattern: 'SHA256:[A-Za-z0-9+/]{43}'
    replace: 'SHA256:<fingerprint>'
  - pattern: 'AAAAC3NzaC1lZDI1NTE5AAAA[A-Za-z0-9+/]{44}'
    replace: '<ssh-key>'
  - pattern: 'x25519:[A-Za-z0-9_-]{43}'
    replace: 'x25519:<key>'
  - pattern: 'roles-genesis [0-9a-f]{64}'
    replace: 'roles-genesis <digest>'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: '[0-9]+(\.[0-9])? s\b'
    replace: '<time> s'
keywords: "rbac permissions admin writer reader promote demote offboard leaver least privilege ci job roles"
---

A sealed state says who may read it. Everyone on that list may also change it, until the list says otherwise. This page gives each member a role, so that some may change the state, some may only read it and plan, and one or two may change who reads and who writes. It then pins the roles in the state, promotes a member, takes one off, and gives a CI job the role its work needs.

| Role | May |
|---|---|
| `reader` | Open the state, read it, plan, ask `why`, and store a plan with `plan --store`. Read a secret that the state keeps. |
| `writer` | What a reader may, and change the state: `apply`, `destroy`, `import`, `state mv`, `state rm`, `discover adopt`. Add and edit a policy. Set a secret. Approve a plan. |
| `admin` | What a writer may, and change who reads the state, change the roles, remove a policy, remove a secret, collect garbage and take the seal off. Apply a plan without the approvals a policy asks for. |

> [!NOTE]
> Every command below runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000. The three people, ada, dan and eve, are three identity directories on one machine, and `PRAVO_CONFIG_DIR` picks one. Each command is written as the person who runs it; a command without a prefix is ada's.

## 1. Start from a sealed state

The root is `team/`, with one resource, and its state is sealed to ada, dan and eve, as [Seal the state](seal-the-state.md) does it. Ada has applied it once.

```cue file=team/main.cue title="team/main.cue"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "team-state"
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

`Roles off`: no line has a role, so each of the three may do everything, which is how a sealed state starts.

## 2. Give each line a role

The role is a fifth word on the line of the readers file: `role:admin`, `role:writer` or `role:reader`. Ada is the admin, dan writes, eve reads. As soon as one line has a role, a line without one is a reader, so give every line one.

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

The roles are in force from this line on, for every pravo that reads this checkout. No `rewrap` is needed, because a role is not a key: the data keys of the state stay as they are. The `Records` line is new as well. With roles, the signature of a writer's run names each record the run wrote, and `protect status` checks every record against the transactions. The one record of this state is from before the roles, which is in order; a record that no writer's run accounts for is reported as untrusted, and refused with exit code 7 before a plan reads it. The file is a request, though, and the next step makes it the state's.

## 3. Pin the roles in the state

A reader who edits their own line in their own checkout is an admin there, for as long as that checkout lasts. `pravo protect roles` closes the gap. An admin runs it: it writes the roles to the state as a signed version, and adds one line to the readers file, the digest of that first version. From then on the roles in the state decide, a new version has to be signed by an admin of the version before it, and a state whose chain was replaced or removed is refused with exit code 7.

```sh run
pravo -C team protect roles --yes
tail -n 1 team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Roles|Records)'
```

```text out
  ada: admin
  dan: writer
  eve: reader
Stored the roles as version 1.
Added roles-genesis <digest> to .pravo/access/readers.txt. Commit it: until it is in the repository the roles of the file decide.
roles-genesis <digest>
Roles        on · 1 admin · 1 writer · 1 reader · pinned, version 1 in the state · you are an admin
Records      0 bound to a transaction of a writer · 1 from before roles
```

Commit the line with the file.

```sh
git add .pravo/access/readers.txt
git commit -m "Give the members of the production state their roles"
git push
```

## 4. See what each role may do

Eve, a reader, adds a resource and tries to apply it.

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

A reader plans, and the plan stays in memory: the records it refreshed are not written to the bucket. The one thing a reader writes is a stored plan, and a writer can apply it.

```sh run
PRAVO_CONFIG_DIR=people/eve pravo -C team plan --store --actor eve | grep '^Stored'
pravo -C team plan list
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
plan-<id>  pending  eve  just now  +1 ~0 -0  expires in 7 days
```

Dan, a writer, applies it by its id. The apply checks the plan as it checks any stored plan, and the transaction is signed by dan, not by eve.

```sh run
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
pravo -C team state log | grep 'signed by'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
    signed by dan (verified)
    signed by ada (verified)
```

What dan may not do is change who reads the state, or the roles:

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

`pravo serve` shows the same in its State section: each member with their role, whether the roles are pinned, and whether every record was written by someone who may change the state. [Explore an estate in a browser](explore-your-estate.md#15-see-who-can-read-the-state) has the picture.

## 5. Promote a member

Eve becomes a writer. The change is an edit of her line, and the command again, run by an admin.

```sh run
sed -i.bak 's/^\(eve .*\) role:reader$/\1 role:writer/' team/.pravo/access/readers.txt && rm team/.pravo/access/readers.txt.bak
pravo -C team protect roles --yes
pravo -C team protect status | grep '^Roles'
```

```text out
  eve: reader -> writer
Stored the roles as version 2.
Roles        on · 1 admin · 2 writer · 0 reader · pinned, version 2 in the state · you are an admin
```

Commit the file. A demotion is the same edit the other way. What a demoted writer wrote earlier stays valid as far as the admin vouched for it, which the next step shows.

## 6. Take a member off

Dan leaves. His line goes from the file, the data keys are made again so that nothing new is sealed to him, and the roles record him as a former key and vouch for what he wrote. The first two are what [Remove a reader](seal-the-state.md#10-remove-a-reader) does; the third is new.

```sh run
grep -v '^dan ' team/.pravo/access/readers.txt > readers.new && mv readers.new team/.pravo/access/readers.txt
pravo -C team protect rewrap
pravo -C team protect roles --yes
```

```text out
Readers in .pravo/access/readers.txt: ada eve  (dan removed)
New data keys for 1 reader group. Records are sealed again as they are next written.
  dan: removed, now a former key
  dan: vouched for 1 transaction (records they wrote stay valid)
Stored the roles as version 3.
```

The transaction that dan ran in step 4 stays verified, because the version that ada signed vouches for it: the record `second` that he wrote is still trusted, and a plan still reads it. What dan cannot do is add anything. A run signed with his key is refused by every honest pravo, with exit code 7, and he cannot open the state any more:

```sh run
pravo -C team protect status | grep -E '^(Roles|Records)'
PRAVO_CONFIG_DIR=people/dan pravo -C team state show second; echo "exit=$?"
```

```text out
Roles        on · 1 admin · 1 writer · 0 reader · pinned, version 3 in the state · you are an admin
Records      0 bound to a transaction of a writer · 1 vouched for by an admin (the key left or was demoted) · 1 from before roles

Error: you are not a reader of this state (identity dan is not a reader).
  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.
exit=5
```

A copy of the bucket made before this step still opens with dan's key, and the records that were sealed in the old group do until they are written again. `pravo protect rewrap --all` seals them again now, `pravo protect gc` drops the old group afterwards, and the credentials of the bucket are taken from dan where they are kept: [Remove a reader](seal-the-state.md#10-remove-a-reader) has the three.

## 7. Give a CI job a role

A job holds an identity of its own, made with `--ci` as [Let a CI job read](seal-the-state.md#9-let-a-ci-job-read) does. Give it the smallest role that its work needs: a job that plans and stores is a reader, and a job that applies is a writer. No job is an admin, because an admin may change who reads the state and may apply without the approvals a policy asks for.

```sh run
pravo id new --ci --name deploy-bot --out deploy-bot.identity > /dev/null
echo "$(PRAVO_IDENTITY="$(cat deploy-bot.identity)" pravo -C team id show | tail -n 1 | sed 's/^ *//') role:writer" >> team/.pravo/access/readers.txt
pravo -C team protect rewrap | head -n 2
pravo -C team protect roles --yes
```

```text out
Readers in .pravo/access/readers.txt: ada eve deploy-bot
Wrapped the data keys for deploy-bot (2 reader groups, 2 new wrapped keys)
  deploy-bot: writer
Stored the roles as version 4.
```

Store the file as the secret `PRAVO_IDENTITY` of the apply job, delete it, and commit the readers file. A job that only plans gets an identity of its own with `role:reader`, and the credentials of the bucket to match.

## 8. Give the bucket the same rules

The roles are checked by every pravo that reads the readers file and the roles in the state. Someone who holds the credentials of the bucket, the data key and a pravo of their own can still write an object: the next honest run finds the record, refuses it with exit code 7, and does not stop it from having been written. What stops it is the bucket. Give its credentials by role:

| Role | Credentials on the bucket |
|---|---|
| `reader` | Get and list on the whole prefix. Put on `plans/`. |
| `writer` | Get, put, delete and list on the prefix, except `meta.json`, `policy/`, `roles/` and `secret/`, where a writer has get and list and may put a new object under `policy/` and under `secret/`. |
| `admin` | Everything on the prefix, `meta.json`, `policy/`, `roles/` and `secret/` included. |

An object lock on `roles/` keeps the newest versions of the chain from being deleted, which the pin cannot see.

## Recap

- A role is a fifth word on a line of the readers file. The file is a request; `pravo protect roles`, run by an admin, makes it the state's and pins it with one more line.
- A reader reads, plans and stores a plan. A writer changes the state and approves plans. An admin changes who reads, changes the roles, removes policies, and needs no approvals.
- A promotion or a demotion is an edit of the file and the command again.
- A member who leaves is removed from the file; `rewrap` stops sealing to them, `protect roles` records them as a former key and vouches for what they wrote, and `rewrap --all` with `gc` takes their old key out of the bucket.
- A job gets the smallest role its work needs, and never admin. The credentials of the bucket follow the roles.

## What next

- [Approve a plan](approve-a-plan.md) uses the roles: a policy makes a plan wait for the approval of a second writer.
- [`pravo protect roles`](../reference/roles.md) lists what each role may do, what the command writes, what is refused and with which message.
- [Signatures and roles](../concepts/signatures-and-roles.md) explains what a role rests on and what it does not do.
- [Run pravo in a pipeline](ci.md) gives the plan job and the apply job their identities.
