---
title: Explore an estate in a browser
description: "Start pravo serve, read the graph, find a resource, open and close modules, read the source, overlay a saved plan, see what your team has planned, find drift and share a view."
order: 11
group: Operate
minutes: 15
needs: A root that you have initialised and applied. The commands are for a POSIX shell.
done_when: "The viewer shows your estate, a link to a view reproduces it, and you know which resources a saved plan changes."
level: intermediate
keywords: "viewer graph ui dashboard visualise visualize browser teammates stored plans"
---

`pravo serve` shows the specification, the state and a saved plan of one root as a graph in a browser. It reads and never writes. This page starts the viewer, reads the graph, finds a resource, opens and closes modules, reads the source of a resource, overlays a plan, reads the plans that your team stored, finds drift and shares a view. [The viewer](../concepts/the-viewer.md) explains what the viewer shows, and [`pravo serve`](../reference/serve.md) lists its flags, keys and parameters.

The pictures come from the `platform` estate. It has 47 resources in three modules, `network`, `api` and `edge`, built from the `random`, `local` and `null` providers, so it needs no cloud account. `api.services.billing` is an instance of the definition `#Service`, which a second package, `kit`, defines. The estate is in `tools/serveshots/scenarios/platform` of the pravo source tree, and the steps work in any root.

The pictures of steps 6, 8 and 10 were taken after three edits: a resource added to the specification, a resource removed from it and a port changed. Those pictures count 48 resources. One is declared and not created yet, and one is recorded and not declared.

## 1. Start the viewer

Run `pravo serve` in the root. It starts a server on the loopback interface, prints the address and opens it in the default browser.

```sh
pravo serve
```

The address carries a token. The first request sets a cookie and redirects to a page without the token, so the token does not stay in the address bar. The server accepts requests from this machine only, and every request for data needs the cookie. A page that is opened without the token asks for the address that `pravo serve` printed. `--no-open` prints the address and opens nothing, `--port N` picks the port, and `--json` prints the address as one line of JSON for a script.

Stop the viewer with Ctrl-C. It has written nothing, so there is nothing to clean up. A page that stays open says that the viewer is not answering. A new run makes a new token, so that page asks for the new address.

## 2. Read the graph

:::figure The platform estate as the viewer opens it, in the theme of the site. The sidebar lists the filters and the modules, the graph draws the modules that fit as lanes and the others as cards, and the inspector shows the project.
<a class="shot shot-dark" href="../img/serve/overview-dark.png"><img src="../img/serve/overview-dark.png" width="1440" height="900" alt="The pravo viewer as it opens. The sidebar lists the filters and the modules network, api and edge. The graph draws edge, api and network as lanes that hold cards for the modules inside them, joined by arrows. Nothing is selected, so the inspector shows the project: 47 resources, 19 modules, no drift and 13 with secrets, then the state, the providers and the outputs." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/overview-light.png"><img src="../img/serve/overview-light.png" width="1440" height="900" alt="The pravo viewer as it opens. The sidebar lists the filters and the modules network, api and edge. The graph draws edge, api and network as lanes that hold cards for the modules inside them, joined by arrows. Nothing is selected, so the inspector shows the project: 47 resources, 19 modules, no drift and 13 with secrets, then the state, the providers and the outputs." loading="lazy"></a>
:::

The page has a bar at the top and three columns under it. The bar names the project directory and the state, switches between Graph and Table, opens the search, says that the page is read-only and where it is served, and switches the theme. The sidebar on the left holds the filters, under the heading Show, and the tree of modules. The graph is in the middle, with its toolbar. The inspector on the right shows the selected resource or module, or the project when nothing is selected, as in the picture.

The graph has one box for each resource and one lane for each open module. A lane is titled with the name of its module and the number of resources below it. A module that is closed is a card: its name, its resource count, the marks on its resources and one small cell for each resource. An arrow runs from a dependency to the resource that uses it, so data flows along the arrows. Select a resource, and its dependencies and dependents stay bright while the rest of the graph dims. Lines to what it depends on are blue, and lines to what uses it are amber. Rest the pointer on a box for a moment, or move to it with the arrow keys, and a small card gives its address, its type, its marks and how many dependencies and dependents it has.

The legend at the foot of the graph names the colours of the actions when a plan is loaded, the two kinds of line, the lock of a sensitive value and the mark of a drifted one. The toolbar above the graph says how many boxes it draws when that differs from the number of resources. Drag to move the picture, turn the wheel or pinch to zoom, and use the minus, Fit and plus buttons for the same. When the drawing is larger than the window, a small map appears in the corner: click or drag in it to move.

The inspector heads with the name of the resource, the modules that it sits in, each a link, its type and its provider. A strip under the head has the cells Record, Revision and Refreshed, Lock when a lock holds the resource, and Plan when a plan is loaded. The Record cell says `in sync`, `drifted`, `gone`, `declared only` or `orphan`. The tabs are State, Source, Schema and History, and Change when a plan is loaded.

The State tab lists the attributes in three groups, Configured, Sensitive and Computed, in the order that the provider schema gives them, each with its type and whether it is required, optional or computed. The Computed group stays closed until you turn on the switch named computed. A filter box above the groups matches names and values. Under the attributes are what the resource depends on, what uses it, everything that a change to it can reach, with a button, Show in graph, that draws the reach, and the record: its revision and the times of its last read and its last apply. The Schema tab lists the attribute definitions of the resource type with the descriptions that the provider gives. The History tab lists the transactions that wrote the resource.

With nothing selected, the inspector shows the project: the state location, its lineage and record count, the providers, the policies and the outputs. Each provider is listed with its source and version, and with its instances, in their colours, when the root configures it more than once. A sensitive output has no value. Select a module, in the graph or the sidebar, and the inspector shows its counts, its resource types, its sub-modules and the places where it is written.

The status bar at the foot shows the state that the viewer reads. It says whether a plan is loaded, the revision of the data, when the viewer last read it and whether the connection that pushes changes is live.

## 3. Find a resource

Press `/`, or Ctrl-K or Cmd-K, to open the palette, or choose the search button in the bar. Type a part of an address, a type or the name of a module. The palette lists the modules and the resources that match, the up and down arrows move, and Enter goes to the one that is highlighted. With no text, it lists the top-level modules and, when a plan is loaded, the resources that it changes.

:::figure The palette with the query `billing`. It lists the module `api.services.billing` and the six resources whose addresses contain the word.
<a class="shot" href="../img/serve/palette-dark.png"><img src="../img/serve/palette-dark.png" width="1440" height="900" alt="The viewer with the search palette open over the graph. The query billing finds one module, api.services.billing with 5 resources, and six resources: edge.routes.billing and the five resources of api.services.billing." loading="lazy"></a>
:::

The box above the graph, Filter by address or type, narrows what the graph and the table show to the matches, and says how many it found. The chips under Show filter by what the viewer knows about a resource: All, In plan, Drifted, Sensitive, Orphans and Not created. In plan counts the resources that a saved plan changes. Drifted counts the drifted and the gone. Sensitive counts the resources that hold withheld values, Orphans the resources that are recorded and not declared, and Not created the resources that are declared and not recorded yet. Each chip carries the number of resources that it matches, and a chip that matches none is dimmed. Choose a chip, and the sidebar lists only the matches, with the modules that hold them open, while the graph dims the rest.

## 4. Open and close modules

A graph of a hundred boxes is too much to read, so the page opens the estate at a size that you can read: modules that hold a change or a mark first, then the larger ones, as far as the drawing stays legible. [The viewer](../concepts/the-viewer.md#the-first-view) describes the choice. The modules that it did not open are cards.

:::figure The platform with `network` and `edge` closed by a link. Each is one card with the number of its resources, and the arrows that cross them end at the cards.
<a class="shot shot-dark" href="../img/serve/modules-collapsed-dark.png"><img src="../img/serve/modules-collapsed-dark.png" width="1440" height="900" alt="The viewer with the modules network and edge drawn as cards that say 13 and 11 resources. The module api is open: its services lane holds the open module billing and the cards orders and search, and its cache and database are cards." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/modules-collapsed-light.png"><img src="../img/serve/modules-collapsed-light.png" width="1440" height="900" alt="The viewer with the modules network and edge drawn as cards that say 13 and 11 resources. The module api is open: its services lane holds the open module billing and the cards orders and search, and its cache and database are cards." loading="lazy"></a>
:::

Click a card to open the module. The view moves to it, and the Zoom out button, or Esc, closes it again and returns to the view that you had. To close an open module, click the arrow in the title of its lane, or double-click the title. Collapse all and Expand all act on every module, and `[` and `]` do the same from the keyboard. A selection inside a module that you close moves to the module. Reset view, or `0`, goes back to the first view: the modules that were open when the page loaded, no cluster open and the whole graph in the window.

Closing hides detail and removes no resource. The sidebar still lists the resources of a closed module. Selecting one in the sidebar, in the palette or through the address opens the modules that hide it.

A large estate needs the same step many times. The `fleet` estate has 143 resources in three regions, each with a production and a staging environment. Here one region is open and the others are cards, and the module `eu.prod` is selected.

:::figure The `fleet` estate with the region `eu` open and its production environment selected. The three services of the production environment are open beside its secret and its manifest, and its pool of nodes is a card. The staging environment and the regions `us` and `ap` are cards.
<a class="shot" href="../img/serve/modules-expanded-dark.png"><img src="../img/serve/modules-expanded-dark.png" width="1440" height="900" alt="The viewer showing the fleet estate with the module eu.prod selected. The region eu is open and its production environment is open inside it, with the services api, jobs and web as lanes of three resources each, a secret, a manifest and a card for the pool nodes. The staging environment and the regions us and ap are cards. The inspector shows the module: its counts, its resource types and its sub-modules." loading="lazy"></a>
:::

A module that holds eight or more resources of one type draws them as one cluster: a box that names the type and the count. Click the box to open the cluster and list its members in place, in their module. The control under the members, `group of 12`, closes it again.

:::figure The pool `eu.prod.nodes` open in place. Its twelve resources of the type `null_resource` are listed in the module, and `n04` is selected.
<a class="shot" href="../img/serve/clusters-dark.png"><img src="../img/serve/clusters-dark.png" width="1440" height="900" alt="The viewer with the cluster of null_resource nodes in the production environment of eu open. The twelve members n01 to n12 stand in two columns inside their module, with a control under them that closes the group, and the node n04 is selected, with an arrow from the resource eu.zone that it depends on." loading="lazy"></a>
:::

## 5. Read the source of a resource

Select a resource and open the Source tab. It shows the lines that write the resource. A resource that comes from a definition has two places: the definition, and the instance that fills it in.

:::figure The Source tab of `api.services.billing.config`. The first block is the definition in `kit/kit.cue`, and the second is the instance in `api.cue`.
<a class="shot shot-dark" href="../img/serve/source-dark.png"><img src="../img/serve/source-dark.png" width="1440" height="1000" alt="The Source tab in the inspector. Under Where it is declared are the lines of the config resource in kit/kit.cue. Under Where it is instantiated are the lines of billing in api.cue. A note says that the same lines write three nodes, and a list under What shapes it names both files." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/source-light.png"><img src="../img/serve/source-light.png" width="1440" height="1000" alt="The Source tab in the inspector. Under Where it is declared are the lines of the config resource in kit/kit.cue. Under Where it is instantiated are the lines of billing in api.cue. A note says that the same lines write three nodes, and a list under What shapes it names both files." loading="lazy"></a>
:::

The two files that the figure shows are these. The lines of `config` are marked in the definition, and `billing` is marked in the file that instantiates it.

:::tabs The files behind the Source tab
```cue title="platform/kit/kit.cue" hl="21-24"
// #Service is one service of the api: an identity, a key that the service
// presents to the database, its rendered configuration and the step that
// deploys it. The key is sensitive, so the file that holds it is too.
#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider
	steps!:  nul.#Provider

	name!:     string
	port!:     int & >1024 & <65536
	database!: string
	subnet!:   int

	id: random.#Resource.random_id & {byte_length: 4}

	api_key: random.#Resource.random_password & {
		length:  32
		special: false
	}

	config: local.#Resource.local_file & {
		filename: "out/api/\(name).json"
		content:  "{\"service\": \"\(name)\", \"id\": \"\(id.hex)\", \"port\": \(port), \"database\": \"\(database)\", \"subnet\": \(subnet)}\n"
	}

	env: local.#Resource.local_sensitive_file & {
		filename: "out/api/\(name).env"
		content:  "SERVICE=\(name)\nAPI_KEY=\(api_key.result)\n"
	}

	deploy: steps.#Resource.null_resource & {
		triggers: {
			config_hash: config.content_sha256
			service_id:  id.hex
		}
	}
}
```

```cue title="platform/api.cue" hl="5-13"
api: {
	...
	// Each service is an instance of the definition in kit/kit.cue.
	services: {
		billing: kit.#Service & {
			random:   $random
			local:    $local
			steps:    $null
			name:     "billing"
			port:     8081
			database: api.database.dsn.filename
			subnet:   network.zones.a.subnet.result
		}
		...
	}
}
```
:::

The definition is the part of the estate that several resources share, so the tab says how many nodes the same lines write. A line that assigns a string literal to a sensitive attribute is masked, and the line carries a mark. A file that changed after the state was written carries a note, since its lines may differ from what created the resource. Under the blocks, What shapes it lists the files, the cached modules, the tags and the policies that contribute to the resource. The tab shows only files that the evaluation loaded.

## 6. Overlay a saved plan

The viewer does not make a plan. Save one with `pravo plan --out`, and start the viewer with it.

```sh
pravo plan --out change.plan
pravo serve --plan change.plan
```

A resource that the plan changes carries a badge and a ring in the colour of its action, and the legend names create, update, replace, delete and not read. A resource that the plan did not run reads `not read`. The In plan chip counts the resources that the plan changes, and the status bar says how many of the resources the plan ran, as `planned 7 of 48`, and how many it did not read. With nothing selected, the inspector shows the summary of the plan: the resources to add, change, destroy and read, the blast radius, the file and when it was made. A plan that cannot be applied, because the specification or the state changed after it was made, is marked stale and shown with the reasons.

:::figure The platform with a saved plan, with `network` and `edge` closed. The port of `orders` changed, which replaces its configuration, its deploy step and its route. The Change tab of `api.services.orders.config` shows the action, the reasons and the attribute that forces the replacement.
<a class="shot shot-dark" href="../img/serve/plan-dark.png"><img src="../img/serve/plan-dark.png" width="1440" height="900" alt="The viewer with a saved plan loaded. The module api is open, with the module orders open inside its services lane: its resources config and deploy carry the badge of the action replace. The cards for network and edge show the counts of the changes inside them, and the status bar says planned 7 of 48. The inspector shows the Change tab of api.services.orders.config: the action replace, the reasons the plan gives, the attribute content that forces the replacement, and the content before and after the change." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/plan-light.png"><img src="../img/serve/plan-light.png" width="1440" height="900" alt="The viewer with a saved plan loaded. The module api is open, with the module orders open inside its services lane: its resources config and deploy carry the badge of the action replace. The cards for network and edge show the counts of the changes inside them, and the status bar says planned 7 of 48. The inspector shows the Change tab of api.services.orders.config: the action replace, the reasons the plan gives, the attribute content that forces the replacement, and the content before and after the change." loading="lazy"></a>
:::

The Change tab lists the reasons that the plan gives, the attributes that force a replacement and each attribute that changes with its value before and after. A value that the viewer withholds is withheld in the plan too. A value that is not known until the apply reads `known after apply`.

## 7. See what your team has planned

`pravo plan --store` writes a plan into the state, so that a teammate can read it before anyone applies it. [Share a plan before you apply it](share-a-plan-before-you-apply-it.md) follows two people through it. The viewer reads the state, so it lists those plans without a flag. The pictures of this step come from a state in which `ana` and `ben` stored plans for the same resources, and `ci` stored one that expired.

```sh
pravo plan --store --actor ana --reason "move orders to port 9082"
pravo serve
```

The sidebar has a section, Plans, when the state holds a plan. Each row is one plan, newest first: the initials of the author, the note that the author wrote with the plan (`--reason`) as the title, or the id when there is none, the status, the author, the age, the id under a note, and the counts of resources to add, change and destroy. Pending lists the plans that can be applied. A mark of two rings on a row says that another plan changes one of the same resources.

Click a row. The plan is laid over the graph, as a saved plan is in step 6. The graph shows its actions, the Change tab shows its attributes, and a bar above the graph names it. The row opens and shows the reason, what overlaps, and the command that applies the plan, with a button that copies it. A second click on the row, Esc in the list or the button in the bar takes the plan off the graph.

:::figure The platform with the plan of `ana` laid over the graph. The plan of `ben` also replaces what writes `orders`, so those resources carry two discs and a ring, and the sidebar says that the plans overlap.
<a class="shot shot-dark" href="../img/serve/plans-dark.png"><img src="../img/serve/plans-dark.png" width="1440" height="900" alt="The viewer with a stored plan laid over the graph. The sidebar section Plans lists two pending plans, each titled with the note its author wrote: new ports for orders and search by ben, and move orders to port 9082 by ana, with the plan id in small type under the title. The plan of ana is selected: its row shows a box that says the plans overlap and names the plan of ben, a closed part for the expiry and the command, and the command pravo apply with a button that copies it. A bar above the graph names the plan with the badge A, the chip pending and the counts +3 and -3. In the graph the resources config and deploy of api.services.orders carry the badge of the action replace, a ring, and the discs A and B. The inspector shows the Change tab of config. A Plans cell says that 2 open plans change it, and the attribute content is marked as forcing replacement." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/plans-light.png"><img src="../img/serve/plans-light.png" width="1440" height="900" alt="The viewer with a stored plan laid over the graph. The sidebar section Plans lists two pending plans, each titled with the note its author wrote: new ports for orders and search by ben, and move orders to port 9082 by ana, with the plan id in small type under the title. The plan of ana is selected: its row shows a box that says the plans overlap and names the plan of ben, a closed part for the expiry and the command, and the command pravo apply with a button that copies it. A bar above the graph names the plan with the badge A, the chip pending and the counts +3 and -3. In the graph the resources config and deploy of api.services.orders carry the badge of the action replace, a ring, and the discs A and B. The inspector shows the Change tab of config. A Plans cell says that 2 open plans change it, and the attribute content is marked as forcing replacement." loading="lazy"></a>
:::

Look for the discs before you apply. Each disc on a resource is the initial of the author of a plan that can be applied and changes it. A resource with two discs and a ring is one that two people have planned and not applied. A plan that has applied its change on a resource has no disc there. Applying one of the plans makes the other stale, or partly stale when its other changes can still be applied, because the resource is then at another revision. The Plans cell of the resource, above the tabs, lists the plans, and a click on a disc lays that plan over the graph. The legend names `plan author` and `overlap`.

A chip that says `partly stale` marks a plan that the state moved on under in some of its changes. The row lists the conflicts, one reason each, and the command that ends in `--only` with the changes that can still be applied. Copy that command, and not `pravo apply ID`, which the whole plan is refused by. A resource whose change conflicts keeps the disc of the plan and draws no ring. The Plans cell says that the plan cannot apply the change, and the Change tab gives the reason.

The viewer applies nothing and has no control that approves or removes a plan. Copy the command, and run it in a checkout that has the files the plan was made from. The status that the list shows comes from the state alone. `pravo apply` also compares the configuration and the providers, and refuses the plan when they differ.

Choose All to list the plans that cannot be applied: the stale, the applied and the expired, until a collection removes them. A plan that cannot be applied has no command to copy, and none of its resources carries a disc.

:::figure The plan of `ci` under All. It expired two days ago. Its row has a note instead of a command, and the inspector shows the plan: its counts, who made it, when it expired, the reason and the command that made it.
<a class="shot" href="../img/serve/plan-expired-dark.png"><img src="../img/serve/plan-expired-dark.png" width="1440" height="900" alt="The viewer with the plan of ci laid over the graph after it expired. The list is set to All and shows three plans. The plan titled listen on 8443 at the edge has the chip expired in a dashed outline, and its row says that the plan expired and will be collected. The graph shows the two resources of edge.balancer, config and reload, that the plan replaces. The inspector shows the stored plan: 2 of 47 resources planned and 45 not read, 2 to add and 2 to destroy, who made it, when it expires, the reason and the command." loading="lazy"></a>
:::

Leave the viewer open and the list follows the state. A plan that a teammate stores gets a row, and a notice names it. The status of a plan changes when an apply from it records progress, and when something else changes a resource it expects. A plan that is collected leaves the list, and a plan that expires without an apply moves to All. When the plan that is laid over the graph is collected, a notice says so and the graph shows no plan.

`pravo serve --plan ID` starts the viewer with a stored plan laid over the graph. Give the same `-t` and `-T` that you give to `pravo plan list`. The fragment of the address names the plan: `plan=plan-2f9c1a4e` lays that plan, and `plan=none` takes off the one that `--plan ID` laid. [The viewer](../concepts/the-viewer.md#stored-plans) explains what each mark means, and [`pravo serve`](../reference/serve.md#stored-plans) lists the keys and the endpoints.

## 8. Find drift

`pravo drift` reads every resource and marks the records whose real object differs or is gone. The viewer shows the marks. Choose the Drifted chip to see only those resources.

```sh
pravo drift
```

:::figure The Drifted filter. The file behind `network.dns.records` was edited outside pravo, and the file behind `api.cache.config` was deleted. The provider `local` reports both as gone.
<a class="shot" href="../img/serve/drift-dark.png"><img src="../img/serve/drift-dark.png" width="1440" height="900" alt="The viewer with the Drifted chip chosen. The sidebar lists only api.cache.config and network.dns.records, the graph dims every other resource, and the filter box says 2 of 48. The inspector shows network.dns.records with the Record cell set to gone." loading="lazy"></a>
:::

The viewer reads the marks from the state. It does not read the real objects, so it shows the drift that a command found and does not look for more. Run `pravo drift` again, and the viewer updates when the state changes: a notice says that the state changed and how many resources differ, and the resources that changed flash once.

## 9. See what the viewer withholds

Select a resource that holds a secret. The State tab lists its sensitive attributes in the Sensitive group, with a name and a type and a locked mark where the value would be. Hover over the mark, or move the keyboard focus to it, for the reason.

:::figure The State tab of `api.database.dsn`, the file that holds the connection string of the database. The attributes `content` and `content_base64` are listed and have no value.
<a class="shot" href="../img/serve/sensitive-dark.png"><img src="../img/serve/sensitive-dark.png" width="1440" height="900" alt="The inspector showing the State tab of api.database.dsn, with a badge that says 1 sensitive. The Sensitive group lists content and content_base64, each with a locked mark that says withheld where the value would be." loading="lazy"></a>
:::

The server removes the value before it answers. The page never has it, and a copy of the page's data holds no secret. The server also withholds a value that contains a secret although the schema does not mark it: `api.cache.config` is a plain file that embeds the password of the cache, and its content is withheld too. [The viewer](../concepts/the-viewer.md#withheld-values) lists the rules.

## 10. Share a view by link

The viewer keeps its view in the fragment of the address: the selected resource or module, the tab, the modules that you opened or closed, the filter, the search text, the view and the theme that you chose. Copy the address to share the view.

```text
http://127.0.0.1:PORT/#/?sel=api.services.billing.config&tab=source&collapse=network,edge&theme=dark
```

A link reproduces the view on a viewer that shows the same estate. The fragment is everything after `#`. Send it, and the other person pastes it after the address of their own viewer, because each viewer has its own port and token. The table in [`pravo serve`](../reference/serve.md#the-address-of-a-view) lists every parameter.

`motion=off` turns every animation off. Use it when you take a picture. The pictures on this page were taken from such links by `tools/serveshots`.

The theme button in the bar, or `t`, switches between the light and the dark theme. Without a `theme` parameter the viewer follows the system and the last choice that you made in the page.

The Table button switches from the graph to a table, and `g` switches back. The table has a row for each resource and the columns Address, Type, Provider, Mode, Status, Plan, Rev and Refreshed. The Plan column shows what the loaded plan does to the resource, and it is left out when no plan is loaded. The address starts with the module, so there is no column for it. Provider is the provider instance that the resource uses, such as `aws` or `aws.west`. The graph and the table colour each instance, in the tile of a card and in the dot in front of the name: the instances of one estate get different colours, up to twelve of them, and the same instance has the same colour in both. A narrow table leaves out Mode and Rev. Click a heading to sort by it, and click it again to reverse the order. The up and down arrows move the selection. The filters apply to the table as they do to the graph, and the filter box also matches the action and the mode.

:::figure The Table view of the same estate: one row for each resource. The statuses show `gone` for `api.cache.config` and `orphan` for `edge.balancer.reload`.
<a class="shot" href="../img/serve/table-dark.png"><img src="../img/serve/table-dark.png" width="1440" height="900" alt="The viewer in the Table view. A table lists the resources with the columns Address, Type, Provider, Status and Refreshed. The Provider column names the provider instance of each resource, here local, null and random. The Status column shows the number of sensitive values, the mark gone and the mark orphan. The inspector shows the selected resource." loading="lazy"></a>
:::

Below 1100 pixels the sidebar becomes a drawer that the menu button in the bar opens, and the inspector is a panel under the graph. The button at the right of the toolbar shows or hides the panel, and the button in the corner of the panel makes it taller.

:::figure A window of 900 pixels. The graph keeps the top of the window, and the inspector is a panel under it.
<a class="shot" href="../img/serve/narrow-dark.png"><img src="../img/serve/narrow-dark.png" width="900" height="1100" alt="The viewer in a narrow window. The graph fills the top of the window with a menu button at the left of the bar, and the inspector of the selected resource is a panel under the graph." loading="lazy"></a>
:::

## 11. Use it in a container or over SSH

The viewer listens on the loopback interface of the machine that runs it, and no flag changes that. A browser on another machine cannot reach it directly.

To view an estate on a remote host, forward a port over SSH. Start the viewer on the host with a fixed port, and forward the same port number to your machine, so that the address that the browser sends is the address that the server expects.

```sh
ssh -L 8700:127.0.0.1:8700 host
pravo serve --no-open --port 8700
```

The server compares the `Host` header of each request with its own address and answers 421 to any other. A forward from port 9000 to port 8700 sends `localhost:9000` and is refused. Use the same number on both sides.

Copy the address with the token from the output of the viewer, and open it in the browser on your machine. A request without the cookie that the token sets is answered with 401, so a link without the token shows nothing.

In a container, the viewer listens on the loopback interface of the container. A port that the container runtime publishes is reached through another interface, so publishing it does not reach the viewer. Run the viewer in a network namespace that the browser can reach, or forward over SSH from the host that runs the container.

## Recap

- `pravo serve` shows the specification, the state and an optional saved plan as modules and resources. It reads and writes nothing.
- The first view opens the modules that fit. Click a card to open a module, and open a cluster to see its members.
- The Source tab shows the definition and the instance that write a resource. The Change tab shows what a saved plan changes.
- The Plans section lists the plans that your team stored in the state. A disc on a resource names who plans to change it, and a ring marks two plans on one resource. The viewer copies the command that applies a plan and applies nothing.
- A value that is sensitive, or has a secret's name, or contains a secret, never reaches the page.
- The fragment of the address holds the view, so a link reproduces it. Press `?` in the viewer for the shortcuts.

Next: [`pravo serve`](../reference/serve.md) lists the flags, the keys and the endpoints. [The viewer](../concepts/the-viewer.md) explains what each mark means.
