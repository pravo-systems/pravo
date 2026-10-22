---
title: pravo serve
description: "Serve a read-only viewer on the loopback interface: a graph of modules and resources from the specification, the state and a saved plan."
order: 13
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '"port":[0-9]+'
    replace: '"port":<port>'
  - pattern: '[0-9a-f]{32}'
    replace: '<token>'
  - pattern: '127\.0\.0\.1:[0-9]+'
    replace: '127.0.0.1:<port>'
keywords: "viewer web ui browser graph dashboard visualise localhost token"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo serve [flags]` |
| Changes infrastructure | No. |
| Reads | The specification, the state, the provider schemas that are cached already and, with `--plan`, a saved plan. |
| Writes | Nothing: no state record, no transaction, no lock, no `pravo.lock.cue`, no generated package. It starts no provider and downloads nothing. |
| Prints | The address of the viewer, with its token. With `--json`, one line of JSON. Then nothing until it stops. |
| Taught in | [Explore an estate in a browser](../how-to/explore-your-estate.md). |

The command runs until a signal ends it. The viewer is one page that the server sends from the pravo binary, and a JSON interface that the page reads. [The viewer](../concepts/the-viewer.md) explains what it shows.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port N` | `0` | The port to listen on. `0` picks a free port. |
| `--no-open` | off | Do not open a browser. The address is printed either way. |
| `--plan FILE` | none | A saved plan to show on top of the graph. The viewer reads the file and never makes a plan. |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field, as `pravo plan` does. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |

The [global flags](global-flags.md) apply. With `--json`, the address is printed as one line:

```json
{"port":8700,"token":"3f6d...","url":"http://127.0.0.1:8700/?token=3f6d..."}
```

With `--plan` and no `-t` or `-T`, the specification is read with the tags that the plan was made with.

There is no flag for the address. The server listens on `127.0.0.1`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The command was stopped by a signal. |
| 1 | An error: a port that is not a number from 0 to 65535, a port that is in use, a plan file that cannot be read, a state that cannot be opened, or an argument, since `serve` takes none. |

## What the viewer shows

- **The graph.** Every resource, data source, ephemeral resource and function call of the specification and of the state, in the module that holds it, with the edges between them. Each node says whether it is declared, recorded or both, whether a read marked it drifted or gone, whether a lock holds it, and how many of its values are withheld.
- **The attributes.** The record of a node in the order of the provider schema, with the type of each attribute and whether it is required, optional or computed. Sensitive values are withheld.
- **The source.** The lines of the specification that write the node: the definition of the resource and each instance that fills it in. Only files that the evaluation loaded are shown.
- **The schema.** The attributes of the resource type with their types and the provider's descriptions.
- **The history.** The transactions that wrote the node: the kind, the time, the user and actor, and the command. The values of `-t` tags, and of flags and `key=value` words that are named like a secret, are withheld from the command.
- **The plan.** With `--plan`, the action of the plan for each node and the attributes that it changes, the numbers of its summary and its hints. A plan that cannot be applied is shown with its reasons.
- **The project.** The directory, the package, the state location, lineage and record count, the providers and the outputs. A sensitive output has no value.

The viewer follows the project. It watches the state, the files that the evaluation loaded and the plan file, and when any of them changes the page fetches the graph again. A specification that does not load is shown as the state alone, with the error as a warning.

## The page

The viewer is one page with a bar, a sidebar, a graph with its toolbar, an inspector and a status bar. It follows the system theme until you choose one.

| Part | Holds |
|---|---|
| Bar | The project directory and the state location, each a button that copies the path. The switch Graph and Tree. The search button, Find a resource. The mark `read-only` with the address of the server. The theme button. |
| Sidebar | Under the heading Show, the filter chips. Under Modules, the project and the tree of modules with their resources. |
| Toolbar | Collapse all, Expand all and Reset view. The filter box, Filter by address or type, with the number of matches. The number of items shown, when it differs from the number of resources. The zoom buttons, minus, Fit and plus. |
| Graph | The lanes of open modules, the cards of closed ones and the boxes of resources. The legend. A map of the drawing, which shows when the drawing is larger than the window. The Zoom out button, which shows after a click on a card opened a module. |
| Inspector | The project, a module or a resource. A resource has a strip of cells and the tabs. |
| Status bar | `no plan loaded`, or `planned N of M` and the number `not read`. The revision. When the data was read. `live`, `reconnecting`, `offline` or `connecting`. `localhost only`. |

### Filters

The chips set `filter`. The box sets `q`, which matches an address or a type.

| Chip | Counts | `filter` |
|---|---|---|
| All | Every resource. | none |
| In plan | The resources that a loaded plan changes. A no-op is not counted. | `plan` |
| Drifted | The resources that a read marked drifted or gone. | `drifted` |
| Sensitive | The resources that hold at least one withheld value. | `sensitive` |
| Orphans | The resources that are recorded and not declared. | `orphans` |
| Not created | The resources that are declared and not recorded. | `declared` |

### The inspector

| Tab | Shows | `tab` |
|---|---|---|
| State | The attributes in the groups Configured, Sensitive and Computed, what the resource depends on, what uses it, what a change to it reaches, the times of the record and a lock. | `state` |
| Source | The lines that write the resource, the definition and the instance, and what else shapes it. | `source` |
| Schema | The attribute definitions of the resource type: name, type, kind and description. | `schema` |
| History | The transactions that wrote the resource. | `history` |
| Change | What the loaded plan does to the resource. The tab exists only when a plan is loaded. | `change` |

The strip above the tabs has the cells Action, Record, Lock, Revision and Refreshed. Action is the action of the plan, `no change`, `not read` or `no plan`. Record is `in sync`, `drifted`, `gone`, `declared only` or `orphan`. Lock shows when a lock or a reservation holds the resource.

## What it never does

- It writes no state, takes no lock, makes no plan, runs no apply, writes no lock file and generates no package. The project directory and the state hold the same bytes after a session as before it.
- It listens on no interface but loopback.
- It sends no value that the provider schema marks sensitive or write-only, no value of an attribute that the schema does not know, no value of an attribute that is named like a secret, no value that contains a string that the state keeps in a sensitive attribute, and no value of a sensitive output.
- It serves no file by path. A request names an address, and the server looks the address up in the specification that it loaded.

## Security model

The viewer reads a state that holds secrets and sends it to a browser. These rules are what stands between the two.

**Loopback only.** The listener binds `127.0.0.1`. A request from another machine does not reach it. Over SSH or in a container, see [Explore an estate in a browser](../how-to/explore-your-estate.md#10-use-it-in-a-container-or-over-ssh).

**A token for each run.** Each run makes a 128-bit random token and prints it in the address. A request for `/?token=TOKEN` sets a cookie named `pravo_serve_PORT` that is `HttpOnly` and `SameSite=Strict`, and redirects to `/` without the token. Every request to `/api/` needs that cookie, or the header `Authorization: Bearer TOKEN`. The server compares tokens in constant time. A request without either gets 401 and a body that names nothing.

**A check of the host.** A request whose `Host` is not `127.0.0.1:PORT`, `localhost:PORT` or `[::1]:PORT` gets 421. A request that another site makes through the browser gets 403, and so does a request to `/api/` from another port of the same host or with an `Origin` header other than the viewer's. Every response carries `Cross-Origin-Resource-Policy` and `Cross-Origin-Opener-Policy` set to `same-origin`. At most 32 event streams are open at once. The server sends no CORS header, so no other page can read its answers. It answers `GET` and `HEAD` and refuses every other method with 405.

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

**Source from loaded files only.** The server reads the lines of files that the evaluation lists, and of no other path. A request names a resource and never a path. A string literal that is assigned to a sensitive attribute, or to one whose name matches the rule above, is masked in the lines and the line is marked. A line that holds a sensitive string of the state is masked wherever it is. A file is read only if it still resolves to the path that it had when the specification was loaded.

**Read-only.** Every endpoint is a `GET`. There is no request that changes the project, the state or a plan.

**The state that the specification names.** The viewer opens the state backend that the specification configures, as every other command does. For a remote state it prints `reading state from s3://BUCKET/PREFIX` at the start, with the endpoint when the specification sets one. A specification from a source that you do not trust can name any endpoint: read it before you serve it.

## The address of a view

The viewer keeps its view in the fragment of the address, so that a link reproduces a view. A fragment looks like `#/?sel=api.services.billing.config&tab=source&collapse=network,edge&theme=dark`. Values are URL-encoded, and a list is comma separated. The page rewrites the fragment as you use it, so the address in the bar is always a link to what you see.

| Parameter | Value | Meaning |
|---|---|---|
| `sel` | An address, or the path of a module | The selected node or module. It opens the modules and the cluster that hide the node, unless `collapse` names a module that holds it. |
| `tab` | `state`, `source`, `schema`, `history` or `change` | The tab of the inspector. `change` shows what a saved plan does to the node. |
| `collapse` | Module paths, or `all` | Modules drawn as one card, on top of the first view. |
| `expand` | Module paths, `c:MODULE\|MODE\|TYPE` or `all` | Modules drawn open, on top of the first view, and the clusters that are open. `c:eu.prod.nodes\|managed\|null_resource` is the cluster of the managed resources of that type in `eu.prod.nodes`. |
| `theme` | `light` or `dark` | The theme. Without it the viewer follows the system and the last choice made in the page. |
| `q` | Text | The filter box above the graph. |
| `filter` | `plan`, `drifted`, `sensitive`, `orphans` or `declared` | Only the nodes that match. |
| `view` | `graph` or `tree` | The graph, or the table. |
| `zoom` | `fit`, or a scale such as `1.2` | `fit`, the default, fits the whole graph in the window. When a link selects a resource and the whole graph would be drawn below 80 percent of full size, the view goes to the resource instead. A scale shows the graph at that size, centred on the selection when there is one. |
| `motion` | `off` | Every animation and transition off. The system setting for reduced motion has the same effect. |
| `palette` | `1` | The search palette open. |
| `pq` | Text | The text typed into the palette. |
| `help` | `1` | The sheet of shortcuts open. |

## Keyboard

The sheet of shortcuts in the viewer opens with `?`. Keys other than Ctrl-K and Esc do nothing while a text field has the focus.

| Key | Action |
|---|---|
| `/`, Ctrl-K, Cmd-K | Open the palette to find a resource, a type or a module. |
| `?` | Open the sheet of shortcuts. |
| `t` | Switch the theme. |
| `g` | Switch between the graph and the table. |
| Esc | Close the palette, the sheet or a drawer. In a text field, leave it. Otherwise clear the reach, then close the module that a click opened, then clear the selection. |

In the graph:

| Key | Action |
|---|---|
| Left arrow | Move to a dependency of the selection. |
| Right arrow | Move to a dependent. |
| Up arrow, Down arrow | Move to the sibling above or below. |
| Enter | Open the selected card or cluster. On an open module or one of its resources, close the module. |
| `f` | Fit the graph to the window. |
| `0` | Go back to the first view: the modules that were open when the page loaded, no cluster open and the whole graph in the window. The selection stays when it still shows, and the filter stays. The Reset view button does the same. |
| `+`, `-` | Zoom in or out. |
| `[` and `]` | Close or open every module. |
| Drag | Pan the graph. |
| Wheel or pinch | Zoom the graph. Shift and the wheel pan sideways. |
| Click a card | Open the module, move the view to it and select it. |
| Click a cluster | Open the cluster. |
| Double-click the title of a lane | Close or open the module. |

In the tree of modules in the sidebar:

| Key | Action |
|---|---|
| Up arrow, Down arrow, Home, End | Move between rows. |
| Left arrow | Close an open module, or move to the parent row. |
| Right arrow | Open a closed module, or move to the next row. |
| Enter, Space | Select the row. |
| `*` | Open every module of this level. |
| A letter | Jump to the next row that starts with it. In the tree, a letter is not a shortcut of the page. |

In the table, the up and down arrows, Page Up, Page Down, Home and End move the selection. In the palette, the arrows and Page Up and Page Down move, Enter goes to the result and Esc closes it.

## Endpoints

The interface of the page is a set of read-only endpoints. They are for the page, and for a script that reads them. They follow the version of pravo: a response carries `version`, and a layout change raises it. Every response has `Cache-Control: no-store`, and every `/api/` request needs the token.

| Path | Response |
|---|---|
| `GET /` and `/assets/...` | The page, with no data. |
| `GET /api/ping` | `{"ok":true,"version":1}`. |
| `GET /api/graph` | The nodes, edges, modules, outputs, the project, the plan and the warnings. No attribute values. |
| `GET /api/detail?addr=ADDR` | One node with its attributes, dependencies, the plan's change, history, lock and times. |
| `GET /api/source?addr=ADDR` | The lines that write the node, and everything that shapes it. |
| `GET /api/search?q=TEXT` | The nodes and modules that match. |
| `GET /api/events` | A stream of server-sent events. Each event `change` carries the new revision and what changed: `state`, `spec`, `plan` or `locks`. |

`ADDR` is URL-encoded. An address that is not known gets 404 and `{"error":"..."}`.

## Examples

Work in an empty directory named `demo`.

```cue file=main.cue
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

```sh run
pravo init > /dev/null
pravo apply --auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

### Start the viewer

A script starts the viewer with `--json`, reads the address from the first line and stops the viewer with a signal. A person runs `pravo serve` and leaves it running.

```sh run
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
cat serve.json
kill $pid
```

```text out
{"port":<port>,"token":"<token>","url":"http://127.0.0.1:<port>/?token=<token>"}
```

### Read the interface from a script

Every request to `/api/` needs the token. The first request here has none, the second has the token as a bearer token, and the third names a host that the server does not own.

```sh run
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -o /dev/null -w "no token: %{http_code}\n" "$base/api/ping"
curl -s -H "Authorization: Bearer $token" "$base/api/ping"; echo
curl -s -o /dev/null -w "other host: %{http_code}\n" -H "Host: example.com" -H "Authorization: Bearer $token" "$base/api/ping"
kill $pid
```

```text out
no token: 401
{"ok":true,"version":1}
other host: 421
```

### The flags

```sh run
pravo serve --help
```

```text out
Serve a read-only viewer on the loopback interface and open it in the default browser.

The viewer shows the specification and the state as a graph of modules and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top. It updates when the state, the specification or the plan file changes.

The server writes nothing: no state, no lock, no lock file, no generated package. It starts no provider and downloads nothing: it reads the provider schemas that are cached already. It listens on 127.0.0.1 only, and every request needs the token in the address that is printed.

Usage:
  pravo serve [flags]

Flags:
  -h, --help              help for serve
  -T, --inject-vars       inject system tag variables (os, arch, cwd, ...)
      --no-open           do not open a browser
      --plan string       a saved plan to show on top of the graph
      --port int          the port to listen on (0 picks a free one)
  -t, --tag stringArray   inject a value for an @tag() field: -t key=value

Global Flags:
      --actor string    a label for who is acting, recorded on transactions, locks and plans; not verified ($PRAVO_ACTOR)
  -C, --chdir string    run as if started in this directory
      --json            machine-readable output on stdout (progress goes to stderr)
      --no-color        disable coloured output
      --reason string   why; recorded on transactions and plans ($PRAVO_REASON)
  -v, --verbose count   log what pravo decides and does to stderr; -vv adds finer detail and the providers' own log ($PRAVO_LOG=debug|trace)
```
