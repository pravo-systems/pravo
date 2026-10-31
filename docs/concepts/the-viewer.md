---
title: The viewer
description: "How pravo serve reads an estate: what a group is, what presence means, how the first view is chosen, what the eight sections show, which values are withheld and why the viewer changes nothing."
order: 12
minutes: 12
keywords: "serve browser graph group inventory plans discovery policies traces state inspector withheld read-only"
---

`pravo serve` shows an estate in a browser. It joins what it can read by address: the specification, which it evaluates, the state, which it reads, the plans that people stored in the state, the runs that pravo recorded, and optionally a plan file. The result is one page with eight sections, and the first of them is a graph of groups and resources. This page explains how the viewer builds that picture and what it leaves out. [Explore an estate in a browser](../how-to/explore-your-estate.md) shows the steps, [The sections of the viewer](../reference/viewer-sections.md) lists every part, and [`pravo serve`](../reference/serve.md) is the command.

## One address, several sources

A resource has one address, such as `api.services.billing.config`. The specification says where it is written. The state says whether it was created and what the provider returned. A plan says what the next apply does to it. The viewer keeps one node for each address and attaches what each source knows.

| Source | What the node gets from it |
|---|---|
| Specification | Whether the resource is declared, the file and line where it is written, the edges that its references make. |
| State | The record: attributes, revision, the times of the last apply and the last read, the marks that a read left, locks, and the transactions that wrote it. |
| A plan file | The action for the node, and the attributes that the action changes. |
| Stored plans | The plans in the state that can be applied and change the node, and who made them. |
| Recorded runs | Nothing on the node itself. A run is read in the Traces section, where the span of each resource says what the run did to it. |

A node is a resource, a data source, an ephemeral resource, a function call, an environment variable or an output of the root. The viewer shows what the specification declares and what the state records, so a node can come from either one or from both.

The kind of a node shows on its card, and a managed resource, the plain case, has no mark. A data source has a dotted outline and a small `data` badge on the top edge. An ephemeral resource has a dashed, faded card, a tile with a dashed edge and an `ephemeral` badge. An output and an environment variable have a grey card and an `output` or `env` badge. The list and the search mark data sources and ephemeral resources with a dotted and a dashed square. The legend names the two marks when the graph has such a node.

An output is the other end of the data flow. Its address is the path it sits at, and it sits in the group that holds that path. The edges into it come from what its value reads: resources, built-in values such as `pravo.#Env`, and other outputs. It has no record, so it has no revision, no history, no lock and no refresh time, and a plan never marks it. It is not a resource, and no count of resources includes it: not the number on a group, not the filters other than `entity:output`, not the numbers of a plan. The graph draws it as a card like the others, with the output glyph in the tile and the word output under the name, and with a lock when its value is withheld. For an output, presence says whether an apply recorded its value. It is `both` when the specification declares it and the state holds a value, `spec` when no apply has recorded it, and `state` when the state holds a value that the specification does not declare.

An ephemeral resource, a function call and an environment variable have no record either, and the viewer says what it can without one. The Record cell reads `not recorded`, the Revision and Refreshed cells are not there, and the History tab says that no transaction can write the node. The State tab of an ephemeral resource lists the values that the specification sets, under the rules of [withheld values](#withheld-values): an attribute that the schema marks sensitive, or whose name matches the rule, is withheld, and a value that reads another node or that the provider fills in reads `known after apply`. The State tab of an environment variable lists `name`, `default` and `value`. The value is the one the variable has in the environment of the `pravo serve` process, or the default when it is not set there. A command reads the variable from its own environment, which can differ. A variable whose name matches the rule has no value shown, and a `pravo.#SecretEnv` never has one, since its value is kept out of the state and out of this page. The Schema tab of an ephemeral resource, and of a resource that is declared and not created yet, lists the attributes of its type; that of an environment variable lists its three.

## Groups

A group, to the viewer, is a struct of the specification that holds resources. It is an instance of a definition, an instance of a package, or a plain struct. It does not need to take providers as inputs, and it does not need a definition: in the platform estate, `network.zones` is a plain struct that holds three instances of one definition, and `network` holds both. A group is a place in the address, not a form of reuse: it needs no [definition](../reference/glossary.md#definition) and takes no providers.

The address of every node below a group starts with the path of the group. The viewer treats every proper prefix of a node's address that holds a resource below it as a group, so `api.services.billing.config` sits in `api.services.billing`, which sits in `api.services`, which sits in `api`. The server sends this tree, with the parent of each group and the number of resources below it, and the page never splits an address.

:::figure The address `api.services.billing.config` and the three groups that are its prefixes. The viewer draws each group as a box that holds what is below it.
<svg viewBox="0 0 560 236" role="img" aria-label="Three nested boxes. The outer box is the group api. Inside it is the group api.services. Inside that is the group api.services.billing, which holds one small box, the resource config of type local_file.">
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

Each group can carry two places in the source. One is where the instance is written. The other is where the definition that it instantiates is written, when that can be told. The Source tab of a resource shows both.

A group has two states in the viewer. An open group is a lane: a frame titled with the name of the group and the number of resources below it, which holds its resources and its nested groups. A closed group is one box, a card, that shows the name, the number of resources, the marks on them and one small cell for each resource, coloured by what a plan does to it or by the mark that it carries. The edges that cross a closed group join its card. A lane and a card take the colour of the provider when every resource below the group uses the same provider instance, and a neutral grey, tinted a little toward the pravo green, when they do not. Built-in values, environment variables and outputs belong to no provider and do not count, so a group of only those is grey as well, and so are their own cards. Closing hides detail and removes no node: a search still finds a resource inside a closed group and opens the groups that hide it.

## The first view

An estate of a few hundred resources drawn all at once is a picture that nobody reads, so the page chooses which groups to open when it loads. It starts with every group closed and opens them one level at a time: the top-level groups first, then the groups inside the ones that opened. A group opens only when the drawing that follows still fits the canvas at 80 percent of full size or more, so that the smallest names stay readable, and when the number of boxes stays within a limit that grows with the size of the canvas. A larger window opens more.

:::figure The first view in three steps. Every group starts as a card. The group that holds a change opens first, then the next level opens, for as long as the drawing fits the canvas. `network` and `edge` stay cards.
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
    <text x="93" y="200">1 · every group closed</text>
    <text x="280" y="200">2 · the change opens api</text>
    <text x="467" y="200">3 · services opens too</text>
  </g>
</svg>
:::

The choice depends on the size of the canvas and on what the plan, the drift and the state say. It does not depend on the selection, a filter or a search, so a reload of the page in the same window opens the same groups. The order inside a level does not change. Groups that hold something to look at open first: a resource that a plan changes, a drifted or gone resource, an orphan, or a resource that is not created yet. Larger groups open before smaller ones. Peers open together or not at all, so that three regions of one shape are all open or all closed and the picture does not show one region in detail beside two boxes. A group that does not fit stays a card, and so does everything below it.

A link applies on top of this choice. The `collapse` and `expand` parameters name groups relative to it, and a link that selects a resource or a group opens the groups above it, unless `collapse` names one of them. A selection that you make in the page does the same. After the first view, a click on a card opens the group and moves the view to it. The Zoom out button closes it again and returns to the view before, and so does Esc while the group is the selection. `0` returns to the first view itself. [Keys and links of the viewer](../reference/viewer-keys-and-links.md#the-address-of-a-view) lists the parameters.

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

An edge follows the data: it leaves a dependency and ends in an arrowhead on what depends on it. The graph reads like a page, from left to right, in columns: what depends on nothing stands in the first columns, and what nothing depends on in the last. An environment variable always stands in the first column of its group, and an output always in the last: an output that reads other outputs stands in a column of its own, to the right of the outputs that it reads. An item that reads another always stands in a column to the right of it, inside a group and across groups alike. An open group is a lane with columns of its own, and the wires that cross its border enter and leave it at its sides. Within a column the items stand in the order that crosses the fewest wires.

The graph draws an edge like a wire in a schematic: it leaves the right edge of the dependency with a dot, runs to the right, turns at right angles only in the gap between two columns, and ends in an arrowhead on the left edge of the item that reads it. The wires that leave one dependency share a line in the gap, and the lines of a gap stand apart, so that each wire can be followed with the eye. A wire is as straight as the picture lets it be: the heights of the items are chosen to give the wires the fewest turns, and a wire leaves an item, and arrives at one, at the height that it runs at whenever that is within the item, clear of its corners, so that it goes straight out and straight in. A wire never crosses an item. Where two wires cross without joining, the horizontal one hops over the other, and a dot marks a wire that joins another. When a wire stands for several edges, as one between two closed groups does, a small square on it says how many. The squares stay on their own wires, as far from other wires as the picture allows, and no two touch. An edge that closes a cycle is the one that cannot run to the right all the way: its wire goes back along a line above the items of its group and comes in from the left all the same, so the arrowhead points right in every case.

A wire moves for three reasons only. It draws in once when the graph first appears, it glides to its new place when the layout changes, and a pulse runs once along each wire that a selected resource reaches, in the direction of the arrowhead, which is the direction of the data. A wire never loops, and the wires of a selection stay solid in their colours. The edges come from the references in the specification where it loads, and from the records of the state where it does not. A node lists what it depends on and what uses it, and the inspector also lists everything that a change to the node can reach: its transitive dependents, as the state records them.

## The inspector

The inspector is the right column of the Inventory, and it shows the selection: a resource, a group, or the project when nothing is selected. For a resource, a strip of cells says what the plan does to it, whether its record is in sync, drifted, gone, an orphan or declared only, whether a lock holds it, and its revision and the time of its last read. Five tabs follow. State holds the attributes, in the order of the provider schema, with the values that the server lets through. Source shows the lines of the files that write the resource: the definition and the instance, when a definition writes it. Schema is the definition of its type. History is the transactions that wrote the node, newest first, with what each altered, who ran it and, in a sealed state, whose signature it carries. Change exists while a plan is laid over the graph and says what that plan does to the node. [The inspector](../reference/viewer-sections.md#the-inspector) lists each cell and tab.

The History tab is where a question about the past is answered. A plan reads a node and leaves no entry; an apply, an import and a move write one, and so does a read that found the object changed outside pravo, which shows what differed. An entry names the stored plan that the apply ran from, when there was one, and the viewer opens that plan over the graph while the state still holds it.

## The plan over the graph

A plan is laid over the graph in two ways. `pravo serve --plan FILE` reads a saved plan file, and the Plans section lays a stored plan with its View button. In both cases the viewer shows the action of the plan on each node that the plan ran: create, update, replace, delete, read, move, import or no-op. A node that the plan did not run reads `not read`: the plan did not look at it. The Change tab of a node lists the attributes that the plan changes, with the value before and after. With nothing selected, the inspector shows the numbers that the plan's summary reports, how many of the nodes it ran and the hints that `pravo plan` printed. A plan that cannot be applied is marked stale, with its reasons. A strip under the bar names the plan that is laid over the graph, and for a stored plan a cross on it takes the plan off.

The viewer does not make a plan. A plan is the result of a refresh, a policy and a lock, and each of those is a command that changes something.

## Stored plans

A plan that someone made with `pravo plan --store` lives in the state, where everyone who can read the state finds it. The viewer reads the state, so it lists those plans whether or not it was started with `--plan`. [Stored plans and approvals](stored-plans-and-approvals.md) explains how a stored plan is kept and how it moves from pending to applied. The Plans section lists the plans with their status, author, age and counts, and a finder narrows the list by what a plan is or what it changes, so `ns:network action:delete` lists the plans that delete something in `network`, and names what. [The Plans section](../reference/viewer-sections.md#the-plans-section) lists the parts of the page and the columns of the finder.

The status of a plan is judged against the state alone, as `pravo plan list` judges it, change by change: a change conflicts when the node it writes has another revision or generation than the plan expected, and so does a change that has to be applied with one that conflicts. A plan with conflicts and changes that can still be applied is `partly stale`, a plan with no change that can be applied is `stale`. The viewer does not compare the configuration or the providers, which belong to one checkout, and it does not know whether a plan has the approvals that a policy asks for. `pravo apply` checks both, so a plan that is `pending` in the viewer can still be refused in a checkout whose files differ or by a policy that waits for an approval.

A click on a row opens its detail: the reason the author gave, the reasons a stale plan is stale, the other plans that overlap, and the command that applies the plan, which a button copies. For a partly stale plan the command is `pravo apply ID --only ...`, which names the changes that can be applied and leaves the conflicts out. The viewer applies nothing. It has no control that approves, applies, claims or removes a plan, and the server has no endpoint that would carry one. The command is text for a terminal in a checkout, and a plan that was made with `-t` or `-T` needs the same tags.

### Who plans what

A resource that a plan which can be applied still has a change on carries a disc on the top edge of its box, with the initial of the author of each such plan. Three discs show, and a number counts the rest. A click on a disc opens its plan in the Plans section. When two plans each have a change left on one resource, a solid ring in a colour of its own marks the box: the two plans overlap, and applying one makes the other stale, because the resource is then at another revision. A plan that has applied its change on a resource has no disc there and no overlap. A partly stale plan keeps its disc on a resource whose change it cannot apply, and that change does not count for an overlap. The inspector shows the same for the selected resource, as a cell, Plans, with a disc for each plan, and the card of a closed group carries the discs of the plans that change something inside it, so that a plan is not hidden by a group that is closed.

A plan whose objects cannot be read, because they are damaged or a newer pravo wrote them, is not listed. A warning above the graph names up to five of them, with a short reason, so that a person who looks for a colleague's plan can tell why it is not there.

## Withheld values

The viewer shows attribute values, and some of them are secrets. The state holds a sensitive value as the provider returned it, and the state is a store that you protect. A viewer that sent every value to a browser would copy those values into a browser's memory, history and extensions. The server therefore decides which values leave it, and a value that it withholds is not in the response. Nothing in the page can show it, because the page never received it.

A value is sent only when the provider schema says that it is not sensitive and not write-only. The rule follows the schema down through nested attributes and blocks. Three more rules apply.

- When the schema of a resource type cannot be read, or does not know an attribute, the value is withheld, because nothing says whether it is sensitive. The reason is `schema`.
- An attribute whose name contains `password`, `passwd`, `secret`, `token`, `private_key`, `credential`, `api_key` or `access_key`, in any letter case, is withheld whatever the schema says. A schema can omit a flag, and the name is a second guard. The reason is `name`.
- A value that the schema does not mark, but that holds a sensitive value of something the resource reads, is withheld. A file whose content embeds the password of a database has no flag, and the password is in it all the same. Sensitivity follows the references, attribute by attribute: a field of the specification that reads an attribute that is withheld, or a field that is itself derived, is withheld, whatever its text. The check is on what the field reads and not on what it spells, so a field that reads the name of a database is shown although the database has a password. The same field is also judged against the sensitive strings of its own resource, since a provider can echo a value into an attribute that it computes. A resource that the specification does not declare has no references to follow and is judged against the sensitive strings of what it reads. A string that is the same as a sensitive value elsewhere, such as a region that a parameter keeps in a sensitive attribute, says nothing of a resource that does not read it, and is shown. A data source can read a value that nothing refers to, so what reads one is judged against every sensitive string. The error of a specification that does not load, and what the template form sent, are not looked at for the values of the state: loading reads none. Strings of six characters or more count, and a type whose schema is unknown contributes every string of its records. The reason is `derived`.

What pravo recorded about a run is not a value of a resource and is not masked against values: the people, hosts, paths, plan ids and revisions that the history, the locks, the plans and the traces show are shown as they were written. The message of an error, the text of a log record and the reasons of a stale plan can quote a value, and are masked against every sensitive value of the state.

A withheld attribute is listed in the Sensitive group of the State tab with its name and type, and a locked mark where the value would be. It has no value. The mark says `withheld`, or `write-only` for an attribute that the provider never records, and its hover text, which a keyboard focus shows too, gives the reason. A null value is never withheld, since null says nothing. A sensitive output has no value either, and neither has an output whose value contains a sensitive string: the State tab of its node shows the same locked mark. Three fields of a record are never sent at all: the private blob that the provider keeps, the hash of the configuration and the configuration of the provider.

A stored plan is withheld by the same rules as a plan file, with the secrets of that plan: those of the state and the values that the plan would write. What a person wrote into it, the author, the reason and the reasons of a stale plan, is masked as the history of a record is. Its command line is masked too, and the values of `-t` and of flags named like a secret are withheld from it. The error text of a span in a recorded run is masked the same way. Each text is cut at 4 KiB.

The Source tab shows lines of the files that the evaluation loaded. A string literal that is assigned to a sensitive attribute, or to an attribute whose name matches the rule above, is masked in the lines, and the line is marked. A value that is computed, such as a reference to `random_password.result`, is shown as written: the line holds the reference and not the value.

Withholding is a rule about what the viewer sends. It is not a way to protect the state. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says where each kind of value is recorded, and [Signatures and roles](signatures-and-roles.md) says what sealing the state protects.

The viewer also bounds what it sends. A long list or map is cut and the response says how many elements it left out, one attribute is cut at 4 KiB, and a block of source is cut at 200 lines.

## Clusters

A group can hold many resources of one type, written by a comprehension. Drawing each of them would fill the graph with boxes that differ in a number. When a group holds eight or more resources of one mode and one type, the viewer draws them as one cluster: a box that names the type, counts the members and carries the marks of its members, so that one drifted member among twelve still shows. A click on the box opens the cluster and lists its members in place, in the group, and the layout makes room for them. A control under the members closes the cluster, and a long cluster shows its first 200 members with a control for the rest. Selecting a resource that sits in a cluster opens the cluster.

Resources of one type in different groups are not one cluster, because a group is the unit that you open and close.

## A sealed state

A state can be sealed to a list of readers. Its attributes, its outputs, the values in its stored plans, the secrets it keeps and the sensitive parts of its transactions are then encrypted, and the readers are named in a file that is kept in the repository, `.pravo/access/readers.txt`. The address, type, name, dependencies and revision of every record stay readable, so the graph and the plans work for someone who holds no key. The viewer reads a sealed state with the identity that you start it with. It finds the identity once, before it listens, and asks for the passphrase on the terminal if the identity has one. The key stays in the memory of the command. It is not in a response.

A record that the viewer cannot open is listed with its address, type, dependencies and revision, marked `sealed`, and has no value. The viewer does not guess at a value, and it drops the attributes of such a record before anything reads them. A person who is not a reader, or who started the viewer with no identity, sees the shape of the estate and none of its values. A stored plan that the viewer cannot open is laid over the graph all the same, from its summary: each change keeps its action, and the page says that the values of the plan are sealed.

With a readers file, the viewer also judges signatures. The History tab says who signed each transaction, the Plans section who stored each plan, and the Policies section who applied each version of a policy, each as `signed by NAME` when the signature verifies against a key of the file. A signature that does not verify is said so, and a version of a policy that is not trusted is not shown as in force. The viewer verifies and reports. It decides nothing: a writer that opens the state makes the same checks and refuses what fails them.

## The eight sections

The bar at the top switches between eight sections, and each keeps its own view in the address, so that a link reproduces it.

| Section | What it is for | What it does not do |
|---|---|---|
| Inventory | The graph and the list of everything the specification declares and the state records, with the inspector. | Makes no plan, writes no record. |
| Templates | The definitions of the specification that are templates, with a form that gives the CUE of a new instance, checked against the specification and the policies, and the instances that exist. | Applies nothing and writes no file. The CUE is shown and a person pastes it. |
| Plans | The plans stored in the state: who made each, where it stands, what it changes, and the command that applies it. | Applies, approves, claims and removes nothing. |
| Discovery | What the discoveries of the specification found, and which objects nothing manages, with the resource that adopts each and the entry that acknowledges it. | Runs no discovery, which would start a provider. |
| Policies | The policies in force, as signed versions, what the next apply adds, changes or removes, and the policy studio, which tries a rule on the resources and on a plan before it is stored. | Stores no policy. A rule you like still goes into the specification and is applied. |
| Traces | The runs that pravo recorded, each as a timeline of spans: what the run did, where its time went and where it stopped. | Records nothing. A run is recorded by the command that made it, with `PRAVO_TRACES=on`. |
| Secrets | The secrets that a sealed state keeps: each by name, with its latest version, who set it and when, and the nodes that read it, with a check that a signed record of a writer stands behind every version. | Shows no value, and holds no key that could open one. |
| State | Where the state is kept, how it is sealed, who can read it, what role each key has, and what needs doing, with the commands to copy. | Runs no command and edits no file. A line to add to the readers file is shown as a line, and you add it. |

[The sections of the viewer](../reference/viewer-sections.md) lists every part of each. Five of them deserve a word here, because each is the window on a concept of its own.

**Templates.** A template is a definition that says it is one, and an instance is an ordinary group of resources that carries the same mark. The viewer reads the inputs of the template from the definition itself, so the types, the defaults, the comments and the constraints of the fields are the form. When a person fills it in, the server loads the specification with the new CUE laid over it in memory, which is the check that the constraints hold, and tries each policy on an approximate plan, which says whether the apply would be refused. It shows the CUE and does nothing with it: the viewer changes nothing, so the paste is yours. An instance stays in the graph as any group does, and the mark ties it to its template, so that the catalog lists the instances of each. [The Templates section](../reference/viewer-sections.md#the-templates-section) lists the parts of the page.

**Discovery.** A discovery lists objects that exist and that no node of the graph is. The viewer does not put them in the graph, and it does not list them with the resources: a graph of what the specification declares and the state records would then hold nodes that nobody wrote, and a count of resources would include them. They have a section of their own, which reads the last run of each discovery from the state and sets it against the records, the `$import` declarations and the `$ack` entries of the specification, as [Discovery](discovery.md#what-each-object-is) describes. The unmanaged objects are shown in the amber that the rest of the page keeps for a change that is wanted, each with the means to adopt it, and the tab says how many ask to be adopted. [Filtering the objects](../reference/viewer-sections.md#filtering-the-objects) lists the columns of its filter.

**Policies and the studio.** The state keeps each policy as numbered, signed versions, and the section reads them with the specification, so it shows what is in force and what the next apply would write, line by line, with the applies each version judged. A rule is easier to write when you can see what it does to plans that exist, which is what the policy studio is for: an editor beside two answers. The first is about the resources that exist, held against the rule as if a plan updated each of them, which says what is not consistent with the policy today and will be refused when a plan first touches it. The second is about one plan, a stored plan or a plan file, shown in full with the part of it that broke the rule. Both follow the editor as you type. The server runs the rule in a CUE context of its own and writes nothing, so the state, the plans and the policies are as they were. [`pravo policy test`](../reference/policy.md) gives the same verdicts on the command line, and [the studio](../reference/viewer-sections.md#the-studio) lists the parts of the page.

**Traces.** A command that runs with `PRAVO_TRACES=on` writes its spans to `.pravo/traces` when it ends, and the section reads that directory. A run opens as a timeline: the command across the top, under it the setup, the engine and its stages, and under those each resource with the calls to its provider, each request to the state and each network call that a provider made or that pravo made to a state on the network. A span that failed is red, and the first red span is where the run stopped. The timeline shows where the time went and the table of the slowest spans says which part took it. [Traces](traces.md) explains what is recorded and what never is.

**Secrets.** A sealed state can keep secrets of its own: named values with a history, set with [`pravo secret`](../reference/secret.md) and read by a specification as ephemeral values ([Secrets and ephemeral values](secrets-and-ephemeral-values.md#secrets-of-the-sealed-state) says how). The section answers two questions without showing a value: which secrets does this state keep, and can what it says about them be trusted. It shows each secret by name, with the number of its latest version, who set it and when, and the nodes of the specification that read it. The server builds the page from the part of each version that is stored in the clear, which is the name, the number, the time and the transaction that wrote it, and from the transactions themselves, which it checks against the readers file and the roles of the state. It holds no key for a value and opens none, so a person who is not a reader sees the same page as a reader does. A version of a secret is a signed record like any other write to a sealed state, and the page says which versions have no record of a writer behind them: a transaction that is missing, a signature that does not verify, a key that may not write, or a key that the readers file does not list. Like the State section, it speaks only when there is something to say: a flagged block for each problem, worst first, with the command that mends it, to copy, and one line, `all clear`, when nothing is wrong. What is long or rare stays on the command line: the history of a secret, with who wrote each version and when, is `pravo secret history NAME`. A node that reads a secret is in the graph, in the first column with the environment variables, since it depends on nothing and feeds the nodes that use it. Its State tab names the secret and the version that the state holds, and shows no value. [The Secrets section](../reference/viewer-sections.md#the-secrets-section) lists the parts of the page and the problems it flags.

## The assistant

The viewer has one part that is not a section: an Ask panel, in the column of the inspector, that answers a question in words. It is off unless the viewer is started with a provider named. A language model reads the same views that the page does, through the same redaction, and answers with links into the page. It has no way to change anything, the command it suggests is text to copy, and each answer lists what was sent to the provider. [The assistant](the-assistant.md) says what it reads, what leaves the machine and how far an answer can be trusted.

## The command palette

The palette, opened with `/`, Ctrl-K or Cmd-K, searches everything the viewer knows: groups, resources and outputs first, then the stored plans, the transactions and the recorded runs, the objects that discoveries found, and the actions of the page. A plan opens in the Plans section, a run or a transaction in the Traces section, and an object in the Discovery section with the filter on it. An action runs at once and shows its key, so that the keyboard is learned from the palette. [Keys and links of the viewer](../reference/viewer-keys-and-links.md#the-command-palette) lists the keys.

## Why the viewer is read-only

The server writes nothing. It creates no record and no transaction, takes no lock, does not write `pravo.lock.cue` and does not generate a package. The project directory and the state hold the same bytes after a session as before it, a sealed state included.

Three consequences follow. You can leave the viewer open while someone else applies: it never contends for a lock, and it shows the locks that the apply holds. You can start it on any root that you can read, because starting it changes no file. And the page has no button that changes anything, since the server has no endpoint that would carry the request. Two requests are not a `GET`: a plan file that you drop on the policy studio is sent to the server, which judges it and holds it in memory, and a question to [the assistant](the-assistant.md) is sent to the server, which reads for it and relays it to the provider that you named. Neither writes a file or a record.

The viewer stays current without writing. It watches the state, the files that the evaluation loaded, the plan file, the plans stored in the state, the runs of the discoveries and the directory of recorded runs. When any of them changes, or the readers file does, it sends one event to the page, and the page fetches the graph, the list of plans and the open node again. The page says what changed, with how many resources changed, are new or are gone, and the resources that changed flash once.

A plan appears in the list when someone stores it. It becomes partial or applied when an apply from it records progress, which a failed apply does for the changes it finished. It becomes partly stale, and then stale, as something else changes the nodes that it expects. It leaves the list when a collection removes it. A plan that nobody applies expires without any write, so the viewer counts the moment that the nearest expiry of a plan passes as a change. When the plan that is laid over the graph is collected, a notice says so and the graph shows no plan. A run appears in the Traces section when the command that made it ends. When the resource that you were reading is gone, a notice says so and the inspector shows the project again. When the server stops, the page says that it is not answering, keeps the last picture and reads the project again when the server returns.

## Recap

- A node is one address with what the specification, the state and a plan know about it.
- A group is any struct that holds resources. Presence says whether a node is declared, recorded or both.
- The first view opens groups one level at a time, the ones that hold a change or a mark first, as far as the drawing stays readable. The rest are cards that a click opens.
- A stored plan is a plan that lives in the state. The viewer lists it, lays it over the graph and shows who else plans the same resource. It copies the command that applies it and applies nothing.
- The server withholds sensitive values, values of a type with no schema and values whose names say secret. A withheld value is not in the response.
- A cluster stands for many resources of one type in one group. Closing a group and clustering hide detail and remove no node.
- A sealed state is read with your identity, and a record that cannot be opened is listed with no value. The viewer verifies signatures and reports what it finds.
- The Secrets section lists the secrets that a sealed state keeps, who set each and what reads it, and checks that a signed record of a writer stands behind every version. It never shows a value, and the tab carries a number only when something waits for you.
- Seven sections: Inventory, Plans, Discovery, Policies with the studio, Traces, Secrets and State. Each reads one thing, and none of them writes.

Next: [Traces](traces.md) explains the runs that the Traces section shows.
