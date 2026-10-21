---
title: A first plan
description: Declare two resources, read the plan, and apply it.
order: 1
group: Track B, pravo
minutes: 12
needs: A3
learn: What does a plan show before anything changes?
done_when: "`pravo plan` ends with `Plan: 2 to add, 0 to change, 0 to destroy.`"
level: beginner
---

A plan lists what pravo would change. Nothing changes until `pravo apply`.

```cue file=main.cue
package main

import "pravo.dev/pravo/providers/random"

pet: random.#Pet & {length: 3}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # pet (random_pet) will be created
  + pet: {
  +     id:        (known after apply)
  +     length:    3
  +     separator: "-"
    }

  # hello (local_file) will be created
  + hello: {
  +     content:  (known after apply)
  +     filename: "hello.txt"
    }

Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.
```

`+` marks a create. `(known after apply)` marks a value that only the provider can produce.

A change to an existing value shows `~`, and a replacement shows `-/+`.

```text
  # hello (local_file) must be replaced
-/+ hello: {
  ~     content:  "Hello!\n" -> (known after apply) # forces replacement
        filename: "hello.txt"
    }

Plan: 1 to add, 0 to change, 1 to destroy.
```

> [!WARNING]
> `pravo destroy` removes every resource recorded in state. Read the plan first.

## Recap

- `pravo plan` shows creates, changes, replacements and destroys.
- `(known after apply)` marks values the provider computes.
- The summary line counts the actions.

:::check
Which glyph marks a replacement?
::answer
`-/+`.
:::
