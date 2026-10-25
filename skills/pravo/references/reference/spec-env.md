<!-- Generated from docs/reference/spec-env.md by tools/skillgen. Do not edit. -->

# Environment and remote outputs

pravo.#Env and pravo.#SecretEnv read environment variables. pravo.#RemoteOutputs reads the outputs another root has recorded.

pravo reads three values for itself. Each is a struct built from a definition of the `pravo.dev/pravo` package.

| Definition | Reads | Where the value may go |
|---|---|---|
| `pravo.#Env` | An environment variable. | Anywhere, the [`$settings`](spec-settings.md) block included. It is an ordinary value. |
| `pravo.#SecretEnv` | An environment variable that is a secret. | Provider configuration, write-only attributes and other ephemeral resources. Never an output or an ordinary attribute. |
| `pravo.#RemoteOutputs` | The outputs another root has recorded. | Anywhere. It is an ordinary value. |

## env

An environment variable is read when the run starts, on every plan and apply. Work in an empty directory.

| Field | `#Env` | `#SecretEnv` | Meaning |
|---|---|---|---|
| `name` | yes | yes | The name of the variable. |
| `default` | yes | no | Used when the variable is not set. A secret has none. |
| `value` | yes | yes | What the variable holds, as a string. Known when the run starts. |

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

region: pravo.#Env & {name: "DEMO_REGION", default: "eu-west-1"}

where: pravo.#Output & {value: region.value}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "region \(region.value)"
}
```

```sh
pravo init > /dev/null
pravo plan | sed -n '/^Outputs:/,$p'
DEMO_REGION=us-east-1 pravo plan | sed -n '/^Outputs:/,$p'
```

Output:

```text
Outputs:
  where: "eu-west-1"
Outputs:
  where: "us-east-1"
```

`#Env` is a plain value. `region.value` can go into an attribute, an output or an interpolation, and it is recorded wherever it is used. An `#Env` without a `default` stops the run when the variable is not set:

File `main.cue`:

```cue
package demo

import "pravo.dev/pravo"

region: pravo.#Env & {name: "DEMO_REGION"}

where: pravo.#Output & {value: region.value}
```

```sh
pravo plan
```

Output:

```text
Error: region: the environment variable DEMO_REGION is not set, and there is no default
```

`pravo eval` does not read the environment. It prints `value` as the type `string`.

The `$settings` block is read before the run starts, so pravo fills in the `#Env` values for it first: the state can live in a bucket that a variable names. [Settings](spec-settings.md) has an example.

Name the struct differently from any attribute that uses it. Inside `pet: ... & {prefix: prefix.value}` the name `prefix` is the attribute being defined, not the variable.

A value that goes through `-t` is recorded in saved plans and feeds the configuration hash. Tags are for configuration such as an environment name, not for credentials.

## secret env

`pravo.#SecretEnv` is an [ephemeral resource](spec-resources.md#ephemeral-resources). Its value may be used in provider configuration, in write-only attributes and in other ephemeral resources, and nowhere that is recorded. It never reaches state or a saved plan. [Keep secrets out of the state](../how-to/secrets.md) uses one in provider configuration and one in a write-only attribute.

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

db: pravo.#SecretEnv & {name: "DEMO_DB_PASSWORD"}

bad: $local.#Resource.local_file & {
	filename: "bad.txt"
	content:  db.value
}
```

An unset variable stops the run. A secret has no default:

```sh
pravo plan
```

Output:

```text
Error: db: the environment variable DEMO_DB_PASSWORD is not set
```

A set variable used in an ordinary attribute is refused:

```sh
DEMO_DB_PASSWORD=x pravo plan
```

Output:

```text
Error: bad.content: references db, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

An output that reads the secret is refused too:

File `main.cue`:

```cue
package demo

import "pravo.dev/pravo"

db: pravo.#SecretEnv & {name: "DEMO_DB_PASSWORD"}

leak: pravo.#Output & {value: db.value}
```

```sh
DEMO_DB_PASSWORD=x pravo plan
```

Output:

```text
Error: output leak references db; outputs are recorded in state, so ephemeral values cannot be used in them
```

## remote outputs

`pravo.#RemoteOutputs` reads the outputs another root has recorded.

| Field | Meaning |
|---|---|
| `state` | Where the other root keeps its state: `path` (a directory, relative to this root) or the S3 keys of [Settings](spec-settings.md#s3-keys). |
| `outputs` | The other root's outputs, by name. Filled in when the run starts. |

An output named with dots, such as `api.url`, is a quoted label: `net.outputs["api.url"]`.

- Only `outputs.json` is read. The other root's resources are never opened. A bucket policy that allows `GetObject` on that one object gives a reader the outputs and nothing else. Nothing is created or locked in the other root's state.
- If the other root's state is [sealed](../concepts/sealed-state.md), its `outputs.json` is sealed to its readers, and `meta.json` is read as well. The outputs are opened with the identity of the person running the command, which must be one of the readers of that state, and pravo looks for it only then. With several identity files and none named like the user, the one whose reader key has a lock in the current group of that state's `meta.json` is used, so that one identity directory serves roots with different readers. `--identity NAME` names one. A person who is not a reader gets exit code 5.
- The outputs are read on every plan and apply. The value is an ordinary one, recorded wherever it is used. A change in the other root shows up as a change here at the next plan.
- An output that the other root marked `sensitive` is not included. A root converted with `pravo state import-tfstate` keeps the mark that its OpenTofu state had.
- pravo does not make this root wait for the other. Apply the producer first. The order between roots is yours to enforce, in CI for example.

Two roots in subdirectories of one directory:

File `network/main.cue`:

```cue
package network

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

subnet: $random.#Resource.random_pet & {length: 2}

subnet_id:  pravo.#Output & {value: subnet.id}
api: url:   pravo.#Output & {value: "https://api.example.com"}
admin_key:  pravo.#Output & {value: "k-123", sensitive: true}
```

File `app/main.cue`:

```cue
package app

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

net: pravo.#RemoteOutputs & {state: path: "../network/pravo.state"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "subnet \(net.outputs.subnet_id) at \(net.outputs["api.url"])"
}
```

```sh
pravo -C network init > /dev/null
pravo -C app init > /dev/null
```

Before the producer is applied, the consumer stops with a message that names the cause:

```sh
pravo -C app plan
```

Output:

```text
Error: net: ./network/pravo.state has no recorded outputs; apply that root first
```

Apply the producer, then plan the consumer:

```sh
pravo -C network apply --auto-approve | sed -n '/^Outputs:/,$p'
pravo -C app plan | grep -E 'content: |^Plan'
```

Output:

```text
Outputs:
  admin_key: (sensitive)
  "api.url": "https://api.example.com"
  subnet_id: (known after apply)
subnet: Creating...
subnet: Creation complete after 0s [id=<pet>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]

3 outputs, 3 changed (admin_key, "api.url", subnet_id). See them with "pravo output" or "pravo serve".
  +     content:              "subnet <pet> at https://api.example.com"
Plan: 1 to add, 0 to change, 0 to destroy.
```

A comprehension over `net.outputs` refers to `net` as well. The resource that holds it plans after the outputs are read:

File `app/main.cue`:

```cue
package app

import (
	"strings"
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

net: pravo.#RemoteOutputs & {state: path: "../network/pravo.state"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  strings.Join([for name, _ in net.outputs {name}], ", ")
}
```

```sh
pravo -C app plan | grep -E 'content: |^Plan'
```

Output:

```text
  +     content:              "api.url, subnet_id"
Plan: 1 to add, 0 to change, 0 to destroy.
```

The sensitive output `admin_key` is not in `net.outputs`. A reference to it, or to a name the other root does not have, plans as unknown and fails at the apply:

File `app/main.cue`:

```cue
package app

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

net: pravo.#RemoteOutputs & {state: path: "../network/pravo.state"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  net.outputs.admin_key
}
```

```sh
pravo -C app apply --auto-approve
```

Output:

```text
pravo will perform the following actions:

  # note (local_file) will be created
  + note: {
  +     content:              (known after apply)
  +     content_base64sha256: (known after apply)
  +     content_base64sha512: (known after apply)
  +     content_md5:          (known after apply)
  +     content_sha1:         (known after apply)
  +     content_sha256:       (known after apply)
  +     content_sha512:       (known after apply)
  +     directory_permission: "0777"
  +     file_permission:      "0777"
  +     filename:             "note.txt"
  +     id:                   (known after apply)
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1

Error: note.content: value depends on net, which is still unknown at apply time (note.content: undefined field: admin_key)
```

Where a data source can look the object up in the real infrastructure, it does not depend on another root's state at all. Use `pravo.#RemoteOutputs` for what has no such lookup, such as a generated name or a computed id.

## See also

- [Keep secrets out of the state](../how-to/secrets.md) uses `pravo.#SecretEnv` step by step.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says where an ephemeral value may go.
- [Keep the state in a shared bucket](../how-to/remote-state.md) reads the outputs of another root from a bucket.
