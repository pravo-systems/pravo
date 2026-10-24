---
title: pravo serve
description: "Serve a read-only viewer on the loopback interface: a graph of modules and resources from the specification, the state, a saved plan and the plans stored in the state."
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
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
keywords: "viewer web ui browser graph dashboard visualise localhost token"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo serve [flags]` |
| Changes infrastructure | No. |
| Reads | The specification, the state, the plans stored in it, the provider schemas that are cached already and, with `--plan FILE`, a saved plan. For a state that is sealed, the readers file and the identity of the person who runs the command. |
| Writes | Nothing: no state record, no transaction, no lock, no `pravo.lock.cue`, no generated package, and nothing of a stored plan: no claim, no progress, no removal. It starts no provider and downloads nothing. |
| Prints | The address of the viewer, with its token. With `--json`, one line of JSON. Then nothing until it stops. |
| Taught in | [Explore an estate in a browser](../how-to/explore-your-estate.md). |

The command runs until a signal ends it. The viewer is one page that the server sends from the pravo binary, and a JSON interface that the page reads. [The viewer](../concepts/the-viewer.md) explains what it shows.

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

A state that is sealed is read with an identity (see [`pravo protect`](protect.md)). The command finds it once, as `pravo protect status` finds it, when it first reads a sealed state, and a passphrase is asked for on the terminal before the server listens: never in the page, and never while a request is served. Without an identity, or with one that is not a reader, the viewer lists the resources of a sealed state and shows no value. The command prints a line that says so. A plain state asks for no identity.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The command was stopped by a signal. |
| 1 | An error: a port that is not a number from 0 to 65535, a port that is in use, a plan file that cannot be read, an id that no stored plan has or that fits several, a state that cannot be opened, or an argument, since `serve` takes none. |

## What the viewer shows

- **The graph.** Every resource, data source, ephemeral resource and function call of the specification and of the state, in the module that holds it, with the edges between them. Each node says whether it is declared, recorded or both, whether a read marked it drifted or gone, whether a lock holds it, and how many of its values are withheld. The outputs of the root are nodes too, with `mode` and `type` `output`, and edges from what their values read. An output is not a resource: it has no record, no history, no lock, and no count of resources includes it.
- **The attributes.** The record of a node in the order of the provider schema, with the type of each attribute and whether it is required, optional or computed. Sensitive values are withheld.
- **The source.** The lines of the specification that write the node: the definition of the resource and each instance that fills it in. Only files that the evaluation loaded are shown.
- **The schema.** The attributes of the resource type with their types and the provider's descriptions.
- **The history.** The transactions that wrote the node, applies and imports among them; a plan only reads the node, so its transactions are not listed: the kind, the time, the user and actor, and the command. The values of `-t` tags, and of flags and `key=value` words that are named like a secret, are withheld from the command. In a sealed state, with a readers file, each entry also says what the signatures of the transaction come to: `signed by NAME`, `signer not in the readers file`, `signature does not verify` or `unsigned`.
- **The plan.** With `--plan`, the action of the plan for each node and the attributes that it changes, the numbers of its summary and its hints. A plan that cannot be applied is shown with its reasons.
- **The stored plans.** The plans in the state, who made each, when, where each stands and what it changes. A plan that the page lays over the graph shows as a plan does. A resource that plans which can be applied change carries the author of each, and a ring when two of them do. See [Stored plans](#stored-plans). In a sealed state the plan view of a stored plan has a `signed` row: who stored it, as the readers file judges the signatures (`signed by NAME`, `signer not in the readers file`, `signature does not verify`), `unsigned` for none, or why they were not checked: the readers file is missing or invalid, or this identity cannot open the plan. The list of plans does not check them, since that opens each plan. The same marks are shown in the history of a node for the transactions. See [Signatures](protect.md#signatures).
- **The project.** The directory, the package, the state location, lineage and record count, the providers and the policies. The outputs of the root are nodes of the graph, and selecting one shows its value; a sensitive output has no value.

The viewer follows the project. It watches the state, the files that the evaluation loaded, the plan file and the plans stored in the state, and when any of them changes the page fetches the graph and the list of plans again. A plan that is stored, applied from or collected changes the state. A plan that nobody applies expires without any write, so the viewer counts the moment that the nearest expiry of a plan that can be applied, or is stale, passes as a change, once. For a bucket it lists `plans/` and reads the progress objects, up to eight at a time and only those that have the name of a plan, without keeping a request for the revision waiting. A specification that does not load is shown as the state alone, with the error as a warning.

## The page

The viewer is one page with a bar, a sidebar, a graph with its toolbar, an inspector and a status bar. It follows the system theme until you choose one.

| Part | Holds |
|---|---|
| Bar | The project directory and the state location, each a button that copies the path. The three sections, Resources, Plans and State. The search button, Find a resource. The identity the page is read as, when the state is sealed: its name, and for a key that is not a reader, `not a reader`, `no identity` or `identity locked` beside it. The theme button. |
| Sidebar | Under the heading Show, the filter chips. Under Modules, the project and the tree of modules with their resources. |
| Toolbar | Of the Resources section: the switch Graph and Table, then Collapse and Expand. The filter box, Filter by address or type, with the number of matches. The zoom buttons, minus, Fit and plus, and the button Hide the sidebars. |
| Graph | The lanes of open modules, the cards of closed ones and the boxes of resources. The legend. A map of the drawing, which shows when the drawing is larger than the window. The Zoom out button, which shows after a click on a card opened a module. |
| Inspector | The project, a module or a resource. A resource has a strip of cells and the tabs. |
| Status bar | In Resources: `no plan loaded`, or the id of a stored plan, `planned N of M` and the number `not read`. The revision. When the data was read. `live`, `reconnecting`, `offline` or `connecting`. `read-only · localhost only`. |

### Filters

The chips set `filter`. The box sets `q`, which matches an address or a type.

| Chip | Counts | `filter` |
|---|---|---|
| All | Every resource. | none |
| In plan | The resources that a loaded plan changes. A no-op is not counted. The chip shows only while a plan is loaded. | `plan` |
| Drifted | The resources that a read marked drifted or gone. | `drifted` |
| Sensitive | The resources that hold at least one withheld value. | `sensitive` |
| Orphans | The resources that are recorded and not declared. | `orphans` |
| To create | The resources that are declared and not recorded. | `declared` |
| Outputs | The outputs of the root. | `outputs` |
| Env | The environment variables that the specification reads, `pravo.#Env` and `pravo.#SecretEnv`. A variable is a node that is declared and never recorded in the state. | `env` |

### The inspector

| Tab | Shows | `tab` |
|---|---|---|
| State | The attributes in the groups Configured, Sensitive and Computed, what the resource depends on, what uses it, what a change to it reaches, the times of the record and a lock. | `state` |
| Source | The lines that write the resource, the definition and the instance, and what else shapes it. | `source` |
| Schema | The attribute definitions of the resource type: name, type, kind and description. | `schema` |
| History | The transactions that wrote the resource. A plan is not one. | `history` |
| Change | What the loaded plan does to the resource, and the stored plans that can be applied and change it. The tab exists only when a plan is loaded or laid over the graph. | `change` |

The strip above the tabs has the cells Action, Record, Lock, Revision and Refreshed, and Plans when plans that can be applied change the resource. Action is the action of the plan, `no change`, `not read` or `no plan`. Record is `in sync`, `drifted`, `gone`, `declared only` or `orphan`. Lock shows when a lock or a reservation holds the resource.

### Stored plans

The state may hold plans that someone stored with [`pravo plan --store`](plan.md#store-a-plan). The Plans section lists them, newest first, A click on a row opens its detail. The button View on a row lays that plan over the resources and goes to Resources, and a strip under the bar says which plan it is while one is. [The viewer](../concepts/the-viewer.md#stored-plans) explains what it shows.

| Part | Holds |
|---|---|
| Pending and All | The filter of the list. Pending lists the plans that can be applied, all of them or a part: `pending`, `partial` and `partly stale`. All lists every plan that no collection has removed. Each shows its number. The list opens on Pending when a plan can be applied. |
| A row | The badge with the initials of the author, the title (the note of the plan, loaded from its overlay a few plans at a time, or the id when there is none, which then is the line under the title), the status chip, the author, the age, and the counts `+add ~change −destroy`. A mark of two rings says that another plan that can be applied changes a resource of this plan. |
| The open row | The reason. For a stale plan, the reasons, twenty at most. For a partly stale plan, how many of its unapplied changes conflict with the state, and the reason of each conflict, twenty at most. The other plans that overlap, each a link that lays that plan over the graph. When the plan expires, the command that made it and the revision it was made at. For a plan that can be applied, the command `pravo apply ID` and a button that copies it. For a partly stale plan the command is `pravo apply ID --only ...`, with the changes that can be applied. For the others, a note. The reason, the command and the revision come from the plan itself, which the page reads when the row is chosen, and not from the list. |
| The plan strip | Under the bar, in Resources, while a plan is laid over the graph: the badge, the note of the author, the id, the status, the counts, the author and the age, in a sealed state a mark after the author that is a check when the signature verifies against the readers file and a sign of caution when it does not, when its key is not in the file, or when there is none (its tooltip says which), and a button with a cross that stops showing the plan. A plan that `--plan FILE` loaded has the strip too, with the name of the file, whether it is stale, the counts and its age, and no cross, since the page cannot take it off. The blast radius is in the tooltip of the counts. |

The button View on a row lays its plan over the graph, as `--plan FILE` lays a plan file: the actions on the nodes are the plan's, the Change tab answers for it, and the first view opens the modules that the plan touches first. The cross in the strip takes the plan off. Without `--plan`, the graph shows no plan until a plan is viewed.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | Some changes were applied. The rest can be. |
| `partly stale` | The state moved on under some of the changes that are left, and the others can be applied by name. The chip is amber with a green edge. The conflicts say which changes cannot be applied, and why. The interface says `partly stale` where the API says `partly-stale`. |
| `stale` | The state moved on, so the plan cannot be applied. The reasons say how. |
| `applied` | Every change was applied. |
| `expired` | The plan was not applied before it expired. |

A plan whose objects cannot be read, because they are damaged or a newer pravo wrote them, is not listed. The graph carries one warning, `Stored plans that cannot be read are not listed`, with the ids of up to five and a short reason for each.

The status is judged against the state alone, as `pravo plan list` judges it, change by change: the lineage, and the revision and the generation of the nodes that the plan changes. The configuration and the providers belong to one checkout. `pravo apply` and `pravo plan show` compare them, and the viewer does not, so a plan that is `pending` here can still be refused in a checkout whose files differ.

A resource that a plan which can be applied still has a change to apply on carries a disc with the initial of the author, one for each plan, the newest at the right. A plan that has applied its change on a resource has no disc there. Three discs show, and a number counts the rest. Two plans that each have a change left on one resource draw a solid ring in the colour of overlap: applying one makes the other stale. The relation is the same for both plans. A change that a partly stale plan cannot apply does not count: the resource keeps the disc of the plan, no ring is drawn for it, and the Plans cell of the inspector says that the plan cannot apply the change and marks that plan with `conflict`. The Change tab of that resource gives the reason. The legend names `plan author` and `overlap` when the graph has them. The card of a closed module or of a group carries the discs of the plans that change something inside it, one for each plan, with the ring when two plans change one resource in it, and its hover names the plans and counts the resources. The Table view shows no badges. In the sidebar, a resource shows the mark of overlap and the first two discs. The Plans cell of the strip has a disc for each plan that changes the resource, four at most, and a click on a disc lays that plan over the graph.

The viewer never applies, approves or removes a plan. It shows the command that applies a plan, and the button copies it.

## The State section

The State section says where the state is kept, how it is protected and who can read it. It is the last section of the page, after Resources and Plans, which holds the graph and the table, and its link is `view=state`; `view=access`, which the section was called before, opens it too. It holds the facts of the state: the location, the backend, the lineage, the format, the number of records and of locks, the version of pravo that read it and when. The project panel does not repeat them. It has no sidebar or toolbar; the inspector stays and shows the project. It reads and runs nothing but the viewer's own endpoint: the commands it offers are text to copy, and the footer says so: `This page suggests commands. It runs nothing and writes nothing.`

| Part | Holds |
|---|---|
| The banner | The state, as a chip and a sentence, with the format, the backend, the location and the last change of the seal, with the name that the machine of the change gave itself (a label, not a proof). One more sentence says how many records are sealed, or how many are not sealed yet or cannot be opened with this identity. `not sealed`: attributes, outputs and stored plans are plain text, and anyone who can read the location can read every value. `immutable records`: each record is stored once and never overwritten, and none is encrypted. This is format 2, and format 3 after an unseal. `sealed`: attributes, outputs and stored plans are encrypted, and addresses, types, names, dependencies and revisions stay readable. `sealing in progress` or `unsealing in progress`: a seal or an unseal started and has not finished, with how many records it has reached. |
| You | The identity that the viewer was started with: `reader`, `not a reader`, `no identity`, or `identity locked` when its passphrase was not available. For the last three, the viewer lists the resources and shows no value. |
| Readers | The readers of the store and of the readers file, matched by reader key. Each row has the name, the reader key and the fingerprint of the signing key, each with a button that copies it, and marks that say whether the reader is in the file and in the store, and the line of the file. A name that does not match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` is listed with the mark `name not usable`, which is a Tab stop and shows the reason as its tip, and is used in no suggestion. |
| Readers file | The path that was read and a verdict: `matches the store`; `differs from the store`, with the readers that are in the file and not in the store, and the ones that are in the store and not in the file, which `pravo protect rewrap` brings together; `no file`; or `does not load`, with the line and the reason, never the text of the line. Writers refuse to write a sealed state until the file and the store agree. When the file loads and the state is `not sealed` or `immutable records`, the verdict is `state not sealed`: the repository lists readers but the state is not sealed: it has not been sealed yet, or its `meta.json` was changed. |
| Only one reader | When exactly one reader holds a lock, a warning says that nobody can read the state if that reader loses their key or its passphrase, unless the state has a recovery recipient, in which case the warning is not shown. Its button opens the suggestion that adds a backup reader and moves the focus to it. With two or more readers the warning is not shown. |
| Recovery | Shown when the state has a recovery recipient: a sentence with the threshold and the short form of the recovery key, which is the whole key in its tip. A warning follows when a group lacks the lock of the recovery key, when the repository has no recovery file, or has one that does not load or names another key; its button opens the suggestion `Bring the recovery in line`. Shares are files and the view never sees them. |
| Groups of data keys | Shown only when the state has more than one group, which a removal of a reader makes: the groups with the number of locks and of records of each, and what resealing does. A state with one group shows nothing here. |
| Suggested changes | Cards that each have a title, who does it, why, and steps. A step is a command with a button that copies it, a line to add to or remove from the readers file, shown as a small diff whose button copies the line only, or a note. The card for the next step is open. |

The view is made for the keyboard and for a screen reader. Tab visits, in the order of the page, each button that copies a reader key, a fingerprint or the path of the readers file, each `name not usable` mark, each suggestion, and the buttons that copy the steps of a suggestion that is open. Enter and Space open and close a suggestion, which says whether it is open; Esc leaves it as it is and hides the tip of a button. A button that copies is named for what it copies, and when the copy is done the page says `Copied` and the name of what was copied, or `Could not copy`. The skip link at the top of the page moves the focus to the view, and the next Tab goes to its first stop. The marks `in the readers file` and `in the store` carry those words as their names, and the chips of the state, of You and of the verdict show theirs, so that no state is told by colour alone. The readers are a table with header cells; when the pane is narrow each row is a small card and keeps its table roles. The page title that a screen reader reads follows the view.

The suggestions are: `seal` (for a state that is not sealed), `check why the state is not sealed` (the verdict `state not sealed`: `pravo protect status`, then `pravo protect init --encrypt` or removing the readers file, whichever was meant), `finish` (a seal that did not finish, or an unseal: its command is `pravo protect unseal`), `identity` (the viewer is not reading as a reader; when the state has a recovery, one more step says how to open it with `pravo protect recover` if no reader can), `rewrap` (the file and the store differ), `set a recovery key` (a sealed state with no recovery recipient; `pravo protect rewrap --recovery <K>/<N>`), `bring the recovery in line` (the recovery warning above), `add a reader`, `remove` for each reader of the file, when the file lists at least two (the only reader is not offered for removal, since the state would have none; the card of the reader who is looking says that it removes them), `seal every record again` (records are under an old group), `drop the groups nothing is sealed in` (the state has more than one group), and `check the state`. A part that you fill in is written in angle brackets and is never quoted, so a command that was pasted without being filled in fails and does nothing. A reader that is removed from the file is removed by editing the file; the page offers no command that edits it. A state that is a local directory gets a note and then the seal command with `--allow-local` (the `finish` step carries the flag too), since `pravo protect` refuses a directory that two people could share unless that flag is given.

The Table and the inspector mark a record that cannot be opened with the chip `sealed`. The inspector shows its address, type, dependencies and revision, and a note, and no attribute. The outputs of a state that cannot be opened show `sealed` and no value. A count that depends on what a record says, the drifted and the sensitive resources of the filter chips and of the project card, shows a dash with the title `sealed` where it would be zero, since a record that cannot be opened is not counted. A stored plan that cannot be opened is laid over the graph from its summary, which gives the address, the type and the action of each change, with the warning "The values of this plan are sealed." and no attribute change.

## What it never does

- It writes no state, takes no lock, makes no plan, runs no apply, writes no lock file and generates no package. It takes no claim on a stored plan, marks no progress and collects none. The project directory and the state hold the same bytes after a session as before it, stored plans included.
- It listens on no interface but loopback.
- It runs no command that the State section suggests, edits no readers file, and sends no key: no secret of an identity and no lock of the state is in a response. Reader keys and signing-key fingerprints are public and are sent.
- It sends no value that the provider schema marks sensitive or write-only, no value of an attribute that the schema does not know, no value of an attribute that is named like a secret, no value that contains a string that the state keeps in a sensitive attribute, and no value of a sensitive output.
- It serves no file by path. A request names an address, and the server looks the address up in the specification that it loaded.

## Security model

The viewer reads a state that holds secrets and sends it to a browser. These rules are what stands between the two.

**Loopback only.** The listener binds `127.0.0.1`. A request from another machine does not reach it. Over SSH or in a container, see [Explore an estate in a browser](../how-to/explore-your-estate.md#11-use-it-in-a-container-or-over-ssh).

**A token for each run.** Each run makes a 128-bit random token and prints it in the address. A request for `/?token=TOKEN` sets a cookie named `pravo_serve_PORT` that is `HttpOnly` and `SameSite=Strict`, and redirects to `/` without the token. Every request to `/api/` needs that cookie, or the header `Authorization: Bearer TOKEN`. The server compares tokens in constant time. A request without either gets 401 and a body that names nothing.

**A check of the host.** A request whose `Host` is not `127.0.0.1:PORT`, `localhost:PORT` or `[::1]:PORT` gets 421. A request that another site makes through the browser gets 403, and so does a request to `/api/` from another port of the same host or with an `Origin` header other than the viewer's. Every response carries `Cross-Origin-Resource-Policy` and `Cross-Origin-Opener-Policy` set to `same-origin`. At most 32 event streams are open at once. The server sends no CORS header, so no other page can read its answers. It answers `GET` and `HEAD` and refuses every other method with 405.

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

**Read-only.** Every endpoint is a `GET`. There is no request that changes the project, the state or a plan, and the page has no control that approves, applies or removes a stored plan.

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
| `filter` | `plan`, `drifted`, `sensitive`, `orphans`, `declared`, `outputs` or `env` | Only the nodes that match. `outputs` shows the outputs of the root, and every other value shows resources only. |
| `view` | `graph`, `table`, `plans`, `state` or `access` | The graph, the table, the Plans section, or the State section (`access` is the old name of it). |
| `zoom` | `fit`, or a scale such as `1.2` | `fit`, the default, fits the whole graph in the window. When a link selects a resource and the whole graph would be drawn below 80 percent of full size, the view goes to the resource instead. A scale shows the graph at that size, centred on the selection when there is one. |
| `motion` | `off` | Every animation and transition off. The system setting for reduced motion has the same effect. |
| `palette` | `1` | The search palette open. |
| `pq` | Text | The text typed into the palette. |
| `help` | `1` | The sheet of shortcuts open. |
| `plan` | `plan-` and the hexadecimal digits of an id, or `none` | A stored plan laid over the graph, by its whole id. The page writes it when you choose a plan, and leaves it out when the plan is the one that `--plan ID` laid. `none` takes off the plan that `--plan ID` laid. An id that the state does not hold is told in a notice and ignored. |

## Keyboard

The sheet of shortcuts in the viewer opens with `?`. Keys other than Ctrl-K and Esc do nothing while a text field has the focus.

| Key | Action |
|---|---|
| `/`, Ctrl-K, Cmd-K | Open the palette to find a resource, a type or a module. |
| `?` | Open the sheet of shortcuts. |
| `t` | Switch the theme. |
| `g` | Switch the resources between the graph and the table. |
| `a` | Switch between the Resources and the State sections. |
| `p` | Switch between the Resources and the Plans sections. |
| Esc | Close the palette, the sheet or a drawer. In a text field, leave it. Otherwise clear the reach, then close the module that a click opened, then clear the selection. |

In the graph:

| Key | Action |
|---|---|
| Left arrow | Move to a dependency of the selection. |
| Right arrow | Move to a dependent. |
| Up arrow, Down arrow | Move to the sibling above or below. |
| Enter | Open the selected card or cluster. On an open module or one of its resources, close the module. |
| `f` | Fit the graph to the window. |
| `w` | Hide or show the sidebars of the graph: the filter sidebar goes and the inspector shows only while something is selected. The bar and the status bar stay. Escape shows them again. The button next to the zoom buttons does the same. |
| `0` | Go back to the first view: the modules that were open when the page loaded, no cluster open and the whole graph in the window. The selection stays when it still shows, and the filter stays. |
| `+`, `-` | Zoom in or out. |
| `[` and `]` | Close or open every module. |
| Drag | Pan the graph. |
| Wheel or pinch | Zoom the graph. Shift and the wheel pan sideways. |
| Click a card | Open the module, move the view to it and select it. |
| Click a cluster | Open the cluster. |
| Double-click the title of a lane | Close or open the module. |

In the list of stored plans:

| Key | Action |
|---|---|
| Up arrow, Down arrow, Home, End | Move between rows. |
| Enter, Space | Lay the plan of the row over the graph, or take it off when it is laid. |
| Esc | Take the plan off the graph. |

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
| `GET /api/detail?addr=ADDR` | One node with its attributes, dependencies, the plan's change, history, lock and times. With `&plan=ID` the plan is that stored plan, and with `&plan=none` there is none. For an output the one attribute is `value`, which is withheld when the output is sensitive or its value contains a sensitive string, and `description` when the output has one. |
| `GET /api/source?addr=ADDR` | The lines that write the node, and everything that shapes it. `&plan=ID` marks the lines that stored plan changes. |
| `GET /api/plans` | The plans stored in the state, newest first, as a list of stored plans. The free text of a plan is not in it. |
| `GET /api/plan?id=ID` | A stored plan laid over the graph: the plan, the action of each node that it ran, and the `reason`, `command` and `git_revision` that the author wrote with it. In a sealed state `plan.signatures` has `items` (`status` of `verified`, `key_not_in_file` or `invalid`, with `signer` or `claimed`, and `at`) and a `note` when they could not be checked; no item and no note is a plan with none. |
| `GET /api/search?q=TEXT` | The nodes and modules that match. |
| `GET /api/access` | How the state is protected, the readers of the store and of the readers file, the verdict between them, who the viewer is, and the suggested commands. See [The State section](#the-state-section). |
| `GET /api/events` | A stream of server-sent events. Each event `change` carries the new revision and what changed: `state`, `spec`, `plan`, `locks`, `plans` or `access`. `plan` is the plan file of `--plan`, `plans` are the plans stored in the state, and `access` is the readers file. |

`ADDR` is URL-encoded. An address that is not known gets 404 and `{"error":"..."}`. An `id` or `plan` that is not `plan-` and six or more hexadecimal digits gets 400, and an id that no stored plan has gets 404. The server looks an id up among the plans that it lists. It is never a path, and the beginning of an id is not taken for it. The additions are `/api/plans`, `/api/plan`, the `plan` parameter, `Node.plans` and `Node.overlap` in the graph, and `id` and `status` in the plan of the graph. A reader that does not know them reads the rest as before.

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

### Read the stored plans

The viewer lists the plans that `pravo plan --store` wrote into the state. Change the specification, so that the plan replaces `pet` and `config`, and store it as `alice`.

```cue file=main.cue hl="11"
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 3}

config: $local.#Resource.local_file & {
	filename: "server.env"
	content:  "NAME=\(pet.id)\n"
}
```

```sh run
pravo plan --store --actor alice --reason "longer pet name" > /dev/null
```

`GET /api/plans` lists the plan. The page reads the same answer.

```sh run
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -c '.plans[0] | {id, status, open, author, summary, planned, touches, apply}'
kill $pid
```

```text out
{"id":"plan-<id>","status":"pending","open":true,"author":"alice","summary":{"add":2,"change":0,"destroy":2,"read":0},"planned":2,"touches":[{"addr":"config","action":"replace"},{"addr":"pet","action":"replace"}],"apply":"pravo apply plan-<id>"}
```

| Field of a stored plan | Meaning |
|---|---|
| `id`, `status`, `open` | The id, `pending`, `partial`, `partly-stale`, `stale`, `applied` or `expired`, and whether the plan can be applied, all of it or a part: `pending`, `partial` and `partly-stale`. |
| `author`, `created`, `expires` | Who made the plan, cut at 4 KiB, when, and when an unapplied plan is collected. The list holds no reason, command or revision. |
| `destroy` | `true` for a destroy plan. |
| `summary` | The counts `add`, `change`, `destroy` and `read`. A replacement counts as an add and a destroy. |
| `planned`, `applied`, `applying` | The number of changes, how many were applied already, and whether an apply from the plan runs now. |
| `reasons`, `reasons_omitted` | Why a stale plan cannot be applied: twenty reasons at most, each cut at 300 characters. `reasons_omitted` counts the others. A partly stale plan has none. |
| `conflicts`, `conflicts_omitted`, `unapplied` | For a partly stale plan: the changes that cannot be applied now, each with its `addr` and its `reasons`, twenty at most, sorted by address. `conflicts_omitted` counts the others, and `unapplied` is the number of changes the plan has left, the conflicts included. |
| `apply_only`, `apply_only_omitted`, `apply_live` | For a partly stale plan: the addresses of the part that can be applied, sorted, up to 2000, with `apply_only_omitted` for the rest. The part is a closed selection for `pravo apply --only`. `apply_live` is the command that applies it, `apply` followed by `--only a,b,c`. It is absent when the part has no change, and when the command would be longer than 16 KiB. |
| `touches`, `truncated` | The resources the plan changes, each with its `addr`, its `action`, `applied` when the plan has applied that change, and `conflict` when a partly stale plan cannot apply it, up to 2000. `truncated` counts the rest. |
| `overlaps` | The other plans that can be applied and have a change left on a resource that this plan has a change left on, in both directions: `id`, `count`, the first twenty addresses in `addrs`, and `truncated` for the rest. A change that a plan has applied does not count, and neither does a change that a partly stale plan cannot apply. |
| `apply` | The command that applies what is left of the plan, `pravo apply ID`, with `-t key=<value>` for each tag the plan was made with and `-T` when it was made with it. It carries the keys of the tags and never the values, so that a line pasted as it is fails in the shell until the values are put in place of `<value>`. It names no state. A partly stale plan is refused by it: `apply_live` is the command for that plan. |

`GET /api/plan?id=ID` lays a stored plan over the graph, as `--plan FILE` does for a plan file: the plan, the action of each node that it ran, and the `reason`, `command` and `git_revision` that the author wrote with it, which the list does not carry. `/api/detail` and `/api/source` take `plan=ID` for the same plan, and `plan=none` for no plan at all. An id that does not have the form of a plan id is 400. An id that no stored plan has is 404, and so is the beginning of an id: the server wants the whole id.

```sh run
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
id=$(curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -r '.plans[0].id')
curl -s -H "Authorization: Bearer $token" "$base/api/plan?id=$id" | jq -c '{plan: (.plan | {id, status, planned, total}), reason, command, nodes}'
curl -s -H "Authorization: Bearer $token" "$base/api/detail?addr=pet&plan=$id" | jq -c '.change | {action}'
curl -s -H "Authorization: Bearer $token" "$base/api/detail?addr=pet&plan=none" | jq -c '.change'
curl -s -o /dev/null -w "malformed: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=x"
curl -s -o /dev/null -w "absent: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=plan-000000"
curl -s -o /dev/null -w "beginning of an id: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=${id%??}"
kill $pid
```

```text out
{"plan":{"id":"plan-<id>","status":"pending","planned":2,"total":2},"reason":"longer pet name","command":"pravo plan --store --actor alice --reason longer pet name","nodes":[{"addr":"config","action":"replace"},{"addr":"pet","action":"replace"}]}
{"action":"replace"}
null
malformed: 400
absent: 404
beginning of an id: 404
```

The graph carries the plans as well. `Node.plans` lists the ids of the plans that can be applied and still have a change to apply on the node, newest first, and `Node.overlap` is set when two or more can apply that change. A plan that is stale, applied or expired is not on a node, and neither is a plan whose change on that node was applied already. A partly stale plan is on the node of a change that it cannot apply, and it does not count for `overlap`. The lists of `overlaps` and the nodes agree. In `/api/plan`, `plan.stale` is not set for a partly stale plan.

### Read a partly stale plan

A stored plan is partly stale when another command moved the record of some of its changes and the others can still be applied. The root `moved` has three files that do not depend on each other. Alice stores a plan that replaces them, and a plain apply replaces `a`.

```cue file=moved/main.cue
package moved

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

a: $local.#Resource.local_file & {filename: "a.txt", content: "a"}
b: $local.#Resource.local_file & {filename: "b.txt", content: "b"}
c: $local.#Resource.local_file & {filename: "c.txt", content: "c"}
```

```sh run
pravo -C moved init > /dev/null
pravo -C moved apply --auto-approve > /dev/null
pravo -C moved plan --store --replace a,b,c --actor alice > /dev/null
pravo -C moved apply --auto-approve --replace a > /dev/null
```

`GET /api/plans` gives the conflicts, the part that can be applied and the command for it. `GET /api/plan?id=ID` does not call the plan stale, and the node `a` still lists the plan.

```sh run
rm -f serve.json
pravo -C moved serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -c '.plans[0] | {status, open, reasons, unapplied, conflicts, apply_only, apply_live, touches}'
id=$(curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -r '.plans[0].id')
curl -s -H "Authorization: Bearer $token" "$base/api/plan?id=$id" | jq -c '.plan | {status, stale}'
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.nodes[] | {addr, plans, overlap}'
kill $pid
```

```text out
{"status":"partly-stale","open":true,"reasons":null,"unapplied":3,"conflicts":[{"addr":"a","reasons":["a changed in state (revision 2, plan expected 1)"]}],"apply_only":["b","c"],"apply_live":"pravo apply plan-<id> --only b,c","touches":[{"addr":"a","action":"replace","conflict":true},{"addr":"b","action":"replace"},{"addr":"c","action":"replace"}]}
{"status":"partly-stale","stale":null}
{"addr":"a","plans":["plan-<id>"],"overlap":null}
{"addr":"b","plans":["plan-<id>"],"overlap":null}
{"addr":"c","plans":["plan-<id>"],"overlap":null}
```

### Start the viewer with a stored plan

`--plan` takes the id of a stored plan in place of a file. The viewer then lays that plan over the graph when it opens. An id that no plan has stops the viewer before it listens.

```sh run exit=1
pravo serve --no-open --plan plan-000000
```

```text out
Error: no such stored plan: plan-000000 (see: pravo plan list --all)
```

```sh run
rm -f serve.json
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo serve --no-open --json --plan "$id" > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.plan | {id, status, planned, total}'
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.nodes[] | {addr, action, plans}'
kill $pid
```

```text out
{"id":"plan-<id>","status":"pending","planned":2,"total":2}
{"addr":"config","action":"replace","plans":["plan-<id>"]}
{"addr":"pet","action":"replace","plans":["plan-<id>"]}
```

### The flags

```sh run
pravo serve --help
```

```text out
Serve a read-only viewer on the loopback interface and open it in the default browser.

The viewer shows the specification and the state as a graph of modules and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top: a plan file, or the id of a plan stored in the state with "pravo plan --store". The plans stored in the state are listed in the viewer whether or not --plan is given, and any of them can be laid over the graph. It updates when the state, the specification, the plan file or the stored plans change.

A state that is sealed (see "pravo protect") is read with your reader identity, named by --identity or found as the other commands find it, and a sealed identity asks for its passphrase on the terminal before the server starts. Without an identity, or with one that is not a reader, the viewer lists the resources and shows no value; --identity none shows the state as someone with no key sees it. The State page shows who can read the state and suggests commands to copy. It runs none of them.

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
