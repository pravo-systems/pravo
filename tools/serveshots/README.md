# serveshots

Makes the screenshots of the documentation of `pravo serve`. It applies two estates with the real binary, starts the viewer on a free loopback port and opens the address of each shot in headless Chrome at a device scale factor of 2. The PNGs go to `docs/img/serve/`, where the pages link them.

```sh
go build -o bin/pravo ./cmd/pravo
go run ./tools/serveshots -bin bin/pravo -out docs/img/serve                # every shot
go run ./tools/serveshots -bin bin/pravo -out docs/img/serve -only plan-dark,fleet
go run ./tools/serveshots -list                                              # the shots and their addresses
go run ./tools/serveshots -out docs/img/serve -placeholders                  # flat panels, no Chrome, no pravo
go test ./tools/serveshots
SERVESHOTS_BIN=bin/pravo go test ./tools/serveshots                          # also runs the scenarios
```

It needs Chrome or Chromium, the providers `random`, `local` and `null` (from `~/.pravo/providers`, or the network) and nothing else. Chrome is found from `-chrome`, then `$CHROME`, then the usual places. The program drives it over the DevTools protocol, with a WebSocket client of its own, so that it sets the window and the device scale factor, waits for the page to settle and reads the console with the level of each message.

| Flag | Default | Meaning |
|---|---|---|
| `-bin PATH` | none | The pravo binary. Required unless `-placeholders` or `-list`. |
| `-out DIR` | none | Where the PNGs go. Required. |
| `-chrome PATH` | `$CHROME`, then the usual places | Chrome or Chromium. |
| `-only NAMES` | every shot | Comma separated shot names, with or without `.png`, and scenario names. |
| `-work DIR` | a temporary directory | Where the workspaces and the browser profile go, as `DIR/clean/NAME` and `DIR/changed/NAME`. A temporary directory is removed afterwards. |
| `-keep` | off | Keep the workspaces and print where they are. |
| `-wait D` | `20s` | The longest that a page gets to settle before the screenshot. A page has settled when what it shows is the same for a second. |
| `-placeholders` | off | Write a flat panel with the name of each shot and run nothing else. |
| `-list` | off | Print the shots and exit. |
| `-v` | off | Print every command that runs. |

The exit status is 0 when every shot was made, 1 when a shot failed and 2 when the program could not start. A shot fails when Chrome fails, when the PNG is not twice the size of the viewport, when it does not decode, when it is blank (fewer than four colours, or one colour on 99.5 percent of the pixels) and when the page logs an error: an uncaught exception, a `console.error`, a failed request or a refused policy. Other console messages are printed under the shot. The program keeps going after a failed shot and reports every failure, and it tries a shot once more when the browser fails and not when the page does.

The viewer shows the directory of the workspace and the address of the server. Once a page has settled, the program replaces the directory with `~/estate/NAME` and the address with `127.0.0.1:8700` in the text and in the `title` and `aria-label` attributes of the page, so that a picture does not show the machine it was made on.

A long `$TMPDIR` makes every provider fail to start with "Failed to read any lines from plugin's stdout": the providers listen on a unix socket below it, and a socket path is limited to about 100 bytes. Leave `$TMPDIR` alone, and use `-work` for the workspaces.

## Scenarios

A scenario is a CUE root in `scenarios/NAME`, embedded in the program. The documentation shows its files.

| Scenario | Resources | What it is |
|---|---|---|
| `platform` | 47 | Three modules, `network`, `api` and `edge`, with nested modules, a second package `kit` that holds the definitions `#Zone`, `#Service` and `#Certificate`, cross-module dependencies, a sensitive output and one that is not. `random_password` stands for secrets, `random_pet` and `random_id` for names, `local_file` and `local_sensitive_file` for rendered configuration and `null_resource` with triggers for deploy steps. |
| `fleet` | 143 | Three regions, each with a production and a staging environment, each with three services and a pool of twelve `null_resource` nodes, all written by comprehensions, so that addresses are three modules deep and each pool is a cluster. |

Both use only the providers `random`, `local` and `null`.

A reference inside a comprehension to a loop variable that stands for a resource (`for s, svc in api.services { ... svc.id.hex ... }`) does not record the dependency, and an apply can fail with "still unknown at apply time". The scenarios write such references out.

## Situations

A shot is made from one of four situations of a scenario.

| Situation | How the workspace gets there |
|---|---|
| `clean` | `pravo init` and `pravo apply -auto-approve`. |
| `changed` | `clean`, then a file that a resource wrote is edited and another is deleted and `pravo drift` runs, then the specification is edited without an apply (a resource added, a resource removed, a value changed), then `pravo plan --out change.plan`. |
| `plan` | `changed`, served with `--plan change.plan`. |
| `stored` | `clean`, then for each stored plan of the scenario the specification is edited, `pravo plan --store --actor ACTOR --reason TEXT` puts a plan in the state, and the specification is put back. The times in the objects of the plans are then moved into the past, which no command can do: the plan of `ana` is made 3 hours ago, the plan of `ben` 40 minutes ago, and the plan of `ci` 9 days ago, which is 2 days past its time to live. Served without `--plan`. |

The changes of `platform` are in `scenarios.go`. The provider `local` reports a file whose content changed as gone, as it does a deleted file, so both drifted resources carry the mark gone. The new port of `orders` replaces three resources, since every attribute of `local_file` and `null_resource` forces a replacement, and the removed `edge.balancer.reload` is the one that the plan destroys. The stored plans of `platform` are the same new port of `orders` (`ana`), a different one with a new port of `search` (`ben`), which overlaps the first on the three resources that `orders` replaces, and a new port of the balancer (`ci`), which expired.

## Shots

`shots.go` lists them: a name, a scenario, a situation, the fragment of the address and the size of the viewport. The name ends in `-dark` or `-light`, which the fragment states as `theme=`. Every fragment has `motion=off`. A shot selects a resource or a module (`sel=eu.prod`), and names the modules to close and to open with `collapse` and `expand`, because the first view that the page chooses depends on the size of the canvas and on the plan: a link that names the modules gives the same picture on every run. The tests check that the names are unique, that the list agrees with the PNGs in `docs/img/serve` and with the pages that link them, that the `width` and `height` of each `img` tag are the size of its viewport, and that every listing of a scenario file in the documentation shows lines that the file has, that a paired shot is linked with the theme classes, and that the reference names the chips and the tabs of the interface as it does. `SERVESHOTS_BIN` adds a check that every address or module a shot selects or names exists in the state of its scenario.

A stored plan has an id that depends on the plan, so a shot names it by the actor that made it, `plan=@ana`, and the program puts the id in when it makes the address. The viewer lists the stored plans whether or not one is laid over the graph.

Five shots come as a dark and a light picture of one view. A page links both in one figure, with the classes `shot-dark` and `shot-light` on the two links, and the style sheet of the site shows the one that matches the theme of the page, whether it follows the system or the visitor chose it. A test checks the markup. A shot that has one picture only is linked with the class `shot`.

The shots `overview`, `modules-collapsed`, `modules-expanded`, `clusters`, `source`, `sensitive`, `palette` and `narrow` are made from the `clean` situation, which has the 47 resources that the apply wrote. The shots `plan`, `drift` and `tree` are made from the `changed` situation, which has 48. The shots `plans` (the plan of `ana` laid over the graph, with `api.services.orders.config` selected, which both pending plans replace) and `plan-expired` (the plan of `ci`, which expired) are made from the `stored` situation, which has the 47 resources of the clean one.

To add a shot, add it to `allShots`, run `-placeholders`, link `img/serve/NAME.png` from a page with the `width` and `height` of the viewport, and run the program for it.

## Files

| File | Role |
|---|---|
| `main.go` | Flags, the order of the work, one workspace and one server for each situation. |
| `shots.go` | The shots, the fragment and its encoding. |
| `scenarios.go`, `scenarios/` | The estates and the changes that make the situations. |
| `workspace.go` | Copies a scenario and runs `init`, `apply`, `drift` and `plan`. |
| `server.go` | Starts `pravo serve --json` and reads the address and the token. |
| `chrome.go`, `cdp.go` | Finds and starts Chrome, drives it over the DevTools protocol, waits for the page to settle, takes the picture and reads the console. |
| `image.go` | Checks a PNG, writes the placeholders. |
