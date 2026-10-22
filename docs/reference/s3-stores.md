---
title: S3-compatible stores
description: The three conditional requests that the S3 backend needs, how pravo checks that a store honours them, and how to test a store before it holds a state.
order: 55
group: Data and formats
---

The S3 backend keeps one object for each record, one object for each lock and up to three objects for each stored plan. Its locks rest on three conditional requests, and so do the stored plans. A store that honours all three keeps two applies apart. A store that ignores one lets two commands hold the same lock, and nothing says so.

| Request | Used for | A store that honours it answers a failed condition with |
|---|---|---|
| `PutObject` with `If-None-Match: *` | Creating a lock: the object must not exist. Storing a plan, and writing the first progress object of one. | `412 PreconditionFailed`, or `409 ConditionalRequestConflict` when another conditional request on the key is running. |
| `PutObject` with `If-Match: ETAG` | Renewing a lock, replacing a record that the command read and replacing the progress object of a stored plan. | The same. |
| `DeleteObject` with `If-Match: ETAG` | Releasing a lock, reclaiming one that went stale and removing the objects of a stored plan. | The same. |

`If-Match` on an object that does not exist is answered with `404 NoSuchKey` by some stores. pravo treats that as a missing object, as it does for a plain read.

A collection of stored plans deletes an object only through `If-Match`, after it has claimed the plan in the progress object with the same request. A store that ignores `If-Match` lets a collection remove a plan that an apply has just claimed. The permissions are those of the rest of the state: get, put, delete and list, here on the keys below `PREFIX/plans/` as well.

Before a command that writes the state first uses a bucket, pravo probes the store. It writes scratch objects under `PREFIX/probe/` and tries each of the three requests with a tag that must be refused and with one that must be accepted. It reads the contents back after a refused request, and it starts eight creates of one key at once and requires one winner. It deletes the scratch objects when it ends. A store that ignores a condition, or answers `501`, is refused with a message that names the condition:

```text
Error: s3://infra-state/prod cannot hold a state that more than one command writes: a delete with a stale entity tag succeeded, so If-Match is ignored on a delete
```

A store that passes is remembered by endpoint and bucket for thirty days, in `$PRAVO_HOME/s3-probe`. The probe needs the same permissions as the backend: get, put, delete and list. A store that cannot be reached or written gives that error, not a refusal. `PRAVO_S3_PROBE=off` skips the probe, for a store that you have tested yourself. [Test a store](#test-a-store) runs it on demand.

## Test a store

`pravo state probe` runs the checks above against the store that the root names. It ignores the remembered verdict and the `PRAVO_S3_PROBE` variable, prints one line for each check, and exits 1 at the first check that fails:

```text
create only (If-None-Match)      ok
replace (If-Match on a put)      ok
delete (If-Match on a delete)    FAILED

Error: s3://infra-state/prod cannot hold a state that more than one command writes: a delete with a stale entity tag succeeded, so If-Match is ignored on a delete
```

`--json` prints the same as one document. A store that passes is remembered for thirty days.

To try a store before a project uses it, make a directory with a `main.cue` that holds only the `$settings` block, run `pravo init` and then `pravo state probe`:

```cue
package probe

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: pravo.#S3State & {backend: "s3", bucket: "my-bucket", endpoint: "https://s3.example.com"}
}
```

The probe shows that the store checks the conditions one request at a time and that concurrent creates have one winner. It does not show that the store keeps that guarantee under every load. Amazon S3 honours the conditions on a versioned bucket. On other stores a versioned bucket is where conditional requests tend to be weakest, so probe the bucket as it will be used, with versioning set the way you will set it.

A sealed state needs more of the store. Its records are objects that are never overwritten, and a pointer to each is replaced by compare-and-swap. `pravo protect check` runs the checks above and two more: eight writers replace one object at once with the entity tag they all read and exactly one must win, and a key that has just been written must be in the listing of its prefix. See [pravo id and pravo protect](protect.md#protect-check).

[Remote state](../how-to/remote-state.md) sets up the bucket, and [State format](state-format.md#remote-layout) describes the objects.
