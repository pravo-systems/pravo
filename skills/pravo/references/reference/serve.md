<!-- Generated from docs/reference/serve.md by tools/skillgen. Do not edit. -->

# pravo serve

Start the viewer: a read-only page on the loopback interface that shows the specification, the state, the plans stored in it, the policies, the discoveries and the recorded runs.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo serve [flags]` |
| Changes infrastructure | No. |
| Reads | The specification, the state, the plans stored in it, the runs of the discoveries stored in it, the policies in force, the runs that pravo recorded in `.pravo/traces` (or `$PRAVO_TRACE_DIR`), the provider schemas that are cached already and, with `--plan FILE`, a saved plan. For a state that is sealed, the readers file and the identity of the person who runs the command. |
| Writes | Nothing: no state record, no transaction, no lock, no `pravo.lock.cue`, no generated package, and nothing of a stored plan: no claim, no progress, no approval, no removal. It runs no discovery. It starts no provider and downloads nothing. |
| Prints | The address of the viewer, with its token. With `--json`, one line of JSON. Then nothing until it stops. |
| Taught in | [Explore an estate in a browser](../how-to/explore-your-estate.md). |

The command runs until a signal ends it. The viewer is one page that the server sends from the pravo binary, and a JSON interface that the page reads. What the page shows is on three pages of its own: [the sections of the viewer](viewer-sections.md), [its keys and links](viewer-keys-and-links.md) and [its API](viewer-api.md). [The viewer](../concepts/the-viewer.md) explains how to read it.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port N` | `0` | The port to listen on. `0` picks a free port. |
| `--no-open` | off | Do not open a browser. The address is printed either way. |
| `--plan FILE\|ID` | none | A plan to show on top of the graph: a saved plan file, or the id of a plan stored in the state, whole or its first six hexadecimal digits when no other plan starts with them. A file with that name is a file. The viewer reads the plan and never makes one. |
| `--identity NAME` | the identity that the other commands find | The identity to read a sealed state with: a file of the identity directory. `none` reads the state as a person with no key sees it. It matters only for a state that is sealed. |
| `--access-dir DIR` | `.pravo/access` | The directory that holds `readers.txt`. `$PRAVO_ACCESS_DIR` sets it too, and the flag wins. A relative directory is relative to the project directory. Without either, the viewer looks in `.pravo/access` of the project directory and of each parent up to the one that holds `.git`. |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field, as `pravo plan` does. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |

The [global flags](global-flags.md) apply. With `--json`, the address is printed as one line:

```json
{"port":8700,"token":"3f6d...","url":"http://127.0.0.1:8700/?token=3f6d..."}
```

With `--plan FILE` and no `-t` or `-T`, the specification is read with the tags that the plan was made with. `--plan ID` does not take the tags of the plan. The stored plans are read from the state that the tags select, so give the `-t` and `-T` that `pravo plan list` takes for the same state. The viewer lists the stored plans whether or not `--plan` is given.

There is no flag for the address. The server listens on `127.0.0.1`.

### A sealed state

A state that is sealed is read with an identity (see [`pravo protect`](protect.md)). The command finds it once, as `pravo protect status` finds it, when it first reads a sealed state, and a passphrase is asked for on the terminal before the server listens: never in the page, and never while a request is served. Without an identity, or with one that is not a reader, the viewer lists the resources of a sealed state and shows no value. The command prints a line that says so. A plain state asks for no identity.

The bar of the page names the identity the page is read as, and beside it, for a key that is not a reader, `not a reader`, `no identity` or `identity locked`. The [State section](viewer-sections.md#the-state-section) says the same with the members of the state.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The command was stopped by a signal. |
| 1 | An error: a port that is not a number from 0 to 65535, a port that is in use, a plan file that cannot be read, an id that no stored plan has or that fits several, a state that cannot be opened, or an argument, since `serve` takes none. |

## What it follows

The viewer follows the project. It watches the state, the files that the evaluation loaded, the plan file, the plans stored in the state, the readers file, the runs of the discoveries and the directory of the recorded runs, and when any of them changes the page fetches what changed again. A plan that is stored, approved, applied from or collected changes the state. A plan that nobody applies expires without any write, so the viewer counts the moment that the nearest expiry of a plan that can be applied, or is stale, passes as a change, once. For a bucket it lists `plans/` and reads the progress objects, up to eight at a time and only those that have the name of a plan, without keeping a request for the revision waiting. A specification that does not load is shown as the state alone, with the error as a warning.

## What it never does

- It writes no state, takes no lock, makes no plan, runs no apply, writes no lock file and generates no package. It takes no claim on a stored plan, approves none, marks no progress and collects none. The project directory and the state hold the same bytes after a session as before it, stored plans included.
- It listens on no interface but loopback.
- It runs no command that the State section or the Discovery section suggests, edits no readers file, and sends no key: no secret of an identity and no lock of the state is in a response. Reader keys and signing-key fingerprints are public and are sent.
- It sends no value that the provider schema marks sensitive or write-only, no value of an attribute that the schema does not know, no value of an attribute that is named like a secret, no value that contains a string that the state keeps in a sensitive attribute, and no value of a sensitive output.
- It serves no file by path. A request names an address, and the server looks the address up in the specification that it loaded. A request for a recorded run names an id of a fixed form, and the server reads the file of that name in the directory of the recorded runs, and nothing else; a link in that directory is not followed.
- It starts no provider. The values of a resource are shown with the provider schema that the last run cached, and a type whose schema is not cached has its values withheld.

## Security model

The viewer reads a state that holds secrets and sends it to a browser. These rules are what stands between the two.

**Loopback only.** The listener binds `127.0.0.1`. A request from another machine does not reach it. Over SSH or in a container, see [Explore an estate in a browser](../how-to/explore-your-estate.md#14-use-it-in-a-container-or-over-ssh).

**A token for each run.** Each run makes a 128-bit random token and prints it in the address. A request for `/?token=TOKEN` sets a cookie named `pravo_serve_PORT` that is `HttpOnly` and `SameSite=Strict`, and redirects to `/` without the token. Every request to `/api/` needs that cookie, or the header `Authorization: Bearer TOKEN`. The server compares tokens in constant time. A request without either gets 401 and a body that names nothing.

**A check of the host.** A request whose `Host` is not `127.0.0.1:PORT`, `localhost:PORT` or `[::1]:PORT` gets 421. A request that another site makes through the browser gets 403, and so does a request to `/api/` from another port of the same host or with an `Origin` header other than the viewer's. Every response carries `Cross-Origin-Resource-Policy` and `Cross-Origin-Opener-Policy` set to `same-origin`. At most 32 event streams are open at once. The server sends no CORS header, so no other page can read its answers. It answers `GET` and `HEAD`, and one `POST`, the plan file that is dropped on the policy studio (see [The studio](viewer-sections.md#the-studio)). Every other method, and a `POST` anywhere else, gets 405.

**A sealed state.** A record of a sealed state that the viewer cannot open is listed with its address, type, dependencies and revision and no value. The server drops the attributes of such a record before anything reads them, withholds the outputs, and carries no key, no lock and no identity in a response. The identity is found once, before the server listens, and held in memory. The State section suggests commands as text. Every reader name in a command passes the rule for names, every word is quoted, and a line of the readers file is rebuilt from its parsed parts and never copied. A line that does not parse is a number and a reason.

**Redaction on the server.** A value is sent only when the provider schema says that it is not sensitive and not write-only. Nested attributes and blocks follow the schema down. These withhold a value too:

| Rule | Withheld | Reason shown |
|---|---|---|
| The schema marks the attribute sensitive. | The value. | `sensitive` |
| The schema marks the attribute write-only. | The value. | `write-only` |
| The schema of the resource type cannot be read, or does not know the attribute. | The value. | `schema` |
| The name of the attribute matches `(?i)(password\|passwd\|secret\|token\|private_key\|credential\|api_key\|access_key)`. | The value. | `name` |
| The value contains a string of six characters or more that the state keeps in a sensitive attribute, the value of a sensitive output, or a sensitive value that a saved plan would write, as it is or percent-encoded, escaped in JSON, in hex or in base64. A type whose schema is unknown contributes every string of its records. | The value. | `derived` |
| The output is sensitive, or its value contains such a string. | The value. | `sensitive` or `derived` |

The `private` blob, the configuration hash and the provider configuration of a record are never sent. A response cuts a long list, the text of an attribute at 4 KiB and a block of source at 200 lines.

A stored plan is redacted as a plan file is, by the same rules: the values of its changes are withheld by the schema, by the name and by the derived rule, and the sensitive values that the plan would write count as secrets for the values of that plan. The answers that are about one plan, `/api/plan`, and `/api/detail` and `/api/source` with `plan=ID`, are masked with the secrets of that plan: those of the state and the values that the plan would write. What a person wrote into a stored plan, the author, the reason, the revision of the repository and the reasons of a stale plan, is masked as the history of a record is, and so are the actor and the hints of its plan. Its command is masked as a recorded command line is: the values of `-t` and `--tag`, and of flags named like a secret, are withheld, also in a cluster such as `-vt`. Each text is cut at 4 KiB. The list of plans, which is about no plan in particular, holds the author only. The reason, the command and the revision of a plan are in the answer of `/api/plan` and nowhere else.

**Source from loaded files only.** The server reads the lines of files that the evaluation lists, and of no other path. A request names a resource and never a path. A string literal that is assigned to a sensitive attribute, or to one whose name matches the rule above, is masked in the lines and the line is marked. A line that holds a sensitive string of the state is masked wherever it is. A file is read only if it still resolves to the path that it had when the specification was loaded.

**Traces and discoveries.** The values in a recorded run are the ones that a trace has anywhere: addresses, types, actions, counts, outcomes and error messages, never attribute values. The names, ids and identities of the objects that a discovery found come from a provider, and the reasons of `$ack` entries from a person. The viewer masks all of it as it masks every other text that it shows, so an error message that quotes a secret of the state does not show it, and removes the control and direction characters from a name. A command is suggested only for a name and an id that a shell can carry, and it is quoted.

**Read-only.** Every endpoint is a `GET`, but for the plan file that the studio takes and keeps in memory. There is no request that changes the project, the state or a plan, and the page has no control that approves, applies or removes a stored plan.

**The state that the specification names.** The viewer opens the state backend that the specification configures, as every other command does. For a remote state it prints `reading state from s3://BUCKET/PREFIX` at the start, with the endpoint when the specification sets one. A specification from a source that you do not trust can name any endpoint: read it before you serve it.

## Examples

Work in an empty directory named `demo`.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

config: $local.#Resource.local_file & {
	filename: "server.env"
	content:  "NAME=\(pet.id)\n"
}
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

### Start the viewer

A person runs `pravo serve` and leaves it running; the browser opens on the address that is printed. A script starts the viewer with `--json`, reads the address from the first line and stops the viewer with a signal.

```sh
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
cat serve.json
kill $pid
```

Output:

```text
{"port":<port>,"token":"<token>","url":"http://127.0.0.1:<port>/?token=<token>"}
```

The page opens on the Inventory section, with nothing selected.

*Figure: The viewer as it opens, on an estate of three groups.*

### Start the viewer with a stored plan

`--plan` takes the id of a stored plan in place of a file. The viewer then lays that plan over the graph when it opens. An id that no plan has stops the viewer before it listens.

```sh
pravo serve --no-open --plan plan-000000
```

Output:

```text
Error: no such stored plan: plan-000000 (see: pravo plan list --all)
```

[The API of the viewer](viewer-api.md) reads the graph with such a plan laid over it from a script.

### The flags

```sh
pravo serve --help
```

Output:

```text
Serve a read-only viewer on the loopback interface and open it in the default browser.

The viewer shows the specification and the state as a graph of groups and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top: a plan file, or the id of a plan stored in the state with "pravo plan --store". The plans stored in the state are listed in the viewer whether or not --plan is given, and any of them can be laid over the graph. It updates when the state, the specification, the plan file or the stored plans change.

A state that is sealed (see "pravo protect") is read with your reader identity, named by --identity or found as the other commands find it, and a sealed identity asks for its passphrase on the terminal before the server starts. Without an identity, or with one that is not a reader, the viewer lists the resources and shows no value; --identity none shows the state as someone with no key sees it. The State page shows who can read the state and suggests commands to copy. It runs none of them.

It also lists the runs that pravo recorded (.pravo/traces, or $PRAVO_TRACE_DIR) as a timeline of spans, so that a slow or failing run can be read without a collector.

The server writes nothing: no state, no lock, no lock file, no generated package. It starts no provider and downloads nothing: it reads the provider schemas that are cached already. It listens on 127.0.0.1 only, and every request needs the token in the address that is printed.

Usage:
  pravo serve [flags]

Flags:
      --access-dir string   directory of the readers file (default .pravo/access); $PRAVO_ACCESS_DIR
  -h, --help                help for serve
  -T, --inject-vars         inject system tag variables (os, arch, cwd, ...)
      --no-open             do not open a browser
      --plan string         a saved plan to show on top of the graph: a file, or the id of a plan stored in the state (plan-<id>)
      --port int            the port to listen on (0 picks a free one)
  -t, --tag stringArray     inject a value for an @tag() field: -t key=value

Global Flags:
      --actor string      a label for who is acting, recorded on transactions, locks and plans; not verified ($PRAVO_ACTOR)
  -C, --chdir string      run as if started in this directory
      --identity string   name of the identity to read a sealed state with; overrides $PRAVO_IDENTITY
      --json              machine-readable output on stdout (progress goes to stderr)
      --no-color          disable coloured output
      --reason string     why; recorded on transactions and plans ($PRAVO_REASON)
  -v, --verbose count     log what pravo decides and does to stderr; -vv adds finer detail and the providers' own log ($PRAVO_LOG=debug|trace)
```
