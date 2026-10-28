---
title: Seal the state
description: A bucket that anyone on the team can read holds every password the providers returned. Make an identity, list the readers in the repository and seal the state, so that the bucket shows addresses and no values, and every change is signed by the key that made it.
order: 63
group: Track C · A team on one state
minutes: 15
needs: C1
learn: What does the state hold that you would not put in a ticket? Who decides who may read a sealed state, and where is that decision kept? What does a signature on a transaction say?
done_when: "The bucket holds no copy of the password, a person without an identity gets exit code 5 from pravo state show, and the log says signed by ada (verified)."
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
  - pattern: 'SHA256:[A-Za-z0-9+/]{43}'
    replace: 'SHA256:<fingerprint>'
  - pattern: 'AAAAC3NzaC1lZDI1NTE5AAAA[A-Za-z0-9+/]{44}'
    replace: '<ssh-key>'
  - pattern: 'x25519:[A-Za-z0-9_-]{43}'
    replace: 'x25519:<key>'
  - pattern: '[0-9]+(\.[0-9])? s\b'
    replace: '<time> s'
  - pattern: 'rec/[0-9a-f]{64}'
    replace: 'rec/<sha256>'
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "(?m)^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}"
    replace: "<txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "\\[id=[^\\]]+\\]"
    replace: "[id=<id>]"
  - pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} [0-9][0-9.a-z]*"
    replace: "<time> <duration>"
  - pattern: '"result": +"[^"]*"'
    replace: '"result": "<password>"'
---

A state holds what the providers returned: ids, addresses, and the password of every database, the token of every service account, the private half of every key pair. In OpenTofu `sensitive = true` hides a value in the output and nowhere else, and the state file holds it in plain text. Encryption on the bucket does not help against the people the bucket is shared with: a backup, an old version of the object or a laptop with read access to the bucket has every value.

:::compare
```hcl title="main.tf"
resource "random_password" "db" {
  length = 16
}
# terraform.tfstate: "result": "cwkfcFw%]sVjLB1="
```

```cue title="main.cue"
secret: $random.#Resource.random_password & {length: 16}
// the bucket, once sealed: "attributes": "<sealed>"
```
:::

pravo can seal a state. The values of every record, the outputs and the stored plans are encrypted to a list of readers, and the list is a file in the repository, reviewed like any other change. The bucket keeps the addresses, the types and the revisions in the open, so that the graph, the locks and the viewer work for everyone, and it keeps no value. Each reader has an identity, which is also a signing key, so every change to a sealed state says which key made it.

## A password in the bucket

Work in an empty directory: it is the team's root, with the state in the in-memory bucket as in C1, and one resource that returns a secret. The people of this track are directories under `people/`, one identity each; `PRAVO_CONFIG_DIR` picks the one a command runs as, and the lesson sets it to Ada's.

```cue file=main.cue title="main.cue"
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

$random: rnd.#Provider

secret: $random.#Resource.random_password & {length: 16}
```

```sh run
pravo init > /dev/null
pravo apply --auto-approve | tail -n 1
curl -s "$FAKES3_URL/northwind-state/prod/resources/secret.json" | grep '"result"'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
    "result": "<password>",
```

`curl` is anyone with read access to the bucket. The password is in the record, as it would be in a state file.

## An identity

`pravo id new` makes an identity: a reader key, which opens sealed values, and a signing key, which signs what the identity does. The reader key is sealed under a passphrase. On a terminal the command asks for it twice; the lesson sets `PRAVO_PASSPHRASE` so that it runs in a script. The command touches no state.

```sh run
pravo id new --name ada
```

```text out
Identity ada created
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase in people/ada/id/ada

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

The last line is public: a name and two public keys. It is what Ada gives the team, and the file is in her config directory, which is `~/.config/pravo/id` on a real machine. Dan makes his own, on his own machine. Here his is a second directory.

## The readers file

The list of readers is `.pravo/access/readers.txt` in the repository, one line per reader, the line that `id new` printed. Whoever can merge a change to this file decides who may read the state. Writers take the list from the checkout, never from the bucket, so someone who can write to the bucket cannot add themselves.

```sh run
mkdir -p .pravo/access
pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
cat .pravo/access/readers.txt
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
```

## Seal it

`pravo protect init --encrypt` is the one step that changes the bucket. It says what it will do and asks for a yes; `--yes` answers in a script. It refuses to run for an identity that is not on the list, since sealing to a list you are not on would lock you out.

```sh run
pravo protect init --encrypt --yes
```

```text out
Readers  .pravo/access/readers.txt  (ada dan)
This will:
  1  write 1 record as immutable objects  rec/<sha256>
  2  encrypt their attributes and private data to 2 readers (ada dan)
  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way
  4  set the state to format 3. Earlier versions of pravo will refuse it
store check passed
1 record sealed in <time> s
outputs sealed · recipients written to meta.json

The state is sealed to ada dan.

Commit the readers file. Writers read the list from git:
  .pravo/access/readers.txt
No recovery recipient. If every reader loses their key, the state cannot be read.
Add one with: pravo protect rewrap --recovery 3/5
```

Read the four lines of the plan. The records become immutable objects, named by their digest, and their attributes are encrypted to the two readers. The outputs and the stored plans are sealed the same way. The last two lines are a fact, not a step: this state has no recovery recipient, so if every reader loses their key, nothing can read it. The how-to page [Seal the state](../how-to/seal-the-state.md#12-add-a-recovery-recipient) adds one, as shares that several people hold.

Now `curl` is the leaked bucket. Every object, searched for the password:

```sh run
PW=$(pravo state show secret --json | jq -r .node.attributes.result)
for k in $(curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/" | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do
  curl -s "$FAKES3_URL/northwind-state/$k"
done | grep -c -F "$PW" || true
```

```text out
0
```

Ada reads it as before, because her identity opens the records:

```sh run
pravo state show secret | grep result | sed 's/"[^"]*"/"<password>"/'
```

```text out
	result:           "<password>"
```

## Break it: read without a key

Someone who has the bucket and no identity on the list. `PRAVO_CONFIG_DIR=nobody` is a directory with no identity in it.

:::predict Can this person list the addresses of the state? Can they read a record?
Ask what the seal left in the open, and why.

::reveal
`pravo state list` works, because the addresses, the types and the revisions are in the open: the graph and the locks need them. `pravo state show` is refused with exit code 5, because the attributes are sealed to the readers.
:::

```sh run exit=5
PRAVO_CONFIG_DIR=nobody pravo state list
PRAVO_CONFIG_DIR=nobody pravo state show secret
```

```text out
secret

Error: you are not a reader of this state (no identity found).
  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.
```

The error says where the list is and what to do. A backup or an old version of an object shows the same: it opens with the keys that were in force when it was written.

## Every change is signed

Ada changes the length and applies. The apply is a transaction, as in C1, and now it is signed by the identity that ran it: once when it starts and once when it ends.

```sh run
sed -i 's/length: 16/length: 20/' main.cue
pravo apply --auto-approve | tail -n 1
pravo state log | head -n 5
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
<txn>  apply    applied   <time> <duration>
    pravo apply --auto-approve
    signed by ada (verified)
    1 added, 0 changed, 1 destroyed
    replace  secret  r1 -> r2
```

`--actor` in C1 was a label that pravo wrote down. `signed by ada (verified)` is a fact that pravo checked: the signing key is the second field of Ada's line in the readers file, so the file that says who may read also says whose signatures to trust. The signature covers the event, which key ran which command and which records it changed at which revisions. It does not cover the values, and it cannot tell that a transaction was deleted from the bucket. [Signatures and roles](../concepts/signatures-and-roles.md#what-a-signature-binds) lists what it binds and what it leaves.

The transaction from before the seal is unsigned, which is not an error. `pravo protect status` is the one screen to read when you are unsure where a state stands:

```sh run
pravo protect status
```

```text out
Sealed       on · format 3 · 2 readers (ada dan) · recovery none
Store        s3://northwind-state/prod
Readers      .pravo/access/readers.txt matches the store
You          ada · reader key x25519:<key>
Roles        off
Signatures   2 of 3 transactions verified · 1 unsigned
Unsettled    none
```

`Roles off` says that every reader may also write the state. The next lesson changes that.

## Exercise

:::exercise Add a reader
Eve joins the team. Make her identity in `people/eve`, add her line to the readers file and let her read the password. Only an existing reader can add one. Say why.

::hint
`PRAVO_CONFIG_DIR=people/eve pravo id new --name eve` makes the identity and prints the line. Append it to `.pravo/access/readers.txt`.

::hint
The readers file changed, and the state has not heard of it yet. `pravo protect rewrap`, run by Ada, locks the data keys for the new reader. It does not rewrite the records.

::solution
```sh run
PRAVO_CONFIG_DIR=people/eve pravo id new --name eve | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
pravo protect rewrap
PRAVO_CONFIG_DIR=people/eve pravo state show secret | grep result | sed 's/"[^"]*"/"<password>"/'
```

```text out
Readers in .pravo/access/readers.txt: ada dan eve
Wrapped the data keys for eve (1 reader group, 1 new wrapped key)
meta.json updated
	result:           "<password>"
```

Only a reader can run `rewrap`, because only a reader holds the data key that has to be locked for Eve. That is why someone who can write to the bucket, and even to the readers file, cannot read the state by adding a line: an existing reader has to act, and the act is signed.

```sh run check
PRAVO_CONFIG_DIR=people/eve pravo state show secret | grep -q result
```
:::

Removing a reader is the same steps the other way, with a difference that no tool can remove: a reader who leaves has read what they read, and a copy of the bucket made before the change opens with their key. `rewrap` after the removal seals new writes to the others, `rewrap --all` seals every record again now, and the secrets that the person could read are replaced where they live. The how-to page [Seal the state](../how-to/seal-the-state.md#10-remove-a-reader) walks through it.

## Clean up

```sh run hidden
# The exercise runs in a copy of the directory, and the bucket is shared, so the file and the state are made to agree first.
pravo protect rewrap
```

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
```

## Recap

- A state holds every value the providers returned, and a bucket that the team can read holds them all. A sealed state keeps the addresses in the open and encrypts the values to a list of readers.
- The list is `.pravo/access/readers.txt` in the repository, and merging a change to it is the act of granting or removing read access. A line is a name and two public keys; the private keys stay with the person.
- Every change to a sealed state is signed by the identity that ran it, and the readers file says whose signatures to trust. `--actor` is a label; `signed by ada (verified)` is a fact.

:::check
Someone has a copy of the bucket and no identity. What can they read?
::answer
The addresses, the types, the dependencies and the revisions of every record, and the transactions. No attribute, no output and no stored plan: those are sealed to the readers.

Why can a person who can write to the bucket not add themselves as a reader?
::answer
Writers take the readers list from the checkout, not from the bucket, and a new reader needs an existing reader to run `rewrap`, which locks the data keys for them. Nobody can lock a key they do not hold.
:::

The how-to page [Seal the state](../how-to/seal-the-state.md) adds a CI job as a reader, a recovery recipient and the way back out. The next lesson, [Readers, writers, admins](c04-readers-writers-admins.md), says which readers may change the state.
