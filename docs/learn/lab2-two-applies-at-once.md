---
title: "Lab 2: two applies at once"
description: Run three applies against one state while one of them holds a lock. Read the locks and the log afterwards.
order: 52
group: Track B · Labs and project
minutes: 20
needs: B7
learn: Which applies can run at the same time? What does a refused apply look like, and where is it recorded?
done_when: "One apply is refused with exit code 4 while another runs, and you can read the lock and the refusal."
level: advanced
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "(?m)^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}"
    replace: "<txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "\\[id=[0-9a-f]{40}\\]"
    replace: "[id=<id>]"
  - pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})"
    replace: "<time>"
  - pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} [0-9][0-9.a-z]*"
    replace: "<time> <duration>"
  - pattern: "pid [0-9]+ on [^ ,)]+"
    replace: "pid <pid> on <host>"
---

OpenTofu holds one lock for the whole state. pravo holds one lock for each resource that an apply writes. Two applies that write different resources run at the same time. An apply that needs a resource another process holds is refused.

Outputs are one record that each apply updates when it ends: an apply writes the outputs it changed and keeps the rest as recorded. The specification of this lab has no outputs.

This lab starts three applies on one state while one of them is busy. It uses a key that takes seconds to generate, which gives the first apply time to hold its lock. The time varies between machines and runs.

## Prepare the plans

Work in an empty directory. Start with one file and apply it, so that the state exists.

```cue file=main.cue title="main.cue"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$local: loc.#Provider
$tls:   tlsp.#Provider

base: $local.#Resource.local_file & {filename: "base.txt", content: "base\n"}
```

```sh run
pravo init
pravo apply --auto-approve
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  tls = hashicorp/tls v4.4.2
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

Add two resources. `slow` is an RSA key of 8192 bits, which takes seconds to create. `quick` is a file.

```cue file=main.cue title="main.cue" hl="13-14"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$local: loc.#Provider
$tls:   tlsp.#Provider

base: $local.#Resource.local_file & {filename: "base.txt", content: "base\n"}

slow:  $tls.#Resource.tls_private_key & {algorithm: "RSA", rsa_bits: 8192}
quick: $local.#Resource.local_file & {filename: "quick.txt", content: "quick\n"}
```

Save one plan. Three people each get a copy: Ana, Bob and a CI job.

```sh run
pravo plan --out all.plan
cp all.plan a.plan
cp all.plan b.plan
cp all.plan c.plan
```

```text out
...
  # slow (tls_private_key) will be created
  + slow: {
  +     algorithm:                     "RSA"
...
  +     rsa_bits:                      8192
    }
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 · skipped 1 · not read 1

Saved the plan to all.plan. To apply exactly these actions, run:
  pravo apply all.plan
```

Each copy creates two resources. A plan may be applied in parts, as in [B7](b07-changing-live-infrastructure.md). The parts here are `slow` and `quick`.

## Wait for a lock

A script that waits until a resource is locked makes the run repeatable. It asks `pravo state locks` ten times a second.

```sh file=wait-for-lock.sh title="wait-for-lock.sh"
for i in $(seq 1 200); do
	pravo state locks | grep -q "^$1 " && exit 0
	sleep 0.1
done
exit 1
```

## Run three applies

:::predict Ana applies slow and holds its lock. While she works, Bob applies quick and the CI job applies slow. Which of the two succeeds?
The locks are per resource. Ask what each apply needs to write.

::reveal
Bob's apply succeeds, because `quick` is free. The CI job is refused with exit code 4, because `slow` is locked. Nothing waits and nothing queues.
:::

Ana starts in the background. The script returns when `slow` is locked. Then the CI job and Bob run, in that order.

```sh run
pravo apply a.plan --only slow --actor ana > a.log 2>&1 &
ana=$!
sh wait-for-lock.sh slow
pravo state locks
pravo apply c.plan --only slow --actor ci
echo "exit $?"
pravo apply b.plan --only quick --actor bob
echo "exit $?"
kill -0 $ana && echo "ana is still running"
wait $ana
cat a.log
```

```text out
slow  locked by ana, pid <pid> on <host> since <time>: pravo apply a.plan --only slow --actor ana
Applying 1 of 2 remaining changes from c.plan.

Error: apply rejected: slow is locked by pravo apply a.plan --only slow --actor ana (pid <pid> on <host>, ana, since <time>)
exit 4
Applying 1 of 2 remaining changes from b.plan.
quick: Creating...
quick: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
1 change remains in b.plan; apply with "pravo apply b.plan".
exit 0
ana is still running
Applying 1 of 2 remaining changes from a.plan.
slow: Creating...
slow: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
1 change remains in a.plan; apply with "pravo apply a.plan".
```

Read it from the top.

- `pravo state locks` lists `slow`: the actor, the process, the host, the time and the command line of the holder.
- The CI job's apply stops with exit code 4. The error names the holder. Nothing was written.
- Bob's apply wrote `quick` and ended. Ana's was still running at that moment.
- Ana's apply ended after the key was generated, and its lock was released.

:::figure Ana holds the lock on slow. The CI job needs slow and is refused. Bob needs quick, which is free, and finishes while Ana works.
<svg font-size="13" viewBox="0 0 560 180" role="img" aria-label="Three lanes over time. Ana's apply of slow is a long bar. The CI job's apply of slow starts inside it and is refused. Bob's apply of quick starts inside it and finishes.">
<g style="fill: var(--accent-wash)"><rect x="96" y="14" width="440" height="120"/></g>
<g fill="none" stroke-width="1.5"><rect style="stroke: var(--accent)" x="96" y="22" width="440" height="30"/><rect style="stroke: var(--conflict)" x="150" y="68" width="22" height="30"/><rect style="stroke: var(--accent)" x="230" y="114" width="22" height="30"/></g>
<g fill="none" style="stroke: var(--conflict)" stroke-width="2"><path d="M155 74l12 18M167 74l-12 18"/></g>
<text x="6" y="42" style="fill: var(--text)">ana</text>
<text x="6" y="88" style="fill: var(--text)">ci</text>
<text x="6" y="134" style="fill: var(--text)">bob</text>
<text x="108" y="42">apply slow: holds the lock on slow</text>
<text x="184" y="88" style="fill: var(--conflict)">apply slow: refused, exit 4</text>
<text x="264" y="134">apply quick: written</text>
<text x="96" y="168">time</text>
<path d="M130 164H536" fill="none" stroke="currentColor" stroke-width="1.5"/>
<path d="M528 160l8 4-8 4z" fill="currentColor"/>
</svg>
:::

The locks are gone.

```sh run
pravo state locks
```

```text out
No locks or reservations held.
```

## Read the log

`pravo state log` lists every transaction, newest first. The refused apply is recorded as well, with no changes.

```sh run
pravo state log | head -n 14
```

```text out
<txn>  apply    applied   <time> <duration>
    pravo apply b.plan --only quick --actor bob
    actor bob
    1 added, 0 changed, 0 destroyed
    create   quick  r0 -> r1
<txn>  apply    rejected  <time> <duration>
    pravo apply c.plan --only slow --actor ci
    actor ci
    apply rejected: slow is locked by pravo apply a.plan --only slow --actor ana (pid <pid> on <host>, ana, since <time>)
<txn>  apply    applied   <time> <duration>
    pravo apply a.plan --only slow --actor ana
    actor ana
    1 added, 0 changed, 0 destroyed
    create   slow  r0 -> r1
```

Bob's apply started after Ana's and is listed above it. The refusal is a transaction with the status `rejected`, and its message is the error that the CI job saw. The actor label comes from `--actor`. pravo does not verify it.

## Break it: a plan made too early

The CI job still has its copy. The plan was made before Ana and Bob applied. It expects to create `slow` and `quick`, and both exist now.

```sh run exit=3
pravo apply c.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - slow changed in state (revision 1, plan expected 0)
  - quick changed in state (revision 1, plan expected 0)
```

Exit code 3 is a stale plan. Each reason names a record, the revision it has now, and the revision the plan expected. A record that did not exist has revision 0. The CI job plans again. It finds `No changes`.

```sh run
pravo plan
```

```text out
...
No changes. The specification matches the state. 3 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 3 · not read 3
```

Remove what the lab created.

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 3 destroyed. [txn <txn>]
```

## Recap

- An apply locks the resources its plan writes, all of them before it changes anything. There is no lock on the state as a whole.
- An apply that needs a locked resource is refused with exit code 4. The log records the refusal.
- A plan that was made before another apply touched its resources is stale. Exit code 3 says so.

:::check
Why did Bob's apply succeed while Ana's was running?
::answer
The two applies wrote different resources. A lock is held per resource.

Where can you read the refusal of the CI job afterwards?
::answer
In `pravo state log`. A refused apply is a transaction with the status `rejected`.
:::

A lock left behind by a command that crashed on another host is released with `pravo state unlock <address>`. `pravo state locks` marks the locks that may be reclaimed with `[stale]`, and `pravo state unlock --stale` releases all of them. A state shared by several machines is the subject of [Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md), and [Track C](c01-one-state-two-people.md) puts a team on one.
