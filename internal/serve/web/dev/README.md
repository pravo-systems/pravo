# web/dev

Development material for the interface. The Go server does not serve this directory, and the page loads nothing from it unless its URL says `mock=`.

## Run the interface without the server

```sh
cd internal/serve/web
python3 -m http.server 8830 --bind 127.0.0.1
```

Open `http://127.0.0.1:8830/index.html?mock=platform`. The text between `?` and `#` selects what answers `/api/*`; the fragment is the view, as in the real page.

| Query | Effect |
|---|---|
| `mock=platform` | About 45 nodes: modules `network`, `api` and `edge` with nested modules, a plan with creates, updates, one replace and one delete, a drifted and a gone node, a spec-only node, an orphan, a locked node, sensitive attributes. |
| `mock=fleet` | About 170 nodes: regions, services and resources three modules deep, with for-each groups of twelve or more siblings of one type, so that clustering shows. |
| `mock=big` | About 1900 nodes in 30 modules, flat groups of siblings. The first view shows about twenty items. |
| `mock=empty` | No nodes: the empty state. |
| `mock=specerror` | `spec_error` set, state only. |
| `plan=plan-2f9c1a4e` | The server lays that stored plan over the graph itself, as `pravo serve --plan plan-2f9c1a4e` does: it is the plan of the graph and the page opens with it selected. Taking it off in the page is `plan=none` in the fragment. |
| `auth=401` | Every `/api/` request answers 401: the page asks for the address with the token. |
| `fail=detail` | `/api/detail` answers 500 with a message: the error toast. |
| `latency=300` | Delays every answer by that many milliseconds: the loading skeletons. |

Only the platform has detail and source answers written out (`detail.db.json`, `detail.password.json`, `detail.drifted.json`, `source.db.json`, `source.password.json`: `api.db`, `api.db_password` and `edge.lb`). For every other node the shim makes the answer from the graph, from a small table of attributes by type: schema known or not (`aws_wafv2_web_acl` has no schema), sensitive attributes, a history from the revision, the plan's change, a lock.

The platform also has seven plans stored in the state: `plans.platform.json` is the answer of `/api/plans`, and `plan.2f9c1a4e.json` and its six siblings are the answers of `/api/plan?id=plan-2f9c1a4e` and so on. Four of the plans can be applied: `ana@acme` and `ben@acme` both change `api.db`, which has the ring of an overlap and two author badges, `dee@acme` has applied one of its two changes (`partial`: the applied one is marked `applied` in its touches and is on no node, and the overlaps of a plan count only the changes that are left), and `kim@acme` has a plan that is `partly-stale` (`plan-b86e2d14`): `api.role` moved in the state, so it conflicts, and `api.policy` has to be applied with it; `api.secret` and `api.logs` are the part that can be applied, by `--only`. The plan has `conflicts`, `unapplied`, `apply_only` and `apply_live`, a `conflict` mark on two of its touches, and its badge on all four nodes; its conflicting changes do not count for an overlap. One plan is stale, one applied and one expired. A listing row (`plans.platform.json`) has no `reason`, `command` or `git_revision`; the overlay (`plan.*.json`) has them, as the server sends them. An overlap is `{id, count, addrs}` and is symmetric. `detail` and `source` take `plan=ID` (or `plan=none`) and the shim makes the answer for that plan from the overlay. The other mocks have no stored plans: the section of the rail is not there.

The page sets `data-ready="1"` on `<html>` when the first view is drawn, which a screenshot program can wait for.

In a mock session `window.__mock` has `mutate()` (changes three nodes and sends a `change` event), `storePlan()` (stores a seventh plan, `plan-d4b7e913` by `dee@acme`, which also changes `api.db`, and sends the event that says `plans`), `collectPlan(id)` (removes a stored plan, with its event), `drop()` and `restore()` (the connection dot), and `window.__errors` collects the errors and warnings of the page. `window.__pravo` exposes the state, the graph view and a few actions, for a headless run; the page defines it only when the URL has `mock=` or `debug=`.

## The fragment

The view is in the fragment: `#/?sel=ADDR&tab=state|source|schema|history|change&collapse=A,B&expand=C,D&theme=light|dark&q=TEXT&filter=plan|drifted|sensitive|orphans|declared&plan=plan-2f9c1a4e&view=graph|tree&motion=off&zoom=fit`. Values are URL-encoded; lists are comma separated, each item encoded. Besides those:

| Parameter | Meaning |
|---|---|
| `sel` | A node address, or the path of a module. A node that a collapsed module hides is revealed unless `collapse` is given. |
| `plan` | A plan stored in the state, by its whole id, laid over the graph: its actions replace those of the nodes, the Change tab and the source answer for it, and the first view is made for it (the modules it touches open first). `plan=none` takes off the plan that the server laid itself (`pravo serve --plan ID`). Without it the page shows the plan of the server, if any. An id that the state does not hold is told in a toast and ignored. The page writes it when a plan is selected, and not when the selected plan is the one the server laid. |
| `collapse`, `expand` | Module paths, applied on top of the first view (see below). `all` means every module. `expand` also takes cluster ids (`c:MODULE\|MODE\|TYPE`). |
| `zoom` | `fit` (the default) or a scale, centred on the selection when there is one. |
| `motion=off` | No animation or transition at all (reduced motion has the same effect). Use it for screenshots. |
| `palette=1`, `pq=TEXT` | Open the search palette, with a query. |
| `help=1` | Open the sheet of shortcuts. |

How the page treats the stored plans when the project moves: the graph and the list of plans are read together and read again (twice at most) until they are of one revision, so a change that lands between the two requests does not leave a list that is newer than the graph; a refresh that was begun before the person chose a plan does not override that choice (`planMark` in `app.js`). An overlay that cannot be read (`/api/plan` fails, or the plan was collected meanwhile) is told in a toast and the page goes on with the graph and the list: it is not fatal, and a pasted `plan=` that the state does not hold is told the same way. The warnings of the plan that the server laid itself (`--plan ID`) are shown while that plan is selected and go when another plan, or none, is. The button that copies `pravo apply ID` turns to a cross and announces "Could not copy" when the clipboard refuses it. The initials of an author are taken by whole characters (a surrogate pair is not cut in two). A refresh that redraws the section of the rail keeps the focus on the row, filter or control that had it.

## The first view

When the page loads it chooses which modules are open, with `firstView` in `assets/model.js`: breadth first, one level of modules at a time. Everything starts as a card; a module opens when the picture that follows still fits the canvas at a zoom of at least 0.8 (a 14 px name is 11 px on screen) and within a budget of items that grows with the area of the canvas (about 75 at 1440 by 900). Modules that the plan, the drift, a spec-only or a state-only resource touch go first, then the larger ones; modules of one shape (three regions) open together or not at all. The first view does not depend on anything that a person chooses (the selection, a filter, a search): the fragment writes the open and closed modules relative to it, so a reload, which works it out again, must reach the same answer. A selection opens the modules above it afterwards (`revealSel` in `app.js`), unless a `collapse=` names one of them. `collapse=` and `expand=` are applied on top, so a link means the same thing on any screen as far as the modules it names go. A module inside a closed one is not written to the fragment (a link cannot say it: `expand=` opens what is above). The key `0` and the toolbar button Reset view go back to the first view (modules, groups and camera), keeping the filter and the search. `firstView` is a pure function (no DOM) that takes the canvas and returns the closed modules; `node check-firstview.mjs` runs it on the fixtures at several canvas sizes and checks the budget, the zoom, the peers, the forced modules and the speed.

## Fixtures

`fixtures/*.json` are answers of the contract in `../../api.go`, field names included and nothing extra. They are made by two programs; run them again after a change:

```sh
node gen-fixtures.js     # platform, fleet, empty, specerror, the platform's detail and source answers, and its stored plans
node gen-big.js          # graph.big.json
node check-fixtures.js   # reads the Go types of api.go and checks every fixture against them
```

`check-fixtures.js` is the contract test: it fails on a key that `api.go` does not have, on a key that is not `omitempty` and is missing, and on a value of the wrong JSON type, and it checks that every edge, module and parent exists. It also checks that the stored plans and the graph say the same: every address a plan touches is a node, the plans on a node are the open plans that change it, a node that two of them can apply a change on has `overlap` (a change that a partly stale plan cannot apply does not count), a partly stale plan has conflicts, a part to apply and the command for it, and the other plans have none, the counts of a plan are those of its touches, and each plan has an overlay whose nodes are its touches. The fixtures are named `graph.*`, `detail.*`, `source.*`, `plans.*` (an answer of `/api/plans`, which goes with the `graph.*` of the same name) and `plan.*` (an answer of `/api/plan`). The Go side reads the same files in `contract_test.go`.
