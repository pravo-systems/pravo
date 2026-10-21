---
title: pravo output
description: Print the output values that the last apply recorded in the state, as CUE or as JSON.
order: 6
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo output [NAME] [--json]` |
| Changes infrastructure | No. |
| Reads | The outputs recorded in the state. It evaluates the specification to find the state, starts no provider and plans nothing. |
| Taught in | [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md). |

### Arguments and flags

| Argument or flag | Default | Meaning |
|---|---|---|
| `NAME` | all outputs | Print only this output as a CUE value. A string keeps its quotes. The name is the path of the output, such as `url` or `api.gateway`. |
| `--json` | off | Print JSON instead of CUE. A sensitive output is printed in the clear. For `output`, this flag replaces the global `--json` and prints no error object. |
| `-t`, `--tag key=value`, `-T` | none | Tags, as for [`pravo plan`](plan.md). |

The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The outputs were printed. With no outputs recorded, nothing is printed. |
| 1 | `NAME` is not recorded in the state. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. An output is a struct built from `pravo.#Output`. Its name is its path.

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 8000, max: 8999, seed: "port"}

url:   pravo.#Output & {value: "http://localhost:\(port.result)"}
token: pravo.#Output & {value: "t-\(port.id)", sensitive: true}
info: pravo.#Output & {value: {host: "localhost", listen: port.result}}
api: gateway: pravo.#Output & {value: "gw-\(port.id)"}
```

Outputs exist after an apply. Before it, `pravo output` prints nothing, and a name is an error.

```sh run exit=1
pravo init > /dev/null
pravo output url
```

```text out
Error: output "url" is not recorded in state (run "pravo apply")
```

```sh run
pravo apply -auto-approve > /dev/null
pravo output
```

```text out
"api.gateway": "gw-8056"
info: {
	host:   "localhost"
	listen: 8056
}
token: (sensitive)
url: "http://localhost:8056"
```

A sensitive output prints as `(sensitive)` in the list. The list quotes a name that contains a dot.

### One output

```sh run
pravo output url
pravo output token
pravo output api.gateway
```

```text out
"http://localhost:8056"
"t-8056"
"gw-8056"
```

Naming a sensitive output prints its value. The state holds it in the clear, so protect the state as any other secret.

### JSON

```sh run
pravo output --json
pravo output url --json
```

```text out
{
  "api.gateway": "gw-8056",
  "info": {
    "host": "localhost",
    "listen": 8056
  },
  "token": "t-8056",
  "url": "http://localhost:8056"
}
"http://localhost:8056"
```

## See also

- [Outputs](spec-outputs.md) describes `pravo.#Output` and its fields.
- [`pravo state`](state.md) reads the state itself.
- [Remote outputs](spec-env.md) reads the outputs of another root.
