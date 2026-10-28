<!-- Generated from docs/reference/spec-outputs.md by tools/skillgen. Do not edit. -->

# Outputs

pravo.#Output publishes a value. It is recorded in state when a plan is applied and printed by pravo output.

An output is a struct built from `pravo.#Output`. It sits wherever you put it, and its name is its path, as for a resource: `url`, or `api.url` for one inside an instance of a definition.

| Field | Type | Meaning |
|---|---|---|
| `value` | any | What to publish: any CUE expression. It must be concrete once the plan is applied. |
| `description` | string, optional | What the value is for. |
| `sensitive` | bool, optional | `true` hides the value where outputs are listed. |

Work in an empty directory.

File `main.cue`:

```cue
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

```sh
pravo init > /dev/null
pravo plan | sed -n '/^Outputs:/,$p'
```

Output:

```text
Outputs:
  "api.url": (known after apply)
  greeting: (known after apply)
  name: (known after apply)
  summary: (known after apply)
  token: (sensitive)
```

An output whose value depends on a resource is unknown in the plan. After the apply it is concrete.

```sh
pravo apply --auto-approve | sed -n '/^Outputs:/,$p'
```

Output:

```text
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

5 outputs, 5 changed ("api.url", greeting, name, summary, token). See them with "pravo output" or "pravo serve".
```

## Reading outputs

Outputs are evaluated after the apply and recorded in state under their path. An apply does not print their values, because the log of an apply is often kept. It prints how many outputs there are and the names of those that changed. `pravo output` prints all of them, and `pravo serve` shows them. `pravo output <name>` prints one value. `--json` prints a JSON object. A struct value is recorded, printed by a plan and printed by `pravo output` with its keys in sorted order, whatever order the specification wrote them in.

```sh
pravo output
pravo output api.url
pravo output --json
```

Output:

```text
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

```sh
pravo output nope
```

Output:

```text
Error: output "nope" is not recorded in state (run "pravo apply")
```

## Sensitive outputs

A sensitive output prints as `(sensitive)` in plans, in the apply summary and in `pravo output` without a name. `pravo output <name>` and `--json` print the value. The state records it as it is, in the form `{"$sensitive": value}`, so protect the state as you protect any other. A value that must never be recorded must not be an output.

```sh
pravo output token
jq .token pravo.state/outputs.json
```

Output:

```text
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

```sh
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

File `main.cue`:

```cue
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

```sh
pravo apply --auto-approve
```

Output:

```text
Error: output summary: the field pet of its value reads itself, not the pet outside it. Rename the field, or bind the name first with let
```

Name the keys differently from the resources they read.

## See also

- [`pravo output`](output.md) prints the recorded outputs.
- [Environment and remote outputs](spec-env.md#remote-outputs) reads the outputs of another root.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says what a sensitive output hides.
