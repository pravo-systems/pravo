<!-- Generated from docs/reference/secret.md by tools/skillgen. Do not edit. -->

# pravo secret

Set, read, list, remove and forget the secrets of a sealed state, and run a command with them in its environment. Subcommands, flags, roles and exit codes.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo secret set NAME`, `get NAME`, `list`, `history NAME`, `rm NAME`, `purge NAME` and `exec --env VAR=NAME... -- COMMAND` |
| Changes infrastructure | No. |
| Starts providers | No. |
| Writes | `set`, `rm` and `purge`: an object below `secret/` and a transaction of the kind `secret`. The first secret of a state raises `meta.json` to format 5. Nothing else. |
| Requires | A sealed state ([`pravo protect init --encrypt`](protect.md#protect-init)). The commands that write also need a readers file, as every command that writes a sealed state does. |
| Taught in | [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md). |

A secret is a named value that the sealed state keeps for its readers, as a series of versions: every `set` is a new one. A run reads it with `pravo.#Secret` ([the specification](spec-env.md#secrets-of-the-sealed-state)) and hands it to a provider. The value is never written to the state, to a plan or to a log.

Everyone who can read the state can read every secret in it. There is no list of readers for one secret: a secret for other readers belongs in another state. `get` and `exec` are the two commands that hand out a value. The [MCP server](mcp.md) has no tool that returns one.

## Subcommands

| Subcommand | What it does | Needs |
|---|---|---|
| `set NAME` | Writes a new version with the value from standard input, a file, an environment variable or a prompt, and creates the secret when the state has none of that name. | A writer. |
| `get NAME` | Prints the latest value, or the value of the version `--version` names. | A reader. |
| `list` | Lists the secrets with their latest version, when it was set and by whom. It shows no value. | No key. |
| `history NAME` | Lists every version, newest first, with when, by whom and what became of it. The notes are sealed with the values, so they are shown to a reader. | No key for the list, a reader for the notes. |
| `rm NAME` | Ends the secret with a removal, as the next version. Runs that read it fail until it is set again. | An admin. |
| `purge NAME` | Forgets the old values of a secret and keeps the newest `--keep` of them. | An admin. |
| `exec --env VAR=NAME... -- COMMAND` | Runs the command with the secrets in its environment. | A reader. |

Where the readers file gives [roles](protect.md#roles), the column says which role the identity needs, and a lower one is refused with exit code 5. Without roles every key may do everything. A role that is higher also does what the lower ones do: an admin sets and reads.

The flags that every command takes are in [Global flags](global-flags.md); `--actor` and `--reason` are written into the transaction of `set`, `rm` and `purge`. Every `secret` command takes:

| Flag | Default | Meaning |
|---|---|---|
| `--access-dir DIR` | `.pravo/access` | The directory that holds `readers.txt`, as for [`pravo protect`](protect.md). |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. The tags select the state, so the commands that open it take them. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE. |

## Names

A name is words of letters, digits, dots, dashes and underscores, joined by slashes: `db_password`, `prod/db_password`, `prod/eu-west-1/api.token`. A word starts with a letter or a digit, and a name is at most 200 bytes. Two names that differ only in case are refused when the state holds one of them, because they would share a file on a disk that does not tell upper case from lower.

## pravo secret set

`pravo secret set NAME [--file PATH | --from-env VAR] [--note TEXT]` writes version 1 of a secret, or the next version of one that exists.

The value is never an argument, so that the shell history and the list of processes do not hold it. It comes from one of these, in this order:

1. `--from-env VAR`: the value of the environment variable. A variable that is not set, or is empty, is an error.
2. `--file PATH`: the contents of the file. `-` is standard input.
3. Standard input, when it is not a terminal.
4. A prompt on the terminal that does not show what you type and asks twice.

`--file` and `--from-env` together are an error. A value read from standard input or a file loses one line break at its end when it has no other, so that `echo` and the terminal give the same secret. A value of several lines, such as a key, is kept as it is. A value may be binary, is at most 1 MiB, and is not empty: to take a secret away, use `rm`.

| Flag | Default | Meaning |
|---|---|---|
| `--file PATH` | none | Read the value from this file. `-` is standard input. |
| `--from-env VAR` | none | Read the value from this environment variable. |
| `--note TEXT` | none | A line of text, up to 1000 bytes, kept with the version and sealed with it. `history` shows it to the readers. |

The command prints `Set NAME, version N.` With `--json` it prints the version: the name, its number, the kind, the time, the transaction, the commit and the trust.

The change is a transaction that [`pravo state log`](state.md) shows, with the command line `pravo secret set NAME`. The command line holds the name and never the value, the note or the place the value came from. A version that another writer put at the same number first is not overwritten: the command stops with exit code 3 and you run it again.

## pravo secret get

`pravo secret get NAME [--version N]` prints the value. It prints the bytes as they are, with no line break added, so that a script gets exactly what was set. On a terminal it adds a line break to a value that has none, and it refuses a value that is not text: redirect it to a file, or use `--json`.

| Flag | Default | Meaning |
|---|---|---|
| `--version N` | the latest | The version to print. A version that was purged has no value, and the command says so. |

With `--json` the document has the fields of a version and `value`, or `value_base64` when the value is not valid UTF-8, with `note` when the version has one.

Where the readers file gives roles, a version that no signed transaction of a writer accounts for is refused with exit code 7 ([Roles](#roles)). Without roles it is printed, with a warning on standard error.

## pravo secret list

`pravo secret list` prints a row for each secret: its name, the number of its latest version, how long ago that was set and who set it. A secret whose latest version is a removal says `removed`. It shows no value and opens no key, so it runs for a person who is not a reader.

A version that no transaction of a key allowed to write accounts for is named under the table, and the command exits 7 after printing. A version whose key is not in the readers file is marked `key not in the readers file`, and one whose transaction did not finish is marked `not finished`. Neither is an error.

With `--json` it prints an array of `{"name", "versions", "head"}`, where `head` is the latest version.

## pravo secret history

`pravo secret history NAME` prints every version of the secret, newest first: the number, the time in the local zone, the name that the readers file gives the key that signed it, and its kind. The kind is `value`, `removed` or `purged`. The note of a version is printed under it, to an identity that is a reader. A version that was purged shows no value; the transaction that purged it is in `state log`.

With `--json` it prints an array of versions, newest first, each with `note` when there is one. Like `list`, it ends with exit code 7 when a version is untrusted.

## pravo secret rm

`pravo secret rm NAME [--yes]` writes a removal as the next version. The values stay in the state until `purge` forgets them, so the history shows what was there. A run that reads the secret stops before it changes anything, and setting the name again continues the numbering. It asks first; `--yes` answers. A secret that was removed already is an error.

## pravo secret purge

`pravo secret purge NAME [--keep N] [--yes]` forgets the values of the old versions and keeps the newest `--keep` of them (1 unless you say). A version that is purged stays in the history, without its value, and the transaction that purged it is in `state log`. A secret that is live keeps at least its latest value; one that was removed may keep none (`--keep 0`). It asks first; `--yes` answers.

| Flag | Default | Meaning |
|---|---|---|
| `--keep N` | `1` | How many of the newest values to keep. |
| `--yes` | off | Do not ask before the values are forgotten. |

The values are gone from the state. A bucket that keeps earlier versions of its objects keeps these too, and everyone who could read the secret has read it: a value that leaked needs a new one, not a purge.

## pravo secret exec

`pravo secret exec --env VAR=NAME... -- COMMAND [ARGUMENTS]` reads the secrets, puts each in the environment variable that `--env` names, and runs the command. `VAR=NAME@3` takes version 3. The values are not written to a file, only the command sees them, and standard input, output and error are those of `pravo`. Everything before `--` is read by pravo, and everything after it belongs to the command.

If a secret cannot be read, the command does not run. The exit code is that of the command, and 1 when a signal ended it. A secret that holds a null byte cannot be put in an environment variable and is refused.

## Roles

| Role | May |
|---|---|
| `reader` | `get`, `exec`, `list` and `history`, and a run that reads a secret. |
| `writer` | `set`. |
| `admin` | `rm` and `purge`. |

A repository whose readers file has no roles lets every key do all of it. With roles, the transaction of each command is signed by the key that ran it, and every command that reads a version checks, in this order:

1. The object is the one its name says: its number, its kind and its transaction.
2. The transaction exists, has finished, and its signature verifies.
3. The transaction has a change `secret:NAME` for that version, with the same commit.
4. The key may do it: a writer for a value, an admin for a removal or a purge. A key that was demoted or dropped counts when an admin vouched for the transaction with [`pravo protect roles`](protect.md#roles).
5. When the value is opened, its commit matches. The commit is a keyed hash of the value, the note, the name, the number and the lineage of the state, so a reader who holds the data key cannot make a version a writer's.

A version that fails is refused with exit code 7. `pravo protect status` counts the versions that pass, on its `Secrets` line, and exits 7 when one does not. A version whose transaction is gone is untrusted: transactions are not collected, so do not delete `txn/`.

What the signatures do not give is freshness. Someone who can write to the bucket can put an old object back, and the check finds that a newer version is missing, since a verified transaction names it, and not that an old object is old. An object lock on `secret/` stops the deletion and the replacement of versions. [Roles](protect.md#roles) lists the credentials that a writer and an admin need on the bucket.

## The objects

A version is one immutable object, named `secret/<name>@<number>.json`. The name is written as a node's address is ([Addresses in file names](state-format.md#addresses-in-file-names)), so `prod/db_password` is `secret/prod%002Fdb_password@00000001.json`. The number has eight digits and counts from 1 without a gap. [Format 5](state-format.md#format-5-secrets) lists the fields.

The name, the number and the time of a version are readable by anyone who can list the bucket. The value and the note are sealed to the readers of the state, in the group that was current when the version was written. A value is sealed with the name and the number as its context, so it opens only where it is. `pravo protect rewrap --all` seals every value again under the current group, `pravo protect gc` keeps a group that a secret is sealed in, and `pravo state pull` and `push` carry the versions with the records.

`pravo protect unseal` refuses a state that holds the value of a secret (exit code 5), because the values would be written in the open. Remove each secret with `rm` and forget its values with `purge NAME --keep 0` first.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | The command did what it was asked. For `exec`, the command exited 0. |
| 1 | An error: the secret is not there, was removed, or its version was purged; a name that is not valid; a value that is empty or too large; a variable that is not set; an answer other than a yes to a question. For `exec`, the exit code of the command. |
| 3 | `set`, `rm` or `purge` found that another writer wrote a version of the secret first. Run it again. |
| 5 | The state is not sealed, or does not exist yet, or the identity is not a reader of it; or the identity has a lower role than the command needs; or the readers file is missing for a command that writes. |
| 7 | A version is not accounted for by a signed transaction of a key allowed to write it. `list` and `history` print first, and then exit 7. |

The [global flags](global-flags.md) apply. In `--json` mode an error also prints `{"code":N,"kind":"error","message":"..."}`.

## Examples

Work in an empty directory. The state is a local directory here, which `pravo protect` seals only with `--allow-local`; a team keeps it in [a bucket](../how-to/store-secrets-in-the-state.md).

File `main.cue`:

```cue
package vault
```

```sh
mkdir -p people .pravo/access
pravo id new --name ada > /dev/null
pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
pravo init > /dev/null
pravo protect init --encrypt --allow-local --yes > /dev/null
```

Set a secret from standard input, from a file and from an environment variable:

```sh
printf %s 'correct-horse-battery' | pravo secret set prod/db_password --note "first password"
printf 'line one\nline two\n' > key.txt
pravo secret set prod/tls_key --file key.txt
API_TOKEN=an-example-token pravo secret set prod/api_token --from-env API_TOKEN
```

Output:

```text
Set prod/db_password, version 1.
Set prod/tls_key, version 1.
Set prod/api_token, version 1.
```

List them. The command shows no value:

```sh
pravo secret list
```

Output:

```text
NAME              VERSION  SET        BY
prod/api_token          1  <age> ago  ada
prod/db_password        1  <age> ago  ada
prod/tls_key            1  <age> ago  ada
```

Read one. `get` prints the bare value; `--json` prints the version with it:

```sh
pravo secret get prod/db_password
echo
pravo secret get prod/db_password --json
```

Output:

```text
correct-horse-battery
{
  "name": "prod/db_password",
  "version": 1,
  "kind": "value",
  "at": "2026-01-01T00:00:00Z",
  "txn": "<txn>",
  "commit": "<commit>",
  "by": "ada",
  "claimed": "ada",
  "key": "SHA256:<fingerprint>",
  "trust": "ok",
  "value": "correct-horse-battery",
  "note": "first password"
}
```

Set the same name again. `history` shows both versions, and the note of the first:

```sh
printf %s 'second-password' | pravo secret set prod/db_password
pravo secret history prod/db_password
```

Output:

```text
Set prod/db_password, version 2.
prod/db_password
  v2    <time>  ada          value
  v1    <time>  ada          value
        first password
```

Run a command with a secret in its environment:

```sh
pravo secret exec --env PGPASSWORD=prod/db_password@1 -- sh -c 'echo "version 1 has ${#PGPASSWORD} characters"'
```

Output:

```text
version 1 has 21 characters
```

A secret that is not there is an error:

```sh
pravo secret get prod/missing
```

Output:

```text
Error: prod/missing: there is no such secret; pravo secret list shows the secrets of the state
```

Remove one and forget the old values of another:

```sh
pravo secret rm prod/api_token --yes
pravo secret purge prod/db_password --yes
pravo secret history prod/db_password --json | grep -E '"(version|kind)"'
```

Output:

```text
Removed prod/api_token, version 2. Its values stay in the state until pravo secret purge prod/api_token --keep 0.
Forgot 1 value of prod/db_password.
    "version": 2,
    "kind": "value",
    "version": 1,
    "kind": "purged",
```

A state that holds the value of a secret cannot be unsealed:

```sh
pravo protect unseal --yes
```

Output:

```text
Error: the state holds secret values: 3 values of secrets are sealed in it, and unsealing would write them in the open. Remove each secret with pravo secret rm NAME and forget its values with pravo secret purge NAME --keep 0 first (pravo secret list shows them)
```
