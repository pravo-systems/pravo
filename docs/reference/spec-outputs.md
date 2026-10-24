---
title: Outputs
description: pravo.#Output publishes a value. It is recorded in state when a plan is applied and printed by pravo output.
order: 25
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '[a-z]+-[a-z]+(\.example\.com|"|!)'
    replace: 'pet$1'
  - pattern: '\[id=[a-z]+-[a-z]+\]'
    replace: '[id=<pet>]'
  - pattern: '\[id=[0-9a-f]{40}\]'
    replace: '[id=<sha1>]'
---

An output is a struct built from `pravo.#Output`. It sits wherever you put it, and its name is its path, as for a resource: `url`, or `api.url` for one inside a module instance.

| Field | Type | Meaning |
|---|---|---|
| `value` | any | What to publish: any CUE expression. It must be concrete once the plan is applied. |
| `description` | string, optional | What the value is for. |
| `sensitive` | bool, optional | `true` hides the value where outputs are listed. |

Work in an empty directory.

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet:   $random.#Resource.random_pet & {length: 2}
hello: $local.#Resource.local_file & {filename: "hello.txt", content: "Hello, \(pet.id)!"}

name: pravo.#Output & {value: pet.id}

greeting: pravo.#Output & {
	value:       hello.content
	description: "The text of the file"
}

summary: pravo.#Output & {
	value: {
		server: pet.id
		file:   hello.filename
	}
}

token: pravo.#Output & {value: pet.id, sensitive: true}

api: url: pravo.#Output & {value: "https://\(pet.id).example.com"}
```

```sh run
pravo init > /dev/null
pravo plan | sed -n '/^Outputs:/,$p'
```

```text out
Outputs:
  "api.url": (known after apply)
  greeting: (known after apply)
  name: (known after apply)
  summary: (known after apply)
  token: (sensitive)
```

An output whose value depends on a resource is unknown in the plan. After the apply it is concrete.

```sh run
pravo apply --auto-approve | sed -n '/^Outputs:/,$p'
```

```text out
Outputs:
  "api.url": (known after apply)
  greeting: (known after apply)
  name: (known after apply)
  summary: (known after apply)
  token: (sensitive)
pet: Creating...
pet: Creation complete after 0s [id=<pet>]
hello: Creating...
hello: Creation complete after 0s [id=<sha1>]

Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

Outputs:
  "api.url": "https://pet.example.com"
  greeting: "Hello, pet!"
  name: "pet"
  summary: {
  	file:   "hello.txt"
  	server: "pet"
  }
  token: (sensitive)
```

## Reading outputs

Outputs are evaluated after the apply and recorded in state under their path. `pravo output` prints all of them. `pravo output <name>` prints one value. `--json` prints a JSON object. A struct value is recorded, printed by a plan and printed by `pravo output` with its keys in sorted order, whatever order the specification wrote them in.

```sh run
pravo output
pravo output api.url
pravo output --json
```

```text out
"api.url": "https://pet.example.com"
greeting: "Hello, pet!"
name: "pet"
summary: {
	file:   "hello.txt"
	server: "pet"
}
token: (sensitive)
"https://pet.example.com"
{
  "api.url": "https://pet.example.com",
  "greeting": "Hello, pet!",
  "name": "pet",
  "summary": {
    "file": "hello.txt",
    "server": "pet"
  },
  "token": "pet"
}
```

A name that is not recorded is an error, with exit code 1:

```sh run exit=1
pravo output nope
```

```text out
Error: output "nope" is not recorded in state (run "pravo apply")
```

## Sensitive outputs

A sensitive output prints as `(sensitive)` in plans, in the apply summary and in `pravo output` without a name. `pravo output <name>` and `--json` print the value. The state records it as it is, in the form `{"$sensitive": value}`, so protect the state as you protect any other. A value that must never be recorded must not be an output.

```sh run
pravo output token
jq .token pravo.state/outputs.json
```

```text out
"pet"
{
  "$sensitive": "pet"
}
```

Another root reads the outputs of this one with `pravo.#RemoteOutputs`. A sensitive output is not included. See [Environment and remote outputs](spec-env.md#remote-outputs).

## Rules

| Rule | Error |
|---|---|
| An output may not use an ephemeral value, because it is recorded in state. | `output text references secret; outputs are recorded in state, so ephemeral values cannot be used in them` |
| An output may not sit inside a resource. | CUE rejects the field: `pet.out: field not allowed`. |
| A field of the value may not read a name that it repeats. | `output summary: the field pet of its value reads itself, not the pet outside it. Rename the field, or bind the name first with let` |

An output is a field like any other, so the usual scoping applies. Inside `value: {pet: pet.id}` the inner `pet` is the field being defined, and the value never resolves.

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}

summary: pravo.#Output & {
	value: {pet: pet.id}
}
```

```sh run exit=6
pravo apply --auto-approve
```

```text out
Error: output summary: the field pet of its value reads itself, not the pet outside it. Rename the field, or bind the name first with let
```

Name the keys differently from the resources they read.

## See also

- [`pravo output`](output.md) prints the recorded outputs.
- [Environment and remote outputs](spec-env.md#remote-outputs) reads the outputs of another root.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says what a sensitive output hides.
