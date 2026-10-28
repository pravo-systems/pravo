---
title: Signatures and roles
description: "What a sealed state encrypts and who can open it, what a signature binds, what a role allows, and what none of the three does. Confidentiality against a copy of the bucket, the provenance of every change, and who may change what."
order: 11
minutes: 14
keywords: "sealed encryption readers identity signature provenance role reader writer admin pin vouch rbac trust"
---

A state records the attributes of every resource, and some of them are secrets. It also records who changed what, and a team wants that record to be worth something. pravo answers three questions about a shared state, each with a mechanism of its own, and this page says what each one protects and what it leaves open.

| Question | Mechanism | Where it lives |
|---|---|---|
| Who can read the values? | Sealing: the values are encrypted to a list of readers. | The readers file in the repository and the locks in the state. |
| Who made this change? | Signatures: every transaction, stored plan and policy version is signed by the identity that wrote it. | The state, verified against the readers file. |
| Who may change what? | Roles: each key is a reader, a writer or an admin, and the roles are pinned in the state. | The readers file, pinned as a signed chain in the state. |

Each builds on the one before it. Signatures need identities, which sealing introduces, and roles are read from signatures. The commands are [`pravo id`](../reference/id.md), [`pravo protect`](../reference/protect.md) and [`pravo protect roles`](../reference/roles.md); [Seal the state to a list of readers](../how-to/seal-the-state.md) and [Give people roles](../how-to/give-people-roles.md) do it step by step.

:::figure The three parts and what joins them. The readers file in the repository is the anchor: the state holds the locks, the signatures and the pinned roles, and every writer compares the two before it writes.
<svg viewBox="0 0 780 250" role="img" aria-label="Three boxes. At the left, the readers file in the repository: names, keys, roles and the pin of the first roles. In the middle, the state: a data key locked for each reader, signed transactions, plans and policy versions, and the chain of roles. At the right, the credentials of the bucket, given by role. Arrows: protect rewrap and protect roles carry the file to the state, every writer compares the file with the state, and the bucket decides who can write at all." style="min-width:680px">
<rect x="0" y="40" width="220" height="130" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="110" y="64" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">the readers file</text>
<text x="110" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">in the repository, reviewed</text>
<text x="14" y="106" style="fill:var(--text-2);font-size:11px">names and keys of the readers</text>
<text x="14" y="124" style="fill:var(--text-2);font-size:11px">the role asked for each key</text>
<text x="14" y="142" style="fill:var(--text-2);font-size:11px">the pin of the first roles</text>
<rect x="280" y="24" width="220" height="162" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="390" y="48" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">the state</text>
<text x="390" y="64" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">a directory or a bucket</text>
<text x="294" y="90" style="fill:var(--text-2);font-size:11px">a data key locked for each reader</text>
<text x="294" y="108" style="fill:var(--text-2);font-size:11px">values sealed under the data key</text>
<text x="294" y="126" style="fill:var(--text-2);font-size:11px">transactions, plans and policies, signed</text>
<text x="294" y="144" style="fill:var(--text-2);font-size:11px">the roles, as a signed chain</text>
<text x="294" y="162" style="fill:var(--text-2);font-size:11px">records bound to their writer</text>
<rect x="560" y="40" width="220" height="130" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="670" y="64" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">the bucket</text>
<text x="670" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">credentials given by role</text>
<text x="574" y="106" style="fill:var(--text-2);font-size:11px">reader: get and list, put plans</text>
<text x="574" y="124" style="fill:var(--text-2);font-size:11px">writer: put and delete records</text>
<text x="574" y="142" style="fill:var(--text-2);font-size:11px">admin: meta, policy and roles too</text>
<line x1="222" y1="84" x2="278" y2="84" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="278,84 272,80.4 272,87.6" style="fill:var(--text-3)"/>
<text x="250" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">rewrap, roles</text>
<line x1="278" y1="130" x2="222" y2="130" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="222,130 228,126.4 228,133.6" style="fill:var(--text-3)"/>
<text x="250" y="146" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">compared by</text>
<text x="250" y="157" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">every writer</text>
<line x1="558" y1="105" x2="502" y2="105" style="stroke:var(--conflict);stroke-width:1.5" stroke-dasharray="5 3"/>
<polygon points="502,105 508,101.4 508,108.6" style="fill:var(--conflict)"/>
<text x="530" y="99" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">who can write</text>
<text x="530" y="121" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">at all</text>
<text x="0" y="222" style="fill:var(--text-3);font-size:11.5px">pravo checks the file against the state and refuses what fails. The bucket is what stops a write that pravo never sees.</text>
</svg>
:::

## What is sealed

Protection is off by default, and a state with protection off is unchanged. `pravo protect init --encrypt` turns it on in two steps that run together. Each record is stored once as an object named by the SHA-256 of its bytes, `rec/SHA256`, and never overwritten again: `resources/ADDRESS.json` becomes a small pointer that is replaced only by compare-and-swap (format 2). The values in those objects are then encrypted (format 3).

| Object | Sealed | Readable by anyone with the bucket |
|---|---|---|
| A record, `rec/SHA256` | The attributes and the private data of the provider, the hash of the configuration that made it and the configuration of its provider. | The address, type, name, provider, dependencies, revision, generation, the times and the transaction that wrote it. |
| `outputs.json` | All of it. | Nothing. |
| A stored plan | The plan, and the command and tags that made it. | The id, the author, the actor, the reason, the times, the counts, the addresses and actions of the changes and the plans it overlaps. |
| A transaction and a lock | The values of `-t`, `--tag`, `--webhook` and `-e` in the command line, which a lock and a transaction show as a tag. The original line is not kept. The text of the error of a run that failed or was rejected, which a transaction keeps for the readers, and what each change altered in the attributes of its record. | Everything else: the kind, the user, the host, the times, the status, the addresses and revisions it changed. The command shows each sealed value as `<sealed:a1b2c3d4>`. The summary of a failed run says that its text is sealed. |
| A policy version | Nothing. | The text of the rule, its signature and who signed it. A policy is a rule about plans, not a secret. |
| `meta.json` | Nothing. | The format, the lineage, the names and reader keys of the readers, and the locks. |
| `roles/NNNNNNNN.json` | Nothing. | The roles of each key, the former keys and the transactions an admin vouched for, with the signature of the admin who stored the version. |
| `gc/marks.json` | Nothing. | The names of the records that `pravo protect gc` found unnamed, and when. They are hashes of sealed bytes. |

Addresses, types, names and dependencies stay readable so that the graph, the locks, the plans that reach only part of a root and [the viewer](the-viewer.md) keep working. A person with the bucket can list what exists and how it is connected. They cannot read a value.

## How a reader opens it

The readers file in the repository, `.pravo/access/readers.txt`, lists the readers. Each reader has an identity, made by `pravo id new`, with a reader key that never leaves their machine except sealed under a passphrase, or under a key that their ssh-agent derives, and a signing key, which is the ssh key of the line.

A group of records shares one data key of 32 random bytes. The state holds that key once for each reader, locked with the reader's public key by HPKE (RFC 9180: X25519, HKDF-SHA256, AES-256-GCM). A reader opens their lock with their reader key and holds the data key until the command ends. A command that moves to a new group wipes the keys it replaces, and the viewer wipes the keys of a state it has replaced after a minute. A value is encrypted with AES-256-GCM under the data key. Its associated data names the lineage of the state, the group, the kind of object, the address, the revision and the generation, so a sealed value that is copied to another address, or to another revision, does not open.

Adding a reader locks the existing data keys for the new reader and rewrites `meta.json`. Removing a reader makes a new group with a new data key and no lock for the reader who left. Records are sealed under the new group as they are next written, or at once with `pravo protect rewrap --all`.

The cost of opening does not depend on the number of readers. The number of locks grows by 80 bytes for each reader.

## Who decides who reads

The list of readers comes from the repository and never from the bucket. A command that writes a sealed state compares the file with the readers recorded in the state, and stops if they differ. Whoever can merge a change to the file decides who may read, and the review of that change is the control. Someone who can only write to the bucket cannot make a command lock a data key for them, because the readers of a command come from the checked-out file.

## What a signature binds

Every transaction of a sealed state is signed by the identity that ran it, with the ssh key of its line in the readers file, and so is every stored plan, by the identity that stored it, and every version of a policy, by the identity that applied it. A signature is verified against the readers file, so a command that holds the file says `signed by ada` for a transaction and `signature does not verify` or `unsigned` for one that fails. The viewer says the same in the History tab of a resource, in the Plans section and in the Policies section. [Signatures](../reference/protect.md#signatures) lists what each one covers.

A signature binds an event: which key ran which transaction, when, and which revisions of which records it changed. It is a receipt that nobody but the holder of the key could have written. On its own it does not bind the bodies of the records, and it does not say that a transaction was not deleted, or that the whole state was not replaced by an older copy of the bucket. Two things narrow that gap. A record that does not match its name, a pointer that disagrees with its record, and a sealed value that does not open where it is, are found and refused with exit code 7. And with roles, the transaction of a writer also signs the key of each record it writes, so a record that nobody with the right to write accounts for is found too.

The signed start of an apply lists the policy versions that judged its plan, and the signed finish names the stored plan it ran from and the approvals it carried. That list is what makes a receipt of a policy, or of an approval, worth checking later: `pravo state verify` checks the signatures, finds the plan and runs the listed versions on it. [Operating policies](../reference/policies-operations.md#what-is-recorded) shows what is recorded.

## Roles

Sealing says who may read. Roles say who may change the state, and they are a field of the readers file: `role:reader`, `role:writer` or `role:admin` at the end of the line of a key. The strength is linear. An admin may do what a writer may, and a writer what a reader may.

| Role | May |
|---|---|
| `reader` | Open the state, read it and plan without changing it. Store a plan with `plan --store`, for a writer to apply. |
| `writer` | Change the state: `apply`, `destroy`, `import`, `state mv`, `state rm`, `discover adopt`, and a plan that writes the records it refreshed. Add and edit a policy. Approve a plan, where a policy asks for a writer's approval. |
| `admin` | Remove a policy, or change one that asks for approvals. Change who reads the state, change roles, collect garbage, take the seal off. Apply a plan without the approvals a policy asks for. |

A readers file in which no line has a role gives none, and every key may do everything. As soon as one line has a role, a line without one is a reader. A command that a role does not allow stops with exit code 5 before it changes anything, and says which role is needed. [`pravo protect roles`](../reference/roles.md#what-is-refused) lists each case.

### Pinned roles

The file alone has a gap: a reader who edits their own line in their own checkout is an admin for as long as that checkout lasts, because the roles would be read from a file that the reader controls. `pravo protect roles` closes it. An admin runs it, and it stores the roles of the readers file in the state as a signed version, `roles/00000001.json`, and adds one line to the readers file, `roles-genesis DIGEST`, which is the SHA-256 of that first version. The line is committed with the file.

From then on the roles in the state decide, and the roles in the file say what an admin asks for. A change of roles is a new version, signed by a key that is an admin in the version before it, so the versions form a chain whose first link is pinned by the file. A reader who holds the data key and the credentials of the bucket cannot make a version that verifies, cannot replace the first one, and cannot delete the chain without every honest run refusing the state with exit code 7.

:::figure The chain of roles. The readers file pins the first version by its digest. Each later version is signed by a key that was an admin in the version before it, so the chain cannot be forged, replaced or shortened without the check failing.
<svg viewBox="0 0 780 150" role="img" aria-label="A row of boxes. At the left, the readers file with the line roles-genesis and a digest. An arrow labelled pins points to the box roles version 1, signed by ada. An arrow labelled signed by an admin of 1 points to roles version 2, signed by ada. Another arrow labelled signed by an admin of 2 points to roles version 3, signed by dan, who was made an admin in version 2. A caption says that a version signed by a key that was not an admin in the version before it is refused." style="min-width:680px">
<rect x="0" y="30" width="170" height="70" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="85" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">readers file</text>
<text x="85" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">roles-genesis 5c1e…</text>
<text x="85" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">in the repository</text>
<line x1="172" y1="65" x2="228" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="228,65 222,61.4 222,68.6" style="fill:var(--text-3)"/>
<text x="200" y="58" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">pins</text>
<rect x="230" y="30" width="150" height="70" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="305" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">roles 1</text>
<text x="305" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">ada admin, dan reader</text>
<text x="305" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">signed by ada</text>
<line x1="382" y1="65" x2="428" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="428,65 422,61.4 422,68.6" style="fill:var(--text-3)"/>
<text x="405" y="58" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">admin of 1</text>
<rect x="430" y="30" width="150" height="70" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="505" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">roles 2</text>
<text x="505" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">ada admin, dan admin</text>
<text x="505" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">signed by ada</text>
<line x1="582" y1="65" x2="628" y2="65" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="628,65 622,61.4 622,68.6" style="fill:var(--text-3)"/>
<text x="605" y="58" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">admin of 2</text>
<rect x="630" y="30" width="150" height="70" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="705" y="54" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">roles 3</text>
<text x="705" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">ada former, dan admin</text>
<text x="705" y="88" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">signed by dan</text>
<text x="0" y="134" style="fill:var(--text-3);font-size:11.5px">A version signed by a key that was not an admin in the version before it does not verify, and the state is refused with exit code 7.</text>
</svg>
:::

A key that is dropped from the file is kept in the next version as a former key, and so is a key that signed a transaction before the roles were stored and that nobody lists. What a former key, or a writer who was demoted, wrote stays valid only as far as an admin vouched for it: when `protect roles` stores a version, it lists the transactions that wrote the records now in the state and that were signed by a key that is not a writer in the new roles, and vouches for each by its digest. The command says so before it asks, so an admin sees what is grandfathered. Offboarding someone therefore does not break the next plan, and a key that left cannot add anything afterwards. A key in neither list is refused outright: a holder of the data key could make such a key for the purpose.

### Records are bound to the writer

With roles, the transaction of a writer signs the key of every record it writes. A reader holds the data key, and can seal a record that opens for everyone, but cannot make the transaction of a writer name it. `plan`, `apply` and `destroy` check every record before they read it, and `pravo protect status` reports a record that no verified transaction of a writer accounts for, with exit code 7. A record written before the roles began reads as `from before roles`, one whose transaction is still running as `not checked`, and one signed by a former or demoted key as `vouched for by an admin` when the roles name its transaction. [Records are bound to the writer](../reference/roles.md#records-are-bound-to-the-writer) lists each case.

### What roles do not do

Roles are checked by a pravo that reads the readers file and the roles in the state. Someone who holds the credentials of the bucket and the data key, and a pravo of their own, can write the bucket; the record they write is found by the next honest run, and not stopped. What stops it is the bucket: give the credentials of the bucket by role, so that a reader can get and list and put a plan, a writer can put and delete records but not `meta.json`, `policy/` or `roles/`, and only an admin can write those. A job that only plans runs with a reader's credentials. [What roles do not do](../reference/roles.md#what-roles-do-not-do) has the table.

A key that left and kept the data key can still write to a bucket that its credentials reach, and honest runs refuse the result. Remove its credentials, and run `pravo protect rewrap --all` and `pravo protect gc` so that the key opens nothing that is left. An object lock on `roles/` stops the deletion of the newest versions, which the pin cannot see.

## What it does not do

These are limits of the version that exists. Each is a fact to plan around.

**A signature covers events, and records only through a writer.** A signature says which key ran which transaction and changed which revisions. Without roles every reader may sign; with them, only a writer may, and a transaction of a writer signs the key of each record it writes. Anyone who can write the bucket can still delete, replace or corrupt its objects. A record that does not match its name, a pointer that disagrees with its record, and a sealed value that does not open where it is are found and refused with exit code 7. A valid record from an earlier time is valid: replacing a record with an older one is not detected, and a pointer that is deleted reads as a resource that was removed.

**A record in the clear is refused.** In a sealed state a record whose values are not sealed, and a whole record where a pointer should be, are untrusted. Every command that opens the state stops with exit code 7 and names the object: `pravo state list`, `pravo state show`, `pravo state log`, `pravo plan`, `pravo drift` and `pravo protect status` among them. An object that someone forged without a key therefore cannot set a value. An `outputs.json` in the clear is refused the same way, so a writer of the bucket cannot set the outputs that `pravo output` prints and that another root takes from this one with `pravo.#RemoteOutputs`.

**A writer of the bucket can lower the format, and every writer that has the repository notices.** `meta.json` is not signed. Someone who can write to the bucket can edit it and lower the format, and a writer that looked only at the bucket would find a state with protection off and write its records in the clear. The repository is the anchor that a bucket writer cannot move. When the access directory holds a readers file and the state is not sealed, a command that writes the state stops with exit code 5 and says that the repository lists readers but the state is not sealed, with the two ways out: `pravo protect init --encrypt` to seal it, or removing the readers file when the state is not meant to be sealed. Sealed records in a state that says it is not sealed are found by any command that opens it, with or without the repository: `protect status` says so on the `Sealed` line, and a command that writes refuses (exit code 7), so that a record without its values is never written over the sealed one. If the records are sealed and only `meta.json` was lowered, `pravo protect init --encrypt` stops with exit code 7 before it writes anything, because sealing again would write new keys over the only copy of the old ones: put `meta.json` back from a version of the bucket. A command that only reads prints the same text as a warning on standard error, and creates no state. A command that runs without the repository, such as one on a machine that has only the bucket and its credentials, has no readers file to check against and does not notice. A CI job that checks out the repository is covered; one that also wants a verdict before it plans runs `pravo protect status --json | jq -e '.state == "sealed"'`.

**A writer of the bucket can replace the keys.** A locked data key is public-key encryption, and anyone can lock a key for a public key. A person who can write `meta.json` can make a new group with a data key of their choosing and locks for the readers' public keys. The comparison of the readers file with the state looks at who has a lock and cannot see this. Limit write access to the bucket to the identities that run pravo, by role.

**A removal is not retroactive.** A reader who is removed keeps what they read, and a copy of the bucket made before the removal opens with their key. The new data key protects records written from then on, and `pravo protect gc` drops the old group once `rewrap --all` has sealed everything again, so that the key the reader holds opens nothing that is left. Replace the secrets the reader could read where they live. A command that is running when a reader is removed looks at `meta.json` every 30 seconds and moves to the new group then. A record it writes in that window is sealed under the old group, which the removed reader can open.

**Garbage stays until `pravo protect gc` runs.** Every revision of every record, the record of a resource that was destroyed or removed with `pravo state rm`, and every reader group stay in the bucket. Each removal of a reader adds a group to `meta.json`, with a lock for each reader that is left, and a command that opens the state opens one lock for each group. `pravo protect rewrap --all` seals the records again under the current group and deletes the records that are sealed in another group and that no pointer names. It does not remove the old groups from `meta.json`. `pravo protect gc` deletes the records that no pointer names once they are 24 hours old and a collection at least 15 minutes earlier has marked them, the record of a destroyed resource among them, and drops a group, other than the current one, that nothing is sealed in. Both intervals are read from clocks, so the machines that write and the ones that collect need clocks that agree. Until it runs, the readers of a group can read the last values of a destroyed resource whose record is sealed in it, and in a state of format 2 that record is in the clear. A bucket that keeps old versions of its objects keeps what `gc` deletes, and the `meta.json` that listed a group it dropped.

**A record is deleted only after a day.** A writer makes a record and then replaces the pointer, and in between nothing names the record. The wait is what protects that writer, and nothing else does. `--older-than` shortens it, and not below 65 seconds. A process that is stopped between the two writes for longer than the wait loses its record, and the next read of that node fails with exit code 7.

**Seal and reseal while nobody applies.** `pravo protect init --encrypt` and `pravo protect rewrap --all` need the state at rest: run them while no one plans, applies or holds a lock. `init` refuses with exit code 4 while a transaction is running or a lock is held. The part of `pravo protect gc` that drops groups needs it too, and the part that deletes records does not. A command that is already writing when `rewrap --all` runs can leave a pointer without its record, and every command then stops with exit code 7 until the object is repaired.

**Reader names and roles are readable.** `meta.json` holds the name and the key of each reader, and the roles chain holds the role of each, so a person with the bucket learns who the readers are and what each may do.

**A recovery recipient is optional, and a sealed state starts without one.** If every reader loses their key, or forgets the passphrase of the only identity, the state cannot be read. Keep more than one reader, and keep the identity of a job as a CI secret. A recovery recipient is a key that nobody holds whole: it is split into N shares, any K of which rebuild it, and every group of the state carries a lock for it. It is a decision about trust and not a delay, since K holders of shares can read the state at once, with no signature and no waiting time. The shares are not an identity, so roles do not apply to them. See [The recovery recipient](../reference/protect.md#the-recovery-recipient).

**An identity that the ssh-agent unlocks is as safe as the agent.** The file is sealed under a key derived from the agent's signature of a challenge, so it opens for any process that the agent signs for. 1Password asks before it lets an application sign, `ssh-add -c` asks for each use, and an agent with neither asks for nothing. The file holds no passphrase to guess, and it opens nothing without the key. If the key is removed, the identity is lost with it, so keep a second reader. See [Unlock with the ssh-agent](../reference/id.md#unlock-with-the-ssh-agent).

**An identity of a job has no passphrase.** `pravo id new --ci` writes both private keys in the clear. Whoever reads the CI secret is the reader, and the writer or the admin if the line of the job says so. Use one identity for each job, and give a job that only plans the role of a reader.

**A plan file of a sealed state is sealed.** `pravo plan --out` and `pravo plan pull` seal the file with the key of the group, as `--store` seals a stored plan. Only a reader can open it, and `pravo show`, `pravo apply`, `pravo verify` and `pravo serve --plan` open it with the identity that opens the state. The tags that select the state are inside the seal, so these commands take the `-t` and `-T` that the plan was made with, as they do for a stored plan. `pravo apply` refuses a plan file that is not sealed when the state is: only a reader can seal one, so a plain file may be anyone's. A tool that needs the plan without a key reads the output of `pravo show --json`.

**Other files hold the same values.** The output of `pravo show`, a log and the memory of a provider hold values that the state seals. A provider receives the values it needs to do its work. pravo starts providers without `PRAVO_IDENTITY` and the passphrase variables. A recorded run holds no attribute value at all, by the rule of [Traces](traces.md).

**A secret is sealed in a command line only where pravo knows it is one.** The values of `-t`/`--tag`, `--webhook` and `-e`/`--expression` are replaced in the history, in a lock and in a stored plan, and so is the value of every other flag that takes one, written `--flag=value` or `--flag value`, except the few flags that carry no secret, such as `--limit`, `--ttl` and `--chdir`. A word after a flag that takes no value, such as `--auto-approve`, is a positional argument. A secret given as a positional argument is shown as it was typed. Pass a secret with `-t`.

**The tags of a plan are printed in the clear where the plan is made.** `pravo plan --store -t key=value` prints the line that applies the plan, `pravo apply plan-ID -t key=value`, with the value as it was typed, to the terminal and to the log of a CI job. `pravo plan show` prints `command:` and `apply:` with the value to a reader, and `pravo mcp` returns it to an agent that holds a reader identity. A command without a key shows the tags as `<sealed:a1b2c3d4>` in `command` of `pravo plan list --json`, and as `-t key=<value>` in `apply_command` and in the `command:` line of `pravo plan show`. The `apply` line that [the viewer](the-viewer.md) shows for a stored plan has `-t key=<value>` for each tag, for a reader as well, and never a value. The seal ends at the bucket: mask the value in the log of the job, as for any secret.

**Key material is cleared on a best effort.** pravo clears a key when it is done with it, and the Go runtime can still hold a copy.

**A state in a local directory is not shared.** The compare-and-swap of a directory is a lock of one user. `pravo protect check` refuses a local directory unless `--allow-local` says that only you use it.

**Earlier versions of pravo refuse a sealed state, and a line with a role.** They stop with `state at LOCATION was written by a newer pravo (format 3)`, and do not read or write it. A readers file with a `role:` field is refused by a pravo that does not know roles, so it fails closed.

**Sealing can be taken off, and it takes the protection with it.** `pravo protect unseal` writes every record, the outputs and the stored plans in the open. From then on anyone who can read the bucket can read every value. A copy of the bucket made while it was sealed still opens with the key of a reader. Values that sealing replaced by a tag in the command lines of old transactions are not restored. The state stays in the same lineage and the same format; see [protect unseal](../reference/protect.md#protect-unseal).

## Recap

- Sealing encrypts attributes, private data, outputs, the values in stored plans and the values in command lines to a list of readers kept in the repository. Addresses, types, names, dependencies and revisions stay readable, and so do the names and roles of the readers.
- A signature says which key wrote which transaction, plan or policy version, and with roles, which records a writer wrote. It does not detect a rollback of the whole bucket.
- A role is a field of the readers file, pinned in the state as a signed chain, so that no reader promotes themselves in a checkout. A former key keeps what an admin vouched for and can add nothing.
- Each mechanism is checked by a pravo that has the repository. The credentials of the bucket, given by role, are what stop a write that pravo never sees.

Next: [The viewer](the-viewer.md) reads a sealed state with your identity and shows who signed what.
