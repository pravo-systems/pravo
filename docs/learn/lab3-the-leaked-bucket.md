---
title: "Lab 3: the leaked bucket"
description: Copy a state bucket, find a secret in the copy, seal the state and copy it again. See what a person with the bucket and no key can read.
order: 53
group: Track B · Labs and project
minutes: 25
needs: "B7, and a checkout of pravo's source with Go, for the in-memory bucket"
learn: What does a copy of the state bucket show? What does sealing hide, and what does it leave open?
done_when: "The secret is in the first copy of the bucket and not in the second. `pravo state show` is refused with exit code 5 for a person with no identity. You can name what the second copy still shows."
level: advanced
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
  - pattern: 'pravo [0-9][0-9A-Za-z.+-]*'
    replace: 'pravo <version>'
  - pattern: 'pravo >= [0-9][0-9A-Za-z.+-]*'
    replace: 'pravo >= <version>'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z'
    replace: '<time>'
  - pattern: '"config_hash": "[0-9a-f]+"'
    replace: '"config_hash": "<hash>"'
  - pattern: '"provider_version": "[0-9][0-9A-Za-z.+-]*"'
    replace: '"provider_version": "<version>"'
  - pattern: '"user": "[^"]*"'
    replace: '"user": "<user>"'
  - pattern: '"host": "[^"]*"'
    replace: '"host": "<host>"'
  - pattern: 'v[0-9]+\.[0-9]+\.[0-9]+ as '
    replace: 'v<version> as '
---

A state holds the attributes of every resource, and a provider returns some of them as secrets. Whoever can read the bucket that holds the state can read them. Sealing encrypts the attributes to a list of readers. This lab copies a bucket, finds a secret in the copy, seals the state and copies the bucket again. [What sealing protects](../concepts/sealed-state.md) explains what it covers, and [pravo id and pravo protect](../reference/protect.md) lists the commands.

> [!NOTE]
> Every command runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000. The two people of the lab are two directories on one machine.

## Prepare a state with a secret

Work in an empty directory. Start the bucket in a second terminal, from a checkout of pravo's source. It needs Go, and it forgets everything when it stops.

```sh
go run ./tools/fakes3 -addr 127.0.0.1:9000
```

Wait for `listening on 127.0.0.1:9000`. If another program uses port 9000, pick a free port here and in the `endpoint` line of `main.cue`.

Give the shell test credentials, a passphrase for the identities of the lab and a directory to keep them in. The directory keeps the identities apart from `~/.config/pravo`.

```sh
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export PRAVO_PASSPHRASE="an example passphrase" PRAVO_CONFIG_DIR=people/ada
```

Run both lines in every terminal that runs a pravo command.

A script reads the passphrase from the environment. In a terminal pravo ignores the variable and asks for the passphrase without echo, twice when it makes an identity. Type `an example passphrase` each time. Every identity of the lab uses it. Where pravo asks, run the next line of a block only after you answer.

The specification keeps its state in the bucket and holds one resource, a password of 24 characters. [Keep the state in a shared bucket](../how-to/remote-state.md) explains the `state` block. The provider generates the value, so it appears nowhere in your files. The password has no special characters. The state file escapes some of them, such as `<` and `&`, and the search would miss the value.

```cue file=main.cue title="main.cue"
package lab

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

db_password: $random.#Resource.random_password & {length: 24, special: false}
```

```sh run
pravo init
pravo apply --auto-approve
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  random = hashicorp/random v3.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
  +     bcrypt_hash: (sensitive)
...
  +     result:      (sensitive)
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The plan prints `(sensitive)` where the value would be. The mark hides the value on the screen. It does not change what the state stores.

## You hold a copy of the bucket

Someone gives you a copy of the bucket: a backup, a snapshot, an export for a support case. `pravo state pull` makes the same thing. It copies the objects of the state to a directory, as they are.

```sh run
pravo state pull copy-1
find copy-1 -type f | sort
```

```text out
Copied 4 objects from s3://northwind-state/prod to copy-1
copy-1/meta.json
copy-1/outputs.json
copy-1/resources/db_password.json
copy-1/txn/<txn>.json
```

The copy holds one file for each resource and one for each transaction, with the outputs and `meta.json`. Now search it for the secret. The next command saves the value in `secret.txt`. Every copy can then be searched for it without printing it. Do not print the file.

```sh run
sed -n 's/^ *"result": "\(.*\)",*$/\1/p' copy-1/resources/db_password.json > secret.txt
awk '{ print length($0) " characters" }' secret.txt
grep -rlF -f secret.txt copy-1
```

```text out
24 characters
copy-1/resources/db_password.json
```

The secret is in the clear in the record of `db_password`. Reading it needs no key, no passphrase and no pravo: a text editor shows it. The record holds more than the secret. This is the whole file, with the two secret lines masked on the screen:

```sh run
sed -E 's/^( *"(result|bcrypt_hash)": ).*/\1"<hidden>",/' copy-1/resources/db_password.json
```

```text out
{
  "addr": "db_password",
  "mode": "managed",
  "type": "random_password",
  "name": "db_password",
  "provider": "registry.opentofu.org/hashicorp/random",
  "provider_key": "random",
  "provider_version": "<version>",
  "schema_version": 3,
  "attributes": {
    "bcrypt_hash": "<hidden>",
    "id": "none",
    "keepers": null,
    "length": 24,
    "lower": true,
    "min_lower": 0,
    "min_numeric": 0,
    "min_special": 0,
    "min_upper": 0,
    "number": true,
    "numeric": true,
    "override_special": null,
    "result": "<hidden>",
    "special": false,
    "upper": true
  },
  "config_hash": "<hash>",
  "revision": 1,
  "generation": "<txn>",
  "refreshed_at": "<time>",
  "applied_at": "<time>",
  "updated_at": "<time>",
  "txn": "<txn>"
}
```

The copy shows the address, the type, the provider and every attribute, with its value. That includes `bcrypt_hash`, which is derived from the password. The files of `txn/` show who ran which command, and when.

## Seal the state

Sealing encrypts the attributes of every record to a list of readers. An identity has two keys. The reader key opens the state. The signing key is not used while the state has no roles. `pravo id new` makes the identity and prints one public line for the list. The lab has two readers: Ada, who runs the commands, and Dan.

```sh run
mkdir -p people
pravo id new --name ada
```

```text out
Identity ada created
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase in people/ada/id/ada

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

The repository keeps the lines in `.pravo/access/readers.txt`, one for each reader. Whoever can merge a change to this file decides who may read, so review a change to it like code. In practice Dan runs `pravo id new` on his own machine and sends his line. Here his machine is a second directory, `people/dan`. The block hides the line that the command prints, because the next block reads it back.

```sh run
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan > /dev/null
```

`pravo id show` prints a line again, so the file takes the line of each reader. In a real repository, commit the file.

```sh run
mkdir -p .pravo/access
pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
cat .pravo/access/readers.txt
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
```

`pravo protect check` tests the store and writes nothing to the state. `pravo protect init --encrypt` seals the state and is the only step that changes the bucket. `--yes` answers its question.

```sh run
pravo protect check
```

```text out
Store            s3://northwind-state/prod  (eu-west-1)
Conditional      create-only ok · replace ok · delete ok
                 8 concurrent replaces: 1 winner · listing sees a new key at once
State            1 record · format 1 · last written by pravo <version>
Readers file     .pravo/access/readers.txt · 2 readers (ada dan)
Running          0 transactions · 0 locks held
Machines         every writer in the last 30 days used pravo >= <version>
Ready to seal. Next: pravo protect init --encrypt
```

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

`pravo protect status` opens the state with your identity and says who it is sealed to.

```sh run
pravo protect status
```

```text out
Sealed       on · format 3 · 2 readers (ada dan) · recovery none
Store        s3://northwind-state/prod
Readers      .pravo/access/readers.txt matches the store
You          ada · reader key x25519:<key>
Roles        off
Unsettled    none
```

The `You` line shows that Ada's identity opens the state.

## A new copy of the bucket

:::predict You copy the bucket again and search the copy for the secret. Which of these can you still read: the value, the address of the resource, the names of the readers?
Sealing rewrote the records. Ask what it had to leave readable so that pravo can still list and order them.

::reveal
The address and the names of the readers. pravo lists and orders resources by their addresses and dependencies, and it needs no key for that. `meta.json` names the readers. The value is encrypted to the readers and is not in the copy.
:::

A person who copies a bucket uses an S3 tool and needs no key. `pravo state pull` needs a reader of a sealed state, so Ada makes this copy. It writes the objects as they are in the bucket. The copy holds the same bytes as one made with an S3 tool.

```sh run
pravo state pull copy-2
find copy-2 -type f | sort
```

```text out
Copied 5 objects from s3://northwind-state/prod to copy-2
copy-2/meta.json
copy-2/outputs.json
copy-2/rec/<sha256>
copy-2/resources/db_password.json
copy-2/txn/<txn>.json
```

The record moved to `rec/`, an object that is named by a hash of its bytes. `resources/db_password.json` is a pointer to it. The number after each name below is the number of files that contain the secret.

```sh run
for c in copy-1 copy-2; do echo "$c: $(grep -rlF -f secret.txt $c | grep -c .)"; done
```

```text out
copy-1: 1
copy-2: 0
```

The secret is gone from the copy. This is the record of `db_password`, with the ciphertext cut out:

```sh run
sed -E 's/("ct": ").*"/\1<ciphertext>"/' copy-2/rec/*
```

```text out
{
  "addr": "db_password",
  "mode": "managed",
  "type": "random_password",
  "name": "db_password",
  "provider": "registry.opentofu.org/hashicorp/random",
  "provider_key": "random",
  "provider_version": "<version>",
  "schema_version": 3,
  "revision": 1,
  "generation": "<txn>",
  "refreshed_at": "<time>",
  "applied_at": "<time>",
  "updated_at": "<time>",
  "txn": "<txn>",
  "v": 1,
  "sealed": {
    "alg": "hpke-x25519-aes256gcm",
    "group": "g1",
    "ct": "<ciphertext>"
  }
}
```

Compare it with the first copy. The address, the type, the provider, the revision and the times are where they were. `attributes` and `config_hash` are gone. `sealed` holds them, encrypted to the readers, and `ct` is the ciphertext. `outputs.json` holds an envelope of the same kind and nothing else.

Two more things stay readable. `meta.json` lists the readers by name. Each transaction shows who ran it, on which host, with which command and with what status.

```sh run
grep -h -E '"(name|command|user|host|status)":' copy-2/meta.json copy-2/txn/* | sed 's/^ *//'
```

```text out
"name": "ada",
"name": "dan",
"command": "pravo apply --auto-approve",
"user": "<user>",
"host": "<host>",
"status": "applied",
```

## Someone without a key

A person who has the bucket and no identity can list what exists. They cannot open a record. `nobody` is a configuration directory with no identity in it.

```sh run
PRAVO_CONFIG_DIR=nobody pravo state list
```

```text out
db_password
```

```sh run exit=5
PRAVO_CONFIG_DIR=nobody pravo state show db_password
```

```text out
Error: you are not a reader of this state (no identity found).
  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.
```

Exit code 5 is a refusal. [B10](b10-policy.md) and [Lab 1](lab1-break-it-on-purpose.md) meet it as a policy. In a sealed state it also means that the caller is not a reader. The [exit codes](../reference/exit-codes.md) page lists every case.

Dan has an identity, and the readers file lists it. He opens the same record. The command counts the output lines that hold the secret from the first copy.

```sh run
PRAVO_CONFIG_DIR=people/dan pravo state show db_password | grep -cF -f secret.txt
```

```text out
1
```

A reader sees the value. Sealing does not hide a value from the people on the list.

## What sealing does not do

- **A copy made before sealing stays open.** The first copy still holds the secret in the clear. So does every earlier version of an object in a bucket that keeps versions. Replace the secret.
- **A reader sees every value.** The list of readers is for the whole state. There is no reader for one resource.
- **A removed reader keeps what they read.** A copy of the bucket made before the removal opens with their key. Replace the secrets they could read where they live.
- **Nothing recovers a state if every reader loses their key,** unless it has a recovery recipient. The lab did not set one. [Seal the state](../how-to/seal-the-state.md) shows how, and keeping more than one reader comes first.
- **Roles and signatures are not part of it.** Sealing hides values. It does not say who may change the state. A person who can write to the bucket can delete or replace its objects.

[What sealing protects](../concepts/sealed-state.md) lists the rest.

## Clean up

Destroy the resource and delete the copies. The first copy holds the secret in the clear.

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
```

```sh run
rm -r copy-1 copy-2 secret.txt
```

Stop the bucket with `Ctrl-C` in the second terminal. A second run needs a bucket that you start again and a new empty directory. The old directory keeps the readers file, which says that the state is sealed. A new bucket has no state, so a command that writes is refused. The old bucket keeps a sealed state, and a new directory has no identity that opens it.

Clear the variables of the lab from the shell:

```sh
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY PRAVO_PASSPHRASE PRAVO_CONFIG_DIR
```

## Recap

- A copy of the bucket of an unsealed state holds every attribute in the clear. `(sensitive)` hides a value on the screen only.
- A copy of a sealed state shows addresses, types, revisions, the names of the readers and who ran which command. The attributes are encrypted to the readers.
- A person with no identity lists the state and is refused by `pravo state show` with exit code 5. A reader sees every value.

:::check
The second copy was made of the same bucket as the first. Why is the secret not in it?
::answer
Sealing rewrote the records before the second copy was made. The attributes are in `sealed`, encrypted to Ada and Dan.

Dan leaves the team and a reader deletes his line from the readers file. Is the second copy of the bucket safe from him now?
::answer
No. The copy was made before the removal, and Dan's key opens it. Removing the line and running `pravo protect rewrap` protects the records that are written after it. Replace the secret.
:::

[Seal the state to a list of readers](../how-to/seal-the-state.md) seals a state of your own. It also adds a reader for a CI job.
