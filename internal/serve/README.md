# internal/serve

`pravo serve` is a read-only viewer: a web server bound to the loopback interface that shows the specification, the state and, when given one, a saved plan as a graph of modules and resources. `api.go` is the contract between this package and the browser interface in `web/`. This file fixes the rest.

## Rules that do not bend

1. **Read-only.** The server writes nothing: no state record, no transaction, no lock, no `pravo.lock.cue`, no generated package, no download. A test hashes the project directory and the state before and after a session and requires equal results.
2. **Loopback only.** The listener binds `127.0.0.1` (or `::1`). There is no flag to bind another address.
3. **Authenticated.** Each run makes a 128-bit random token. `GET /?token=T` sets the cookie `pravo_serve_<port>` (HttpOnly, SameSite=Strict, Path=/; the port is in the name because a browser shares the cookies of a host between its ports, and two servers would overwrite each other's) and redirects to `/` without the token. Every `/api/` request needs that cookie (any cookie of that name will do: another page of the host can set one with a longer path, which a browser then sends first) or `Authorization: Bearer T`, compared in constant time. A request without it gets 401 and no body that names anything.
4. **Host and origin checked.** A request whose `Host` is not `127.0.0.1:<port>`, `localhost:<port>` or `[::1]:<port>` gets 421. A request with `Sec-Fetch-Site: cross-site` gets 403, and so does a request to `/api/` with `Sec-Fetch-Site: same-site` (a page of another port of the same host). A request with an `Origin` header that is not `http://` and one of those hosts gets 403, for a browser that sends no `Sec-Fetch-Site`. No CORS header is ever sent, and every response carries `Cross-Origin-Resource-Policy: same-origin` and `Cross-Origin-Opener-Policy: same-origin`, so that no other page can embed or read one. At most 32 event streams are open at once (503 after), and each write to a stream has a deadline.
5. **Values are redacted on the server.** A value is sent only when the provider schema says it is not sensitive and not write-only. Nested attributes and blocks follow the schema down; where a record does not have the shape of the schema, a key below an attribute of the schema that is named like a sensitive attribute of it is withheld all the same. When the schema of a type cannot be read, every value of that type is withheld (`redacted: "schema"`). An attribute whose name matches `(?i)(password|passwd|secret|token|private_key|credential|api_key|access_key)` is withheld too (`redacted: "name"`), as a second guard. Sensitive outputs have no value. A value that the schema does not mark but that contains a string the state keeps in a sensitive attribute (6 characters or more), or the value of a sensitive output, or a sensitive value that a saved plan would write, is withheld too (`redacted: "derived"`, `Output.redacted` for an output): a file whose content embeds a password. The string is looked for as it is and in the forms a specification puts it in: percent-encoded (a connection string), escaped inside a JSON string, hex, and base64 at any alignment (user data). Where a type has no schema, every string of its records counts. The same check masks the text of the transactions and locks that are shown (`user`, `actor`, `reason`, `command`). In a recorded command line the value of every `-t`/`--tag` is withheld, and so is the value of a flag or `key=value` word whose name matches the rule above. The `private` blob, `config_hash` and `provider_config` of a record are never sent.
6. **Source comes from loaded files only.** `/api/source` serves lines of files that `config.Root.Sources` lists, and of no other path; it takes an address, never a path. A file is read only when it still resolves to the path it had when the specification was loaded (a link that was pointed elsewhere since is not followed). A string literal assigned to an attribute that is sensitive for the resource's type, or whose name matches rule 5, is masked in the lines (`"••••••••"`) and the line is marked `sensitive`; so is any occurrence, in a line that is shown, of a string that rule 5 says is a secret (the header of a comprehension, a comment).
7. **Bounded.** Responses cap list elements (`truncated`), attribute text (4 KiB) and source blocks (200 lines each). A record whose values nest deeper than 200 levels is not decoded: its values are withheld and a note says so.
8. **Live without writing.** The server watches the state (the backend's own change signal, or polling every 2 s), the files of `Root.Sources` and the plan file, and sends one `change` event on `/api/events` when any differs. The first event of a stream carries the revision the server holds, with an empty `what`, so that a page that fetched the graph before it connected learns what it missed. It re-reads the state and re-loads the specification then, on the next request.

## Endpoints

| Path | Response |
|---|---|
| `GET /` and `/assets/...` | the embedded interface (`web/`), no data |
| `GET /api/ping` | `{"ok":true,"version":N}` |
| `GET /api/graph` | `Graph` |
| `GET /api/detail?addr=A` | `Detail` |
| `GET /api/source?addr=A` | `Source` |
| `GET /api/search?q=Q` | `Search` |
| `GET /api/events` | `text/event-stream`, event `change`, data `Event`; a comment line every 15 s |

All responses are `Cache-Control: no-store`. `addr` is URL-encoded. An unknown address is 404 with `{"error":"..."}`.

## Meaning

* A **module** is every proper prefix of a node address that holds a resource below it (an instance of a definition, a package instance, a plain struct). `Node.Module` and `Module.Parent` carry the tree: the page never parses an address.
* **Presence**: `both` (declared and recorded), `spec` (declared only), `state` (recorded only: an orphan the next apply would destroy).
* **Edges** run from the dependency to the dependent. They come from the specification where it loads and from the state records where it does not.
* **Plan**: `--plan FILE` reads a saved plan, shows its actions on the nodes (`Node.Action`) and the attribute changes in `Detail.Change`. A stale plan is shown with its reasons. The viewer never creates a plan itself.
* A specification that does not load is shown as the state alone, with `Project.SpecError` set and a warning.

## Interface (web/)

Plain HTML, CSS and JavaScript, no build step, no dependency, embedded with `go:embed`. It uses the site's design tokens (`site/design/css/00-tokens.css`, paper and ink themes) and its fonts. It is one page whose state is in the URL fragment: `#/?sel=ADDR&tab=source&collapse=PATH,PATH&theme=dark&q=TEXT&filter=plan`. A link reproduces the view; the documentation screenshots are made from such links.

## What these rules do not claim

* The token is a bearer secret. It is in the address that is printed, so in the terminal and in the argument list of the browser launcher while it starts. The cookie that carries it is `HttpOnly` and `SameSite=Strict`, but a browser sends a cookie to every port of a host: a server on another port of `localhost` that the browser visits receives it. The token is of no use to a page there (no CORS header, and the checks of rule 4), and a process of the same user can read the project directly.
* Redaction knows what the state and the saved plan hold. A secret that the specification writes as a literal and that no record keeps is masked only where it is assigned to a sensitive attribute, and a secret built from pieces that are not secrets themselves is not found. A secret shorter than six characters is not looked for in other values, and neither is one that is encoded in a way not listed in rule 5.
* A resource that is named after a secret shows the name: an address is a key, and no part of the page can do without it.
* Evaluating a specification is not bounded in time or memory, here as in every command.
* On Windows a program that sets `SO_REUSEADDR` can share a port with the server. The port is random unless `--port` names one.

