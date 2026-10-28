---
title: pravo protect roles
description: Give each identity in the readers file a role, reader, writer or admin, and pin the roles in the state, so that nobody promotes themselves by editing a copy of the file. What each role may do, and what the bucket has to enforce.
order: 20
group: Commands
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
  - pattern: '[0-9]+(\.[0-9])? s\b'
    replace: '<time> s'
keywords: "rbac permissions admin writer reader promote demote offboard former vouch least privilege"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo protect roles [--yes]` |
| Changes infrastructure | No. |
| Reads | The state and the readers file. |
| Writes | One version of the roles in the state, `roles/NNNNNNNN.json`, signed by the admin who runs it. The first time, the line `roles-genesis DIGEST` at the end of the readers file. |
| Prints | What changes in the roles, whose records an admin vouches for, and the version that was stored. |
| Taught in | [Give people roles](../how-to/give-people-roles.md). |

A sealed state says who may read it. Roles say who may change it. A key has one of three roles, and the strength is linear: an admin may do what a writer may, and a writer what a reader may. The role is a field of the key's line in the readers file, and `pravo protect roles` stores the roles in the state so that the file in one person's checkout is not the last word. [Signatures and roles](../concepts/signatures-and-roles.md) explains what the roles rest on; this page says what each role may do and what the command writes.

## The roles

| Role | May |
|---|---|
| `reader` | Open the state, read it, and plan (or ask `why`) without changing it. Store a plan with `plan --store`. Read a secret: `secret get`, `secret exec`, and a run that reads one. |
| `writer` | Change the state: `apply`, `destroy`, `import`, `state mv`, `state rm`, `state push`, `discover adopt`, and a plan that writes the records it refreshed. Add and edit a policy. Set a secret with `secret set`. Approve a plan, where a policy asks for a writer's approval. |
| `admin` | Remove a policy, or change one that asks for approvals. Remove a secret and forget its old values (`secret rm`, `secret purge`). Change who reads the state (`protect rewrap`), change roles (`protect roles`), collect garbage (`protect gc`) and take the seal off (`protect unseal`). Apply a plan without the approvals a policy asks for. |

The role is the fifth field of the line of the key in the readers file, which the repository holds:

```text
ada ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIQx7mT… x25519:7Hc3Kp… role:admin
dan ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK2fPq… x25519:Qm41Zt… role:writer
eve ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB8dYw… x25519:Vb92Nc… role:reader
```

Whoever can merge a change to this file decides the roles. A readers file in which no line has a role gives none, and every key may do everything. As soon as one line has a role, a line without one is a reader. A pravo that does not know roles refuses a line with a fifth field, so it fails closed. A promotion needs no `rewrap`: the state holds the locks, and the roles are a separate thing, which the next section describes.

## Pinned roles

The file alone has a gap: a reader who edits their own line in their own checkout becomes an admin for as long as that checkout lasts. `pravo protect roles` closes it. An admin runs it, and it stores the roles of the readers file in the state as `roles/00000001.json`, signed, and adds one line to the readers file:

```text
roles-genesis 5c1e…
```

The line is the SHA-256 of the first roles. Commit it with the file. From then on the roles in the state decide, and the roles in the file say what an admin asks for. A change of roles is a new version, made by `pravo protect roles` and signed by a key that is an admin in the version before it; the first version is signed by a key that is an admin in the readers file. So a reader who holds the data key and the credentials of the bucket cannot make a version that verifies, cannot replace the first one (its digest is the pin), and cannot delete the chain without every honest run refusing the state with exit 7.

| Case | What the roles say |
|---|---|
| A key in the file and not in the roles of the state | A reader, until an admin stores a version with it. |
| A key dropped from the file | Kept in the next version as a *former* key, `NAME SHA256:FINGERPRINT role:former`. |
| A key that signed a transaction before the roles were stored, and that no one lists | Recorded as a former key as well. `protect roles` shows those and asks first. |
| A former key, or a writer who was demoted | What they wrote stays valid only as far as an admin *vouched* for it (below). A record of theirs that no vouch names is refused with exit 7. |
| A key in neither list | Refused: `signed by a key that was never in the roles`, exit 7. A holder of the data key could make such a key for the purpose. |

**Vouching.** When `protect roles` stores a version, it lists the transactions that wrote the records now in the state and that were signed by a key that is not a writer in the new roles, and adds one line for each, `@vouch FINGERPRINT TRANSACTION DIGEST`. The digest covers everything that the finish signature covers, so a transaction rewritten in place is not the one that was vouched for. The command prints `dan: vouched for 12 transactions (records they wrote stay valid)` before it asks, so an admin sees what is grandfathered. Offboarding someone therefore does not break the next `plan`, and a key that left cannot add anything afterwards. Up to 5000 transactions are vouched for in one version; `protect roles` stops with a count when there are more.

**Before the pin is committed.** The file decides, `protect status` says `from the readers file, not pinned`, and a record of a key that is not a writer is `not checked`. A state that holds roles that the file does not pin is reported too: `the state holds roles it does not pin`.

**A key that left.** A key that left and kept the data key can still write to a bucket that its credentials reach, and honest runs refuse the result. Remove its credentials from the bucket, and run `pravo protect rewrap --all` and `pravo protect gc` to stop it writing records that open. An object lock on `roles/` stops the deletion of the newest versions, which the pin cannot see.

## What a reader can do

The state is opened as it is and what a plan writes stays in memory: the refreshed records, the marks and the locks are not written to the bucket. The one thing a reader writes is a stored plan, with `plan --store`. A writer can apply it. The apply checks the plan as it checks any stored plan, against the revisions of the state and the fingerprint of the configuration, and the prompt says which key stored it. `plan --reserve` is for a writer.

## What is refused

| Who | Command | Message | Exit |
|---|---|---|---|
| A reader | `apply`, `destroy`, `import`, `state mv`, `state rm`, `state push`, `discover adopt` | `your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.` | 5 |
| A reader or a writer | `protect rewrap`, `protect gc`, `protect unseal` | `your identity is a reader in .pravo/access/readers.txt, and an admin is needed to change who reads the state.` | 5 |
| A reader or a writer | `protect roles` | `your identity is a reader in the roles in the state, and an admin is needed to store the roles.` | 5 |
| A writer | An apply that removes a policy, or changes one that asks for approvals | Refused before it changes anything. | 5 |
| A key that is not in the file | Any command that changes the state, when the file has roles | Has no role, and is refused as well. | 5 |

The shares of a recovery key are not an identity, so the roles do not apply to `protect recover`: K holders of shares can read the state together.

**Policies.** A writer adds a policy and edits one. Only an admin removes one, because a removal is the change that lets a plan through that the policy would have stopped, and only an admin changes a policy that asks for approvals, for the same reason. A version that was signed by a key whose role the file has lowered since reads as `role_changed` in the viewer and is accepted, as a version signed by a key that left reads as `key_not_in_file`: nothing says whether the key had the role when it signed.

## Records are bound to the writer

With roles, the transaction of a writer signs the key of every record it writes, in the finish signature. A reader holds the data key, and can seal a record that opens for everyone, but cannot make the transaction of a writer name it. `pravo protect status` prints a `Records` line, and `Untrusted:` and the address of a record that no verified transaction of a writer accounts for, and exits 7. `plan`, `apply` and `destroy` check every record before they read it: a writer who refreshed a record that a reader had sealed would give it a transaction of their own. Remove such a record with `pravo state rm`, or restore it from a version of the bucket.

| A record | Reads as |
|---|---|
| Written by a verified transaction of a writer | Bound. |
| Written before the roles began | `from before roles`. |
| Whose transaction is still running or was killed | `not checked`. |
| Whose transaction is not in the state | Untrusted. Transactions are not collected, so do not delete them. |
| Signed by a former key or a demoted key | `vouched for by an admin` when the roles name its transaction, untrusted when they do not. |
| Signed by a key that was never in the roles | Untrusted with the roles pinned; `not checked` without the pin, since a member who has left looks the same as a key that someone made. |

## What roles do not do

They are checked by a pravo that reads the readers file and the roles in the state. Someone who holds the credentials of the bucket and the data key, and a pravo of their own, can write the bucket; the record they write is found by the next honest run, and not stopped. What stops it is the bucket. Give the keys of the bucket by role:

| Role | Credentials on the bucket |
|---|---|
| `reader` | Get and list on the whole prefix. Put on `plans/`. |
| `writer` | Get, put, delete and list on the prefix, except `meta.json`, `policy/`, `roles/` and `secret/`, where a writer has get and list and may put a new object under `policy/` and under `secret/`. |
| `admin` | Everything on the prefix, `meta.json`, `policy/`, `roles/` and `secret/` included. |

A job that only plans runs with the first row. The first secret of a state raises `meta.json` to format 5, which needs the put of an admin; a writer sets every later version with a put of a new object. `secret rm` writes a new object too, and `secret purge` and `rewrap --all` replace objects of `secret/`, so they need an admin's credentials as well as an admin's role.

## Example

The state is sealed to two identities, ada as an admin and dan as a reader. The store is an S3-compatible service on the loopback interface.

```cue file=main.cue
package ws

$settings: state: {
	backend:    "s3"
	bucket:     "northwind-state"
	prefix:     "prod"
	region:     "eu-west-1"
	endpoint:   "http://127.0.0.1:9000"
	path_style: true
}
```

```sh run
mkdir -p people .pravo/access
pravo id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan > /dev/null
echo "$(pravo id show | tail -n 1 | sed 's/^ *//') role:admin" > .pravo/access/readers.txt
echo "$(PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//') role:reader" >> .pravo/access/readers.txt
cat .pravo/access/readers.txt
pravo protect init --encrypt --yes | grep 'sealed to'
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:reader
The state is sealed to ada dan.
```

The roles are in the file and not yet in the state. `pravo protect status` says so, and `pravo protect roles` stores them and adds the pin line:

```sh run
pravo protect status | grep '^Roles'
pravo protect roles --yes
tail -n 1 .pravo/access/readers.txt
pravo protect status | grep '^Roles'
```

```text out
Roles        on · 1 admin · 0 writer · 1 reader · from the readers file, not pinned (pravo protect roles) · you are an admin
  ada: admin
  dan: reader
Stored the roles as version 1.
Added roles-genesis <digest> to .pravo/access/readers.txt. Commit it: until it is in the repository the roles of the file decide.
roles-genesis <digest>
Roles        on · 1 admin · 0 writer · 1 reader · pinned, version 1 in the state · you are an admin
```

dan, a reader, cannot change who reads the state, and cannot store roles:

```sh run
PRAVO_CONFIG_DIR=people/dan pravo protect rewrap --all --yes; echo "exit=$?"
PRAVO_CONFIG_DIR=people/dan pravo protect roles --yes; echo "exit=$?"
```

```text out
Error: your identity is a reader in .pravo/access/readers.txt, and an admin is needed to change who reads the state.
exit=5

Error: your identity is a reader in the roles in the state, and an admin is needed to store the roles.
exit=5
```

A change of roles is an edit of the file and the command again, run by an admin. dan becomes a writer:

```sh run
sed -i.bak 's/role:reader/role:writer/' .pravo/access/readers.txt && rm .pravo/access/readers.txt.bak
pravo protect roles --yes
pravo protect status | grep '^Roles'
```

```text out
  dan: reader -> writer
Stored the roles as version 2.
Roles        on · 1 admin · 1 writer · 0 reader · pinned, version 2 in the state · you are an admin
```

With `--json`, the command prints `version`, `written` (false when the state already holds these roles), `pin`, `pin_added` and `changes`, the lines it printed above.

## See also

- [Give people roles](../how-to/give-people-roles.md) walks through it, with the offboarding of a member.
- [Signatures and roles](../concepts/signatures-and-roles.md) says what a signature proves and what the roles add.
- [`pravo protect`](protect.md) seals the state, and its `status` reports the roles and the records.
- [`pravo id`](id.md) makes an identity.
- [Operating policies](policies-operations.md#ask-for-approvals) says how a policy asks for the approval of a writer or an admin.
