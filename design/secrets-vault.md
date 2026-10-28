# Secrets vault on sealed state

Status: built in the draft pull request that carries this note. The sections down to "Tests" are the design as it was agreed before the code; "As built" at the end says where the code chose, added or differs. Requested by Marcin after "How far would sealed Pravo be from a viable secret vault?".

## What it is

A secret is a named, versioned item that lives in the sealed state of a root. It is sealed to the same readers as the records, written by writers, removed by admins, and used by a run as an ephemeral value, like `pravo.#SecretEnv`, so it reaches provider configuration and write-only attributes and never the state or a plan.

The model is SOPS, not Vault. There is no server, no lease, no dynamic secret and no per-secret access list. Everyone who can read the state can read every secret of that state. A different audience gets a different root and a different bucket prefix.

What Pravo adds to SOPS is what it already has: signed transactions (who set which version, when), roles (a reader cannot set a secret, only an admin removes one), a pinned roles chain, a viewer, and a run that consumes the value.

## Storage

One immutable object per version, flat, like policies:

    secret/<encoded name>@<8-digit seq>.json

`seq` counts from 1 without a gap. A new version is created with `PutIfAbsent`. The first object raises `meta.json` to format 5, which an older pravo refuses to open. A version is one of three shapes:

- A value: `{"v":1,"at","txn","commit","sealed":{alg,group,ct}}`. The sealed body is `{value, nonce, note}`. It is sealed in the current group with the context (lineage, kind `secret`, name, seq), so it opens only where it is.
- A removal (tombstone), written by `secret rm`: `{"v":1,"at","txn","commit","removed":true}`. The secret has no value after it, and a later `set` continues the numbering.
- A purge stub: the value object with its `sealed` part dropped and `"purged":true,"purged_by":TXN` added. `secret purge` replaces old value objects by stubs in place. The numbering never has a gap, so a deleted version is always noticed.

Names are `[A-Za-z0-9._-]` words joined by `/`, at most 200 bytes. Two names that differ only in case are refused, because they would share a file on a case-insensitive disk. A name, the version numbers and the times are readable by anyone who can list the bucket. The value and the note are not. The limit of a value is 1 MiB; it may be binary.

## Audit: the transaction signs the version

A secret command is a transaction of kind `secret`. The change it records is `secret:NAME` with the action (`set`, `remove`, `purge`), the previous and the new version, and `Change.Record` set to the *commit* of the version. The finish signature covers it, as it covers a record key. So "who set version 4 of db_password, from which machine, for which reason" is `pravo state log` plus a signature check, and a reader who holds the data key cannot make a version a writer's.

The commit is `HMAC-SHA256(nonce, value ‖ note ‖ name ‖ seq ‖ lineage)`. The nonce is random, 32 bytes, and sits inside the sealed body. A commit that was a plain hash would let anyone who reads the transactions test guesses against a weak secret. With the nonce they learn nothing, and a reader who opens the body recomputes it and compares. Sealing and a rewrap rewrite the ciphertext and not the commit, so signatures stay valid.

Reading a version checks, in this order: the object is what its name says; the transaction it names exists, finished, with a verifying signature; the transaction has a `secret:NAME` change for that version with the same commit; the signing key may do it (writer for a value, admin for a removal or a purge); and, when the body is opened, the commit matches. When the roles are on, a version that fails is refused with exit 7. Without roles every key is an admin and the check reports and does not refuse, as for records. A key that was demoted or dropped stays valid for the versions an admin vouched for: `LiveSigners` lists the transactions of all version objects, so `pravo protect roles` vouches for them with the transactions of records.

The order of a write is: sign the start, put the object, record the change, sign the finish. A crash between the put and the finish leaves a version whose transaction did not finish. It reads as `unconfirmed`, and the fix is to set it again. A signature that cannot be made stops the write before anything is put.

What the signatures do not give is freshness. A bucket writer can put an old object back. The answer is the one policies and roles have: a version-locked bucket (object lock on `secret/`). The check finds the other attacks, a missing newest version among them, because a verified transaction names a version that is not there.

## Roles

| Command | Needs |
|---|---|
| `get`, `exec`, a run that reads a secret | reader |
| `list`, and the list of `history` | no key (the notes of `history` are sealed, so they need a reader) |
| `set` | writer |
| `rm`, `purge` | admin |

A repository without roles lets every key do all of it. `protect status` gets a Secrets line.

## In a run

Two new kinds in the `pravo` package:

    db: pravo.#Secret & {name: "db_password"}           // db.value, ephemeral
    rev: pravo.#SecretVersion & {name: "db_password"}   // rev.version, an ordinary int

`#Secret` is registered as an ephemeral builtin, like `#SecretEnv`, so the existing rules apply unchanged: provider configuration, write-only attributes and other ephemeral resources only; never an output, an ordinary attribute, a state record, a saved or stored plan, or the config hash. It reads the latest version at each plan and apply, or `version: N` when pinned. `#SecretVersion` exists because ephemeral is a property of a whole node: the number is not secret, and wiring `rev.version` into `password_wo_version` is how a rotation reaches a write-only attribute (the provider applies the value when the version changes). A missing, removed or purged secret fails the run before any provider starts, with the command that fixes it. A run on a state that is not sealed, or one the process holds no key for, fails with the existing exit codes (5 and 7).

`validate`, `fmt` and `eval` do not open the state; `value` is unknown there, as for `#SecretEnv`.

## CLI

    pravo secret set NAME [--file PATH | --from-env VAR] [--note TEXT]
    pravo secret get NAME [--version N]
    pravo secret list
    pravo secret history NAME
    pravo secret rm NAME
    pravo secret purge NAME [--keep N]
    pravo secret exec [--env VAR=NAME]... -- COMMAND [ARGS]

The value is never a flag or an argument, so shell history and `ps` do not see it. `set` reads standard input, a file or an environment variable, or asks on the terminal without echo. `get` prints the bare value. `--json` prints a document; a value that is not UTF-8 is `value_base64`. `exec` runs a command with the named secrets in its environment and writes nothing to disk. `purge` keeps the newest N values (default 1, and 0 only for a removed secret) and turns the others into stubs. `rm` and `purge` ask first unless `--yes`.

The MCP server has no tool that returns a value.

## Sealed-state interactions

- `rewrap --all` seals the body of every value version under the current group, in place; `gc` counts them so that a group they are sealed in is not dropped (a `Secrets` count in `GCGroup`).
- `protect unseal` is refused while any version holds a value, with the commands that clear it. An unsealed state would keep them in the open.
- `Copy` (state push, pull and migration) carries `secret/`. A version that exists on both sides is the same version when its transaction and commit agree, and the source wins, because a rewrap or a purge rewrites the bytes of a version.
- Format 5. The first `secret set` raises it. A state with no secret stays in its format.
- The leaver gap is the one of the records: a member who left can open what they could open. After `rewrap --all`, `gc` and cutting their bucket credentials they can open nothing new, but they have seen the values. They must be rotated. `secret history` shows what each member could have read.

## Viewer

A Secrets section next to Policies, read-only, and it never opens a value: everything it shows is in the clear part of the objects and in the transactions, so the server needs no key and cannot leak one. It shows the name, the version, when and by whom it last changed, and which nodes of the specification read it. Only problems get colour: a version that no signed change accounts for, one signed by a key that may not write, one that is removed or missing while a node reads it, a transaction that did not finish. When there are none, one line says all clear. History, notes, purge and every other rare thing stay in the CLI. The tab shows a number only when something needs action. Secret nodes in the graph and the inspector say that the value is read from the vault and is never recorded, and show no value.

## Not in this change

Leases, dynamic secrets, per-secret readers, a read audit (the bucket's own access log is the record of who fetched the ciphertext), expiry and rotation reminders (no clock-based enforcement), an HTTP API, and automatic rotation of the data key of a secret when a member leaves.

## Tests

The state package: set, get, rotate, remove, re-create, purge, gap and tamper detection (a swapped body, a version with another commit, a missing newest version, a stub with no purge transaction, an unsigned version), roles (reader and writer refused, admin allowed, demoted writer vouched), rewrap and gc with a secret in the old group, unseal refused, copy. The engine: a value reaches provider configuration and a write-only attribute and appears in no state record, plan file, stored plan, log line or transaction; rotation changes `#SecretVersion`; an ordinary attribute and an output refuse it. The CLI: set from stdin, file and environment, get, list, history, rm, purge, exec, exit codes, JSON, roles. The viewer: model, API contract, fixtures, a check that the section never carries a value, screenshots.

## As built

The implementation follows the design above. These are the places where it chose, added or differs.

**Commands.**

- `list` opens no key, so it runs for a person who is not a reader. `history` lists without a key and shows the notes to a reader.
- `set` takes the value from `--from-env`, from `--file` (`-` is standard input), from standard input when it is not a terminal, or from a prompt that asks twice. It drops one trailing line break when the value has exactly one, so `echo` and the prompt give the same secret, and a key of several lines is kept as it is. A value is not empty, at most 1 MiB, and a note is at most 1000 bytes of text.
- `get` prints the bare bytes. On a terminal it adds a line break to a value that has none, and it refuses a value that is not text.
- `exec --env VAR=NAME[@N]` can take a version, refuses a value with a null byte, and exits with the code of the command.
- `rm` and `purge` ask unless `--yes`. A live secret keeps at least its latest value (`--keep` is 1 or more); a removed one may keep none.
- The command line that `state log` shows is `pravo secret set NAME`. It holds the name and never the value, the note or where the value came from.
- Exit codes: 1 for a secret that is not there, was removed or purged, a name or value that is not valid; 3 when another writer put the version first (run it again); 5 for a state that is not sealed or does not exist, an identity that is not a reader, or a role that is too low; 7 for a version that no signed transaction of a key allowed to write accounts for.

**Trust.** Every version is judged against its transaction and gets one of five verdicts: `ok`; `vouched` (a key that was demoted or dropped, with an admin's vouch); `unchecked` (the key is not in the readers file, while the roles are not locked in the state); `unconfirmed` (the transaction did not finish, so nothing says what the writer meant to put there); `problem`. With roles on, `problem` and `unconfirmed` are refused with exit 7. Without roles the command goes on and warns on standard error. `list` and `history` print first and then exit 7.

**In a run.** The first node of a run that names a secret settles the version for the whole run (`secretPins`), so a value and the number that rotates it belong together even when someone sets the secret while the run goes. A saved plan and the apply of it are two runs. When a new version was set in between and the specification reads its number, the provider plans differently from the file and the apply stops with exit 3, the way any stale plan does. The value never reaches an error message: a CUE conflict at the value says that the value is not shown.

**Format.** Only the first secret writes `meta.json`. A later version needs a credential to put `secret/` objects and nothing for `meta.json`, so a writer on a bucket that keeps `meta.json` for admins can still set it. `protect status` has a `Secrets` line and exits 7 when a version fails.

**Viewer.** The section is `Secrets`, key `e`, link `view=secrets`, after Plans and before State. `GET /api/secrets` is built from the clear parts of the versions and their transactions, bounded at 2000 versions, 20 items under a problem and 40 nodes under a secret; it needs no key and no response holds a value. The page says one of three things: the secrets with nothing to flag (`all clear`), a flagged block for each problem with the command that mends it (worst first: the state is not sealed, the secrets cannot be read as trusted, the roles fail their check, versions that no signed record accounts for, secrets that nodes read and cannot be read, versions signed by a key the access list does not list, signers not checked), or the empty state with the command that sets the first secret. The tab carries a number only for the problems, and none at 0. A node that reads a secret is in the graph, in the first column with the environment variables, and its State tab names the secret and the version and shows no value. A `secret` filter selects them.

**Not built, on purpose.** A secret problem is on the Secrets page and in `protect status`. It is not repeated in the State page's `Needs attention`, to keep one place that says what is wrong with each thing.

## Limits worth knowing

1. A bad old version stays flagged until an admin vouches for it or the secret is set again. A run reads the latest version, so it keeps working, while `protect status` and the viewer count every version.
2. A key that is demoted or dropped keeps the versions it wrote only if an admin vouched for them, which `pravo protect roles` does for the live transactions of records and, since `LiveSigners` lists them, of secret versions. A version that is signed by a key that nobody listed, or written between two vouches, is refused with exit 7 until an admin vouches for it, as a record would be.
3. Nothing gives freshness. Someone who can write to the bucket can put an old object back. An object lock on `secret/` stops that.
4. The leaver gap is that of the records. A member who left has read what they could read. Rotate the secrets they could open; `rewrap --all` and `gc` stop new reads, they do not take back old ones.
5. There is no read audit. The bucket's access log is the record of who fetched the ciphertext.
