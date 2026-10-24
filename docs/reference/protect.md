---
title: pravo id and pravo protect
description: Make an identity, seal the state to a list of readers, check that it holds, change the readers and collect old records. Commands, flags and exit codes.
order: 19
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
  - pattern: '[0-9]+(\.[0-9])? s\b'
    replace: '<time> s'
  - pattern: 'pravo [0-9][0-9A-Za-z.+-]*'
    replace: 'pravo <version>'
  - pattern: 'pravo >= [0-9][0-9A-Za-z.+-]*'
    replace: 'pravo >= <version>'
  - pattern: '"(fingerprint|path|line|reader)": "[^"]*"'
    replace: '"$1": "<value>"'
keywords: "encrypt seal secrets readers identity access passphrase rewrap state encryption"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo id new`, `pravo id show`, `pravo protect check`, `pravo protect init --encrypt`, `pravo protect status`, `pravo protect rewrap`, `pravo protect recover`, `pravo protect unseal`, `pravo protect gc` |
| Changes infrastructure | No. |
| Reads | The state, and `.pravo/access/readers.txt` in the repository. `id` reads and writes only identity files. |
| Writes | `id new` writes an identity file. `protect init`, `protect rewrap`, `protect recover`, `protect unseal` and `protect gc` write the state; `protect unseal --remove-files` also deletes the readers file and the recovery file of the repository; a command that sets a recovery also writes `recovery-shares/` and `.pravo/access/recovery.txt`; `gc` writes the object `gc/marks.json` as well. `check` and `status` write nothing, and so does `gc --dry-run`. |
| Prints | One line for each check, then a verdict. The line to add to the readers file. For `gc`, what it deleted and dropped. |
| Taught in | [Seal the state to a list of readers](../how-to/seal-the-state.md) and [Lab 3](../learn/lab3-the-leaked-bucket.md). |

Protection is off by default. A state with protection off is unchanged: format 1, with the layout that every version of pravo reads. These commands turn protection on for one state, show where it stands and keep it in line with the list of readers. [What sealing protects](../concepts/sealed-state.md) says what the seal covers and what it leaves open.

A state has three formats, and a sealed state is a format 3 state that has a sealed part in `meta.json`.

| Format | Name | What it is |
|---|---|---|
| 1 | off | One file for each resource, `resources/ADDRESS.json`. The default, and the only format earlier versions of pravo read. |
| 2 | immutable records | Each record is an object named by the SHA-256 of its bytes, `rec/SHA256`, and never overwritten. `resources/ADDRESS.json` is a small pointer to it, replaced only by compare-and-swap. Nothing is encrypted. |
| 3 | sealed | Format 2, and while the state is sealed, the attributes and private data of each record, the outputs, the stored plans and the values in command lines are encrypted to a list of readers. The address, type, name, dependencies and revision stay readable. A state that was unsealed stays in format 3, with nothing encrypted. |

`pravo protect init --encrypt` takes a state from format 1 to format 3. A pravo that does not know format 3 refuses the state with `state at LOCATION was written by a newer pravo (format 3)`.

The format is the layout, and it only goes up. Whether the state is sealed is a matter of its `meta.json` having a sealed part. `pravo protect unseal` takes that part away and leaves the format at 3: the state is the same state, in the same lineage, with the layout of format 2 and no encryption. It is not a step back to an older format, and sealing it again is the same command as the first time.

### Subcommands

| Subcommand | What it does | Reads the state | Writes the state | `--json` |
|---|---|---|---|---|
| `id new` | Makes an identity and prints the line for the readers file. | No. | No. | yes |
| `id show` | Prints the keys and the line of an identity again. | No. | No. | yes |
| `protect check` | Says whether the store can be sealed. | yes | No. | yes |
| `protect init --encrypt` | Seals the state to the readers file. | yes | yes | no |
| `protect status` | Says whether the state is sealed, to whom, and whether it verifies. | yes | No. | yes |
| `protect rewrap` | Brings the keys of the state in line with the readers file. | yes | yes | yes |
| `protect recover` | Opens the state with the shares of its recovery key, and makes the readers those of the readers file. | yes | yes | yes |
| `protect unseal` | Takes the seal off: writes everything in the open, and removes the sealed part of `meta.json`. | yes | yes | yes |
| `protect gc` | Deletes the records that no pointer names, and the reader groups that nothing is sealed in. | yes | yes | yes |

### Flags

| Flag | Command | Meaning |
|---|---|---|
| `--name NAME` | `id new`, `id show` | The name of the identity. For `new`, the default is the name of the operating system user (`$USER`, or `$USERNAME` on Windows), except with `--ci`, which requires `--name`. With neither variable set, `new` stops and asks for `--name`. For `show`, the default is the only identity, or the one named like the user. |
| `--ssh FILE` | `id new` | Use the `ssh-ed25519` public key `FILE.pub`, or `FILE` if that is a public key, as the signing key. The private key is never read. |
| `--ci` | `id new` | Make an identity for a job. It has no passphrase and is written to `--out`. It requires `--name`. |
| `--out FILE` | `id new` | With `--ci`: the file to write the identity to. |
| `--unlock MODE` | `id new` | What opens the identity file: `passphrase`, the default, or `agent`. `agent` seals the file under a key that the ssh-agent derives and asks for no passphrase. `--ci` refuses it. |
| `--agent-key SELECTOR` | `id new` | With `--unlock agent`: the key of the ssh-agent, by its fingerprint (`SHA256:...`) or by part of its comment, which is the title of the item in 1Password. Required when the agent holds more than one `ssh-ed25519` key. |
| `--access-dir DIR` | `protect` | The directory of the readers file, in place of `.pravo/access`. `$PRAVO_ACCESS_DIR` sets the same. A relative directory is relative to the root. |
| `--identity NAME` | every command that opens a state, and `id show` | The identity to open the state with, a file of the identity directory. A global flag, also written before the command. For `id show` it is another way to write `--name`. |
| `--allow-local` | `protect check`, `protect init` | Accept a state in a local directory. |
| `--encrypt` | `protect init` | Required. Seal the attributes, outputs and stored plans to the readers. |
| `--yes` | `protect init`, `protect recover`, `protect rewrap`, `protect unseal` | Do not ask for confirmation. `rewrap` asks only before it replaces or removes a recovery recipient, since that makes the shares that were given out worth nothing. |
| `--recovery K/N` | `protect init`, `protect rewrap`, `protect recover` | Set a recovery recipient, or replace it: a new key is split into N shares, any K of which rebuild it. `2/3` to `20/20`; K is at least 2. |
| `--remove-recovery` | `protect rewrap` | Take the recovery recipient out of the state, and delete the recovery file. |
| `--share FILE` | `protect recover`, `protect unseal` | A share file. Give it once for each share. For `unseal` the shares open the state in place of an identity. |
| `--remove-files` | `protect unseal` | Also delete the readers file and the recovery file of the repository, which tell writers to expect a sealed state. |
| `--all` | `protect rewrap` | Seal every record, the outputs, the stored plans and the sealed text of errors again under the current key. |
| `--older-than DURATION` | `protect gc` | How old a record that no pointer names must be to be deleted: `36h`, `7d`. The default is `24h`. A value below 65 seconds is raised to 65 seconds. It should exceed the skew between the clocks of the machines that write to the state. |
| `--dry-run` | `protect gc` | Say what would be marked, deleted and dropped, and change nothing. It writes no marks. |

### Exit codes

| Code | When |
|---|---|
| 0 | The command did what it was asked. `check` found the store ready. |
| 1 | Any other failure: a store that cannot be sealed, a local directory without `--allow-local`, an identity that exists, no terminal to ask on and no `--yes`, a state that is not sealed for `rewrap`. |
| 4 | A command is running or a lock is held. `protect init`, `protect check` and `protect unseal` do not run then, and neither does `rewrap --all`. A stored plan that is sealed and in use by an apply stops `protect unseal` with 4 as well. `protect gc` collects the records and then exits with 4 without looking at the groups. |
| 5 | You are not a reader of the state, your identity is not on the readers file, or the readers file and the state differ for a command that writes. The shares do not rebuild the recovery key of the state (`protect recover`). The recovery file and the state name different recovery keys, or the state has a recovery and the repository has no recovery file, for a command that writes. For any other command that writes a sealed state, such as `state rm` or `apply`: the readers file is missing. |
| 6 | The recovery file is invalid, for a command that writes a sealed state. The readers file is invalid, for `protect check`, `init` and `rewrap` and for a command that writes a sealed state. For `protect check`, `init` and `rewrap`: the readers file is missing. |
| 7 | A signature of a transaction does not verify, for `protect status` and `state log`. A command that writes finds records that are sealed in a state whose `meta.json` says it is not sealed. A record does not match its name. `protect init` also stops with 7, before it writes anything, when the state says it is not sealed and a record in it is: `meta.json` was lowered or replaced by an older one. Put it back from a version of the bucket. [Exit codes](exit-codes.md#7-untrusted) says what to do. |

## The readers file

`.pravo/access/readers.txt` in the repository lists who may read the state, one line for each reader:

```text
NAME ssh-ed25519 BASE64 x25519:BASE64URL
```

Whoever can merge a change to this file decides who may read. Writers take the list from the checked-out repository and never from the state, so a person who can only write to the bucket cannot add themselves. Commit the file.

| Rule | |
|---|---|
| Fields | Exactly four, separated by blanks: the name, `ssh-ed25519`, the SSH public key as `pravo id new` prints it without a comment, and the reader key. |
| Name | Letters, digits, `.`, `_` and `-`, starting with a letter or a digit, at most 64 characters. A name is used once, without regard to case. |
| Comments | A line that starts with `#` is skipped. A `#` after the fourth field starts a comment. Blank lines are skipped. |
| Readers | At most 200 readers. The line that exceeds the limit is reported by its number. |
| Size | At most 64 KiB. pravo reads it as a regular file, and not through a symbolic link that leaves the access directory. |
| Errors | A line that does not parse is reported as its number and a reason. The text of the line is never repeated. |

pravo looks for the file in `--access-dir`, then in `$PRAVO_ACCESS_DIR`, then in `.pravo/access` of the root, then in the same place in each parent directory up to the directory that holds `.git`. Outside a repository it looks in the root only.

A command that writes a sealed state compares the file with the readers recorded in the state. If they differ, it stops with exit code 5 and `.pravo/access/readers.txt and the state differ (dan added, fay removed). Run pravo protect rewrap.` A command that only reads prints the same text as a warning on standard error. A missing file stops a command that writes the state with exit code 5, and an invalid one with exit code 6. The `protect` commands exit with 6 for both: `protect check`, `init` and `rewrap` have no verdict without a valid file. `protect status` prints `file not found` or `file is invalid:` and exits 0.

A state that is not sealed in a repository that has a readers file is refused in the same way. A command that writes stops with exit code 5 and `the repository lists readers in .pravo/access/readers.txt, but the state is not sealed: it has not been sealed yet, or its meta.json was changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file and the recovery file.` A state that does not exist yet is refused before anything is created: the command stops with exit code 5 and `the repository lists readers in .pravo/access/readers.txt, but the state does not exist yet. Create it sealed with: pravo protect init --encrypt`. A command that only reads prints the text as a warning, and creates no state. A file that is found counts whether or not it can be read. `protect check`, `protect init`, `protect status`, `protect unseal` and the `id` commands are not stopped. A root in a repository whose readers file is meant for other roots names another access directory with `--access-dir` or `$PRAVO_ACCESS_DIR`, and is then not expected to be sealed.

## The identity

An identity is a name, a signing key and a reader key. The reader key opens the sealed state. The signing key is part of the line in the readers file, and signs the transactions and the stored plans that the identity writes: see [Signatures](#signatures).

| Where | What |
|---|---|
| `$PRAVO_CONFIG_DIR/id/NAME`, or `~/.config/pravo/id/NAME` | The identity file made by `id new`, when `$PRAVO_IDENTITY_DIR` is not set: mode 0600, in a directory of mode 0700. The reader key is sealed under a passphrase. On Windows the directory is `%AppData%\pravo`. |
| `$PRAVO_IDENTITY_DIR` | The directory that holds identity files, in place of the `id` directory above. `id new` writes there as well, and makes the directory with mode 0700 when it is missing. |
| `$PRAVO_IDENTITY` | The contents of an identity file, if the value starts with `{`, or the path of one. For a CI job. |

The identity directory is not `~/.pravo`, which is the provider cache and may be deleted.

pravo finds the identity in this order: `--identity NAME`, `$PRAVO_IDENTITY`, then the files of the identity directory. When there are several files, it uses the one named like the user. If none is, it uses the one file whose reader key has a lock in the current group of the state, which `meta.json` lists. For `protect init`, which has no locks yet, the reader key is one of the readers file. The reader key of an identity file is public: choosing reads no passphrase, and only the chosen identity is asked for its passphrase. If no file or more than one matches, the command stops with `no identity found: ... holds several identities (ada dan) and none is named like you`, and names the files that match when there are several. Name one with `--identity`. The same choice is made for the state of another root that `pravo.#RemoteOutputs` reads, by the locks in that state's `meta.json`. pravo looks only when the state is sealed, so a state with protection off never asks for a passphrase.

A passphrase is asked for on the terminal without echo. Without a terminal it is read from `$PRAVO_PASSPHRASE` or `$PRAVO_IDENTITY_PASSPHRASE`, and a command with neither stops with `the identity ada is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE.` An identity that the ssh-agent unlocks asks the agent once for each command and neither asks for a passphrase nor reads the variables. pravo removes `PRAVO_IDENTITY`, `PRAVO_PASSPHRASE` and `PRAVO_IDENTITY_PASSPHRASE` from its environment once it has read them, and provider processes are started without them. [`pravo mcp`](mcp.md) never asks on a terminal: the identity of a server comes from the environment it was started in.

## Signatures

In a sealed state every transaction is signed by the identity that ran the command, and every stored plan is signed by the identity that stored it. The signing key is the second field of the identity's line in the readers file. A signature is trusted when its key is in the readers file of the repository, which a writer of the bucket cannot change, so the readers file is the whole key set: there is no other file. A state that is not sealed gets no new signatures. One that was unsealed keeps those it has, which are checked only when a readers file is there.

**What is signed.** What a key signs is a fixed prefix and the SHA-256 of the message below, so that an ssh-agent, which refuses large requests, gets the same few bytes for a transaction with ten changes and one with ten thousand. A text field is signed as the log stores it, with each byte that is not valid UTF-8 as U+FFFD. A transaction is signed twice, under the domain tags `pravo/v1/txn-start` and `pravo/v1/txn-finish`, together with the lineage of the state:

| Signature | Covers |
|---|---|
| Start | The id, kind, command, user, host, start time, actor, reason, git revision, trace id, version and, for an apply, the id and the SHA-256 of the plan file. |
| Finish | The same, and the end time, the status and each change: address, action and the two revisions. |

A run that is killed leaves the first signature, which attributes what it set out to do. The text of the error of a run that failed is not covered, because sealing, a rewrap and an unseal rewrite it. A stored plan has a signature of its own, in the object `plans/<id>.sig-<key>.json`, over the SHA-256 of the plan file and the time. The object is created and never replaced, so a second person who stores the same plan adds a signature. It holds no digest: a reader recomputes it from the plan, and the object holds nothing that a guess of the plan could be checked against. It does not cover the summary.

**What a verifier says.** `pravo state log`, `pravo plan show`, `pravo protect status`, and in `pravo serve` the history of a node and the plan view of a stored plan, check the signatures against the readers file:

| Status | Means | Effect |
|---|---|---|
| `verified` | A key in the readers file made the signature of this content. | |
| `key not in the file` | The key is not listed: a member who has left, or a key that never was. The transaction shows the name that the signature gives itself. | Reported. Not an error, since every departure would otherwise turn the history red. |
| `unsigned` | No signature: written before signatures, or by a run that could not sign its end. A finished transaction with no signature on its end is unsigned even if its start is signed. | Reported. |
| `invalid` | The key is listed and the signature is not its signature of this content: the transaction was changed. So is a transaction that is still running and holds an outcome that only the end signs, and one whose two signatures are by different keys. | `state log` and `protect status` exit with 7 and say which transaction. |

Without a readers file, or with one that is invalid, nothing is checked, and `state log`, `plan show` and `protect status` say so. The JSON listings omit the signature. Exit code 7 catches a change that leaves the signatures in place. Someone who writes to the bucket can remove both signatures, and the transaction then shows as unsigned, or give them a key that is not listed, which shows as `key not in the file`: the listings count both, and neither is an error. A signature that is not verified is a label: a member who has left leaves signatures that are verified only against a copy of the readers file from the time, which git has.

**When signing fails.** A command that changes the state, an apply, a destroy, an import or a state edit, and cannot sign its start, because its identity signs through an ssh-agent that is not reachable or does not hold the key, is refused before it changes anything. A plan that refreshes records is signed when it ends. A run that cannot sign its end has already changed the infrastructure: the transaction is recorded without the second signature, and the command prints a warning and does not fail, since a failure would have a caller treat an apply that was done as one that was not. The agent is waited for 90 seconds, which a touch needs. A plan that cannot be signed is not stored. An invalid signature on a stored plan is shown and does not stop an apply, since anyone who can write to the bucket can add objects to it.

**What it does not cover.**

- *The bodies of records.* A signature says which key ran a transaction, which addresses and revisions it changed, and, for an apply, which plan. It does not bind the values. In a sealed state a writer without a reader key cannot make a record at all; a reader can seal any record, and a signature does not say that a value is the one a run wrote.
- *Deletion, replacement by an older copy and order.* An old signed transaction is as valid as a new one. Finding a deleted transaction or a state that was rolled back takes a ledger.
- *Who may write.* Every reader may sign. Roles come after.

## The recovery recipient

A recovery recipient is one more reader of a sealed state whose key nobody holds whole. The key is made when the recovery is set, split into N shares by Shamir secret sharing, and forgotten. Any K of the shares rebuild it, and fewer give nothing. Every group of the state carries a lock for its public key, so that K holders of shares can open the state when every reader has lost their key.

| Where | What |
|---|---|
| `recovery-shares/share-I-of-N.txt` | One share each, with mode 0600, in the directory where the command ran. They are written once. Give each to a different person, offline, and do not commit them. A directory that already holds shares is refused, so that two sets are not mixed, and it gets a `.gitignore` that ignores everything in it. |
| `.pravo/access/recovery.txt` | The public key and the threshold, `x25519:BASE64URL K/N`, with comments and blank lines allowed. Commit it. It sits in the access directory next to the readers file. |
| `meta.json` | The key and the threshold under `sealed.recovery`, and a lock named `recovery` in every group. The readers file may not use the name `recovery`. |

A command that is interrupted after it set the recovery in the state keeps the shares and writes the recovery file, and says so: the lock is there, and the shares are the only way in. Shares of a key that the state does not hold are removed. With `--json`, `rewrap` and `recover` write the files first and report a `recovery` object: `action` (`set`, `replaced`, `from_file` or `removed`), `k`, `n`, `key`, `file` and, for new shares, `shares`.

Nothing opens the lock of the recovery key until the shares are used, so the lock carries a keyed check under the data key of its group. A reader, and so every command that writes the state, sees a box that someone with no data key changed: the command stops with exit code 5, `protect status` says which groups, and `protect rewrap` makes the lock again. The check shows that the box is the one a reader made. It does not show that the shares open it, so rehearse once: copy the bucket, and run `protect recover` against the copy with K shares.

Two commands that change the recovery at once do not both succeed. The one that finds the recovery changed since it read the state writes nothing, removes the shares it wrote and exits with code 3; the other one's shares are the ones that open the state.

A share file holds one line, `pravo-share-1`, the id of the key, the index, `K/N`, the share and a short check that catches a mistyped or damaged line, after two lines of comment.

A command that writes a sealed state compares the recovery file with the state, as it compares the readers file, and stops with exit code 5 when they disagree:

| Message | Meaning | What to do |
|---|---|---|
| `.pravo/access/recovery.txt lists a recovery recipient that the state does not have.` | The file names a key and the state has none. | `pravo protect rewrap` sets the key of the file in the state. |
| `.pravo/access/recovery.txt and the state name different recovery keys.` | The state was changed, or the file was. | `pravo protect rewrap` makes the state follow the file. |
| `the state has a recovery recipient, and .pravo/access/recovery.txt was not found` | The file was not committed, or the recovery was set from another checkout. | Commit the file, or remove the recovery with `pravo protect rewrap --remove-recovery`. |
| `the recovery lock is missing from group g1.` | A lock was taken out of `meta.json`. | `pravo protect rewrap` makes it again. |

A recovery file that does not load stops a command that writes with exit code 6. A command that only reads prints the same text as a warning.

Setting a recovery is a decision about trust. K holders of shares can read the state together, and nothing delays them: there is no waiting time and no approval until roles exist. A recovery key that was used has been rebuilt on one machine, so replace it afterwards. The shares of a replaced key open nothing that is sealed from then on: replacing or removing the recovery makes a new data key, as removing a reader does, so a holder of old shares who kept a copy of the bucket opens what was in it and nothing written after. A reader may not hold the recovery key: `rewrap` refuses a readers file that lists it.

## id new

`pravo id new` makes a reader key and a signing key, writes the identity to `NAME` in the identity directory, and prints the line for the readers file. It asks for a passphrase twice, at least 8 characters. It touches no state, and anyone can run it at any time.

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

The line is public and safe to paste into a chat or a pull request. Give it to whoever keeps the readers file.

An identity is never overwritten. `pravo id new --name ada` for a name that exists stops with `identity ada already exists at PATH` and exit code 1, before it asks for a passphrase.

`--ssh FILE` uses an `ssh-ed25519` public key as the signing key, so that there is no new signing key to manage. pravo reads `FILE.pub`, or `FILE` if that is itself a public key, and never reads a private key. Keys of other types are refused. Such an identity signs through the ssh-agent that `SSH_AUTH_SOCK` names (on Windows the OpenSSH pipe), and the key has to be loaded in it; an agent that asks for a touch asks for it at each signature, which is two for a run.

### Unlock with the ssh-agent

`--unlock agent` seals the identity file under a key that the ssh-agent derives, so that opening it takes whatever the agent takes to sign, and no passphrase is set. With the SSH agent of 1Password that is an approval, which Touch ID can give. The agent must hold an `ssh-ed25519` key. If it holds one, pravo uses it. If it holds several, `--agent-key` names one by its fingerprint or by part of its comment.

```sh
pravo id new --name ada-mac --unlock agent --agent-key "pravo key"
```

The command prints the same lines as one with a passphrase, and the reader line says `sealed under ssh-agent key SHA256:<fingerprint>`. The line for the readers file is the same too: the readers file and `rewrap` do not know how an identity file is opened.

The wrapping key is HKDF-SHA256 over the agent's signature of a challenge. The challenge holds the name of the identity, its reader key, the agent key and a random salt of 32 bytes that the file stores, so a signature made for one file opens no other. An Ed25519 signature of one message by one key is the same every time (RFC 8032), so the same signature opens the file each time. The challenge starts with a tag that the signatures of events never start with, so signing an event never yields the key.

`id new` asks the agent for the signature twice and stops with `the ssh-agent signs differently each time` if the two differ, since a file sealed under a key that cannot be derived again could never be opened. Nothing is written then. Keys of other types are not offered, and the agent has to be reachable through `SSH_AUTH_SOCK`.

**The agent decides who can open the file.** Any process that the agent signs for opens it. 1Password asks before it lets an application use a key, and its settings say how long an approval lasts; `ssh-add -c` asks for each use; an agent that signs for every process of the user asks for nothing, and the file is then as open as the agent. The file holds no passphrase to guess, and a copy of it opens nothing without the key.

**The key has to stay.** If the key is removed from the agent, the identity is lost with it. Keep a second reader that does not depend on the key: an identity with a passphrase, under another name. Adding it is the same as adding any reader; see [Seal the state to a list of readers](../how-to/seal-the-state.md#1-make-an-identity).

A pravo that does not know this seal refuses the file with `identity uses kdf "ssh-agent-ed25519", which this pravo does not know`.

### For a CI job

A job has no person to type a passphrase. `--ci` makes an identity with no passphrase and writes it to the file named by `--out`, with mode 0600. It writes nothing to the identity directory. `--out` is required with `--ci` and refused without it. An existing file is not overwritten.

```sh run
pravo id new --ci --name deploy-bot --out deploy-bot.identity
```

```text out
Identity deploy-bot created for a job. It has no passphrase: protect the file.
  private  deploy-bot.identity   (store it as the CI secret PRAVO_IDENTITY, then delete this file)
  public   deploy-bot ssh-ed25519 <ssh-key> x25519:<key>
```

`--ci` requires `--name`, and `id new --ci` without it stops with exit code 1. A default taken from the operating system user would be the name of a person, and a name is used once in the readers file, so give each job a name of its own.

Store the contents of the file as the secret `PRAVO_IDENTITY` of the job, then delete the file. Use one identity for each job, and never a person's identity in a job. Add the public line to the readers file and run `pravo protect rewrap`. The identity is the authority of the job in pravo and is not its cloud credential.

## id show

`pravo id show` prints the signing key, the reader key and the line for the readers file. It reads the public part of the file and does not ask for the passphrase or call the ssh-agent.

```sh run
pravo id show
```

```text out
Identity ada (people/ada/id/ada)
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

For an identity that the ssh-agent unlocks, the reader line says `sealed under ssh-agent key SHA256:<fingerprint>`.

With `--json`, `id new` and `id show` print one document with `name`, `path`, `signing` (`alg`, `fingerprint`, `source`), `reader`, `line` and `unlock`. `unlock.by` is `passphrase`, `ssh-agent` or `none` (the file of a job). For `ssh-agent`, `unlock.key` is the fingerprint of the agent key, and `id new` adds `unlock.comment`.

## protect check

`pravo protect check` reads the store and says whether it can be sealed. It writes nothing to the state. It tests the store harder than the probe that every command makes ([S3-compatible stores](s3-stores.md)): eight writers replace one object at once and exactly one must win, and a key that has just been written must be in the listing. It then lists what is running now.

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
pravo protect check; echo "exit=$?"
```

```text out
Store            s3://northwind-state/prod  (eu-west-1)
Conditional      create-only ok · replace ok · delete ok
                 8 concurrent replaces: 1 winner · listing sees a new key at once
State            0 records · format 1
Readers file     not found (looked in .pravo/access)
Running          0 transactions · 0 locks held
Machines         no writer in the last 30 days
exit=6
```

| Line | Meaning |
|---|---|
| `Conditional` | The conditional requests that the records and the locks rest on. A store that ignores one is refused: the command prints `FAILED` for the check and exits 1. |
| `State` | The number of records, the format and the version of pravo that wrote last. |
| `Readers file` | The file that was found and the readers in it. Without a valid file there is no verdict and the exit code is 6. |
| `Running` | Transactions that are running and locks that are held. Either one stops `protect init`: the command prints `Not ready: wait for them, or release stale locks with pravo state unlock --stale.` and exits 4. |
| `Machines` | The oldest version of pravo that wrote in the last 30 days, taken from the transactions. A state written by pravo before versions were recorded says so. A pravo that does not know format 3 refuses a sealed state. |

A state in a local directory is refused: its compare-and-swap is a lock of one user, so two users of one directory would not be excluded. `--allow-local` accepts a directory that only you use. A state that is already sealed prints `Already sealed. Use pravo protect status.`

## protect init

`pravo protect init --encrypt` seals the state. It needs the readers file and an identity that is on it, because sealing to a list that you are not on would lock you out: the command stops with exit code 5 otherwise. A state that does not exist yet is created sealed, with no records.

It shows what it will do and asks `Proceed? [y/N]`. `--yes` answers it. Without a terminal and without `--yes` it refuses to ask. Nothing is written before the answer. The command does not run while a transaction is running or a lock is held. Run it while nobody plans or applies.

1. Every record is written once as the object `rec/SHA256`, and `resources/ADDRESS.json` becomes a pointer to it. A record is never overwritten again.
2. The attributes and the private data of each record are encrypted with one data key, which is locked for each reader.
3. `outputs.json`, the values of the stored plans, the values in the command lines of transactions and the text of the errors of transactions that failed are sealed the same way. The words of a command and the names of its flags stay readable, and so does a positional argument. The value of a flag that takes one is sealed, written `--flag value` or `--flag=value`, unless it is a flag with a public value such as `--limit`, `--ttl`, `--only`, `--out`, `--policy` or `--chdir`. A word after a flag that takes no value is a positional argument. A secret belongs in `-t`.
4. The state is set to format 3, and `meta.json` holds the locks.

`--recovery K/N` also sets a recovery recipient: a lock for a key that is split into N shares, any K of which rebuild it. The shares are written to `recovery-shares/` before the state is touched, so that a seal that fails leaves no recovery lock that nobody can open, and the public key is written to `.pravo/access/recovery.txt` when the seal is done. The prompt lists it as one more step. [The recovery recipient](#the-recovery-recipient) says what to do with the files.

A run that is interrupted leaves the state in the middle of sealing, and an earlier pravo cannot open it. A kill leaves it where it was. An interrupt (`Ctrl-C`) finishes the records that are being sealed, starts no other and ends with `interrupted after N records were sealed in this run`. Run the command again to finish: it skips the records it has done. `pravo protect status` says `migrating` until then, and commands that write the state refuse with `a seal is in progress; run pravo protect init --encrypt to finish`. Commit the readers file when the command ends.

```json file=terraform.tfstate hidden
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 1,
  "lineage": "abe75052-395d-84be-f062-c43444404a1a",
  "outputs": {"endpoint": {"value": "db.internal", "type": "string"}},
  "resources": [
    {"mode": "managed", "type": "aws_db_instance", "name": "main", "provider": "provider[\"registry.opentofu.org/hashicorp/aws\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "db-1", "master_password": "example-password", "engine": "postgres"}, "sensitive_attributes": [], "dependencies": []}]},
    {"mode": "managed", "type": "aws_vpc", "name": "main", "provider": "provider[\"registry.opentofu.org/hashicorp/aws\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "vpc-1", "cidr_block": "10.0.0.0/16"}, "sensitive_attributes": [], "dependencies": []}]}
  ]
}
```

```sh run hidden
pravo state import-tfstate terraform.tfstate
```

```text out
Imported 2 resources and 1 outputs from terraform.tfstate into s3://northwind-state/prod
```

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

```sh run
pravo protect init --encrypt --yes
```

```text out
Readers  .pravo/access/readers.txt  (ada dan)
This will:
  1  write 2 records as immutable objects  rec/<sha256>
  2  encrypt their attributes and private data to 2 readers (ada dan)
  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way
  4  set the state to format 3. Earlier versions of pravo will refuse it
store check passed
2 records sealed in <time> s
outputs sealed · recipients written to meta.json

The state is sealed to ada dan.

Commit the readers file. Writers read the list from git:
  .pravo/access/readers.txt
No recovery recipient. If every reader loses their key, the state cannot be read.
Add one with: pravo protect rewrap --recovery 3/5
```

A state that is already sealed prints `Already sealed (format 3). Use pravo protect rewrap to change the readers.` and writes nothing. Without `--recovery` there is no recovery recipient, and the command prints `No recovery recipient.` with the consequence and the line that adds one, `pravo protect rewrap --recovery 3/5`.

## protect status

`pravo protect status` is the command to run when you are unsure. It opens the state with your identity, checks every record against its name and every pointer against its record, and prints one screen. It writes nothing.

```sh run
pravo protect status
```

```text out
Sealed       on · format 3 · 2 readers (ada dan) · recovery none
Store        s3://northwind-state/prod
Readers      .pravo/access/readers.txt matches the store
You          ada · reader key x25519:<key>
Roles        off
Signatures   0 of 1 transaction verified · 1 unsigned
Unsettled    none
```

| Line | Values |
|---|---|
| `Sealed` | `on`, `off`, `off · format 2 · immutable records` (format 3 for a state that was unsealed), `migrating` with the number of records done, or `unsealing` with the same. A state that is off in a repository that has a readers file ends in `· the repository expects a sealed state`: it has not been sealed yet, or its `meta.json` was changed. A state that does not exist yet is `no state yet`, and with a readers file `no state yet · create it sealed with: pravo protect init --encrypt`. `status` creates nothing. |
| `Readers` | `matches the store`; `differs:` and who is in the file and not in the store, or the other way round; `file not found (looked in DIR)`; `file is invalid:` and the line and reason. |
| `Recovery` | Shown when the state has a recovery recipient or the repository has a recovery file: `2 of 3 shares · key x25519:…` and whether the recovery file matches the state, differs from it or is missing, and a group whose lock is missing. |
| `You` | The identity and its reader key. `identity NAME is not a reader`. `not a reader of this state (no identity found)`. |
| `Signatures` | For a sealed state: `41 of 43 transactions verified`, then the count of those signed with a key that is not in the readers file, of the unsigned ones, and of those whose signature does not verify. `not checked` without a readers file. |
| `Unsettled` | `none`, or the number of interrupted transactions; `pravo state log` lists them. |

With `--json`, `status` prints one document: `state` (`none` for a state that does not exist yet, `off`, `immutable`, `migrating`, `unsealing` or `sealed`), `format` (absent when there is no state), `store`, `records`, `checked`, `unsettled`, `untrusted`, `readers` (the names the state is sealed to), `readers_file` (`found`, `valid`, `matches`, `path`, `added`, `removed`), `signatures` (`verified`, `key_not_in_file`, `unsigned`, `invalid` and the ids in `invalid_transactions`, for a sealed state with a readers file), `recovery` (`k`, `n`, `key`, `locked`, and `file`: `match`, `differs`, `missing` or `invalid`, present only when the state has a recovery) and `you` for a sealed state, and `expects_sealed: true` when the repository lists readers and the state is not sealed, which includes a state that does not exist yet. A job that wants a verdict before it plans runs `pravo protect status --json | jq -e '.state == "sealed"'`.

A record that does not match its name, a pointer that disagrees with its record, or a sealed value that does not open where it is, prints `Untrusted:` and the object, and the command exits 7.

## protect rewrap

`pravo protect rewrap` compares the readers file with the readers the state is sealed to, and changes the state to match. It needs an identity that is a reader, and the readers file. It does not need the readers to agree to anything, and it rewrites `meta.json` only. If another command changes `meta.json` while it runs, `rewrap` reads it again and repeats; if it keeps changing, `rewrap` stops and asks you to run it again. A state with nothing to change prints `The store already matches the readers file.`

**Adding a reader.** The new reader makes an identity and sends the line it prints. A reader adds the line to the file, commits it and runs `rewrap`. The data keys are locked for the new reader in every group, so that the new reader can open what is already there. No record is rewritten.

**Removing a reader.** A reader who is removed loses their lock in every group, and the state gets a new data key. Records are sealed under it as they are next written. Until then they stay under the old key. `--all` seals every record, the outputs, the stored plans and the sealed text of the errors of transactions again under the new key at once. A copy of the state that was made before the removal still opens with the removed reader's key, and the reader has read what they read. Replace the secrets they could read where they live.

`--all` needs the state at rest: run it while nobody plans or applies. It seals again and deletes the records that are sealed in another group and that no pointer names. It does not remove the old groups from `meta.json`, and the last record of a resource that was destroyed or removed with `state rm` stays in `rec/`, sealed under the group it was written in. [`pravo protect gc`](#protect-gc) removes both. [What sealing protects](../concepts/sealed-state.md) lists what stays in the bucket.

**The recovery.** `--recovery K/N` sets the recovery recipient of the state, or replaces it: a new key is made, split into N shares and written to `recovery-shares/`, the public key goes to `.pravo/access/recovery.txt`, and every group gets a lock for it. A replaced key loses its lock in every group and the state gets a new data key, so its shares open nothing that is written afterwards; a copy of the bucket made before still opens with them, so collect and destroy them. `--remove-recovery` takes the recovery out of the state and deletes the file; commit the deletion. Both ask first, unless `--yes` is given. `--remove-recovery` also deletes a recovery file when the state has no recovery. Without either flag, a recovery file that names a key that the state does not have is set in the state with no new shares, and a recovery lock that is missing from a group is made again. `--all` and a replacement of the recovery run as two steps when the recovery key opens the state; [`protect recover`](#protect-recover) does that for you.

## protect recover

`pravo protect recover --share FILE --share FILE …` opens a state with the shares of its recovery key when no reader can, and makes the readers of the state the readers in the readers file. It is the way back when every reader has lost their key.

1. Make an identity: `pravo id new --name NAME`.
2. Write its line to the readers file in place of the lines of the readers who are lost.
3. Collect K share files from their holders, and run `pravo protect recover --share a.txt --share b.txt`.

The command rebuilds the recovery key in memory, checks it against the public key in `meta.json`, shows what it will do and asks `Proceed? [y/N]`. It then opens the state with that key, locks the data keys for the new readers, removes the old ones and makes a new data key, as `rewrap` does, and writes `meta.json`. Commit the readers file. It does not read the readers file for a verdict first: a state is recovered when no identity can open it.

```text
Recovery key rebuilt from 2 shares.
Readers  .pravo/access/readers.txt  (eve)
This will:
  1  lock the data keys for eve
  2  remove ada dan, and make a new data key
meta.json updated
New data keys for 1 reader group. Records are sealed again as they are next written.
The recovery key was rebuilt on this machine. Replace it: pravo protect rewrap --recovery K/N
```

`--all` seals every record, the outputs and the stored plans again under the new key, and does not run while a command is running or a lock is held. `--recovery K/N` also replaces the recovery key, in a second step after the records are sealed again, and writes the new shares; without it the old recovery stays and the command says to replace it. A recovery key that was rebuilt has been seen in one place.

| Failure | Exit code |
|---|---|
| Fewer than K shares, a share of another recovery, a share given twice, a wrong or mistyped share | 5, with the reason and the file; the text of a share is never repeated. |
| The state is not sealed, or has no recovery recipient | 1. A state that was sealed without one can only be opened by a reader. |
| The readers file is missing or invalid | 6 |

## protect unseal

`pravo protect unseal` takes the seal off the state. It opens every record, the outputs and the stored plans with your key and writes them in the open, deletes the sealed records, and removes the sealed part of `meta.json`. The format stays 3, the lineage stays, and the history of the seal stays with an entry `unsealed`.

It shows what it will do and asks `Proceed? [y/N]`. `--yes` answers it. Without a terminal and without `--yes` it refuses to ask. Nothing is written before the answer. The command does not run while a transaction is running or a lock is held. It needs an identity that is a reader of every group that anything is sealed in, or K shares of the recovery key (`--share`, once for each), which open the state in place of an identity when no reader can and are a reader of every group. The shares open nothing afterwards; destroy them. If some records, outputs, plans or error texts are sealed in a group that your key cannot open, it says how many and stops before it writes.

The order keeps the state safe when the command stops. Before step 2 it waits a few seconds, so that a command that last looked at `meta.json` just before the mark has looked again and is refused:

1. The command checks that nothing is running, and `meta.json` gets a `migration` key marked `unseal`. The state is still sealed, and every writer is refused (exit code 4, `an unseal is in progress; run pravo protect unseal to finish`).
2. Each sealed record is written again in the open, by compare-and-swap of its pointer, and the sealed record is deleted. A record that is in the open already is skipped.
3. `outputs.json`, the stored plans and the text of the errors of transactions are written in the open. A stored plan that an apply uses cannot be written in the open yet; the command stops with the state still sealed and exit code 4, and goes on when it is run again. A plan summary that this pravo cannot read is left as it is.
4. Before the seal comes off it checks that nothing the state names is sealed any more. A command that wrote a sealed record in the meantime is found here, while the keys still open it, and the command is run again.
5. `meta.json` is replaced without the sealed part, with the highest number of a group in `groups_used`.

A run that is interrupted leaves the state unsealing, which `protect status` says, and it is finished by running the command again, with an identity or with `--share` when every reader has lost their key. `protect recover` does not run on an unsealing state. Sealing a state that is unsealing is refused until the unseal is done.

What it cannot restore: the values of `-t` in the command lines of transactions that were recorded before the seal were replaced by a tag when the state was sealed, and nothing kept them.

What it leaves for you to do:

| Step | Why |
|---|---|
| Remove `.pravo/access/readers.txt` and `.pravo/access/recovery.txt` from the repository, or run with `--remove-files` and commit the deletion. | The repository is the anchor that tells a writer to expect a sealed state. Until the files are gone a command that writes refuses the unsealed state with exit code 5, which is what you want from a state that was unsealed by someone else. |
| Destroy the recovery shares, if there were any. | They open nothing. |
| Treat the secrets as readable by everyone who can read the bucket. | That is what the unseal does. A copy of the bucket made while it was sealed still opens with the key of a reader. |

On a state that is not sealed the command says `The state is not sealed.` and writes nothing, and for a state that does not exist it says so and creates nothing. `--remove-files` then deletes the files only for a state that was sealed once, which is a state of format 3, and asks first; for any other state the files belong to a seal that is yet to come, and they stay. The readers file and the recovery file are found as the other commands find them, in the access directory, so a repository whose roots share one access directory loses it for all of them: take that into account before `--remove-files`.

With `--json` the command prints `unsealed`, `records`, `plans`, `resumed`, `files_removed` and `files_left`; for a state that is not sealed it prints `unsealed: false`, `state`, `files_removed` and `files_left`.

A state that was unsealed is sealed again with `pravo protect init --encrypt`, to the same readers or to others. The groups of the new seal are numbered from where the first one stopped, so that no record left from the first is taken for one of the second.

## protect gc

`pravo protect gc` deletes what a state of format 2 or 3 keeps and nothing reads. A state of format 1 keeps one object for each resource, and the command says that it has nothing to collect. It works in two parts, and a state that is not sealed has the first only.

**Records.** A write never overwrites a record. The bucket therefore keeps every revision of every record, the record of a resource that was destroyed or removed with `pravo state rm`, and the record that a write left when it failed. In format 2 these hold their attributes in the clear. A record that no pointer names is garbage once it is older than the grace, 24 hours or `--older-than`, and two collections have found it so. The age is the newest of the times `updated_at`, `refreshed_at` and `applied_at` in the record, which are readable without a key. A record that shows none of them is kept, and so is an object in `rec/` that does not match its name: the command lists it and does not touch it. A record that a pointer names is never deleted.

Nothing names a record from the time a writer has made it until the writer replaces the pointer. Two things protect that interval. The grace covers a writer that is stopped between the two writes, as long as the record carries the time it was written at. It is never shorter than 65 seconds: twice the 30 seconds that a command goes on sealing in the group it opened with after a rewrap, and five more. The age is read from the record, so it is the clock of the machine that wrote the record against the clock of the machine that collects: `gc` needs the clocks of the machines that write to agree to within the grace. Give `--older-than` more than the skew of the clocks you have.

Some writers copy a record with the times it already had: `pravo protect rewrap --all`, `pravo protect init` and `pravo state push` and `pull` write every record, old times included, and the pointers after. The grace says nothing of such a record, so a collection is in two phases. A collection that finds an old record unnamed marks it in the object `gc/marks.json` and deletes nothing. A collection at least 15 minutes later deletes the records that are still unnamed and that it marked, and drops the marks of the ones that a pointer names or that are gone. A writer that finishes inside 15 minutes is therefore never caught between its record and its pointer. The 15 minutes are judged by clocks as well: a mark holds the time of the machine that wrote it, and the collection that decides compares it with its own clock. A machine whose clock is ahead of the one that marked shortens the interval by the difference, and a collection from machines whose clocks differ by a large part of 15 minutes can delete a record whose writer is still between its record and its pointer. Run `gc` from one machine, or keep the clocks of the machines that run it, as well as of those that write, close to each other. The first collection of a state prints `Marked 3 records that no pointer names. A collection at least 15m later deletes the ones that are still unnamed.` and a collection inside the 15 minutes keeps what it marked. A copy of the state does not carry the marks, and `--dry-run` reads them and writes nothing, so it does not create them either.

The command lists the pointers again right before it deletes, and deletes a record only if it still holds the bytes it judged, so a record that was named or replaced meanwhile stays. This part needs no key, and it does not need the state at rest. It can run while commands run, and two collections that run at the same time lose no mark that matters.

**Groups.** `rewrap` leaves the old groups in `meta.json`, with a lock for each reader that is left, and a command opens one lock for each group. The data key of a reader who was removed still opens whatever is sealed in the group they were in. `gc` drops a group, other than the current one, when nothing is sealed in it: no record that a pointer names, no record that the first part spared, not `outputs.json`, no object of a stored plan, and no sealed text of the error of a transaction that failed. Run `pravo protect rewrap --all` first, so that everything is under the current group, and give a command that opened the state before the rewrap time to finish: while the last change of the readers is younger than 65 seconds, `gc` keeps every group and says so, because such a command still seals in the old group and a record sealed in a group that `meta.json` does not list any more stops the state from opening. A stored plan that an apply uses is left by `rewrap --all`, and keeps its group until the apply is done and `rewrap --all` runs again. A group with something in it is kept, and the command says what. A dropped group is gone from `meta.json` and the history gets the entry `gc`. A removed reader then holds a key that opens nothing that is left. A copy of the bucket made before still opens with it, and so does a version of `meta.json` that a bucket which keeps old versions of its objects holds: the deleted records stay there too until the lifecycle rules of the bucket remove them.

This part needs an identity that is a reader, the readers file (a command that writes a sealed state compares it with the state, as above) and the state at rest: no transaction is running and no lock is held. With no identity, or one that is not a reader, `gc` collects the records, says `Groups were not looked at` and exits with 0. With a command running it collects the records and exits with 4. With a readers file that is missing, invalid or different from the state it collects the records and exits with 5 or 6, as any command that writes a sealed state does. `--dry-run` writes nothing, so it warns of the same readers file and exits with 0; a lock that is held still gives 4.

| Line | Meaning |
|---|---|
| `Deleted 412 records older than 24h (38 MB)` | The records that were deleted. With `--dry-run`, `Would delete`. |
| `Marked 3 records that no pointer names. A collection at least 15m later deletes the ones that are still unnamed.` | Old records that no pointer names and that no earlier collection had seen. They are marked and kept. With `--dry-run`, `Would mark`. |
| `Kept 3 records that no pointer names: marked less than 15m ago.` | Records that an earlier collection marked less than 15 minutes ago. |
| `Kept 3 records that no pointer names: ...` | Records that no pointer names and that stay, because they are younger than the grace or show no time. |
| `Left alone 1 object in rec/ that does not match its name` | An object that pravo did not write. `pravo protect status` is the command that checks the records against their names. |
| `Dropped group g1: nothing refers to it.` | A group removed from `meta.json`. With `--dry-run`, `Would drop`. |
| `Kept group g1: 2 records and the outputs are sealed in it.` | A group that something is sealed in. The next line says what to run. |
| `Groups were not looked at: ...` | Why the second part did not run. |

The example waits for those 65 seconds, because the state was sealed just before.

```sh run
sleep 66
pravo protect gc
```

```text out
No record is garbage: a pointer names every record.
No group needs to be dropped: the state has only its current group.
```

With `--json`, `gc` prints one document: `format`, `dry_run`, `older_than_seconds`, `settle_seconds` (15 minutes, 900), `records` (`listed`, `named`, `unnamed`, `young`, `unknown_age`, `marked`, `settling`, `deleted`, `bytes` and `foreign`) and, for a sealed state, `groups` (`examined`, `dropped`, `kept` with `id`, `records`, `unnamed_records`, `outputs`, `plans` and `summaries`, and `skipped` with the reason when the groups were not looked at).

A stored plan that an apply holds a claim on is left as it is. `--all` counts these plans and prints `2 stored plans in use were not sealed again; run pravo protect rewrap --all when they are done.` With `--json` the count is `plans_skipped`.

## See also

- [Seal the state to a list of readers](../how-to/seal-the-state.md) walks through it.
- [What sealing protects](../concepts/sealed-state.md) lists what the seal covers and what it leaves open.
- [State format](state-format.md) describes the files of each format.
- [Exit codes](exit-codes.md) lists code 7.
- [Global flags, environment and exit codes](global-flags.md) lists the variables.
