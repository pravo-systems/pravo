<!-- Generated from docs/reference/id.md by tools/skillgen. Do not edit. -->

# pravo id

Make the identity that opens a sealed state and signs what you write, or show one. A name, a signing key and a reader key, under a passphrase, the ssh-agent or a file for a job.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo id new [--name NAME] [--ssh FILE] [--unlock agent] [--agent-key SELECTOR]`, `pravo id new --ci --name NAME --out FILE`, `pravo id show [--name NAME]` |
| Changes infrastructure | No. |
| Reads | Identity files, and with `--ssh` a public key. Never a private key, and never a state. |
| Writes | `id new` writes one identity file, in the identity directory or at `--out`. `id show` writes nothing. |
| Prints | The keys of the identity and the line for the readers file. |
| Taught in | [Seal the state to a list of readers](../how-to/seal-the-state.md). |

An identity is what a person or a job is to a sealed state: a name, a signing key and a reader key. The reader key opens the values that the state seals. The signing key signs the transactions and the stored plans that the identity writes, and it is the part of the identity's line in the readers file that others verify a signature against. [`pravo protect`](protect.md) seals a state to a list of identities, and [Signatures and roles](../concepts/signatures-and-roles.md) says what the keys prove. An identity is made once and never changes; a lost one is replaced by a new one under a new name.

### Subcommands

| Subcommand | What it does | `--json` |
|---|---|---|
| `id new` | Makes an identity and prints the line for the readers file. | yes |
| `id show` | Prints the keys and the line of an identity again. | yes |

### Flags

| Flag | Command | Meaning |
|---|---|---|
| `--name NAME` | `id new`, `id show` | The name of the identity. For `new`, the default is the name of the operating system user (`$USER`, or `$USERNAME` on Windows), except with `--ci`, which requires `--name`. With neither variable set, `new` stops and asks for `--name`. For `show`, the default is the only identity, or the one named like the user. |
| `--ssh FILE` | `id new` | Use the `ssh-ed25519` public key `FILE.pub`, or `FILE` if that is a public key, as the signing key. The private key is never read. |
| `--ci` | `id new` | Make an identity for a job. It has no passphrase and is written to `--out`. It requires `--name`. |
| `--out FILE` | `id new` | With `--ci`: the file to write the identity to. |
| `--unlock MODE` | `id new` | What opens the identity file: `passphrase`, the default, or `agent`. `agent` seals the file under a key that the ssh-agent derives and asks for no passphrase. `--ci` refuses it. |
| `--agent-key SELECTOR` | `id new` | With `--unlock agent`: the key of the ssh-agent, by its fingerprint (`SHA256:...`) or by part of its comment, which is the title of the item in 1Password. Required when the agent holds more than one `ssh-ed25519` key. |
| `--identity NAME` | `id show` | Another way to write `--name`. For every other command it is the global flag that chooses the identity to open a state with. |

### Exit codes

| Code | When |
|---|---|
| 0 | The identity was made, or shown. |
| 1 | An identity of that name exists; `--ci` without `--name` or without `--out`; no name and no operating system user to take one from; a passphrase under 8 characters, or two that differ; the ssh-agent holds no `ssh-ed25519` key, or several and no `--agent-key`; the agent signs differently each time. Nothing is written in any of these cases. |

## Where an identity is

| Where | What |
|---|---|
| `$PRAVO_CONFIG_DIR/id/NAME`, or `~/.config/pravo/id/NAME` | The identity file made by `id new`, when `$PRAVO_IDENTITY_DIR` is not set: mode 0600, in a directory of mode 0700. The reader key is sealed under a passphrase. On Windows the directory is `%AppData%\pravo`. |
| `$PRAVO_IDENTITY_DIR` | The directory that holds identity files, in place of the `id` directory above. `id new` writes there as well, and makes the directory with mode 0700 when it is missing. |
| `$PRAVO_IDENTITY` | The contents of an identity file, if the value starts with `{`, or the path of one. For a CI job. |

The identity directory is not `~/.pravo`, which is the provider cache and may be deleted.

A command that opens a sealed state finds the identity in this order: `--identity NAME`, `$PRAVO_IDENTITY`, then the files of the identity directory. When there are several files, it uses the one named like the user. If none is, it uses the one file whose reader key has a lock in the current group of the state, which `meta.json` lists. For `protect init`, which has no locks yet, the reader key is one of the readers file. The reader key of an identity file is public: choosing reads no passphrase, and only the chosen identity is asked for its passphrase. If no file or more than one matches, the command stops with `no identity found: ... holds several identities (ada dan) and none is named like you`, and names the files that match when there are several. Name one with `--identity`. The same choice is made for the state of another root that `pravo.#RemoteOutputs` reads, by the locks in that state's `meta.json`. pravo looks only when the state is sealed, so a state with protection off never asks for a passphrase.

A passphrase is asked for on the terminal without echo. Without a terminal it is read from `$PRAVO_PASSPHRASE` or `$PRAVO_IDENTITY_PASSPHRASE`, and a command with neither stops with `the identity ada is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE.` An identity that the ssh-agent unlocks asks the agent once for each command and neither asks for a passphrase nor reads the variables. pravo removes `PRAVO_IDENTITY`, `PRAVO_PASSPHRASE` and `PRAVO_IDENTITY_PASSPHRASE` from its environment once it has read them, and provider processes are started without them. [`pravo mcp`](mcp.md) never asks on a terminal: the identity of a server comes from the environment it was started in.

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

The line is public and safe to paste into a chat or a pull request. Give it to whoever keeps the readers file; [the readers file](protect.md#the-readers-file) says what the fields are and how a role is added to the line.

An identity is never overwritten. `pravo id new --name ada` for a name that exists stops with `identity ada already exists at PATH` and exit code 1, before it asks for a passphrase.

`--ssh FILE` uses an `ssh-ed25519` public key as the signing key, so that there is no new signing key to manage. pravo reads `FILE.pub`, or `FILE` if that is itself a public key, and never reads a private key. Keys of other types are refused. Such an identity signs through the ssh-agent, which pravo finds [as ssh does](#which-ssh-agent), and the key has to be loaded in it; an agent that asks for a touch asks for it at each signature, which is two for a run.

### Unlock with the ssh-agent

`--unlock agent` seals the identity file under a key that the ssh-agent derives, so that opening it takes whatever the agent takes to sign, and no passphrase is set. With the SSH agent of 1Password that is an approval, which Touch ID can give. The agent must hold an `ssh-ed25519` key. If it holds one, pravo uses it. If it holds several, `--agent-key` names one by its fingerprint or by part of its comment.

```sh
pravo id new --name ada-mac --unlock agent --agent-key "pravo key"
```

The command prints the same lines as one with a passphrase, and the reader line says `sealed under ssh-agent key SHA256:<fingerprint>`. The line for the readers file is the same too: the readers file and `rewrap` do not know how an identity file is opened.

The wrapping key is HKDF-SHA256 over the agent's signature of a challenge. The challenge holds the name of the identity, its reader key, the agent key and a random salt of 32 bytes that the file stores, so a signature made for one file opens no other. An Ed25519 signature of one message by one key is the same every time (RFC 8032), so the same signature opens the file each time. The challenge starts with a tag that the signatures of events never start with, so signing an event never yields the key.

`id new` asks the agent for the signature twice and stops with `the ssh-agent signs differently each time` if the two differ, since a file sealed under a key that cannot be derived again could never be opened. Nothing is written then. Keys of other types are not offered.

**The agent decides who can open the file.** Any process that the agent signs for opens it. 1Password asks before it lets an application use a key, and its settings say how long an approval lasts; `ssh-add -c` asks for each use; an agent that signs for every process of the user asks for nothing, and the file is then as open as the agent. The file holds no passphrase to guess, and a copy of it opens nothing without the key.

**The key has to stay.** If the key is removed from the agent, the identity is lost with it. Keep a second reader that does not depend on the key: an identity with a passphrase, under another name. Adding it is the same as adding any reader; see [Seal the state to a list of readers](../how-to/seal-the-state.md).

A pravo that does not know this seal refuses the file with `identity uses kdf "ssh-agent-ed25519", which this pravo does not know`.

### Which ssh-agent

pravo finds the agent as ssh does, which is not always the one that `$SSH_AUTH_SOCK` names. ssh takes the `IdentityAgent` of its configuration first, and 1Password's setup writes its agent there, in `~/.ssh/config`, and leaves the variable to the agent of macOS, which holds no key. pravo looks in this order and uses the first that names one: `$PRAVO_SSH_AUTH_SOCK`, the `IdentityAgent` that `ssh -G` reports for a host with no entry of its own, and `$SSH_AUTH_SOCK`. On Windows, with none of them, it opens the pipe `\\.\pipe\openssh-ssh-agent`. `id new` names the agent it asked when the agent holds no key.

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

`--ci` requires `--name`, and `id new --ci` without it stops with exit code 1. A default taken from the operating system user would be the name of a person, and a name is used once in the readers file, so give each job a name of its own.

Store the contents of the file as the secret `PRAVO_IDENTITY` of the job, then delete the file. Use one identity for each job, and never a person's identity in a job. Add the public line to the readers file and run `pravo protect rewrap`. The identity is the authority of the job in pravo and is not its cloud credential. [Run pravo in CI](../how-to/ci.md) shows where the secret goes.

## id show

`pravo id show` prints the signing key, the reader key and the line for the readers file. It reads the public part of the file and does not ask for the passphrase or call the ssh-agent.

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

For an identity that the ssh-agent unlocks, the reader line says `sealed under ssh-agent key SHA256:<fingerprint>`.

With `--json`, `id new` and `id show` print one document with `name`, `path`, `signing` (`alg`, `fingerprint`, `source`), `reader`, `line` and `unlock`. `unlock.by` is `passphrase`, `ssh-agent` or `none` (the file of a job). For `ssh-agent`, `unlock.key` is the fingerprint of the agent key, and `id new` adds `unlock.comment`.

## See also

- [`pravo protect`](protect.md) seals a state to the identities in the readers file.
- [`pravo protect roles`](roles.md) gives each identity a role.
- [Seal the state to a list of readers](../how-to/seal-the-state.md) walks through it.
- [Signatures and roles](../concepts/signatures-and-roles.md) says what an identity proves.
- [Global flags, environment and exit codes](global-flags.md) lists `--identity` and the variables.
