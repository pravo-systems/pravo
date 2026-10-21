---
title: "`pravo eval`"
description: Print the evaluated configuration with recorded state filled in.
order: 1
---

| Flag | Meaning |
|---|---|
| `-e EXPR` | Evaluate one expression. |
| `--json` | Print JSON on standard output. |
| `-t name=value` | Set an input. |

Exit code 0 means the configuration evaluated. Exit code 6 means it did not.

```sh run exit=6
pravo eval -t env=qa
```

```text out
Error: evaluating .:
env: conflicting values "prod" and "qa":
    ./main.cue:3:7
```

See [unification](glossary.md#unification) and the [moved page](eval.md).
