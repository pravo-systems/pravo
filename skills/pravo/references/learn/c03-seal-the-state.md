<!-- Generated from docs/learn/c03-seal-the-state.md by tools/skillgen. Do not edit. -->

# Seal the state

A bucket that anyone on the team can read holds every password the providers returned. Make an identity, list the readers in the repository and seal the state, so that the bucket shows addresses and no values, and every change is signed by the key that made it.

A state holds what the providers returned: ids, addresses, and the password of every database, the token of every service account, the private half of every key pair. In OpenTofu `sensitive = true` hides a value in the output and nowhere else, and the state file holds it in plain text. Encryption on the bucket does not help against the people the bucket is shared with: a backup, an old version of the object or a laptop with read access to the bucket has every value.

```hcl
resource "random_password" "db" {
  length = 16
}
# terraform.tfstate: "result": "cwkfcFw%]sVjLB1="
```

```cue
secret: $random.#Resource.random_password & {length: 16}
// the bucket, once sealed: "attributes": "<sealed>"
```

pravo can seal a state. The values of every record, the outputs and the stored plans are encrypted to a list of readers, and the list is a file in the repository, reviewed like any other change. The bucket keeps the addresses, the types and the revisions in the open, so that the graph, the locks and the viewer work for everyone, and it keeps no value. Each reader has an identity, which is also a signing key, so every change to a sealed state says which key made it.

## A password in the bucket

Work in an empty directory: it is the team's root, with the state in the in-memory bucket as in C1, and one resource that returns a secret. The people of this track are directories under `people/`, one identity each; `PRAVO_CONFIG_DIR` picks the one a command runs as, and the lesson sets it to Ada's.

File `main.cue`:

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

$random: rnd.#Provider

secret: $random.#Resource.random_password & {length: 16}
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | tail -n 1
curl -s "$FAKES3_URL/northwind-state/prod/resources/secret.json" | grep '"result"'
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
    "result": "<password>",
```

`curl` is anyone with read access to the bucket. The password is in the record, as it would be in a state file.

## An identity

`pravo id new` makes an identity: a reader key, which opens sealed values, and a signing key, which signs what the identity does. The reader key is sealed under a passphrase. On a terminal the command asks for it twice; the lesson sets `PRAVO_PASSPHRASE` so that it runs in a script. The command touches no state.

```sh
pravo id new --name ada
```

Output:

```text
Identity ada created
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase in people/ada/id/ada

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

The last line is public: a name and two public keys. It is what Ada gives the team, and the file is in her config directory, which is `~/.config/pravo/id` on a real machine. Dan makes his own, on his own machine. Here his is a second directory.

## The readers file

The list of readers is `.pravo/access/readers.txt` in the repository, one line per reader, the line that `id new` printed. Whoever can merge a change to this file decides who may read the state. Writers take the list from the checkout, never from the bucket, so someone who can write to the bucket cannot add themselves.

```sh
mkdir -p .pravo/access
pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
cat .pravo/access/readers.txt
```

Output:

```text
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
```

## Seal it

`pravo protect init --encrypt` is the one step that changes the bucket. It says what it will do and asks for a yes; `--yes` answers in a script. It refuses to run for an identity that is not on the list, since sealing to a list you are not on would lock you out.

```sh
pravo protect init --encrypt --yes
```

Output:

```text
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

```sh
PW=$(pravo state show secret --json | jq -r .node.attributes.result)
for k in $(curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/" | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do
  curl -s "$FAKES3_URL/northwind-state/$k"
done | grep -c -F "$PW" || true
```

Output:

```text
0
```

Ada reads it as before, because her identity opens the records:

```sh
pravo state show secret | grep result | sed 's/"[^"]*"/"<password>"/'
```

Output:

```text
	result:           "<password>"
```

## Break it: read without a key

Someone who has the bucket and no identity on the list. `PRAVO_CONFIG_DIR=nobody` is a directory with no identity in it.

```sh
PRAVO_CONFIG_DIR=nobody pravo state list
PRAVO_CONFIG_DIR=nobody pravo state show secret
```

Output:

```text
secret

Error: you are not a reader of this state (no identity found).
  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.
```

The error says where the list is and what to do. A backup or an old version of an object shows the same: it opens with the keys that were in force when it was written.

## Every change is signed

Ada changes the length and applies. The apply is a transaction, as in C1, and now it is signed by the identity that ran it: once when it starts and once when it ends.

```sh
sed -i 's/length: 16/length: 20/' main.cue
pravo apply --auto-approve | tail -n 1
pravo state log | head -n 5
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
<txn>  apply    applied   <time> <duration>
    pravo apply --auto-approve
    signed by ada (verified)
    1 added, 0 changed, 1 destroyed
    replace  secret  r1 -> r2
```

`--actor` in C1 was a label that pravo wrote down. `signed by ada (verified)` is a fact that pravo checked: the signing key is the second field of Ada's line in the readers file, so the file that says who may read also says whose signatures to trust. The signature covers the event, which key ran which command and which records it changed at which revisions. It does not cover the values, and it cannot tell that a transaction was deleted from the bucket. [Signatures and roles](../concepts/signatures-and-roles.md#what-a-signature-binds) lists what it binds and what it leaves.

The transaction from before the seal is unsigned, which is not an error. `pravo protect status` is the one screen to read when you are unsure where a state stands:

```sh
pravo protect status
```

Output:

```text
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

Removing a reader is the same steps the other way, with a difference that no tool can remove: a reader who leaves has read what they read, and a copy of the bucket made before the change opens with their key. `rewrap` after the removal seals new writes to the others, `rewrap --all` seals every record again now, and the secrets that the person could read are replaced where they live. The how-to page [Seal the state](../how-to/seal-the-state.md#10-remove-a-reader) walks through it.

## Clean up

```sh
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
```

## Recap

- A state holds every value the providers returned, and a bucket that the team can read holds them all. A sealed state keeps the addresses in the open and encrypts the values to a list of readers.
- The list is `.pravo/access/readers.txt` in the repository, and merging a change to it is the act of granting or removing read access. A line is a name and two public keys; the private keys stay with the person.
- Every change to a sealed state is signed by the identity that ran it, and the readers file says whose signatures to trust. `--actor` is a label; `signed by ada (verified)` is a fact.

The how-to page [Seal the state](../how-to/seal-the-state.md) adds a CI job as a reader, a recovery recipient and the way back out. A sealed state can also keep secrets of its own, as named values with a history, which [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) sets and hands to a provider. The next lesson, [Readers, writers, admins](c04-readers-writers-admins.md), says which readers may change the state.
