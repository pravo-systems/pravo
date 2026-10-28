<!-- Generated from docs/reference/spec-env.md by tools/skillgen. Do not edit. -->

# Environment and remote outputs

pravo.#Env and pravo.#SecretEnv read environment variables, pravo.#Secret reads the sealed state, and pravo.#RemoteOutputs reads the outputs of another root.

pravo reads five values for itself. Each is a struct built from a definition of the `pravo.dev/pravo` package.

| Definition | Reads | Where the value may go |
|---|---|---|
| `pravo.#Env` | An environment variable. | Anywhere, the [`$settings`](spec-settings.md) block included. It is an ordinary value. |
| `pravo.#SecretEnv` | An environment variable that is a secret. | Provider configuration, write-only attributes and other ephemeral resources. Never an output or an ordinary attribute. |
| `pravo.#Secret` | A secret that the [sealed state](../concepts/sealed-state.md) keeps. | The same places as `pravo.#SecretEnv`. |
| `pravo.#SecretVersion` | The number of the version of such a secret. | Anywhere. It is an ordinary value. |
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

## secrets of the sealed state

`pravo.#Secret` reads a secret from the sealed state, the one that [`pravo secret set`](secret.md) stored. It is an ephemeral resource like `pravo.#SecretEnv`, and the same rules say where its value may go. `pravo.#SecretVersion` reads the number of the version, which is not secret, so it is an ordinary value that can go anywhere.

| Field | `#Secret` | `#SecretVersion` | Meaning |
|---|---|---|---|
| `name` | yes | yes | The name of the secret, such as `prod/db_password`. |
| `version` | optional | computed | In a `#Secret`, the version to read; without it, the latest. In a `#SecretVersion`, the version that the run reads. |
| `value` | computed | no | What the secret holds, as a string. Known when the run starts. |

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

db:  pravo.#Secret & {name: "prod/db_password"}
rev: pravo.#SecretVersion & {name: "prod/db_password"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "password version \(rev.version)"
}
```

A secret is kept only in a sealed state. On a state that is not sealed the run stops before any provider starts, with exit code 5:

```sh
pravo plan
```

Output:

```text
Error: db: prod/db_password: secrets are kept in a sealed state: seal the state with pravo protect init --encrypt
```

`pravo validate`, `pravo fmt` and `pravo eval` do not open the state. `pravo eval` prints `value` as the type `string` and `version` as the type `int`:

```sh
pravo eval -e rev
```

Output:

```text
$kind: "secret_version"
// The name of the secret.
name: "prod/db_password"
// The version a run reads.
version: int
```

- **One version for a run.** The first node of a run that names a secret without a version decides which version it is, and the other nodes follow. A value and the number that rotates it belong together even when someone sets the secret while the run goes.
- **Read again at every plan and apply.** A plan file holds no value, and an apply from it reads the secret again. If a new version was set since the plan was made and the specification reads its number, the apply stops with exit code 3 and you make the plan again.
- **A secret that cannot be read stops the run.** A secret that is not set, was removed, or whose version was purged stops it before any provider starts, with the command that mends it. A version that the [roles](protect.md#roles) refuse stops it with exit code 7.
- **Rotation.** A write-only attribute is never stored, so the provider cannot see that the value changed. Wire `#SecretVersion` to the number that sits next to it, such as `password_wo_version`, and a new version of the secret changes that attribute. [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) does this with a certificate.

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
- [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) does the same for `pravo.#Secret`, with versions, roles and a rotation.
- [`pravo secret`](secret.md) sets, reads and removes the secrets that `pravo.#Secret` reads.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says where an ephemeral value may go.
- [Keep the state in a shared bucket](../how-to/remote-state.md) reads the outputs of another root from a bucket.
