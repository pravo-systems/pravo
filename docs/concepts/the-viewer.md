---
title: The viewer
description: "How pravo serve reads an estate: what a namespace is, what presence means, which values are withheld and why the viewer changes nothing."
order: 10
minutes: 9
---

`pravo serve` shows an estate in a browser. It joins three things by address: the specification, which it evaluates, the state, which it reads, and optionally a plan, which is a saved plan file or a plan that someone stored in the state. The result is one graph of namespaces and resources. This page explains how the viewer builds that graph and what it leaves out. [Explore an estate in a browser](../how-to/explore-your-estate.md) shows the steps, and [`pravo serve`](../reference/serve.md) is the reference.

## One address, three sources

A resource has one address, such as `api.services.billing.config`. The specification says where it is written. The state says whether it was created and what the provider returned. A saved plan says what the next apply does to it. The viewer keeps one node for each address and attaches what each source knows.

| Source | What the node gets from it |
|---|---|
| Specification | Whether the resource is declared, the file and line where it is written, the edges that its references make. |
| State | The record: attributes, revision, the times of the last apply and the last read, the marks that a read left, locks and the transactions that wrote it. |
| Saved plan | The action for the node, and the attributes that the action changes. |
| Stored plans | The plans in the state that can be applied and change the node, and who made them. |

A node is a resource, a data source, an ephemeral resource, a function call or an output of the root. The viewer shows what the specification declares and what the state records, so a node can come from either one or from both.

The kind of a node shows on its card, and a managed resource, the plain case, has no mark. A data source has a dotted outline, a round tile and a small `data` badge on the top edge. An ephemeral resource has a dashed, faded card, a tile with a dashed edge and an `ephemeral` badge. An output and an environment variable have a grey card and an `output` or `env` badge. The list and the search mark data sources and ephemeral resources with a round and a dashed square. The legend names the two marks when the graph has such a node.

An output is the other end of the data flow. Its address is the path it sits at, and it sits in the namespace that holds that path. The edges into it come from what its value reads: resources, built-in values such as `pravo.#Env`, and other outputs. It has no record, so it has no revision, no history, no lock and no refresh time, and a plan never marks it. It is not a resource, and no count of resources includes it: not the number on a namespace, not the filters other than `entity:output`, not the numbers of a plan. The graph draws it as a card like the others, with the output glyph in the tile and the word output under the name, and with a lock when its value is withheld. For an output, presence says whether an apply recorded its value. It is `both` when the specification declares it and the state holds a value, `spec` when no apply has recorded it, and `state` when the state holds a value that the specification does not declare.

## Namespaces

A namespace, to the viewer, is a struct of the specification that holds resources. It is an instance of a definition, an instance of a package, or a plain struct. It does not need to take providers as inputs, and it does not need a definition: in the platform estate, `network.zones` is a plain struct that holds three instances of one definition, and `network` holds both. A namespace is a place in the address, not a [module](../reference/glossary.md#module).

The address of every node below a namespace starts with the path of the namespace. The viewer treats every proper prefix of a node's address that holds a resource below it as a namespace, so `api.services.billing.config` sits in `api.services.billing`, which sits in `api.services`, which sits in `api`. The server sends this tree, with the parent of each namespace and the number of resources below it, and the page never splits an address.

:::figure The address `api.services.billing.config` and the three namespaces that are its prefixes. The viewer draws each namespace as a box that holds what is below it.
<svg viewBox="0 0 560 236" role="img" aria-label="Three nested boxes. The outer box is the namespace api. Inside it is the namespace api.services. Inside that is the namespace api.services.billing, which holds one small box, the resource config of type local_file.">
  <g fill="none" stroke="currentColor" stroke-width="1.5">
    <rect x="8" y="8" width="544" height="220"/>
    <rect x="28" y="44" width="504" height="172" stroke-opacity=".8"/>
    <rect x="48" y="80" width="464" height="124" stroke-opacity=".65"/>
    <rect x="300" y="120" width="190" height="64"/>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace">
    <text x="20" y="28" font-size="13" font-weight="600">api</text>
    <text x="40" y="64" font-size="13" font-weight="600">api.services</text>
    <text x="60" y="100" font-size="13" font-weight="600">api.services.billing</text>
    <text x="395" y="148" font-size="15" font-weight="600" text-anchor="middle">config</text>
    <text x="395" y="168" font-size="11" text-anchor="middle" opacity=".7">local_file</text>
    <text x="60" y="150" font-size="11" opacity=".7">5 resources</text>
    <text x="60" y="168" font-size="11" opacity=".7">id, api_key, config,</text>
    <text x="60" y="184" font-size="11" opacity=".7">env, deploy</text>
  </g>
</svg>
:::

Each namespace can carry two places in the source. One is where the instance is written. The other is where the definition that it instantiates is written, when that can be told. The Source tab of a resource shows both.

A namespace has two states in the viewer. An open namespace is a lane: a frame titled with the name of the namespace and the number of resources below it, which holds its resources and its nested namespaces. A closed namespace is one box, a card, that shows the name, the number of resources, the marks on them and one small cell for each resource, coloured by what a saved plan does to it or by the mark that it carries. The edges that cross a closed namespace join its card. A lane and a card take the colour of the provider when every resource below the namespace uses the same provider instance, and a neutral grey, tinted a little toward the pravo green, when they do not. Built-in values, environment variables and outputs belong to no provider and do not count, so a namespace of only those is grey as well, and so are their own cards. Closing hides detail and removes no node: a search still finds a resource inside a closed namespace and opens the namespaces that hide it.

## The first view

An estate of a few hundred resources drawn all at once is a picture that nobody reads, so the page chooses which namespaces to open when it loads. It starts with every namespace closed and opens them one level at a time: the top-level namespaces first, then the namespaces inside the ones that opened. A namespace opens only when the drawing that follows still fits the canvas at 80 percent of full size or more, so that the smallest names stay readable, and when the number of boxes stays within a limit that grows with the size of the canvas. A larger window opens more.

:::figure The first view in three steps. Every namespace starts as a card. The namespace that holds a change opens first, then the next level opens, for as long as the drawing fits the canvas. `network` and `edge` stay cards.
<svg viewBox="0 0 560 210" role="img" aria-label="Three canvases side by side. In the first, three cards: network, api and edge, and api carries a mark for a change. In the second, api is open as a lane that holds the cards cache and services, and network and edge are still cards. In the third, services is open as well and holds the cards billing and orders, and network and edge are still cards.">
  <g fill="none" stroke="currentColor" stroke-width="1.5">
    <rect x="8" y="8" width="170" height="170" stroke-opacity=".45"/>
    <rect x="195" y="8" width="170" height="170" stroke-opacity=".45"/>
    <rect x="382" y="8" width="170" height="170" stroke-opacity=".45"/>
    <rect x="24" y="26" width="138" height="38"/>
    <rect x="24" y="74" width="138" height="38"/>
    <rect x="24" y="122" width="138" height="38"/>
    <rect x="207" y="20" width="146" height="94" stroke-opacity=".8"/>
    <rect x="215" y="46" width="130" height="26"/>
    <rect x="215" y="78" width="130" height="26"/>
    <rect x="207" y="122" width="146" height="20"/>
    <rect x="207" y="148" width="146" height="20"/>
    <rect x="394" y="20" width="146" height="140" stroke-opacity=".8"/>
    <rect x="402" y="46" width="130" height="20"/>
    <rect x="402" y="72" width="130" height="50" stroke-opacity=".65"/>
    <rect x="410" y="96" width="54" height="18"/>
    <rect x="470" y="96" width="54" height="18"/>
    <rect x="402" y="130" width="62" height="22"/>
    <rect x="470" y="130" width="62" height="22"/>
  </g>
  <g style="fill: var(--change)">
    <rect x="148" y="80" width="8" height="8"/>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace" font-size="12">
    <text x="34" y="49" font-weight="600">network</text>
    <text x="34" y="97" font-weight="600">api</text>
    <text x="34" y="145" font-weight="600">edge</text>
    <text x="215" y="34" font-size="11" font-weight="600">api</text>
    <text x="225" y="63" font-size="11">cache</text>
    <text x="225" y="95" font-size="11">services</text>
    <text x="217" y="136" font-size="11" font-weight="600">network</text>
    <text x="217" y="162" font-size="11" font-weight="600">edge</text>
    <text x="402" y="34" font-size="11" font-weight="600">api</text>
    <text x="412" y="60" font-size="11">cache</text>
    <text x="412" y="85" font-size="11" font-weight="600">services</text>
    <text x="417" y="109" font-size="10">billing</text>
    <text x="477" y="109" font-size="10">orders</text>
    <text x="410" y="145" font-size="11" font-weight="600">network</text>
    <text x="478" y="145" font-size="11" font-weight="600">edge</text>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace" font-size="11" text-anchor="middle" opacity=".7">
    <text x="93" y="200">1 · every namespace closed</text>
    <text x="280" y="200">2 · the change opens api</text>
    <text x="467" y="200">3 · services opens too</text>
  </g>
</svg>
:::

The choice depends on the size of the canvas and on what the plan, the drift and the state say. It does not depend on the selection, a filter or a search, so a reload of the page in the same window opens the same namespaces. The order inside a level does not change. Namespaces that hold something to look at open first: a resource that a saved plan changes, a drifted or gone resource, an orphan, or a resource that is not created yet. Larger namespaces open before smaller ones. Peers open together or not at all, so that three regions of one shape are all open or all closed and the picture does not show one region in detail beside two boxes. A namespace that does not fit stays a card, and so does everything below it.

A link applies on top of this choice. The `collapse` and `expand` parameters name namespaces relative to it, and a link that selects a resource or a namespace opens the namespaces above it, unless `collapse` names one of them. A selection that you make in the page does the same. After the first view, a click on a card opens the namespace and moves the view to it. The Zoom out button closes it again and returns to the view before, and so does Esc while the namespace is the selection. `0` returns to the first view itself. [`pravo serve`](../reference/serve.md#the-address-of-a-view) lists the parameters.

## Presence

Presence says where a node is written.

| Presence | Meaning | The next apply |
|---|---|---|
| `both` | Declared in the specification and recorded in the state. | Compares it with the specification and changes it when they differ. |
| `spec` | Declared and not recorded. | Creates it. |
| `state` | Recorded and not declared. The node is an orphan. | Destroys it. |

:::figure Presence. A node that the specification declares and the state records is `both`. A node in one source only is `spec` or `state`.
<svg viewBox="0 0 560 220" role="img" aria-label="Two overlapping circles. The left circle is labelled declared in the specification and the right circle is labelled recorded in the state. The left part is spec: the next apply creates it. The overlap is both. The right part is state, an orphan: the next apply destroys it.">
  <g fill="none" stroke="currentColor" stroke-width="1.5">
    <circle cx="205" cy="104" r="86"/>
    <circle cx="355" cy="104" r="86"/>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace" text-anchor="middle">
    <text x="205" y="22" font-size="11" opacity=".7">declared in the specification</text>
    <text x="355" y="22" font-size="11" opacity=".7">recorded in the state</text>
    <text x="150" y="104" font-size="15" font-weight="600">spec</text>
    <text x="150" y="124" font-size="11" opacity=".7">next apply creates</text>
    <text x="280" y="104" font-size="15" font-weight="600">both</text>
    <text x="280" y="124" font-size="11" opacity=".7">compared</text>
    <text x="410" y="104" font-size="15" font-weight="600">state</text>
    <text x="410" y="124" font-size="11" opacity=".7">orphan, destroyed</text>
  </g>
</svg>
:::

The page words presence as the state of the record. A node that is `both` and has no mark reads `in sync`. A node that is `spec` reads `declared only`, and `status:declared` finds it. A node that is `state` reads `orphan`, and `status:orphan` finds it.

A specification that does not load cannot say what is declared. The viewer then shows the state alone, marks every node `state` and shows the error from the specification as a warning. The nodes are still the records that exist.

Presence is separate from the marks that a read leaves on a record. A node is drifted when a read found that the real object differs from the record. It is gone when a read found that the object is absent. A node can also be locked, when a lock or a reservation holds it. The filter `status:drifted` finds the drifted nodes and the gone nodes together, and the Record cell of a node tells them apart. These marks come from `pravo drift` and from the refresh of `pravo plan`; the viewer reads them and sets none.

## Edges

An edge runs from a dependency to its dependent, the direction in which data flows. The edges come from the references in the specification where it loads, and from the records of the state where it does not. A node lists what it depends on and what uses it, and the inspector also lists everything that a change to the node can reach: its transitive dependents, as the state records them.

## The plan overlay

`pravo serve --plan FILE` reads a saved plan. The viewer shows the action of the plan on each node that the plan ran: create, update, replace, delete, read, move, import or no-op. A node that the plan did not run reads `not read`: the plan did not look at it. The Change tab of a node lists the attributes that the plan changes, with the value before and after. With nothing selected, the inspector shows the numbers that the plan's summary reports, how many of the nodes it ran and the hints that `pravo plan` printed. A plan that cannot be applied is marked stale, with its reasons.

The viewer does not make a plan. A plan is the result of a refresh, a policy and a lock, and each of those is a command that changes something.

## Stored plans

A plan that someone made with `pravo plan --store` lives in the state, where everyone who can read the state finds it. The viewer reads the state, so it lists those plans whether or not it was started with `--plan`. [Plans](plans.md#stored-plans) explains how a stored plan is kept. This section explains what the viewer makes of one.

### The list

The Plans section lists the plans of the state. Each row is one plan: a badge with the initials of its author, the note that the author wrote with the plan as the title (the id is the title of a plan that has none, and the line under one that has), a chip with the status, the author, the age, and the counts of resources to add, change and destroy. Pending lists the plans that can be applied, all of them or a part. All adds the plans that are stale, applied or expired and that no collection has removed yet. A plan that changes a resource which another plan that can be applied changes too carries a mark of two rings.

The status is judged against the state alone, as `pravo plan list` judges it, change by change. A change conflicts when the node it writes has another revision or generation than the plan expected, and so does a change that has to be applied with one that conflicts. A plan with conflicts and changes that can still be applied is `partly stale`: its chip is amber with a green edge. A plan with no change that can be applied is `stale`, and so is a plan made against another lineage or one that reads a node that moved. The viewer does not compare the configuration or the providers. They belong to one checkout, and `pravo apply` compares them. A plan that is `pending` in the viewer can still be refused by `pravo apply` in a checkout whose files differ.

### One plan over the graph

A click on a row opens its detail, and the button View on it lays the plan over the graph and goes to Inventory. What the [saved plan overlay](#the-plan-overlay) does follows: the actions of the plan replace the actions on the nodes, the Change tab lists the attributes that the plan changes, and the inspector shows the numbers of its summary. The first view is made again, and the namespaces that the plan touches open first. A bar on the canvas names the plan that is laid over the graph, with its author, id, status and counts. Detail in the bar moves the focus to the row, and the button beside it takes the plan off.

The selected row opens. It shows the reason that the author gave and, for a stale plan, the reasons that it is stale. For a partly stale plan it says how many of the unapplied changes conflict with the state and lists the reason of each, twenty at most. The reason, the command and the revision come with the plan itself, which the page reads when you choose the row. The list does not carry them. It lists the other plans that overlap, each a link that lays that plan over the graph instead. A closed part holds the expiry, the command line that made the plan and its revision. For a plan that can be applied, the row ends with the command `pravo apply ID` and a button that copies it. For a partly stale plan the command is `pravo apply ID --only ...`, which names the changes that can be applied and leaves the conflicts out, because `pravo apply ID` is refused for the whole plan. For a plan that is applied, stale or expired, a note says so.

The viewer applies nothing. It has no control that approves, applies, claims or removes a plan, and the server has no endpoint that would carry one. The command is text for a terminal in a checkout. It is the plain command. A plan that was made with `-t` or `-T` needs the same tags, and `pravo apply` names them when they are missing.

### Who plans what

A resource that plans which can be applied still have a change to apply on carries a disc on the top edge of its box, with the initial of the author of each plan. Three discs show, and a number counts the rest. A click on a disc opens its plan on the Plans page. When two plans each have a change left on one resource, a solid ring in a colour of its own marks the box. The two plans overlap: applying one makes the other stale, because the resource is then at another revision. Each of the two sees the other. A plan that has applied its change on a resource has no disc there and no overlap. A partly stale plan keeps its disc on a resource whose change it cannot apply, and that change does not count for an overlap: the inspector says that the plan cannot apply it, the plan carries a `conflict` mark in the list of that resource, and the Change tab gives the reason. The legend names `plan author` and `overlap` while the graph has them. The inspector shows the same for the selected resource, as a cell, Plans, with a disc for each plan. A click on a disc lays that plan over the graph. A plan that is stale, applied or expired is not on a resource.

A plan whose objects cannot be read, because they are damaged or a newer pravo wrote them, is not listed. A warning above the graph names up to five of them, with a short reason, so that a person who looks for a colleague's plan can tell why it is not there.

Badges are drawn on boxes, and the list has the discs of a resource in its Plan column. The card of a closed namespace or of a group carries the discs of the plans that change something inside it, so that a plan is not hidden by a namespace that is closed.

[Share a plan before you apply it](../how-to/share-a-plan-before-you-apply-it.md) stores plans for two people. [Explore an estate in a browser](../how-to/explore-your-estate.md#7-see-what-your-team-has-planned) reads them in the viewer.

## Withheld values

The viewer shows attribute values, and some of them are secrets. The state holds a sensitive value as the provider returned it, and the state is a store that you protect. A viewer that sent every value to a browser would copy those values into a browser's memory, history and extensions. The server therefore decides which values leave it, and a value that it withholds is not in the response. Nothing in the page can show it, because the page never received it.

A value is sent only when the provider schema says that it is not sensitive and not write-only. The rule follows the schema down through nested attributes and blocks. Three more rules apply. A stored plan is withheld by the same rules as a plan file, with the secrets of that plan: those of the state and the values that the plan would write. What a person wrote into it, the author, the reason and the reasons of a stale plan, is masked as the history of a record is. Its command line is masked too, and the values of `-t` and of flags named like a secret are withheld from it. Each text is cut at 4 KiB. The reason, the command and the revision are sent only with the plan that you choose.

- When the schema of a resource type cannot be read, or does not know an attribute, the value is withheld, because nothing says whether it is sensitive. The reason is `schema`.
- An attribute whose name contains `password`, `passwd`, `secret`, `token`, `private_key`, `credential`, `api_key` or `access_key`, in any letter case, is withheld whatever the schema says. A schema can omit a flag, and the name is a second guard. The reason is `name`.
- A value that the schema does not mark, but that contains a string which the state keeps in a sensitive attribute, is withheld. A file whose content embeds a password has no flag, and the password is in it all the same. Strings of six characters or more count, and a type whose schema is unknown contributes every string of its records. The reason is `derived`.

A withheld attribute is listed in the Sensitive group of the State tab with its name and type, and a locked mark where the value would be. It has no value. The mark says `withheld`, or `write-only` for an attribute that the provider never records, and its hover text, which a keyboard focus shows too, gives the reason. A null value is never withheld, since null says nothing. A sensitive output has no value either, and neither has an output whose value contains a sensitive string: the State tab of its node shows the same locked mark. Three fields of a record are never sent at all: the private blob that the provider keeps, the hash of the configuration and the configuration of the provider.

The Source tab shows lines of the files that the evaluation loaded. A string literal that is assigned to a sensitive attribute, or to an attribute whose name matches the rule above, is masked in the lines, and the line is marked. A value that is computed, such as a reference to `random_password.result`, is shown as written: the line holds the reference and not the value.

Withholding is a rule about what the viewer sends. It is not a way to protect the state. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says where each kind of value is recorded.

The viewer also bounds what it sends. A long list or map is cut and the response says how many elements it left out, one attribute is cut at 4 KiB, and a block of source is cut at 200 lines.

## Clusters

A namespace can hold many resources of one type, written by a comprehension. Drawing each of them would fill the graph with boxes that differ in a number. When a namespace holds eight or more resources of one mode and one type, the viewer draws them as one cluster: a box that names the type, counts the members and carries the marks of its members, so that one drifted member among twelve still shows. A click on the box opens the cluster and lists its members in place, in the namespace, and the layout makes room for them. A control under the members closes the cluster, and a long cluster shows its first 200 members with a control for the rest. Selecting a resource that sits in a cluster opens the cluster.

Resources of one type in different namespaces are not one cluster, because a namespace is the unit that you open and close.

## A sealed state

A state can be sealed to a list of readers ([Sealed state](sealed-state.md) says what that protects and what it leaves open). Its attributes, outputs and stored plans are then encrypted, and the readers are named in a file that is kept in the repository, `.pravo/access/readers.txt`. The address, type, name, dependencies and revision of every record stay readable, so the graph and the plans work for someone who holds no key. The viewer reads a sealed state with the identity that you start it with. It finds the identity once, before it listens, and asks for the passphrase on the terminal if the identity has one. The key stays in the memory of the command. It is not in a response.

A record that the viewer cannot open is listed with its address, type, dependencies and revision, marked `sealed`, and has no value. The viewer does not guess at a value, and it drops the attributes of such a record before anything reads them. A person who is not a reader, or who started the viewer with no identity, sees the shape of the estate and none of its values. A stored plan that the viewer cannot open is laid over the graph all the same, from its summary: each change keeps its action, and the page says that the values of the plan are sealed.

The page has three sections, Inventory, Plans and State, as tabs of the bar. Inventory holds the graph and the list, and a strip under the bar says which plan is laid over them; Plans lists the stored plans and is where one is read and chosen with its View button; State is a page of its own, without the filters, the plans and the namespaces, since it is about who can read the state and not about its resources. It says in a sentence whether the state is sealed, lists the readers of the store and of the readers file side by side, and says whether the two agree. Writers refuse to write a sealed state until they do, so a difference is a thing to resolve: someone edited the file and has not run `pravo protect rewrap` yet, or the store lists someone that the repository does not. The bar names who the viewer is reading as, and the page says so when that is not a reader. The inspector stays at the right, with the project. And it suggests commands: to seal the state, to add a reader, to remove one, to bring the store in line with the file, to drop the groups that nothing is sealed in. A suggestion is text with a button that copies it. The viewer runs none of it and edits no file: a line to add to the readers file is shown as a line, and you add it.

A name in the store or in the file is data that someone else wrote, and it can be anything. The viewer puts a reader's name into a command or a line only if it matches the rule that the readers file uses, and it quotes every word of a command. A name that does not match is listed and used nowhere.

## Why the viewer is read-only

The server writes nothing. It creates no record and no transaction, takes no lock, does not write `pravo.lock.cue` and does not generate a package. The project directory and the state hold the same bytes after a session as before it, a sealed state included.

Three consequences follow. You can leave the viewer open while someone else applies: it never contends for a lock, and it shows the locks that the apply holds. You can start it on any root that you can read, because starting it changes no file. And the page has no button that changes anything, since the server has no endpoint that would carry the request.

The viewer stays current without writing. It watches the state, the files that the evaluation loaded, the plan file and the plans stored in the state. When any of them changes, or the readers file does, it sends one event to the page, and the page fetches the graph, the list of plans and the open node again. The page says what changed, the state, the specification, the plan, the stored plans or a lock, with how many resources changed, are new or are gone, and the resources that changed flash once.

A plan appears in the list when someone stores it. It becomes partial or applied when an apply from it records progress, which a failed apply does for the changes it finished. It becomes partly stale, and then stale, as something else changes the nodes that it expects. It leaves the list when a collection removes it. A plan that nobody applies expires without any write, so the viewer counts the moment that the nearest expiry of a plan that can be applied, or is stale, passes as a change. When the plan that is laid over the graph is collected, a notice says so and the graph shows no plan. The badges on the resources follow the plans that can be applied. When the resource that you were reading is gone, a notice says so and the inspector shows the project again. When the server stops, the page says that it is not answering, keeps the last picture and reads the project again when the server returns.

## Recap

- A node is one address with what the specification, the state and a saved plan know about it.
- A namespace is any struct that holds resources. Presence says whether a node is declared, recorded or both.
- A stored plan is a plan that lives in the state. The viewer lists it, lays it over the graph and shows who else plans the same resource. It copies the command that applies it and applies nothing.
- The server withholds sensitive values, values of a type with no schema and values whose names say secret. A withheld value is not in the response.
- The first view opens namespaces one level at a time, the ones that hold a change or a mark first, as far as the drawing stays readable. The rest are cards that a click opens.
- A cluster stands for many resources of one type in one namespace. Closing a namespace and clustering hide detail and remove no node.
- A sealed state is read with your identity, and a record that cannot be opened is listed with no value. The State section shows who can read the state and suggests commands to copy; it runs none of them.
- The viewer reads the project and the state and writes neither.

Next: [Explore an estate in a browser](../how-to/explore-your-estate.md) uses the viewer on an estate of three namespaces.
