---
title: Reading a plan
description: Follow a scoped plan and a refused plan from the summary line to the cause.
order: 2
group: Track B, pravo
minutes: 11
needs: B1
learn: How do you tell what a plan touches and why?
done_when: "You can name the resources a plan skipped."
level: intermediate
---

A scoped plan walks only what changed. The `Graph:` line says how much.

```sh run
pravo plan --no-color
pravo why pet
```

```text out
pravo will perform the following actions:

  # pet (random_pet) will be updated in-place
  ~ pet: {
  ~     length: 3 -> 4
    }

Plan: 0 to add, 1 to change, 0 to destroy.
Graph: planned 1 of 340, 339 skipped.
...
pet changes because length changed.
```

```json
{
  "format": 1,
  "summary": {"add": 0, "change": 1, "destroy": 0},
  "graph": {"planned": 1, "total": 340}
}
```

```yaml
# ci.yml
steps:
  - run: pravo plan --json > plan.json
```
