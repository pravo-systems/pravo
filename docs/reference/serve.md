---
title: pravo serve
description: "Serve a read-only viewer on the loopback interface: a graph of namespaces and resources from the specification, the state, a saved plan and the plans stored in the state."
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
| Reads | The specification, the state, the plans stored in it, the runs of the discoveries stored in it, the runs that pravo recorded in `.pravo/traces` (or `$PRAVO_TRACE_DIR`), the provider schemas that are cached already and, with `--plan FILE`, a saved plan. For a state that is sealed, the readers file and the identity of the person who runs the command. |
| Writes | Nothing: no state record, no transaction, no lock, no `pravo.lock.cue`, no generated package, and nothing of a stored plan: no claim, no progress, no removal. It runs no discovery. It starts no provider and downloads nothing. |
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

- **The graph.** Every resource, data source, ephemeral resource and function call of the specification and of the state, in the namespace that holds it, with the edges between them. Each node says whether it is declared, recorded or both, whether a read marked it drifted or gone, whether a lock holds it, and how many of its values are withheld. The outputs of the root are nodes too, with `mode` and `type` `output`, and edges from what their values read. An output is not a resource: it has no record, no history, no lock, and no count of resources includes it.
- **The attributes.** The record of a node in the order of the provider schema, with the type of each attribute and whether it is required, optional or computed. Sensitive values are withheld.
- **The source.** The lines of the specification that write the node: the definition of the resource and each instance that fills it in. Only files that the evaluation loaded are shown.
- **The schema.** The attributes of the resource type with their types and the provider's descriptions.
- **The history.** The transactions that wrote the node, applies and imports among them; a plan only reads the node, so its transactions are not listed: the kind, the time, the user and actor, and the command. The values of `-t` tags, and of flags and `key=value` words that are named like a secret, are withheld from the command. In a sealed state, with a readers file, each entry also says what the signatures of the transaction come to: `signed by NAME`, `signer not in the readers file`, `signature does not verify` or `unsigned`.
- **The plan.** With `--plan`, the action of the plan for each node and the attributes that it changes, the numbers of its summary and its hints. A plan that cannot be applied is shown with its reasons.
- **The stored plans.** The plans in the state, who made each, when, where each stands and what it changes. A plan that the page lays over the graph shows as a plan does. A resource that plans which can be applied change carries the author of each, and a ring when two of them do. See [Stored plans](#stored-plans). In a sealed state the plan view of a stored plan has a `signed` row: who stored it, as the readers file judges the signatures (`signed by NAME`, `signer not in the readers file`, `signature does not verify`), `unsigned` for none, or why they were not checked: the readers file is missing or invalid, or this identity cannot open the plan. The list of plans does not check them, since that opens each plan. The same marks are shown in the history of a node for the transactions. See [Signatures](protect.md#signatures).
- **The project.** The directory, the package, the state location, lineage and record count, the providers and the policies. The outputs of the root are nodes of the graph, and selecting one shows its value; a sensitive output has no value.

The viewer follows the project. It watches the state, the files that the evaluation loaded, the plan file and the plans stored in the state, and when any of them changes the page fetches the graph and the list of plans again. A plan that is stored, applied from or collected changes the state. A plan that nobody applies expires without any write, so the viewer counts the moment that the nearest expiry of a plan that can be applied, or is stale, passes as a change, once. For a bucket it lists `plans/` and reads the progress objects, up to eight at a time and only those that have the name of a plan, without keeping a request for the revision waiting. A specification that does not load is shown as the state alone, with the error as a warning.

## The page

The viewer is one page with a bar, a toolbar, a graph or a list, an inspector that comes in with a selection, and a status bar. It follows the system theme until you choose one.

| Part | Holds |
|---|---|
| Bar | The project directory and the state location, each a button that copies the path. The five sections, Inventory, Discovery, Traces, Plans and State. The search button, Find a resource. The identity the page is read as, when the state is sealed: its name, and for a key that is not a reader, `not a reader`, `no identity` or `identity locked` beside it. The theme button. The title of the browser tab names the project and the section, so that tabs of several estates tell apart. |
| Toolbar | Of the Inventory section: the switch Graph and List, the switch Grouped and Flat of the list, then Collapse and Expand. The filter box, which takes words and column:value terms such as `type:s3_bucket` and `mode:data`, with the number of matches, or of items. The zoom buttons, minus, Fit and plus, and the button that shows the project. |
| Pills | Under the toolbar, one pill for each term of the filter box, with a cross that takes it out. The row is not there while the box has no term. |
| Graph | The lanes of open namespaces, the cards of closed ones and the boxes of resources. The legend, a small button at the corner that opens the key to the marks that the graph shows and remembers whether it was left open. A map of the drawing, which shows when the drawing is larger than the window. The Zoom out button, which shows after a click on a card opened a namespace. |
| List | One row for each namespace and each resource, grouped by namespace or flat, with the columns Name (Address when flat), Type, Provider, Mode, Status, Plan and Refreshed. |
| Inspector | It comes in at the right when a resource or a namespace is selected, or when the project is asked for with `i`. It shows the project, a namespace or a resource. A resource has a strip of cells and the tabs. |
| Status bar | In Inventory: `no plan loaded`, or the id of a stored plan, `planned N of M` and the number `not read`. The revision. When the data was read. `live`, `reconnecting`, `offline` or `connecting`. `read-only · localhost only`. |

### Filters

The filter box sets `q`: words, and terms of the form `column:value`. A word matches the address, the type, the action and the mode of a node, and every word must match. The terms and the words together must all match. A comma between values means either one, a minus before the column means the others, and a word with a colon that names no column is a word. The box says how many nodes match, or how many resources there are while it is empty.

| Column | Matches | Values |
|---|---|---|
| `entity` | What the node is. Every node is one of them. | `resource`, `data` (a data source), `ephemeral`, `output`, `env` (an environment variable that the specification reads, `pravo.#Env` and `pravo.#SecretEnv`). `managed`, `datasource` and `environment` are read as `resource`, `data` and `env`. |
| `type` | The resource type, whole. | A type, such as `aws_s3_bucket`. A `*` stands for any run of characters: `aws_s3*`. |
| `provider` | The provider type of the resource. | A provider type, such as `aws`, whatever its instance. A `*` works. |
| `instance` | The provider instance of the resource, by its whole key. | `aws.west`: the provider type and the name of the instance, which is only unique within its type. A `*` works. |
| `mode` | The mode of the block. | `managed`, `data`, `ephemeral`, `output`. |
| `status` | The marks of the record and of the declaration. | `drifted` (drifted or gone), `gone`, `orphan` (recorded and not declared), `declared` (declared and not recorded), `locked`, `sensitive` (holds withheld values). |
| `plan` | What the loaded plan does to the resource. | `any` (every change, a no-op not counted), `create`, `update`, `replace`, `delete`, `read`, `none`. |
| `ns` | A namespace and everything below it. | A namespace path, such as `api.services`. |
| `addr` | The address, whole. | An address. A `*` works. |
| `field` | A value in the record, searched by the server over what the inspector shows. | `path=value`, as `settings.region=eu`; a name alone is a field of that name at any depth, a path alone matches the nodes that hold it, and a `*` works in either. A value with a space or a comma is in double quotes: `tags.team="platform core"`. A withheld value never matches. |
| `value` | A value anywhere in the record: an id, an arn, a url. Searched like `field`, over every path. | The value, whole: `vpc-0abc123`, or with a `*` for any run, `*inventory*`. A value with a space or a comma is in double quotes. A withheld value never matches. |
| `refreshed` | How long ago the record was read. | A number and a unit, `m`, `h`, `d` or `w`, with `>` or `<` before it: `>1d` is older than a day. |

The menu under the box offers, while the box is empty, the filters that the estate has something for, each with the number of nodes that it leaves, and then the columns. While a word is begun it offers the columns, and after a colon the values that the estate holds, with how many nodes hold each. The counts are of what the rest of the filter leaves. The older `filter` key of the address is read as the term that it stood for.

### The inspector

| Tab | Shows | `tab` |
|---|---|---|
| State | The attributes in the groups Configured, Sensitive and Computed, what the resource depends on, what uses it, what a change to it reaches, the times of the record and a lock. | `state` |
| Source | The lines that write the resource, the definition and the instance, and what else shapes it. | `source` |
| Schema | The attribute definitions of the resource type: name, type, kind and description. | `schema` |
| History | The transactions that wrote the resource. A plan is not one. | `history` |
| Change | What the loaded plan does to the resource, and the stored plans that can be applied and change it. The tab exists only when a plan is loaded or laid over the graph. | `change` |

The strip above the tabs has the cells Action, Record, Lock, Revision and Refreshed, and Plans when plans that can be applied change the resource. Action is the action of the plan, `no change`, `not read` or `no plan`. Record is `in sync`, `drifted`, `gone`, `declared only`, `orphan` or, for a node that is never recorded, `not recorded`. An ephemeral resource, a function call and an environment variable have no Revision and Refreshed cells. Lock shows when a lock or a reservation holds the resource.

### Stored plans

The state may hold plans that someone stored with [`pravo plan --store`](plan.md#store-a-plan). The Plans section lists them, newest first, A row of a plan that can still be applied ends its second line with the same ring: it is blue, turns amber under a day and red under three hours, and then says the time that is left in words; its tooltip gives the time of expiry. A click on a row opens its detail. The button View on a row lays that plan over the resources and goes to Inventory, and a strip under the bar says which plan it is while one is. [The viewer](../concepts/the-viewer.md#stored-plans) explains what it shows.

| Part | Holds |
|---|---|
| The finder | A filter box over the plans and what they change, with a menu of the columns and the values, and a pill for each term. A row names the changes that the terms asked for. The detail of a plan lists its changes, with chips that narrow the finder. The text is in the address as `plq`. |
| Pending and All | The filter of the list. Pending lists the plans that can be applied, all of them or a part: `pending`, `partial` and `partly stale`. All lists every plan that no collection has removed. Each shows its number. The list opens on Pending when a plan can be applied. |
| A row | The badge with the initials of the author, the title (the note of the plan, loaded from its overlay a few plans at a time, or the id when there is none, which then is the line under the title), the status chip, the author, the age, and the counts `+add ~change −destroy`, followed by `N imported` and `N moved` when the plan has such changes. A mark of two rings says that another plan that can be applied changes a resource of this plan. |
| The open row | The reason. For a stale plan, the reasons, twenty at most. For a partly stale plan, how many of its unapplied changes conflict with the state, and the reason of each conflict, twenty at most. The other plans that overlap, each a link that lays that plan over the graph. When the plan expires, the command that made it and the revision it was made at. For a plan that can be applied, the command `pravo apply ID` and a button that copies it. For a partly stale plan the command is `pravo apply ID --only ...`, with the changes that can be applied. For the others, a note. The reason, the command and the revision come from the plan itself, which the page reads when the row is chosen, and not from the list. |
| The plan strip | Under the bar, in Inventory, while a plan is laid over the graph: the badge, the note of the author, the id, the status, the counts, for a partial plan how many of its changes were applied (`1 of 2 applied`), the author and the age, in a sealed state a mark after the author that is a check when the signature verifies against the readers file and a sign of caution when it does not, when its key is not in the file, or when there is none (its tooltip says which), a thin ring for a plan that can still be applied, grey for the time that has gone and coloured for the time that is left, and a button with a cross that stops showing the plan. A plan that `--plan FILE` loaded has the strip too, with the name of the file, whether it is stale, the counts and its age, and no cross, since the page cannot take it off. The blast radius is in the tooltip of the counts. The counts of a stored plan are a button that goes to the Plans page with the detail of that plan open, which lists what it changes, applied or not. |

The button View on a row lays its plan over the graph, as `--plan FILE` lays a plan file: the actions on the nodes are the plan's, the Change tab answers for it, and the first view opens the namespaces that the plan touches first. The cross in the strip takes the plan off. Without `--plan`, the graph shows no plan until a plan is viewed.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | Some changes were applied. The rest can be. |
| `partly stale` | The state moved on under some of the changes that are left, and the others can be applied by name. The chip is amber. The conflicts say which changes cannot be applied, and why. The interface says `partly stale` where the API says `partly-stale`. |
| `stale` | The state moved on, so the plan cannot be applied. The reasons say how. |
| `applied` | Every change was applied. |
| `expired` | The plan was not applied before it expired. |

A plan whose objects cannot be read, because they are damaged or a newer pravo wrote them, is not listed. The graph carries one warning, `Stored plans that cannot be read are not listed`, with the ids of up to five and a short reason for each.

The status is judged against the state alone, as `pravo plan list` judges it, change by change: the lineage, and the revision and the generation of the nodes that the plan changes. The configuration and the providers belong to one checkout. `pravo apply` and `pravo plan show` compare them, and the viewer does not, so a plan that is `pending` here can still be refused in a checkout whose files differ.

A resource that a plan which can be applied still has a change to apply on carries a disc with the initial of the author, one for each plan, the newest at the right. A plan that has applied its change on a resource has no disc there. Three discs show, and a number counts the rest. A click on a disc goes to the Plans page and opens its plan under its row. Two plans that each have a change left on one resource draw a solid ring in the colour of overlap: applying one makes the other stale. The relation is the same for both plans. A change that a partly stale plan cannot apply does not count: the resource keeps the disc of the plan, no ring is drawn for it, and the Plans cell of the inspector says that the plan cannot apply the change and marks that plan with `conflict`. The Change tab of that resource gives the reason. The legend names `plan author` and `overlap` when the graph has them. The card of a closed namespace or of a group carries the discs of the plans that change something inside it, one for each plan, with the ring when two plans change one resource in it, and its hover names the plans and counts the resources. In the list, a resource shows its first two discs in the Plan column. The Plans cell of the strip has a disc for each plan that changes the resource, four at most, and a click on a disc lays that plan over the graph.

The viewer never applies, approves or removes a plan. It shows the command that applies a plan, and the button copies it.

## The Discovery section

The Discovery section shows what the discoveries of the specification found, and what each object found is to the state. [Discovery](../concepts/discovery.md) explains the statuses. The objects are shown here and in no other section: they are not in the graph or in the list of the Inventory. Its link is `view=discovery`, and its key is `d`. The tab carries the number of objects that ask to be adopted. It has no toolbar of the Inventory and no inspector, and a box that filters the objects by id, name or identity.

| Part | Holds |
|---|---|
| The head | A sentence that says how many objects ask to be adopted, or that none does, in amber when some do, and the command that runs the discoveries that need to run. |
| A card | One for each discovery. It is closed until you open it, by its name or the chevron before it, unless it is the only one or the filter asks something of the objects. A closed card shows the objects of its last run by status. It has its name, the list resource type, the provider instance and a chip: `current`, `scope changed` (the run answers the scope that was written before: run it again), `not run`, `cannot run` (the scope has no value, with the reason), `not in the specification` (the state holds a run of it) or `not compared` (the specification does not load). The scope, when it ran and who ran it, and where it is written. The notice and the command to run it, for a discovery that needs one. A warning of the provider, a warning that the list was cut off at its limit, and a warning that records of the type could not be opened. |
| The counts | The objects of the last run by status: unmanaged, adopting, acknowledged and managed. Only unmanaged is in colour. |
| Unmanaged | The objects that ask to be adopted, each with an amber edge, its name, its id, its identity and an Adopt button and an Acknowledge button. They work as a toggle: one open at a time, and a second press closes it. Adopt opens the resource that adopts the object, with a button that copies it, and the command that writes it with the attributes of the object (`pravo discover adopt NAME ID --read`). Acknowledge opens the `$ack` entry for the object, with a button that copies it and the reason for you to fill in. Beside the heading of the group, Acknowledge all opens the same kind of entry for all of them, when there are no more than 50. |
| Adopting | The objects that a resource names in `$import`, with the address of the resource. Quiet. |
| Acknowledged | The objects that an entry of `$ack` covers, in grey, each with its reason. Quiet. Below the list, the `$ack` entries of the discovery as the specification has them: their patterns, their reason and how many objects of the last run each covers. |
| Managed | Counted. Never listed. |

### Filtering the objects

The filter box above the cards is the filter box of the Inventory over the objects that the discoveries found: words, terms of the form `column:value`, a comma between values for either, a minus before the column for the others, `*` for any run of characters, double quotes around a value that holds a space or a comma. The text is in the address as `dq`. Every term that names a card, the discovery or its type or provider, says which cards are shown. Every term that names an object says which objects a card lists, and a card with none left is not shown. A word matches the id, the name, the status, the adopter or the entry, the reason, or a value of the identity.

| Column | Holds | Value |
|---|---|---|
| `status` | What the object is to the state. | `unmanaged`, `adopting` or `acknowledged`. A managed object is counted and is not listed, so it has no value. |
| `discovery` | The discovery that listed it. | Its address, whole, or a namespace, which takes the discoveries below it: `network` or `network.vpcs`. |
| `type` | The list resource type of the discovery. | The type, whole: `aws_s3_bucket`. A `*` stands for any run, as in `aws_s3*`. |
| `provider` | The provider of the discovery. | The provider type, as in `provider:aws`: every instance of it. |
| `instance` | The provider instance. | Its whole key, as in `instance:aws.west`. |
| `freshness` | Whether the last run answers the scope that is written now. | `current`, `stale`, `never`, `broken`, `undeclared` or `unknown`. |
| `id` | The id of the object. | The id, or the whole identity, whole. A `*` works. |
| `name` | The name that the provider gave it. | The name, whole. A `*` works. |
| `by` | What adopts or acknowledges it. | The address of the resource that names it in `$import`, or the entry of `$ack`, as `$ack[0]`. |
| `field` | An attribute of its identity. | `path=value`, as in `region=eu`; a path alone matches the objects that have the attribute. |
| `value` | A value of its identity, anywhere. | The value, whole: an account, a name, a region. |

The menu under the box works as it does in the Inventory. While the box is empty it offers the statuses with how many objects each leaves, the discoveries and the columns. After a colon it offers the values that the page holds, with the counts of what the rest of the filter leaves, and for `field` the attributes of the identity with the values that few objects share. Each pill takes one term out. The type, the provider, the name of a discovery and the count of a status on a card are buttons that add the term for it. The count at the end of the box is the number of objects that the filter leaves. What a term names is lit in green where the page shows it: the name, the type, the provider and the freshness chip of a card, the count of a status, an attribute of an identity. The menu offers the columns that can tell these objects apart: one that every card shares is not offered.

A group lists 100 objects and a button shows more. A discovery lists at most 2000, and says how many it leaves out. The text of every name, identity and reason comes from a provider or a person, is masked as other text of the page is, and has its control and direction characters removed. A command is suggested only for a name and an id that a shell can carry, and it is quoted. The page runs nothing: its footer says `This page suggests commands. It runs nothing and writes nothing.`

## The Traces section

The Traces section shows the runs that pravo recorded in the project: with `PRAVO_TRACES=on`, every command that did some work, such as `plan`, `apply`, `destroy`, `drift` and `discover`, leaves its spans in `.pravo/traces`, and the section reads them, so a slow or failing run can be debugged without a collector. Nothing is recorded without that variable, and the section says so when it is empty. [Telemetry](telemetry.md#the-recorded-runs) says what is recorded and how to turn it on. Its link is `view=traces`, and its key is `r`. It has no toolbar of the Inventory and no inspector.

| Part | Holds |
|---|---|
| The runs | The recorded runs, newest first: the command, how long ago, how long it took, how many resources it handled, the reason it was given, and a red square and `failed` for a run in which a span failed. The newest run is open, and a run that lands while the page is open is opened unless you chose another. |
| The head of a run | The command and whether a span failed, the exit code, when it started, how long it took, how many spans and resources, the transaction an apply wrote (with a button that copies it), the actor and the reason, and the trace id. |
| The timeline | The time of the run across and the nesting down. A bar is a span: dark for the command, the engine and the stages of a run, blue for a resource, light blue for a call to a provider, pale grey for the setup of a command and for a request to the state, red for a span that failed. A span that runs at the same time as another sits in a row of its own, so the width of a bar is how long it took and the number of rows is how much ran at once. The ruler marks the time. Click a bar to select it, double click it to fit the timeline to it. Drag to move along the run, and hold Ctrl or Cmd and scroll to zoom. The buttons zoom out, fit the whole run and zoom in. |
| Slowest | The same spans as a table, longest first. A column heading sorts by it. `In itself` is the time in a span that none of its parts covers: a resource that took four seconds, of which the provider call took 3.9, has little in itself, and a command with a large figure spent its time between its parts. |
| The filter | The filter box of the Inventory, over the spans: words that must all be in the name, the message or an attribute of a span, and terms of the form `column:value`: `is:error` for the spans that failed, `kind:` for one or more kinds (`engine`, `phase`, `setup`, `resource`, `provider`, `state`, `seal`, `network`, `command`, `mcp`; `kind:state,provider` means either), `provider:aws` for the spans of a provider `type:aws_s3*` for a resource type and `attr:pravo.provider=aws` for a span that has an attribute (the name alone, or the name and a value; the Only and Hide buttons beside an attribute in the detail of a span add the term). A minus before the column leaves the spans out, and `*` stands for any run of characters. The menu under the box offers the kinds, the columns and the values the run holds with how many spans hold each, and, for a word, the spans whose names hold it, the slowest first. A pill for each term takes it out. The timeline dims the spans that do not match, and the table lists the ones that do. |
| The kinds | Under the box, a button for each kind of span the run has, with the number of its spans, and one for the failed spans when there are any. A press adds the kind to the filter and a second press takes it out. |
| The span | What the selection did: the path from the command to it, the error text of a failed span, how long it took and how long it spent in itself, when it started and ended, its attributes, its events (the lines of the debug log that were attached to it, with the time since it started) and its slowest parts, each a button that selects it. |

Keys in the timeline: the arrows move the selection (up to the span that holds it, down to its first part, left and right to its neighbours), Shift and the left and right arrows move along the run, `+` and `-` zoom, `0` fits the whole run, Enter fits the selection and Esc clears it.

The values in a trace are the ones that a trace has anywhere: addresses, types, actions, counts, outcomes and error messages, never attribute values. The viewer masks them as it masks every other text that it shows (see [the security model](#security-model)), so an error message that quotes a secret of the state does not show it. A directory that holds a file that is not a run of this version shows its count and not the file.

## The State section

The State section says where the state is kept, how it is protected and who can read it. It is the last section of the page, after Inventory, Discovery and Plans, which holds the graph and the list, and its link is `view=state`; `view=access`, which the section was called before, opens it too. It holds the facts of the state: the location, the backend, the lineage, the format, the number of records and of locks, the version of pravo that read it and when. The project panel does not repeat them. It has no chips or toolbar; the inspector stays and shows the project. It reads and runs nothing but the viewer's own endpoint: the commands it offers are text to copy, and the footer says so: `This page suggests commands. It runs nothing and writes nothing.`

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

The list and the inspector mark a record that cannot be opened with the chip `sealed`. The inspector shows its address, type, dependencies and revision, and a note, and no attribute. The outputs of a state that cannot be opened show `sealed` and no value. A count that depends on what a record says, the drifted and the sensitive resources of the filter chips and of the project card, shows a dash with the title `sealed` where it would be zero, since a record that cannot be opened is not counted. A stored plan that cannot be opened is laid over the graph from its summary, which gives the address, the type and the action of each change, with the warning "The values of this plan are sealed." and no attribute change.

## What it never does

- It writes no state, takes no lock, makes no plan, runs no apply, writes no lock file and generates no package. It takes no claim on a stored plan, marks no progress and collects none. The project directory and the state hold the same bytes after a session as before it, stored plans included.
- It listens on no interface but loopback.
- It runs no command that the State section suggests, edits no readers file, and sends no key: no secret of an identity and no lock of the state is in a response. Reader keys and signing-key fingerprints are public and are sent.
- It sends no value that the provider schema marks sensitive or write-only, no value of an attribute that the schema does not know, no value of an attribute that is named like a secret, no value that contains a string that the state keeps in a sensitive attribute, and no value of a sensitive output.
- It serves no file by path. A request names an address, and the server looks the address up in the specification that it loaded. A request for a recorded run names an id of a fixed form, and the server reads the file of that name in the directory of the recorded runs, and nothing else; a link in that directory is not followed.

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
| `sel` | An address, or the path of a namespace | The selected node or namespace. It opens the namespaces and the cluster that hide the node, unless `collapse` names a namespace that holds it. |
| `tab` | `state`, `source`, `schema`, `history` or `change` | The tab of the inspector. `change` shows what a saved plan does to the node. |
| `collapse` | Namespace paths, or `all` | Namespaces drawn as one card, on top of the first view. |
| `expand` | Namespace paths, `c:NAMESPACE\|MODE\|TYPE` or `all` | Namespaces drawn open, on top of the first view, and the clusters that are open. `c:eu.prod.nodes\|managed\|null_resource` is the cluster of the managed resources of that type in `eu.prod.nodes`. |
| `theme` | `light` or `dark` | The theme. Without it the viewer follows the system and the last choice made in the page. |
| `plq` | Text | The finder of the Plans section: words, and terms of the form `column:value`. About the plan: status, by, id, reason, created, expires, overlap. About its changes: action, resource, ns, type, provider, change. |
| `dq` | Text | The filter box of the Discovery section: words, and terms of the form `column:value` (status, discovery, type, provider, instance, freshness, id, name, by, field, value). |
| `q` | Text | The filter box above the graph: words, and terms of the form `column:value` (entity, type, provider, instance, mode, status, plan, ns, addr, refreshed). The list cells and the menu write their terms in it. |
| `filter` | `resources`, `data`, `ephemeral`, `plan`, `drifted`, `sensitive`, `orphans`, `declared`, `outputs` or `env` | The old name of a filter. A link that has it opens with the term of that filter in `q`, so `filter=drifted` is `q=status:drifted` and `filter=data` is `q=entity:data`, and the viewer does not write it any more. |
| `view` | `graph`, `list`, `discovery`, `traces`, `plans`, `state` or `access` | The graph, the list (`table` is the old name of it), the Discovery section, the Traces section, the Plans section, or the State section (`access` is the old name of it). |
| `trace` | A run id: 32 hexadecimal digits, a hyphen and 16 more | The recorded run that the Traces section opens. Without it the newest is open. A run that is not in the directory is told in a notice and ignored. |
| `span` | A span id of 16 hexadecimal digits | The selected span of that run. |
| `tv` | `table` | The Traces section shows the slowest spans as a table and not as a timeline. |
| `tz` | `START-END` | The part of the run that the timeline shows, in microseconds from its start. |
| `tq` | Text | The filter box of the Traces section. |
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
| `/`, Ctrl-K, Cmd-K | Open the palette to find a resource, a type or a namespace. |
| `?` | Open the sheet of shortcuts. |
| `t` | Switch the theme. |
| `g` | Switch the resources between the graph and the list. |
| `a` | Switch between the Inventory and the State sections. |
| `p` | Switch between the Inventory and the Plans sections. |
| `d` | Switch between the Inventory and the Discovery sections. |
| `r` | Switch between the Inventory and the Traces sections. |
| Esc | Close the palette, the sheet or a drawer. In a text field, leave it. Otherwise clear the reach, then go back to where a search began while its result is selected, then close the namespace that a click opened, then clear the selection. |

In the graph:

| Key | Action |
|---|---|
| Left arrow | Move to a dependency of the selection. |
| Right arrow | Move to a dependent. |
| Up arrow, Down arrow | Move to the sibling above or below. |
| Enter | Open the selected card or cluster. On an open namespace or one of its resources, close the namespace. |
| `f` | Fit the graph to the window. |
| `i` | Show the project in the inspector, or take it away. Escape takes it away too. The button at the right of the toolbar does the same. |
| `0` | Go back to the first view: the namespaces that were open when the page loaded, no cluster open and the whole graph in the window. The selection stays when it still shows, and the filter stays. |
| `+`, `-` | Zoom in or out. |
| `[` and `]` | Close or open every namespace. |
| Drag | Pan the graph. |
| Wheel or pinch | Zoom the graph. Shift and the wheel pan sideways. |
| Click a card | Open the namespace, move the view to it and select it. |
| Click a disc of an author | Go to the Plans page and open that plan under its row. |
| Click a cluster | Open the cluster. |
| Double-click the title of a lane | Close or open the namespace. |

In the list of stored plans:

| Key | Action |
|---|---|
| Up arrow, Down arrow, Home, End | Move between rows. |
| Enter, Space | Lay the plan of the row over the graph, or take it off when it is laid. |
| Esc | Take the plan off the graph. |

In the list, with the focus in it:

| Key | Action |
|---|---|
| Up arrow, Down arrow, Page Up, Page Down, Home, End | Select the row above or below, or the first or last. |
| Left arrow | Close an open namespace, or go to the row of the namespace that holds it. |
| Right arrow | Open a closed namespace, or go to the next row. |
| Enter, Space | Close or open the namespace. |
| `*` | Open every namespace of this level. |

In the palette, the arrows and Page Up and Page Down move, Enter goes to the result and Esc closes it. Each search that moves the picture adds an entry to the history of the browser, which keeps the open namespaces, the selection and the camera of the picture it left: the button Back to where you were, Esc and the browser's Back return to it.

## Endpoints

The interface of the page is a set of read-only endpoints. They are for the page, and for a script that reads them. They follow the version of pravo: a response carries `version`, and a layout change raises it. Every response has `Cache-Control: no-store`, and every `/api/` request needs the token.

| Path | Response |
|---|---|
| `GET /` and `/assets/...` | The page, with no data. |
| `GET /api/ping` | `{"ok":true,"version":1}`. |
| `GET /api/graph` | The nodes, edges, namespaces, outputs, the project, the plan and the warnings. No attribute values. |
| `GET /api/detail?addr=ADDR` | One node with its attributes, dependencies, the plan's change, history, lock and times. With `&plan=ID` the plan is that stored plan, and with `&plan=none` there is none. For an output the one attribute is `value`, which is withheld when the output is sensitive or its value contains a sensitive string, and `description` when the output has one. For a node with no record, the field `schema` lists the attributes of its type, with their types, kinds and descriptions and no values. An ephemeral resource has the `attrs` that the specification sets, withheld by the same rules as a record, and an attribute that is not known before a command runs has `unknown`. An environment variable has the `attrs` `name`, `default` and `value`: the value is that of the variable in the environment of the `pravo serve` process, or the default, and it is withheld when the name matches the rule of the name and always for `pravo.#SecretEnv`. |
| `GET /api/source?addr=ADDR` | The lines that write the node, and everything that shapes it. `&plan=ID` marks the lines that stored plan changes. |
| `GET /api/plans` | The plans stored in the state, newest first, as a list of stored plans. The free text of a plan is not in it. |
| `GET /api/plan?id=ID` | A stored plan laid over the graph: the plan, the action of each node that it ran, and the `reason`, `command` and `git_revision` that the author wrote with it. In a sealed state `plan.signatures` has `items` (`status` of `verified`, `key_not_in_file` or `invalid`, with `signer` or `claimed`, and `at`) and a `note` when they could not be checked; no item and no note is a plan with none. |
| `GET /api/search?q=TEXT` | The nodes and namespaces that match. |
| `GET /api/fields?q=PATH=VALUE` | The addresses of the nodes whose recorded values match, as `{"version":1,"addrs":[...],"indexed":N,"withheld":M}`. `PATH` is dotted (`settings.region`, `rule.0.cidr`), a name alone is a field of that name at any depth, and `*` stands for any run in the path or the value; with no `=VALUE` it matches the nodes that hold the field. Only what `/api/detail` sends is searched, so a withheld value never matches. `indexed` is the number of nodes searched, `withheld` the number whose values are withheld whole. The index is built on the first search and kept for the revision. |
| `GET /api/access` | How the state is protected, the readers of the store and of the readers file, the verdict between them, who the viewer is, and the suggested commands. See [The State section](#the-state-section). |
| `GET /api/discovery` | The discoveries of the specification and what their last runs found, set against the state: for each its scope, its freshness, its last run, the counts by status, the objects that are not managed (at most 2000), its `$ack` entries and the commands. See [The Discovery section](#the-discovery-section). |
| `GET /api/adopt?discovery=NAME&key=KEY` | The resource that adopts one object of the last run of a discovery, as CUE, and the commands to copy. `KEY` is the `key` of the object in `/api/discovery`. 404 for an object that is not in the run or is managed, 400 for a name or key that is too long. The server asks no provider. |
| `GET /api/traces` | The runs that pravo recorded in `.pravo/traces` or `$PRAVO_TRACE_DIR`, newest first, at most 200: for each its `id` (the trace id, a hyphen and the id of its first root span), the command, when it started, how long it took, the exit code, `status` (`ok`, or `error` when a span failed), the counts of spans, failed spans and resources, the transaction it wrote and the actor and reason. `unreadable` counts the files that are not a run of this version. |
| `GET /api/trace?id=ID` | The spans of one run: for each its id and parent, its name, its `kind` (`command`, `engine`, `resource`, `provider`, `mcp` or `other`), its status and message, when it started and how long it took in microseconds from the start of the run, its attributes and its events. At most 30,000 spans, and `omitted` counts the rest. 400 for an id that is not of that form, 404 for a run that is not there, 422 for a file that is not a run. The id names a file and is never a path. |
| `GET /api/events` | A stream of server-sent events. Each event `change` carries the revision and what changed: `state`, `spec`, `plan`, `locks`, `plans`, `access`, `discovery` or `traces`. `plan` is the plan file of `--plan`, `plans` are the plans stored in the state, `access` is the readers file, `discovery` is the runs of the discoveries, and `traces` is the directory of the recorded runs: a run that lands changes no node, so its event carries the revision that the page holds. |

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
| `summary` | The counts `add`, `change`, `destroy` and `read`. A replacement counts as an add and a destroy. A move, and an import that has nothing else to change, are in `touches` and in no count: the page counts them from `touches`. |
| `planned`, `applied`, `applying` | The number of changes, how many were applied already, and whether an apply from the plan runs now. |
| `reasons`, `reasons_omitted` | Why a stale plan cannot be applied: twenty reasons at most, each cut at 300 characters. `reasons_omitted` counts the others. A partly stale plan has none. |
| `conflicts`, `conflicts_omitted`, `unapplied` | For a partly stale plan: the changes that cannot be applied now, each with its `addr` and its `reasons`, twenty at most, sorted by address. `conflicts_omitted` counts the others, and `unapplied` is the number of changes the plan has left, the conflicts included. |
| `apply_only`, `apply_only_omitted`, `apply_live` | For a partly stale plan: the addresses of the part that can be applied, sorted, up to 2000, with `apply_only_omitted` for the rest. The part is a closed selection for `pravo apply --only`. `apply_live` is the command that applies it, `apply` followed by `--only a,b,c`. It is absent when the part has no change, and when the command would be longer than 16 KiB. |
| `touches`, `truncated` | The resources the plan changes, each with its `addr`, its `action` (`no-op` is a change that only imports an object), `applied` when the plan has applied that change, and `conflict` when a partly stale plan cannot apply it, up to 2000. `truncated` counts the rest. |
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

The viewer shows the specification and the state as a graph of namespaces and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top: a plan file, or the id of a plan stored in the state with "pravo plan --store". The plans stored in the state are listed in the viewer whether or not --plan is given, and any of them can be laid over the graph. It updates when the state, the specification, the plan file or the stored plans change.

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
