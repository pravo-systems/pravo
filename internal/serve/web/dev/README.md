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
| `auth=401` | Every `/api/` request answers 401: the page asks for the address with the token. |
| `fail=detail` | `/api/detail` answers 500 with a message: the error toast. |
| `latency=300` | Delays every answer by that many milliseconds: the loading skeletons. |

Only the platform has detail and source answers written out (`detail.db.json`, `detail.password.json`, `detail.drifted.json`, `source.db.json`, `source.password.json`: `api.db`, `api.db_password` and `edge.lb`). For every other node the shim makes the answer from the graph, from a small table of attributes by type: schema known or not (`aws_wafv2_web_acl` has no schema), sensitive attributes, a history from the revision, the plan's change, a lock.

The page sets `data-ready="1"` on `<html>` when the first view is drawn, which a screenshot program can wait for.

In a mock session `window.__mock` has `mutate()` (changes three nodes and sends a `change` event), `drop()` and `restore()` (the connection dot), and `window.__errors` collects the errors and warnings of the page. `window.__pravo` exposes the state, the graph view and a few actions, for a headless run; the page defines it only when the URL has `mock=` or `debug=`.

## The fragment

The view is in the fragment: `#/?sel=ADDR&tab=state|source|schema|history|change&collapse=A,B&expand=C,D&theme=light|dark&q=TEXT&filter=plan|drifted|sensitive|orphans|declared&view=graph|tree&motion=off&zoom=fit`. Values are URL-encoded; lists are comma separated, each item encoded. Besides those:

| Parameter | Meaning |
|---|---|
| `sel` | A node address, or the path of a module. A node that a collapsed module hides is revealed unless `collapse` is given. |
| `collapse`, `expand` | Module paths, applied on top of the first view (see below). `all` means every module. `expand` also takes cluster ids (`c:MODULE\|MODE\|TYPE`). |
| `zoom` | `fit` (the default) or a scale, centred on the selection when there is one. |
| `motion=off` | No animation or transition at all (reduced motion has the same effect). Use it for screenshots. |
| `palette=1`, `pq=TEXT` | Open the search palette, with a query. |
| `help=1` | Open the sheet of shortcuts. |

## The first view

When the page loads it chooses which modules are open, with `firstView` in `assets/model.js`: breadth first, one level of modules at a time. Everything starts as a card; a module opens when the picture that follows still fits the canvas at a zoom of at least 0.8 (a 14 px name is 11 px on screen) and within a budget of items that grows with the area of the canvas (about 75 at 1440 by 900). Modules that the plan, the drift, a spec-only or a state-only resource touch go first, then the larger ones; modules of one shape (three regions) open together or not at all. The first view does not depend on anything that a person chooses (the selection, a filter, a search): the fragment writes the open and closed modules relative to it, so a reload, which works it out again, must reach the same answer. A selection opens the modules above it afterwards (`revealSel` in `app.js`), unless a `collapse=` names one of them. `collapse=` and `expand=` are applied on top, so a link means the same thing on any screen as far as the modules it names go. A module inside a closed one is not written to the fragment (a link cannot say it: `expand=` opens what is above). The key `0` and the toolbar button Reset view go back to the first view (modules, groups and camera), keeping the filter and the search. `firstView` is a pure function (no DOM) that takes the canvas and returns the closed modules; `node check-firstview.mjs` runs it on the fixtures at several canvas sizes and checks the budget, the zoom, the peers, the forced modules and the speed.

## Fixtures

`fixtures/*.json` are answers of the contract in `../../api.go`, field names included and nothing extra. They are made by two programs; run them again after a change:

```sh
node gen-fixtures.js     # platform, fleet, empty, specerror, and the platform's detail and source answers
node gen-big.js          # graph.big.json
node check-fixtures.js   # reads the Go types of api.go and checks every fixture against them
```

`check-fixtures.js` is the contract test: it fails on a key that `api.go` does not have, on a key that is not `omitempty` and is missing, and on a value of the wrong JSON type, and it checks that every edge, module and parent exists. The Go side can read the same files in a test.
