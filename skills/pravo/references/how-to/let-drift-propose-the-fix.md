<!-- Generated from docs/how-to/let-drift-propose-the-fix.md by tools/skillgen. Do not edit. -->

# Let drift propose the fix

Run drift on a schedule so that it stores the plan that puts the drift right, tells a webhook, and leaves the approval and the apply to people.

[Drift](../learn/b09-drift.md) tells you that something changed outside pravo. It does not say what to do. `pravo drift --plan` also makes the plan that puts the resources back as the specification has them, and `--store` keeps it in the state. A person reads it, approves it where a policy asks for that, and applies it by its id. Drift never applies anything.

The examples use `demo_server` from `tools/fakeprovider`, as [Find what nothing manages](find-what-nothing-manages.md) does: it needs no cloud account, and deleting an object from its catalog is a drift. The output on this page was written from a run of it, so `capture: false` keeps `tools/capture` from running the page. Build the provider and apply two servers.

```sh
go build -o terraform-provider-demo ./tools/fakeprovider
mkdir objects
export FAKEPROVIDER_DIR=$PWD/objects
```

File `main.cue`:

```cue
package estate

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "./terraform-provider-demo"}

web:   $demo.#Resource.demo_server & {name: "web"}
batch: $demo.#Resource.demo_server & {name: "batch"}
```

```sh
pravo init
pravo apply -auto-approve
```

## 1. Read the proposal

Someone deletes the object of `batch` outside pravo. Here that is an edit of `objects/catalog.json`; in a cloud it is a click in a console. Ask drift for the plan.

```sh
pravo drift --plan
```

```text
Drift. 1 of 2 resources changed outside pravo:
  batch: create
    - refresh found the real state differs from the state (drift)

Putting it right adds 1, changes 0 and destroys 0. Run "pravo drift --plan --store" to keep that plan for review.
```

The list is the one `pravo drift` prints. The last line is the plan: one resource to create. The command exits 2, as `pravo drift` does when it finds drift. Nothing is stored and the object is still gone.

The plan is what `pravo plan` would make after reading every record. If the specification has changes that nobody applied, they are in the plan too. Section 4 shows how to take the drift alone.

## 2. Store it

```sh
pravo --actor drift-cron drift --plan --store --ttl 36h
```

```text
Drift. 1 of 2 resources changed outside pravo:
  batch: create
    - refresh found the real state differs from the state (drift)

Stored the plan as plan-89f9966a; unless it is applied it is removed after 36 hours. To apply exactly these actions, run:
  pravo apply plan-89f9966a
```

The plan is an ordinary [stored plan](store-a-plan-and-apply-it-later.md): the author is `drift-cron`, the plan appears in `pravo plan list` and in the Plans section of [the viewer](explore-your-estate.md), and a policy that asks for approvals holds it until they are given. `--ttl` is how long it is kept if nobody applies it; the default is seven days. A plan that a policy refuses is not stored, and the report says which rule refused it.

```sh
pravo plan list
```

```text
plan-89f9966a  pending  drift-cron  just now  +1 ~0 -0  expires in 1 day 12 hours
```

## 3. Tell someone

`--webhook` posts one JSON document to a URL, whatever the outcome. A chat bot, a ticket system or a flow in a workflow tool can read it. Start a receiver of your own, or use any URL that accepts a POST, and run the drift again with the address.

```sh
pravo --actor drift-cron drift --plan --store --webhook https://ops.example/hooks/pravo
```

The document is the same for every event; the fields that do not apply are left out.

```json
{
  "event": "plan.stored",
  "time": "2026-10-29T12:16:51Z",
  "root": "/srv/estate",
  "actor": "drift-cron",
  "plan": {
    "id": "plan-89f9966a",
    "expires": "2026-10-31T00:16:51Z",
    "apply_command": "pravo apply plan-89f9966a",
    "summary": {"add": 1, "change": 0, "destroy": 0}
  },
  "drifted": [
    {"addr": "batch", "action": "create", "reasons": ["refresh found the real state differs from the state (drift)"], "gone": true}
  ]
}
```

The plan then moves on without drift. Give the same `--webhook` to the commands that move it, and the receiver follows the plan from the first sight of the drift to the end:

| Event | Sent by |
|---|---|
| `plan.stored` | `pravo drift --plan --store` |
| `plan.approved` | `pravo plan approve plan-ID --webhook URL` |
| `plan.applied` | `pravo apply plan-ID --webhook URL` |
| `plan.refused` | `pravo drift --plan` when a policy refuses the plan, and `pravo apply plan-ID` when the apply does not go through. `reason` says why. |
| `plan.drifted` | `pravo drift --plan` without `--store`: the drift and the plan, not kept. |

[`pravo why` and `pravo drift`](../reference/why-and-drift.md#events) lists the fields. A run that finds no drift sends nothing.

## 4. Apply it

A person reads the plan, and the policies decide whether others must approve it first ([Approve a plan](approve-a-plan.md)). Then:

```sh
pravo apply plan-89f9966a --webhook https://ops.example/hooks/pravo
```

```text
batch: Creating... 
batch: Creation complete after 0s [id=srv-batch]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn 20261029T121655Z-c5d5d3]
```

The receiver gets `plan.applied`. When the stored plan holds changes of the specification besides the drift, take the drifted resources alone with `--only`, and the rest of the plan stays stored:

```sh
pravo apply plan-89f9966a --only batch
```

A plan whose state moved on is stale and cannot be applied; run drift again for a new one. [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) explains staleness and `--only`.

## 5. Run it on a schedule

```sh
pravo --actor drift-cron drift --plan --store --ttl 2d --webhook "$HOOK_URL"
```

Exit code 2 means drift was found, which a scheduler may treat as a failure: accept it, as [Run pravo in a pipeline](ci.md#5-find-drift-on-a-schedule) does. Two runs that find the same drift store two plans; `pravo plan list` shows that they change the same resource, and applying one makes the other stale.

Nothing in this page applies a plan without a person. Drift stores, the webhook tells, and a command that someone runs applies.
