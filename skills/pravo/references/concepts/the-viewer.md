<!-- Generated from docs/concepts/the-viewer.md by tools/skillgen. Do not edit. -->

# The viewer

How pravo serve reads an estate: what a module is, what presence means, which values are withheld and why the viewer changes nothing.

`pravo serve` shows an estate in a browser. It joins three things by address: the specification, which it evaluates, the state, which it reads, and optionally a saved plan. The result is one graph of modules and resources. This page explains how the viewer builds that graph and what it leaves out. [Explore an estate in a browser](../how-to/explore-your-estate.md) shows the steps, and [`pravo serve`](../reference/serve.md) is the reference.

## One address, three sources

A resource has one address, such as `api.services.billing.config`. The specification says where it is written. The state says whether it was created and what the provider returned. A saved plan says what the next apply does to it. The viewer keeps one node for each address and attaches what each source knows.

| Source | What the node gets from it |
|---|---|
| Specification | Whether the resource is declared, the file and line where it is written, the edges that its references make. |
| State | The record: attributes, revision, the times of the last apply and the last read, the marks that a read left, locks and the transactions that wrote it. |
| Saved plan | The action for the node, and the attributes that the action changes. |

A node is a resource, a data source, an ephemeral resource or a function call. The viewer shows what the specification declares and what the state records, so a node can come from either one or from both.

## Modules

A module, to the viewer, is a struct of the specification that holds resources. It is an instance of a definition, an instance of a package, or a plain struct. It does not need to take providers as inputs, and it does not need a definition: in the platform estate, `network.zones` is a plain struct that holds three instances of one definition, and `network` holds both.

The address of every node below a module starts with the path of the module. The viewer treats every proper prefix of a node's address that holds a resource below it as a module, so `api.services.billing.config` sits in `api.services.billing`, which sits in `api.services`, which sits in `api`. The server sends this tree, with the parent of each module and the number of resources below it, and the page never splits an address.

*Figure: The address api.services.billing.config and the three modules that are its prefixes. The viewer draws each module as a box that holds what is below it.*

Each module can carry two places in the source. One is where the instance is written. The other is where the definition that it instantiates is written, when that can be told. The Source tab of a resource shows both.

A module has two states in the viewer. An open module is a lane: a frame titled with the name of the module and the number of resources below it, which holds its resources and its nested modules. A closed module is one box, a card, that shows the name, the number of resources, the marks on them and one small cell for each resource, coloured by what a saved plan does to it or by the mark that it carries. The edges that cross a closed module join its card. Closing hides detail and removes no node: a search still finds a resource inside a closed module and opens the modules that hide it.

## The first view

An estate of a few hundred resources drawn all at once is a picture that nobody reads, so the page chooses which modules to open when it loads. It starts with every module closed and opens them one level at a time: the top-level modules first, then the modules inside the ones that opened. A module opens only when the drawing that follows still fits the canvas at 80 percent of full size or more, so that the smallest names stay readable, and when the number of boxes stays within a limit that grows with the size of the canvas. A larger window opens more.

*Figure: The first view in three steps. Every module starts as a card. The module that holds a change opens first, then the next level opens, for as long as the drawing fits the canvas. network and edge stay cards.*

The choice depends on the size of the canvas and on what the plan, the drift and the state say. It does not depend on the selection, a filter or a search, so a reload of the page in the same window opens the same modules. The order inside a level does not change. Modules that hold something to look at open first: a resource that a saved plan changes, a drifted or gone resource, an orphan, or a resource that is not created yet. Larger modules open before smaller ones. Peers open together or not at all, so that three regions of one shape are all open or all closed and the picture does not show one region in detail beside two boxes. A module that does not fit stays a card, and so does everything below it.

A link applies on top of this choice. The `collapse` and `expand` parameters name modules relative to it, and a link that selects a resource or a module opens the modules above it, unless `collapse` names one of them. A selection that you make in the page does the same. After the first view, a click on a card opens the module and moves the view to it. The Zoom out button closes it again and returns to the view before, and so does Esc while the module is the selection. The Reset view button, or `0`, returns to the first view itself. [`pravo serve`](../reference/serve.md#the-address-of-a-view) lists the parameters.

## Presence

Presence says where a node is written.

| Presence | Meaning | The next apply |
|---|---|---|
| `both` | Declared in the specification and recorded in the state. | Compares it with the specification and changes it when they differ. |
| `spec` | Declared and not recorded. | Creates it. |
| `state` | Recorded and not declared. The node is an orphan. | Destroys it. |

*Figure: Presence. A node that the specification declares and the state records is both. A node in one source only is spec or state.*

The page words presence as the state of the record. A node that is `both` and has no mark reads `in sync`. A node that is `spec` reads `declared only`, and the Not created chip counts it. A node that is `state` reads `orphan`, and the Orphans chip counts it.

A specification that does not load cannot say what is declared. The viewer then shows the state alone, marks every node `state` and shows the error from the specification as a warning. The nodes are still the records that exist.

Presence is separate from the marks that a read leaves on a record. A node is drifted when a read found that the real object differs from the record. It is gone when a read found that the object is absent. A node can also be locked, when a lock or a reservation holds it. The Drifted chip counts the drifted nodes and the gone nodes together, and the Record cell of a node tells them apart. These marks come from `pravo drift` and from the refresh of `pravo plan`; the viewer reads them and sets none.

## Edges

An edge runs from a dependency to its dependent, the direction in which data flows. The edges come from the references in the specification where it loads, and from the records of the state where it does not. A node lists what it depends on and what uses it, and the inspector also lists everything that a change to the node can reach: its transitive dependents, as the state records them.

## The plan overlay

`pravo serve --plan FILE` reads a saved plan. The viewer shows the action of the plan on each node that the plan ran: create, update, replace, delete, read, move, import or no-op. A node that the plan did not run reads `not read`: the plan did not look at it. The Change tab of a node lists the attributes that the plan changes, with the value before and after. With nothing selected, the inspector shows the numbers that the plan's summary reports, how many of the nodes it ran and the hints that `pravo plan` printed. A plan that cannot be applied is marked stale, with its reasons.

The viewer does not make a plan. A plan is the result of a refresh, a policy and a lock, and each of those is a command that changes something.

## Withheld values

The viewer shows attribute values, and some of them are secrets. The state holds a sensitive value as the provider returned it, and the state is a store that you protect. A viewer that sent every value to a browser would copy those values into a browser's memory, history and extensions. The server therefore decides which values leave it, and a value that it withholds is not in the response. Nothing in the page can show it, because the page never received it.

A value is sent only when the provider schema says that it is not sensitive and not write-only. The rule follows the schema down through nested attributes and blocks. Three more rules apply.

- When the schema of a resource type cannot be read, or does not know an attribute, the value is withheld, because nothing says whether it is sensitive. The reason is `schema`.
- An attribute whose name contains `password`, `passwd`, `secret`, `token`, `private_key`, `credential`, `api_key` or `access_key`, in any letter case, is withheld whatever the schema says. A schema can omit a flag, and the name is a second guard. The reason is `name`.
- A value that the schema does not mark, but that contains a string which the state keeps in a sensitive attribute, is withheld. A file whose content embeds a password has no flag, and the password is in it all the same. Strings of six characters or more count, and a type whose schema is unknown contributes every string of its records. The reason is `derived`.

A withheld attribute is listed in the Sensitive group of the State tab with its name and type, and a locked mark where the value would be. It has no value. The mark says `withheld`, or `write-only` for an attribute that the provider never records, and its hover text, which a keyboard focus shows too, gives the reason. A null value is never withheld, since null says nothing. A sensitive output has no value either, and neither has an output whose value contains a sensitive string. Three fields of a record are never sent at all: the private blob that the provider keeps, the hash of the configuration and the configuration of the provider.

The Source tab shows lines of the files that the evaluation loaded. A string literal that is assigned to a sensitive attribute, or to an attribute whose name matches the rule above, is masked in the lines, and the line is marked. A value that is computed, such as a reference to `random_password.result`, is shown as written: the line holds the reference and not the value.

Withholding is a rule about what the viewer sends. It is not a way to protect the state. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says where each kind of value is recorded.

The viewer also bounds what it sends. A long list or map is cut and the response says how many elements it left out, one attribute is cut at 4 KiB, and a block of source is cut at 200 lines.

## Clusters

A module can hold many resources of one type, written by a comprehension. Drawing each of them would fill the graph with boxes that differ in a number. When a module holds eight or more resources of one mode and one type, the viewer draws them as one cluster: a box that names the type, counts the members and carries the marks of its members, so that one drifted member among twelve still shows. A click on the box opens the cluster and lists its members in place, in the module, and the layout makes room for them. A control under the members closes the cluster, and a long cluster shows its first 200 members with a control for the rest. Selecting a resource that sits in a cluster opens the cluster.

Resources of one type in different modules are not one cluster, because a module is the unit that you open and close.

## Why the viewer is read-only

The server writes nothing. It creates no record and no transaction, takes no lock, does not write `pravo.lock.cue` and does not generate a package. The project directory and the state hold the same bytes after a session as before it.

Three consequences follow. You can leave the viewer open while someone else applies: it never contends for a lock, and it shows the locks that the apply holds. You can start it on any root that you can read, because starting it changes no file. And the page has no button that changes anything, since the server has no endpoint that would carry the request.

The viewer stays current without writing. It watches the state, the files that the evaluation loaded and the plan file. When any of them changes, it sends one event to the page, and the page fetches the graph again, and the open node with it. The page says what changed, the state, the specification, the plan or a lock, with how many resources changed, are new or are gone, and the resources that changed flash once. When the resource that you were reading is gone, a notice says so and the inspector shows the project again. When the server stops, the page says that it is not answering, keeps the last picture and reads the project again when the server returns.

## Recap

- A node is one address with what the specification, the state and a saved plan know about it.
- A module is any struct that holds resources. Presence says whether a node is declared, recorded or both.
- The server withholds sensitive values, values of a type with no schema and values whose names say secret. A withheld value is not in the response.
- The first view opens modules one level at a time, the ones that hold a change or a mark first, as far as the drawing stays readable. The rest are cards that a click opens.
- A cluster stands for many resources of one type in one module. Closing a module and clustering hide detail and remove no node.
- The viewer reads the project and the state and writes neither.

Next: [Explore an estate in a browser](../how-to/explore-your-estate.md) uses the viewer on an estate of three modules.
