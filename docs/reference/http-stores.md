---
title: HTTP state servers
description: The object protocol that the http backend speaks, the conditional requests that the locks need, and how pravo retries and checks a server.
order: 56
group: Data and formats
services:
  - fakehttp:9000
---

The http backend keeps the same objects as the S3 backend: one for each record, one for each lock and the transactions, outputs and stored plans. It reaches them through a small HTTP protocol under the `url` of `$settings.state`, so that a team can keep a state on a server of its own. [Settings](spec-settings.md#http-keys) lists the keys.

```cue
$settings: state: {backend: "http", url: "https://state.example.com/team/app"}
```

## The protocol

A key such as `resources/api.config.json` is the path below the URL. Every request carries `Authorization: Bearer TOKEN`, or basic authentication when the settings give a user name.

| Request | Meaning | Answers |
|---|---|---|
| `GET URL/KEY` | Read an object. | `200` with the body and an `ETag` header, or `404`. |
| `PUT URL/KEY` | Write an object. | `200`, `201` or `204`. |
| `PUT URL/KEY` with `If-None-Match: *` | Create the object only if none exists. | A success, or `412` when the object exists. |
| `PUT URL/KEY` with `If-Match: ETAG` | Replace the object only if it still has that tag. | A success, `412` when the tag differs, or `404` when there is no object. |
| `DELETE URL/KEY` | Delete an object. | A success, or `404`. |
| `DELETE URL/KEY` with `If-Match: ETAG` | Delete the object only if it still has that tag. | As for `PUT`. |
| `GET URL/?prefix=DIR/` | List the keys below `DIR/`. | `200` and `{"keys": ["DIR/a.json", "DIR/b.json"]}`, with the keys written relative to the URL. |

The listing is not paged: the server sends every key below the prefix. A key that ends in `/` is not an object and is skipped.

Locks, the replacement of a record that a command read, and the stored plans rest on the three conditional requests, as they do for [S3-compatible stores](s3-stores.md). A server that honours all three keeps two applies apart. A server that ignores one lets two commands hold the same lock, and nothing says so. The tag of an object must change whenever its contents change, and the server must apply a conditional request and the write it guards as one step.

## Retries

A request is sent again when the server answers `429`, `500`, `502`, `503` or `504` and when the connection fails. It is sent up to six times, with a random wait that doubles from 100 ms up to 5 s.

A conditional write whose answer was lost may have been applied. When pravo sends it again and gets `412`, it reads the object and counts the write as made if the object holds the bytes that were sent. When the server stays busy, or the read fails, the error says `the store did not say whether the write was made`, and the command stops with exit code 1. Run it again.

## Test a server

Before a command that writes the state first uses a server, pravo probes it. It writes scratch objects under `probe/`, tries each conditional request with a tag that must be refused and with one that must be accepted, and starts eight creates of one key at once, which must have one winner. The scratch objects are deleted at the end. The verdict is remembered for 30 days by URL, and `PRAVO_S3_PROBE=off` skips the probe.

`pravo state probe` runs the probe and prints one line for each check. `pravo protect check` also requires that eight replaces with one tag have one winner, and that a key just written is in the listing.

## Try the protocol

`tools/fakehttp` in pravo's source is an in-memory server for the protocol. It forgets everything when it stops. The examples on this page run against it on port 9000. Start it in a second terminal to follow along:

```sh
go run ./tools/fakehttp -addr 127.0.0.1:9000
```

Point a root at it. A directory that holds only this block is enough to try a server:

```cue file=main.cue
package probe

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: pravo.#HTTPState & {backend: "http", url: "http://127.0.0.1:9000/demo"}
}
```

`pravo init` makes the module, and `pravo state probe` runs the checks against the server:

```sh run
pravo init
pravo state probe
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:

pravo has been initialised. Run "pravo plan" to see what it would do.
create only (If-None-Match)      ok
replace (If-Match on a put)      ok
delete (If-Match on a delete)    ok
concurrent creates               ok
http://127.0.0.1:9000/demo honours the conditional requests that the locks need.
```

A command that writes the state creates `meta.json` and the other objects under the URL. A root with no resources has none to list, and every checkout with the same `url` reads the same state:

```sh run
pravo apply --auto-approve
pravo state list
```

```text out
No changes. Your infrastructure matches the configuration.
```
