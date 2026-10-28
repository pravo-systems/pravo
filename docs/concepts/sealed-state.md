---
title: What sealing protects
description: What a sealed state encrypts, who can read it, and what it does not do. It gives confidentiality of values against a copy of the bucket, and nothing else.
order: 11
minutes: 10
---

A state records the attributes of every resource, and some of them are secrets. Sealing encrypts those values to a list of readers, so that a copy of the bucket, a backup or an old version of an object shows no value. This page says what is sealed, how a reader opens it, and what a sealed state still leaves open. The commands are in [pravo id and pravo protect](../reference/protect.md).

Sealing answers one question: who can read the values? It does not say who may change the state, and it does not make a change visible after the fact. A signature says who made a change; [roles](../reference/protect.md#roles), which say who may, are an option of the readers file, and `pravo protect roles` pins them in the state so that a reader cannot promote themselves by editing a copy of the file.

## What is sealed

Protection is off by default, and a state with protection off is unchanged. `pravo protect init --encrypt` turns it on in two steps that run together. Each record is stored once as an object named by the SHA-256 of its bytes, `rec/SHA256`, and never overwritten again: `resources/ADDRESS.json` becomes a small pointer that is replaced only by compare-and-swap (format 2). The values in those objects are then encrypted (format 3).

| Object | Sealed | Readable by anyone with the bucket |
|---|---|---|
| A record, `rec/SHA256` | The attributes and the private data of the provider, the hash of the configuration that made it and the configuration of its provider. | The address, type, name, provider, dependencies, revision, generation, the times and the transaction that wrote it. |
| `outputs.json` | All of it. | Nothing. |
| A secret, `secret/NAME@NUMBER.json` | The value and the note of a version. | The name, the number of the version, the time and the transaction that wrote it. |
| A stored plan | The plan, and the command and tags that made it. | The id, the author, the actor, the reason, the times, the counts, the addresses and actions of the changes and the plans it overlaps. |
| A transaction and a lock | The values of `-t`, `--tag`, `--webhook` and `-e` in the command line, which a lock and a transaction show as a tag. The original line is not kept. The text of the error of a run that failed or was rejected, which a transaction keeps for the readers, and what each change altered in the attributes of its record. | Everything else: the kind, the user, the host, the times, the status, the addresses and revisions it changed. The command shows each sealed value as `<sealed:a1b2c3d4>`. The summary of a failed run says that its text is sealed. |
| `meta.json` | Nothing. | The format, the lineage, the names and reader keys of the readers, and the locks. |
| `gc/marks.json` | Nothing. | The names of the records that `pravo protect gc` found unnamed, and when. They are hashes of sealed bytes. |

Addresses, types, names and dependencies stay readable so that the graph, the locks, the plans that reach only part of a root and [the viewer](the-viewer.md) keep working. A person with the bucket can list what exists and how it is connected. They cannot read a value.

## How a reader opens it

The readers file in the repository, `.pravo/access/readers.txt`, lists the readers. Each reader has an identity, made by `pravo id new`, with a reader key that never leaves their machine except sealed under a passphrase, or under a key that their ssh-agent derives.

A group of records shares one data key of 32 random bytes. The state holds that key once for each reader, locked with the reader's public key by HPKE (RFC 9180: X25519, HKDF-SHA256, AES-256-GCM). A reader opens their lock with their reader key and holds the data key until the command ends. A command that moves to a new group wipes the keys it replaces, and the viewer wipes the keys of a state it has replaced after a minute. A value is encrypted with AES-256-GCM under the data key. Its associated data names the lineage of the state, the group, the kind of object, the address, the revision and the generation, so a sealed value that is copied to another address, or to another revision, does not open.

Adding a reader locks the existing data keys for the new reader and rewrites `meta.json`. Removing a reader makes a new group with a new data key and no lock for the reader who left. Records are sealed under the new group as they are next written, or at once with `pravo protect rewrap --all`.

The cost of opening does not depend on the number of readers. The number of locks grows by 80 bytes for each reader.

## Who decides who reads

The list of readers comes from the repository and never from the bucket. A command that writes a sealed state compares the file with the readers recorded in the state, and stops if they differ. Whoever can merge a change to the file decides who may read, and the review of that change is the control. Someone who can only write to the bucket cannot make a command lock a data key for them, because the readers of a command come from the checked-out file.

## Secrets

A secret that the state keeps is sealed like a record: one object for each version, `secret/NAME@NUMBER.json`, with the value and its note encrypted to the readers of the state. [`pravo secret`](../reference/secret.md) sets, reads and removes them, and a run reads one with `pravo.#Secret` ([the specification](../reference/spec-env.md#secrets-of-the-sealed-state)). A state that holds a secret is in format 5.

Each `secret` command is a transaction that the identity signs, and the finish signature covers the version it wrote, as it covers the key of a record. The history of a secret says who set each version, from which machine and for which reason. With [roles](../reference/protect.md#roles) only a writer sets a value and only an admin removes or forgets one, and a version that no signed transaction of such a key accounts for is refused with exit code 7. A reader holds the data key and can seal a value that opens for everyone, and cannot make a writer's transaction name it.

What a secret does not get:

**Every reader reads every secret.** There is no list of readers for one secret. A secret that some of the readers of a state should not read belongs in another state, with its own list of readers and its own prefix in the bucket.

**A reader who leaves has read what they could open.** Removing the line, `pravo protect rewrap --all` and `pravo protect gc` leave nothing in the bucket that their key opens, and do not take back what they read. Set each secret they could read again at its source, and forget the old values with `pravo secret purge`. [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md#10-when-a-reader-leaves) lists the steps.

**Reads are not recorded.** A read of a secret is a read of an object. The access log of the bucket is the only record that an identity fetched its ciphertext, and pravo keeps no audit of reads.

**No freshness.** A signature does not say that a version is the newest. Someone who can write to the bucket can put an old object back. The check finds a missing newer version, because a verified transaction names it, and not an old object in its place. An object lock on `secret/` stops the replacement.

**No expiry.** A secret has no lifetime, and no run is refused because of the clock. A rotation is a `pravo secret set`.

## What it does not do

These are limits of the version that exists. Each is a fact to plan around.

**Without roles every reader may write, and the signatures cover events and not records.** Every transaction of a sealed state is signed by the identity that ran it, against the readers file, and a stored plan by the identity that stored it: [Signatures](../reference/protect.md#signatures). A signature says which key ran which transaction and changed which revisions. It does not bind the bodies of records, and it does not say that a transaction was not deleted or that the state was not replaced by an older copy. Without roles every reader may sign; with them, only a writer may, and a transaction of a writer signs the key of each record it writes, so a record that a reader sealed is found. Anyone who can write the bucket can delete, replace or corrupt its objects. A record that does not match its name, a pointer that disagrees with its record, and a sealed value that does not open where it is are found and refused with exit code 7. A valid record from an earlier time is valid: replacing a record with an older one is not detected, and a pointer that is deleted reads as a resource that was removed.

**A record in the clear is refused.** In a sealed state a record whose values are not sealed, and a whole record where a pointer should be, are untrusted. Every command that opens the state stops with exit code 7 and names the object: `pravo state list`, `pravo state show`, `pravo state log`, `pravo plan`, `pravo drift` and `pravo protect status` among them. An object that someone forged without a key therefore cannot set a value. An `outputs.json` in the clear is refused the same way, so a writer of the bucket cannot set the outputs that `pravo output` prints and that another root takes from this one with `pravo.#RemoteOutputs`.

**A writer of the bucket can lower the format, and every writer that has the repository notices.** `meta.json` is not signed. Someone who can write to the bucket can edit it and lower the format, and a writer that looked only at the bucket would find a state with protection off and write its records in the clear. The repository is the anchor that a bucket writer cannot move. When the access directory holds a readers file and the state is not sealed, a command that writes the state stops with exit code 5 and says that the repository lists readers but the state is not sealed, and that it has not been sealed yet or its `meta.json` was changed, with the two ways out: `pravo protect init --encrypt` to seal it, or removing the readers file when the state is not meant to be sealed. Sealed records in a state that says it is not sealed are found by any command that opens it, with or without the repository: `protect status` says so on the `Sealed` line, and a command that writes refuses (exit code 7), so that a record without its values is never written over the sealed one. A state that does not exist yet is not created by that command: it stops in the same way and says to create the state sealed with `pravo protect init --encrypt`. If the records are sealed and only `meta.json` was lowered, `pravo protect init --encrypt` stops with exit code 7 before it writes anything, because sealing again would write new keys over the only copy of the old ones: put `meta.json` back from a version of the bucket. A command that only reads prints the same text as a warning on standard error, and creates no state. A readers file that does not parse counts: it is found. `pravo protect status` says `Sealed off · format 2 · immutable records · the repository expects a sealed state`, or `no state yet` when there is none, and `pravo protect check`, `pravo protect init` and the `pravo id` commands are not stopped, because `init` is how the situation is resolved. A command that runs without the repository, such as one on a machine that has only the bucket and its credentials, does not notice: nothing in the state stops it until signatures arrive with roles. A CI job that checks out the repository is covered; one that also wants a verdict before it plans runs `pravo protect status --json | jq -e '.state == "sealed"'`. When `meta.json` is put back, a record that was written in the clear in between is refused with exit code 7, and it holds no attributes, because the writer could not open the sealed ones. Restore that object from a version of the bucket.

**A writer of the bucket can replace the keys.** A locked data key is public-key encryption, and anyone can lock a key for a public key. A person who can write `meta.json` can make a new group with a data key of their choosing and locks for the readers' public keys. The comparison of the readers file with the state looks at who has a lock and cannot see this. Limit write access to the bucket to the identities that run pravo, as before.

**A removal is not retroactive.** A reader who is removed keeps what they read, and a copy of the bucket made before the removal opens with their key. The new data key protects records written from then on, and `pravo protect gc` drops the old group once `rewrap --all` has sealed everything again, so that the key the reader holds opens nothing that is left. Replace the secrets the reader could read where they live. A command that is running when a reader is removed looks at `meta.json` every 30 seconds and moves to the new group then. A record it writes in that window is sealed under the old group, which the removed reader can open.

**Garbage stays until `pravo protect gc` runs.** Every revision of every record, the record of a resource that was destroyed or removed with `pravo state rm`, and every reader group stay in the bucket. Each removal of a reader adds a group to `meta.json`, with a lock for each reader that is left, and a command that opens the state opens one lock for each group. `pravo protect rewrap --all` seals the records again under the current group and deletes the records that are sealed in another group and that no pointer names. It does not remove the old groups from `meta.json`. `pravo protect gc` deletes the records that no pointer names once they are 24 hours old and a collection at least 15 minutes earlier has marked them, the record of a destroyed resource among them, and drops a group, other than the current one, that nothing is sealed in. Both intervals are read from clocks: the 24 hours from the time that the writer put in the record, and the 15 minutes from the time that the earlier collection put in its mark, each against the clock of the machine that runs `gc`. Machines whose clocks differ by a large part of an interval shorten it by the difference, so the machines that write and the ones that collect need clocks that agree. Until it runs, the readers of a group can read the last values of a destroyed resource whose record is sealed in it, and in a state of format 2 that record is in the clear. A bucket that keeps old versions of its objects keeps what `gc` deletes, and the `meta.json` that listed a group it dropped.

**A record is deleted only after a day.** A writer makes a record and then replaces the pointer, and in between nothing names the record. The wait is what protects that writer, and nothing else does. `--older-than` shortens it, and not below 65 seconds. A process that is stopped between the two writes for longer than the wait loses its record, and the next read of that node fails with exit code 7.

**Seal and reseal while nobody applies.** `pravo protect init --encrypt` and `pravo protect rewrap --all` need the state at rest: run them while no one plans, applies or holds a lock. `init` refuses with exit code 4 while a transaction is running or a lock is held. The part of `pravo protect gc` that drops groups needs it too, and the part that deletes records does not. A command that is already writing when `rewrap --all` runs can leave a pointer without its record, and every command then stops with exit code 7 until the object is repaired.

**Reader names are readable.** `meta.json` holds the name and the key of each reader, so a person with the bucket learns who the readers are.

**A recovery recipient is optional, and a sealed state starts without one.** If every reader loses their key, or forgets the passphrase of the only identity, the state cannot be read. Keep more than one reader, and keep the identity of a job as a CI secret. A recovery recipient is a key that nobody holds whole: it is split into N shares, any K of which rebuild it, and every group of the state carries a lock for it. It is a decision about trust and not a delay, since K holders of shares can read the state at once, with no signature and no waiting time. See [The recovery recipient](../reference/protect.md#the-recovery-recipient).

**An identity that the ssh-agent unlocks is as safe as the agent.** The file is sealed under a key derived from the agent's signature of a challenge, so it opens for any process that the agent signs for. 1Password asks before it lets an application sign, `ssh-add -c` asks for each use, and an agent with neither asks for nothing. The file holds no passphrase to guess, and it opens nothing without the key. If the key is removed, the identity is lost with it, so keep a second reader. See [Unlock with the ssh-agent](../reference/protect.md#unlock-with-the-ssh-agent).

**An identity of a job has no passphrase.** `pravo id new --ci` writes both private keys in the clear. Whoever reads the CI secret is the reader. Use one identity for each job.

**A plan file of a sealed state is sealed.** `pravo plan --out` and `pravo plan pull` seal the file with the key of the group, as `--store` seals a stored plan. Only a reader can open it, and `pravo show`, `pravo apply`, `pravo verify` and `pravo serve --plan` open it with the identity that opens the state. The tags that select the state are inside the seal, so these commands take the `-t` and `-T` that the plan was made with, as they do for a stored plan. `pravo apply` refuses a plan file that is not sealed when the state is: only a reader can seal one, so a plain file may be anyone's. A tool that needs the plan without a key reads the output of `pravo show --json`.

**Other files hold the same values.** The output of `pravo show`, a log and the memory of a provider hold values that the state seals. A provider receives the values it needs to do its work. pravo starts providers without `PRAVO_IDENTITY` and the passphrase variables.

**A secret is sealed in a command line only where pravo knows it is one.** The values of `-t`/`--tag`, `--webhook` and `-e`/`--expression` are replaced in the history, in a lock and in a stored plan, and so is the value of every other flag that takes one, written `--flag=value` or `--flag value`, except the few flags that carry no secret, such as `--limit`, `--ttl` and `--chdir`. A word after a flag that takes no value, such as `--auto-approve`, is a positional argument. A secret given as a positional argument is shown as it was typed. Pass a secret with `-t`.

**The tags of a plan are printed in the clear where the plan is made.** `pravo plan --store -t key=value` prints the line that applies the plan, `pravo apply plan-ID -t key=value`, with the value as it was typed, to the terminal and to the log of a CI job. `pravo plan show` prints `command:` and `apply:` with the value to a reader, and `pravo mcp` returns it to an agent that holds a reader identity. A command without a key shows the tags as `<sealed:a1b2c3d4>` in `command` of `pravo plan list --json`, and as `-t key=<value>` in `apply_command` and in the `command:` line of `pravo plan show`. The `apply` line that [the viewer](the-viewer.md) shows for a stored plan has `-t key=<value>` for each tag, for a reader as well, and never a value. None of these can be pasted until the value is put in place of `<value>`: the shell refuses the angle brackets. Applying a plan made with `-t` without its tags is refused, and the refusal tells a reader the command with the tags. The seal ends at the bucket: mask the value in the log of the job, as for any secret.

**Key material is cleared on a best effort.** pravo clears a key when it is done with it, and the Go runtime can still hold a copy.

**A state in a local directory is not shared.** The compare-and-swap of a directory is a lock of one user. `pravo protect check` refuses a local directory unless `--allow-local` says that only you use it.

**Earlier versions of pravo refuse a sealed state.** They stop with `state at LOCATION was written by a newer pravo (format 3)`, and do not read or write it. A state that was unsealed stays in format 3, so they refuse that too. A state that holds a secret is in format 5, and a pravo that does not know secrets refuses it in the same way.

**Sealing can be taken off, and it takes the protection with it.** `pravo protect unseal` writes every record, the outputs and the stored plans in the open. From then on anyone who can read the bucket can read every value. A copy of the bucket made while it was sealed still opens with the key of a reader. Values that sealing replaced by a tag in the command lines of old transactions are not restored. The state stays in the same lineage and the same format; see [protect unseal](../reference/protect.md#protect-unseal).

## Recap

- Sealing encrypts attributes, private data, outputs, the values in stored plans and the values in command lines to a list of readers kept in the repository.
- Addresses, types, names, dependencies and revisions stay readable. So do the names of the readers.
- A removal protects what is written after it. It does not take back what a reader read.
- A secret is a signed, versioned object in the sealed state. Every reader reads every secret, and a reader who leaves has read what they could open.
- Sealing is confidentiality against a copy of the bucket. It is not access control for writers, and it does not detect a rollback. A lowered format is noticed by every command that has the repository, because the readers file says the state should be sealed.

[Seal the state to a list of readers](../how-to/seal-the-state.md) does it step by step.
