<!-- Generated from docs/reference/protect.md by tools/skillgen. Do not edit. -->

# pravo id and pravo protect

Make an identity, seal the state to a list of readers, check that it holds and change the readers. The commands, the readers file, the identity files and the exit codes.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo id new`, `pravo id show`, `pravo protect check`, `pravo protect init --encrypt`, `pravo protect status`, `pravo protect rewrap` |
| Changes infrastructure | No. |
| Reads | The state, and `.pravo/access/readers.txt` in the repository. `id` reads and writes only identity files. |
| Writes | `id new` writes an identity file. `protect init` and `protect rewrap` write the state. `check` and `status` write nothing. |
| Prints | One line for each check, then a verdict. The line to add to the readers file. |
| Taught in | [Seal the state to a list of readers](../how-to/seal-the-state.md). |

Protection is off by default. A state with protection off is unchanged: format 1, with the layout that every version of pravo reads. These commands turn protection on for one state, show where it stands and keep it in line with the list of readers. [What sealing protects](../concepts/sealed-state.md) says what the seal covers and what it leaves open.

A state has three formats.

| Format | Name | What it is |
|---|---|---|
| 1 | off | One file for each resource, `resources/ADDRESS.json`. The default, and the only format earlier versions of pravo read. |
| 2 | immutable records | Each record is an object named by the SHA-256 of its bytes, `rec/SHA256`, and never overwritten. `resources/ADDRESS.json` is a small pointer to it, replaced only by compare-and-swap. Nothing is encrypted. |
| 3 | sealed | Format 2, with the attributes and private data of each record, the outputs, the stored plans and the values in command lines encrypted to a list of readers. The address, type, name, dependencies and revision stay readable. |

`pravo protect init --encrypt` takes a state from format 1 to format 3. A pravo that does not know format 3 refuses the state with `state at LOCATION was written by a newer pravo (format 3)`.

### Subcommands

| Subcommand | What it does | Reads the state | Writes the state | `--json` |
|---|---|---|---|---|
| `id new` | Makes an identity and prints the line for the readers file. | No. | No. | yes |
| `id show` | Prints the keys and the line of an identity again. | No. | No. | yes |
| `protect check` | Says whether the store can be sealed. | yes | No. | yes |
| `protect init --encrypt` | Seals the state to the readers file. | yes | yes | no |
| `protect status` | Says whether the state is sealed, to whom, and whether it verifies. | yes | No. | yes |
| `protect rewrap` | Brings the keys of the state in line with the readers file. | yes | yes | yes |

### Flags

| Flag | Command | Meaning |
|---|---|---|
| `--name NAME` | `id new`, `id show` | The name of the identity. For `new`, the default is the name of the operating system user (`$USER`, or `$USERNAME` on Windows), also with `--ci`, so an identity for a job made without `--name` is named like the person who ran the command. With neither variable set, `new` stops and asks for `--name`. For `show`, the default is the only identity, or the one named like the user. |
| `--ssh FILE` | `id new` | Use the `ssh-ed25519` public key `FILE.pub`, or `FILE` if that is a public key, as the signing key. The private key is never read. |
| `--ci` | `id new` | Make an identity for a job. It has no passphrase and is written to `--out`. |
| `--out FILE` | `id new` | With `--ci`: the file to write the identity to. |
| `--access-dir DIR` | `protect` | The directory of the readers file, in place of `.pravo/access`. `$PRAVO_ACCESS_DIR` sets the same. A relative directory is relative to the root. |
| `--identity NAME` | `protect` | The identity to open the state with, a file of the identity directory. |
| `--allow-local` | `protect check`, `protect init` | Accept a state in a local directory. |
| `--encrypt` | `protect init` | Required. Seal the attributes, outputs and stored plans to the readers. |
| `--yes` | `protect init` | Do not ask for confirmation. |
| `--all` | `protect rewrap` | Seal every record, the outputs, the stored plans and the sealed text of errors again under the current key. |

### Exit codes

| Code | When |
|---|---|
| 0 | The command did what it was asked. `check` found the store ready. |
| 1 | Any other failure: a store that cannot be sealed, a local directory without `--allow-local`, an identity that exists, no terminal to ask on and no `--yes`, a state that is not sealed for `rewrap`. |
| 4 | A command is running or a lock is held. `protect init` and `protect check` do not run then. |
| 5 | You are not a reader of the state, your identity is not on the readers file, or the readers file and the state differ for a command that writes. For any other command that writes a sealed state, such as `state rm` or `apply`: the readers file is missing. |
| 6 | The readers file is invalid, for `protect check`, `init` and `rewrap` and for a command that writes a sealed state. For `protect check`, `init` and `rewrap`: the readers file is missing. |
| 7 | A record does not match its name. [Exit codes](exit-codes.md#7-untrusted) says what to do. |

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

A state that is not sealed in a repository that has a readers file is refused in the same way. A command that writes stops with exit code 5 and `the repository lists readers in .pravo/access/readers.txt, but the state is not sealed: its meta.json may have been changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file.` A command that only reads prints the text as a warning. A file that is found counts whether or not it can be read. `protect check`, `protect init`, `protect status` and the `id` commands are not stopped. A root in a repository whose readers file is meant for other roots names another access directory with `--access-dir` or `$PRAVO_ACCESS_DIR`, and is then not expected to be sealed.

## The identity

An identity is a name, a signing key and a reader key. The reader key opens the sealed state. The signing key is part of the line in the readers file and is not used while the state has no roles.

| Where | What |
|---|---|
| `$PRAVO_CONFIG_DIR/id/NAME`, or `~/.config/pravo/id/NAME` | The identity file made by `id new`: mode 0600, in a directory of mode 0700. The reader key is sealed under a passphrase. On Windows the directory is `%AppData%\pravo`. |
| `$PRAVO_IDENTITY_DIR` | The directory that holds identity files, in place of the `id` directory above. |
| `$PRAVO_IDENTITY` | The contents of an identity file, if the value starts with `{`, or the path of one. For a CI job. |

The identity directory is not `~/.pravo`, which is the provider cache and may be deleted.

pravo finds the identity in this order: `--identity NAME`, `$PRAVO_IDENTITY`, then the files of the identity directory. When there are several, it uses the one named like the user. It looks only when the state is sealed, so a state with protection off never asks for a passphrase.

A passphrase is asked for on the terminal without echo. Without a terminal it is read from `$PRAVO_PASSPHRASE` or `$PRAVO_IDENTITY_PASSPHRASE`, and a command with neither stops with `the identity ada is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE.` pravo removes `PRAVO_IDENTITY`, `PRAVO_PASSPHRASE` and `PRAVO_IDENTITY_PASSPHRASE` from its environment once it has read them, and provider processes are started without them. [`pravo mcp`](mcp.md) never asks on a terminal: the identity of a server comes from the environment it was started in.

## id new

`pravo id new` makes a reader key and a signing key, writes the identity to `NAME` in the identity directory, and prints the line for the readers file. It asks for a passphrase twice, at least 8 characters. It touches no state, and anyone can run it at any time.

```sh
mkdir -p people
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

The line is public and safe to paste into a chat or a pull request. Give it to whoever keeps the readers file.

An identity is never overwritten. `pravo id new --name ada` for a name that exists stops with `identity ada already exists at PATH` and exit code 1, before it asks for a passphrase.

`--ssh FILE` uses an `ssh-ed25519` public key as the signing key, so that there is no new signing key to manage. pravo reads `FILE.pub`, or `FILE` if that is itself a public key, and never reads a private key. Keys of other types are refused.

### For a CI job

A job has no person to type a passphrase. `--ci` makes an identity with no passphrase and writes it to the file named by `--out`, with mode 0600. It writes nothing to the identity directory. `--out` is required with `--ci` and refused without it. An existing file is not overwritten.

```sh
pravo id new --ci --name deploy-bot --out deploy-bot.identity
```

Output:

```text
Identity deploy-bot created for a job. It has no passphrase: protect the file.
  private  deploy-bot.identity   (store it as the CI secret PRAVO_IDENTITY, then delete this file)
  public   deploy-bot ssh-ed25519 <ssh-key> x25519:<key>
```

Without `--name`, `--ci` names the identity after the operating system user, which is the name of a person. A name is used once in the readers file, so give a job its own name.

Store the contents of the file as the secret `PRAVO_IDENTITY` of the job, then delete the file. Use one identity for each job, and never a person's identity in a job. Add the public line to the readers file and run `pravo protect rewrap`. The identity is the authority of the job in pravo and is not its cloud credential.

## id show

`pravo id show` prints the signing key, the reader key and the line for the readers file. It reads the public part of the file and does not ask for the passphrase.

```sh
pravo id show
```

Output:

```text
Identity ada (people/ada/id/ada)
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

With `--json`, `id new` and `id show` print one document with `name`, `path`, `signing` (`alg`, `fingerprint`, `source`), `reader` and `line`.

## protect check

`pravo protect check` reads the store and says whether it can be sealed. It writes nothing to the state. It tests the store harder than the probe that every command makes ([S3-compatible stores](s3-stores.md)): eight writers replace one object at once and exactly one must win, and a key that has just been written must be in the listing. It then lists what is running now.

File `main.cue`:

```cue
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

```sh
pravo protect check; echo "exit=$?"
```

Output:

```text
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

`pravo protect init --encrypt` seals the state. It needs the readers file and an identity that is on it, because sealing to a list that you are not on would lock you out: the command stops with exit code 5 otherwise.

It shows what it will do and asks `Proceed? [y/N]`. `--yes` answers it. Without a terminal and without `--yes` it refuses to ask. Nothing is written before the answer. The command does not run while a transaction is running or a lock is held. Run it while nobody plans or applies.

1. Every record is written once as the object `rec/SHA256`, and `resources/ADDRESS.json` becomes a pointer to it. A record is never overwritten again.
2. The attributes and the private data of each record are encrypted with one data key, which is locked for each reader.
3. `outputs.json`, the values of the stored plans, the values in the command lines of transactions and the text of the errors of transactions that failed are sealed the same way. The words of a command and the names of its flags stay readable, and so does a positional argument. The value of a flag that takes one is sealed, written `--flag value` or `--flag=value`, unless it is a flag with a public value such as `--limit`, `--ttl`, `--only`, `--out`, `--policy` or `--chdir`. A word after a flag that takes no value is a positional argument. A secret belongs in `-t`.
4. The state is set to format 3, and `meta.json` holds the locks.

A run that is interrupted leaves the state in the middle of sealing, and an earlier pravo cannot open it. A kill leaves it where it was. An interrupt (`Ctrl-C`) finishes the records that are being sealed, starts no other and ends with `interrupted after N records were sealed in this run`. Run the command again to finish: it skips the records it has done. `pravo protect status` says `migrating` until then, and commands that write the state refuse with `a seal is in progress; run pravo protect init --encrypt to finish`. Commit the readers file when the command ends.

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

```sh
pravo protect init --encrypt --yes
```

Output:

```text
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
```

A state that is already sealed prints `Already sealed (format 3). Use pravo protect rewrap to change the readers.` and writes nothing. There is no recovery recipient: if every reader loses their key, the state cannot be read.

## protect status

`pravo protect status` is the command to run when you are unsure. It opens the state with your identity, checks every record against its name and every pointer against its record, and prints one screen. It writes nothing.

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
Unsettled    none
```

| Line | Values |
|---|---|
| `Sealed` | `on`, `off`, `off · format 2 · immutable records`, or `migrating` with the number of records done. A state that is off in a repository that has a readers file ends in `· the repository expects a sealed state`: its `meta.json` may have been changed. |
| `Readers` | `matches the store`; `differs:` and who is in the file and not in the store, or the other way round; `file not found (looked in DIR)`; `file is invalid:` and the line and reason. |
| `You` | The identity and its reader key. `identity NAME is not a reader`. `not a reader of this state (no identity found)`. |
| `Unsettled` | `none`, or the number of interrupted transactions; `pravo state log` lists them. |

A record that does not match its name, a pointer that disagrees with its record, or a sealed value that does not open where it is, prints `Untrusted:` and the object, and the command exits 7.

## protect rewrap

`pravo protect rewrap` compares the readers file with the readers the state is sealed to, and changes the state to match. It needs an identity that is a reader, and the readers file. It does not need the readers to agree to anything, and it rewrites `meta.json` only. If another command changes `meta.json` while it runs, `rewrap` reads it again and repeats; if it keeps changing, `rewrap` stops and asks you to run it again. A state with nothing to change prints `The store already matches the readers file.`

**Adding a reader.** The new reader makes an identity and sends the line it prints. A reader adds the line to the file, commits it and runs `rewrap`. The data keys are locked for the new reader in every group, so that the new reader can open what is already there. No record is rewritten.

**Removing a reader.** A reader who is removed loses their lock in every group, and the state gets a new data key. Records are sealed under it as they are next written. Until then they stay under the old key. `--all` seals every record, the outputs, the stored plans and the sealed text of the errors of transactions again under the new key at once. A copy of the state that was made before the removal still opens with the removed reader's key, and the reader has read what they read. Replace the secrets they could read where they live.

`--all` needs the state at rest: run it while nobody plans or applies. It seals again and deletes the records that are sealed in another group and that no pointer names. It does not remove the old groups from `meta.json`, and the last record of a resource that was destroyed or removed with `state rm` stays in `rec/`, sealed under the group it was written in. [What sealing protects](../concepts/sealed-state.md) lists what stays in the bucket.

A stored plan that an apply holds a claim on is left as it is. `--all` counts these plans and prints `2 stored plans in use were not sealed again; run pravo protect rewrap --all when they are done.` With `--json` the count is `plans_skipped`.

## See also

- [Seal the state to a list of readers](../how-to/seal-the-state.md) walks through it.
- [What sealing protects](../concepts/sealed-state.md) lists what the seal covers and what it leaves open.
- [State format](state-format.md) describes the files of each format.
- [Exit codes](exit-codes.md) lists code 7.
- [Global flags, environment and exit codes](global-flags.md) lists the variables.
