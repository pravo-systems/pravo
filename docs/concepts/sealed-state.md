---
title: What sealing protects
description: What a sealed state encrypts, who can read it, and what it does not do. It gives confidentiality of values against a copy of the bucket, and nothing else.
order: 11
minutes: 10
---

A state records the attributes of every resource, and some of them are secrets. Sealing encrypts those values to a list of readers, so that a copy of the bucket, a backup or an old version of an object shows no value. This page says what is sealed, how a reader opens it, and what a sealed state still leaves open. The commands are in [pravo id and pravo protect](../reference/protect.md).

Sealing answers one question: who can read the values? It does not say who may change the state, and it does not make a change visible after the fact. Those need roles and signatures, which pravo does not have yet.

## What is sealed

Protection is off by default, and a state with protection off is unchanged. `pravo protect init --encrypt` turns it on in two steps that run together. Each record is stored once as an object named by the SHA-256 of its bytes, `rec/SHA256`, and never overwritten again: `resources/ADDRESS.json` becomes a small pointer that is replaced only by compare-and-swap (format 2). The values in those objects are then encrypted (format 3).

| Object | Sealed | Readable by anyone with the bucket |
|---|---|---|
| A record, `rec/SHA256` | The attributes and the private data of the provider. | The address, type, name, provider, dependencies, revision, generation, the times, the transaction that wrote it and the hash of its configuration. |
| `outputs.json` | All of it. | Nothing. |
| A stored plan | The plan, and the command and tags that made it. | The id, the author, the actor, the reason, the times, the counts, the addresses and actions of the changes and the plans it overlaps. |
| A transaction and a lock | The values of `-t`, `--tag`, `--webhook` and `-e` in the command line, which a lock and a transaction show as a tag. The original line is not kept. | Everything else: the kind, the user, the host, the times, the status, the addresses and revisions it changed. The command shows each sealed value as `<sealed:a1b2c3d4>`. |
| `meta.json` | Nothing. | The format, the lineage, the names and reader keys of the readers, and the locks. |

Addresses, types, names and dependencies stay readable so that the graph, the locks, the plans that reach only part of a root and [the viewer](the-viewer.md) keep working. A person with the bucket can list what exists and how it is connected. They cannot read a value.

## How a reader opens it

The readers file in the repository, `.pravo/access/readers.txt`, lists the readers. Each reader has an identity, made by `pravo id new`, with a reader key that never leaves their machine except sealed under a passphrase.

A group of records shares one data key of 32 random bytes. The state holds that key once for each reader, locked with the reader's public key by HPKE (RFC 9180: X25519, HKDF-SHA256, AES-256-GCM). A reader opens their lock with their reader key and holds the data key for the life of the process. A value is encrypted with AES-256-GCM under the data key. Its associated data names the lineage of the state, the group, the kind of object, the address, the revision and the generation, so a sealed value that is copied to another address, or to another revision, does not open.

Adding a reader locks the existing data keys for the new reader and rewrites `meta.json`. Removing a reader makes a new group with a new data key and no lock for the reader who left. Records are sealed under the new group as they are next written, or at once with `pravo protect rewrap --all`.

The cost of opening does not depend on the number of readers. The number of locks grows by 80 bytes for each reader.

## Who decides who reads

The list of readers comes from the repository and never from the bucket. A command that writes a sealed state compares the file with the readers recorded in the state, and stops if they differ. Whoever can merge a change to the file decides who may read, and the review of that change is the control. Someone who can only write to the bucket cannot make a command lock a data key for them, because the readers of a command come from the checked-out file.

## What it does not do

These are limits of the version that exists. Each is a fact to plan around.

**There are no roles and no signatures.** Sealing says who can read. Anyone who can write the bucket can delete, replace or corrupt its objects. A record that does not match its name, a pointer that disagrees with its record, and a sealed value that does not open where it is are found and refused with exit code 7. A valid record from an earlier time is valid: replacing a record with an older one is not detected, and a pointer that is deleted reads as a resource that was removed.

**A record in the clear is refused.** In a sealed state a record whose values are not sealed, and a whole record where a pointer should be, are untrusted. Every command that opens the state stops with exit code 7 and names the object: `pravo state list`, `pravo state show`, `pravo state log`, `pravo plan`, `pravo drift` and `pravo protect status` among them. An object that someone forged without a key therefore cannot set a value. An `outputs.json` in the clear is refused the same way, so a writer of the bucket cannot set the outputs that `pravo output` prints and that another root takes from this one with `pravo.#RemoteOutputs`.

**A writer of the bucket can turn sealing off.** `meta.json` is not signed. Someone who can write to the bucket can edit it and lower the format, and the writers that follow find a state with protection off and write their records in the clear. Nothing in the state stops this until signatures arrive with roles. To notice it, run `pravo protect status`: it prints the format, `Sealed on · format 3`, or `Sealed off · format 2 · immutable records` after the edit, and exits 0 either way. A CI job runs it before it plans or applies and fails when the format is not the one it expects, for example `pravo protect status --json | jq -e '.format == 3'`. When `meta.json` is put back, a record that was written in the clear in between is refused with exit code 7, and it holds no attributes, because the writer could not open the sealed ones. Restore that object from a version of the bucket.

**A writer of the bucket can replace the keys.** A locked data key is public-key encryption, and anyone can lock a key for a public key. A person who can write `meta.json` can make a new group with a data key of their choosing and locks for the readers' public keys. The comparison of the readers file with the state looks at who has a lock and cannot see this. Limit write access to the bucket to the identities that run pravo, as before.

**A removal is not retroactive.** A reader who is removed keeps what they read, and a copy of the bucket made before the removal opens with their key. The new data key protects records written from then on. Replace the secrets the reader could read where they live. A command that is running when a reader is removed looks at `meta.json` every 30 seconds and moves to the new group then. A record it writes in that window is sealed under the old group, which the removed reader can open.

**Nothing prunes.** Reader groups, superseded records and the records of destroyed resources stay in the bucket. Each removal of a reader adds a group to `meta.json`, with a lock for each reader that is left, and a command that opens the state opens one lock for each group. `pravo protect rewrap --all` seals the records again under the current group and deletes the records that are sealed in another group and that no pointer names. It does not remove the old groups from `meta.json`. The record of a resource that was destroyed, or removed with `pravo state rm`, stays in `rec/`, sealed under the group it was written in, and the readers of that group can read its last values. In a state of format 2 that record is in the clear.

**Seal and reseal while nobody applies.** `pravo protect init --encrypt` and `pravo protect rewrap --all` need the state at rest: run them while no one plans, applies or holds a lock. `init` refuses with exit code 4 while a transaction is running or a lock is held. A command that is already writing when `rewrap --all` runs can leave a pointer without its record, and every command then stops with exit code 7 until the object is repaired.

**Reader names are readable.** `meta.json` holds the name and the key of each reader, so a person with the bucket learns who the readers are.

**The hash of a configuration is readable.** Each record keeps a short hash of the configuration that made it. It is not keyed. A person who can guess an entire resource configuration can test the guess.

**There is no recovery recipient.** If every reader loses their key, or forgets the passphrase of the only identity, the state cannot be read. Keep more than one reader, and keep the identity of a job as a CI secret.

**An identity of a job has no passphrase.** `pravo id new --ci` writes both private keys in the clear. Whoever reads the CI secret is the reader. Use one identity for each job.

**Other files hold the same values.** A plan file written with `--out`, the output of `pravo show`, a log and the memory of a provider hold values that the state seals. A provider receives the values it needs to do its work. pravo starts providers without `PRAVO_IDENTITY` and the passphrase variables.

**A secret is sealed in a command line only where pravo knows it is one.** The values of `-t`/`--tag`, `--webhook` and `-e`/`--expression` are replaced in the history, in a lock and in a stored plan, and so is the value of any other flag that is written `--flag=value`, except the few flags that carry no secret, such as `--limit` and `--ttl`. A secret given as a positional argument, or as the value of any other flag written `--flag value` with a space, is shown as it was typed. Pass a secret with `-t`.

**The tags of a plan are printed in the clear where the plan is made.** `pravo plan --store -t key=value` prints the line that applies the plan, `pravo apply plan-ID -t key=value`, with the value as it was typed, to the terminal and to the log of a CI job. `pravo plan show` prints `command:` and `apply:` with the value to a reader, and `pravo mcp` returns it to an agent that holds a reader identity. A command without a key shows the tags as `<sealed:a1b2c3d4>` in `command` and as `<value>` in `apply_command` of `pravo plan list --json`, and neither can be pasted. The `apply` line that [the viewer](the-viewer.md) shows for a stored plan leaves the tags out. Applying a plan made with `-t` from such a line is refused, and the refusal tells a reader the command with the tags. The seal ends at the bucket: mask the value in the log of the job, as for any secret.

**Key material is cleared on a best effort.** pravo clears a key when it is done with it, and the Go runtime can still hold a copy.

**A state in a local directory is not shared.** The compare-and-swap of a directory is a lock of one user. `pravo protect check` refuses a local directory unless `--allow-local` says that only you use it.

**Earlier versions of pravo refuse a sealed state.** They stop with `state at LOCATION was written by a newer pravo (format 3)`, and do not read or write it.

## Recap

- Sealing encrypts attributes, private data, outputs, the values in stored plans and the values in command lines to a list of readers kept in the repository.
- Addresses, types, names, dependencies and revisions stay readable. So do the names of the readers.
- A removal protects what is written after it. It does not take back what a reader read.
- Sealing is confidentiality against a copy of the bucket. It is not access control for writers, and it does not detect a rollback or a lowered format; `pravo protect status` shows the format.

[Seal the state to a list of readers](../how-to/seal-the-state.md) does it step by step.
