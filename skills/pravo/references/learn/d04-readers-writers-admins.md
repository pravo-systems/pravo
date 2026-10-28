<!-- Generated from docs/learn/d04-readers-writers-admins.md by tools/skillgen. Do not edit. -->

# Readers, writers, admins

Everyone who can read a sealed state can change it, until the readers file says otherwise. Give each line a role, pin the roles in the state so that nobody promotes themselves in a copy of the file, and see what a reader, a writer and an admin may do.

D3 sealed the state to a list of readers. Every one of them may also apply, destroy, move and remove, because a reader key opens the records and nothing else was said. OpenTofu has no word for this at all: whoever has the credentials of the bucket has the state, and whoever has the state and the cloud credentials can change anything. Teams put the difference in IAM, in a pipeline that is the only thing allowed to apply, or in a convention.

```text
the people who hold the bucket credentials and the cloud credentials
```

pravo puts it in the readers file. A fifth word on a line gives the key a role: `reader`, `writer` or `admin`. A reader opens the state, plans and stores a plan. A writer changes the state. An admin changes who reads, changes the roles and removes policies. The file is in the repository, so a role is granted the way read access is: by merging a line. And because the file could be edited in a checkout by the person it is about, the roles are pinned in the state as a signed version, and from then on the version in the state decides.

| Role | May |
|---|---|
| `reader` | Open the state, read it, plan, ask `why`, and store a plan with `plan --store`. |
| `writer` | What a reader may, and change the state: `apply`, `destroy`, `import`, `state mv`, `state rm`, `discover adopt`. Add and edit a policy. Approve a plan. |
| `admin` | What a writer may, and change who reads the state, change the roles, remove a policy, collect garbage and take the seal off. Apply a plan without the approvals a policy asks for. |

## A sealed state, three readers

Work in an empty directory. The root is `team/`, one resource in the in-memory bucket, sealed to Ada, Dan and Eve as D3 did it, and applied once by Ada. The setup is the steps of D3, run for you.

File `team/main.cue`:

```cue
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

```sh
cat team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Sealed|Roles)'
```

Output:

```text
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
eve ssh-ed25519 <ssh-key> x25519:<key>
Sealed       on · format 3 · 3 readers (ada dan eve) · recovery none
Roles        off
```

## A fifth word

Ada is the admin, Dan writes, Eve reads. As soon as one line has a role, a line without one is a reader, so every line gets one.

```sh
sed -i.bak -e '/^ada /s/$/ role:admin/' -e '/^dan /s/$/ role:writer/' -e '/^eve /s/$/ role:reader/' team/.pravo/access/readers.txt && rm team/.pravo/access/readers.txt.bak
cat team/.pravo/access/readers.txt
pravo -C team protect status | grep -E '^(Roles|Records)'
```

Output:

```text
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:writer
eve ssh-ed25519 <ssh-key> x25519:<key> role:reader
Roles        on · 1 admin · 1 writer · 1 reader · from the readers file, not pinned (pravo protect roles) · you are an admin
Records      0 bound to a transaction of a writer · 1 from before roles
```

The roles are in force from this line on, for every pravo that reads this checkout. No `rewrap` is needed: a role is not a key, and the data keys stay as they are. The status says `from the readers file, not pinned`, which is the gap that the next step closes.

## Pin them

`pravo protect roles`, run by an admin, writes the roles to the state as a version signed by the admin's key, and adds one line to the readers file: the digest of that first version. From then on the roles in the state decide, a new version has to be signed by an admin of the version before it, and a state whose chain was replaced or removed is refused with exit code 7.

```sh
pravo -C team protect roles --yes
tail -n 1 team/.pravo/access/readers.txt
pravo -C team protect status | grep '^Roles'
```

Output:

```text
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

```sh
cp -r team eve-team
sed -i.bak 's/^\(eve .*\) role:reader$/\1 role:writer/' eve-team/.pravo/access/readers.txt && rm eve-team/.pravo/access/readers.txt.bak
sed -i.bak 's/length: 2/length: 3/' eve-team/main.cue && rm eve-team/main.cue.bak
PRAVO_CONFIG_DIR=people/eve pravo -C eve-team apply --auto-approve; echo "exit=$?"
PRAVO_CONFIG_DIR=people/eve pravo -C eve-team protect status | grep '^Roles'
```

Output:

```text
Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
exit=5
Roles        on · 1 admin · 1 writer · 1 reader · pinned, version 1 in the state (the readers file asks for other roles: pravo protect roles) · you are a reader
```

The roles in the state say reader, and the apply is refused with exit code 5. The status reports the difference between the file and the state, and names the command that would resolve it, which only an admin can run. The copy carried the identity directories along, which is how the command runs as Eve.

```sh
rm -r eve-team
```

## What a reader does

Eve adds a resource and tries to apply it from the real checkout.

File `team/second.cue`:

```cue
package team

second: $random.#Resource.random_pet & {length: 2, prefix: "eve"}
```

```sh
PRAVO_CONFIG_DIR=people/eve pravo -C team apply --auto-approve; echo "exit=$?"
```

Output:

```text

Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
exit=5
```

A reader plans, and the plan stays in memory: the records it refreshed are not written back. The one thing a reader writes is a stored plan, from [D2](d02-store-a-plan.md), and a writer applies it:

```sh
PRAVO_CONFIG_DIR=people/eve pravo -C team plan --store --actor eve | grep '^Stored'
ID=$(pravo -C team plan list | awk 'NR==1 {print $1}')
PRAVO_CONFIG_DIR=people/dan pravo -C team apply "$ID" --auto-approve | grep '^Apply complete'
pravo -C team state log | grep 'signed by'
```

Output:

```text
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
    signed by dan (verified)
    signed by ada (verified)
```

The transaction is signed by Dan, who applied, and the stored plan keeps Eve's signature as the key that stored it. That is the shape of a review: one person asks, another runs, and the state says which was which.

## What a writer may not do

Dan may change the state and not who reads it, nor the roles:

```sh
PRAVO_CONFIG_DIR=people/dan pravo -C team protect rewrap --yes; echo "exit=$?"
PRAVO_CONFIG_DIR=people/dan pravo -C team protect roles --yes; echo "exit=$?"
```

Output:

```text

Error: your identity is a writer in .pravo/access/readers.txt, and an admin is needed to change who reads the state.
exit=5

Error: your identity is a writer in the roles in the state, and an admin is needed to store the roles.
exit=5
```

With roles, the signature of a writer's run names each record the run wrote. `protect status` checks every record against the transactions, and reports one that no writer's run accounts for as untrusted. That is what the `Records` line counts, and it is why a writer's signature says more than a reader's: it vouches for records, and a reader has none to vouch for.

```sh
pravo -C team protect status | grep '^Records'
```

Output:

```text
Records      1 bound to a transaction of a writer · 1 from before roles
```

## Exercise

## Clean up

```sh
pravo -C team destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A role is a fifth word on a line of the readers file: `reader`, `writer` or `admin`. A reader reads, plans and stores a plan; a writer changes the state and approves plans; an admin changes who reads and the roles.
- The file is a request. `pravo protect roles`, run by an admin, pins the roles in the state as a signed version, and a better role in a copy of the file changes nothing.
- A writer's signature names the records the run wrote, so the state can tell a record that a writer wrote from one that was put in the bucket.

The how-to page [Give people roles](../how-to/give-people-roles.md) takes a member off, gives a CI job a role and lists the credentials each role needs on the bucket. [Signatures and roles](../concepts/signatures-and-roles.md) says what a role rests on and what it does not do. The next lesson, [Write a plan policy](d05-write-a-plan-policy.md), writes the rules that every writer's plan has to fit.
