---
title: Settings
description: The $settings block. It sets where the state lives and the default refresh age, and nothing else.
order: 21
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

`$settings` is the one top-level field that pravo reads for itself. It takes two keys. Any other key is an error.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `state.path` | string | `pravo.state` | Directory of the local state, relative to the root. |
| `state.backend` | `"local"` or `"s3"` | `"local"` | Where the state is kept. A `bucket` without a `backend` selects `"s3"`. |
| `state.bucket` and the other S3 keys | string, bool | none | The S3 backend. See [S3 keys](#s3-keys). |
| `refresh` | `"always"`, `"never"` or a duration | `"always"` | The default [refresh age](glossary.md#refresh-age) of every resource. |

Write the block as `pravo.#Settings & {...}`. The schema comes from the generated `pravo.dev/pravo` package. It lets editors complete the keys, and CUE rejects a misspelt key or a value of the wrong type. Work in an empty directory.

```cue file=main.cue
package demo

import "pravo.dev/pravo"

env: *"dev" | "staging" | "prod" @tag(env)

$settings: pravo.#Settings & {
	state: path: "state/\(env)"
	refresh: "1h"
}
```

```sh run
pravo init
pravo eval -e '$settings.state.path'
pravo eval -t env=prod -e '$settings.state.path'
pravo eval -e '$settings.refresh'
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:

pravo has been initialised. Run "pravo plan" to see what it would do.
"state/dev"
"state/prod"
"1h"
```

The state path depends on the tag `env`. When `env` has no default and no tag is given, the path is not concrete, and pravo says so:

```cue file=main.cue
package demo

import "pravo.dev/pravo"

env: "dev" | "staging" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.state.path is not concrete: $settings.state.path: non-concrete value string
(if it depends on a tagged field such as env, pass -t env=...)
```

The block can read an environment variable too. A [`pravo.#Env`](spec-env.md#env) is filled in before the block is read, with the variable when it is set and with its `default` when it is not, so one specification can keep its state where a variable says. The `name` and the `default` are strings written out in the field. A `pravo.#SecretEnv` is not read into the block. A variable that is not set and has no default leaves the setting not concrete, and the error names the variable.

```cue file=main.cue
package demo

import "pravo.dev/pravo"

stateDir: pravo.#Env & {name: "DEMO_STATE_DIR", default: "state/dev"}

$settings: pravo.#Settings & {state: path: stateDir.value}

where: pravo.#Output & {value: stateDir.value}
```

```sh run
pravo apply > /dev/null
DEMO_STATE_DIR=state/prod pravo apply > /dev/null
ls state
```

```text out
dev
prod
```

## state

`state.path` is the directory of the state. A command that writes the state creates the directory when it does not exist. A command that only reads it, such as `eval`, `output` or `pravo state list`, reads an empty state and creates nothing. A path that names a file is an error, and pravo changes nothing. The layout is in [State format](state-format.md).

The block is typed by backend. `path` belongs to the local backend. The S3 keys belong to `s3`. Mixing them is an error when the root is evaluated.

```cue file=main.cue
package demo

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: {backend: "s3", bucket: "infra-state", path: "x"}
}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
$settings.state: 2 errors in empty disjunction:
$settings.state.backend: conflicting values "local" and "s3":
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:83:10
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:97:33
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:114:12
    ./main.cue:5:12
    ./main.cue:6:19
$settings.state.path: field not allowed:
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:101:2
    ./main.cue:6:48
```

The same mistakes without the schema are reported in pravo's words:

```cue file=main.cue
package demo

$settings: state: {backend: "local", bucket: "infra-state"}
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.state.bucket belongs to the s3 backend: set backend: "s3", or remove it for local state
```

### S3 keys

The graph can live in an S3 bucket or in any store that speaks the S3 API. The layout is the same, with objects in place of files.

| Key | Meaning |
|---|---|
| `backend` | `"s3"`. Required for these keys, unless `bucket` is set. |
| `bucket` | Bucket name. Required. The bucket must exist. |
| `prefix` | Key prefix under which the graph lives. Several roots can share a bucket. Default: the bucket root. |
| `region` | Region to sign for. Default: the AWS configuration, then `us-east-1`. |
| `endpoint` | Service URL of an S3-compatible store. Implies path-style addressing. |
| `path_style` | Forces path-style addressing: `host/bucket/key`. |
| `profile` | A shared-configuration profile. |
| `access_key`, `secret_key` | Static credentials, used when both are set. They are stored in the configuration. |

Without static credentials, pravo uses the AWS credential chain: environment variables, the shared files, then an instance or task role. A lock is an object created with a conditional write. See [State format](state-format.md#locks-and-reservations).

```cue file=main.cue
package demo

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: {
		backend:    "s3"
		bucket:     "infra-state"
		prefix:     "teams/platform/app"
		endpoint:   "https://minio.internal:9000"
		path_style: true
	}
}
```

```sh run
pravo validate
```

```text out
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.
```

`pravo validate` does not open the state. Commands that read or write it, such as `plan` and `eval`, connect to the bucket.

Any other `backend` value is rejected:

```cue file=main.cue
package demo

$settings: state: backend: "gcs"
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.state.backend: unknown backend "gcs" (want local or s3)
```

## refresh

`refresh` is the default refresh age: how old a record may be before a plan that looks at the resource reads it from its provider again. A plan looks at the resources it reaches and at the resources they read; the rest are not read, whatever their age. See [Plans](../concepts/plans.md#refresh-age).

| Value | Meaning |
|---|---|
| `"always"` | Age zero. Every plan reads the records it looks at. This is the default. |
| `"never"` | No limit. A plan reads a record only on request. |
| a number and a unit | `ms`, `s`, `m`, `h` or `d`, such as `"30m"`, `"6h"` or `"7d"`. |

A resource overrides the default with `$lifecycle: refresh`; see [Meta fields](spec-meta-fields.md#lifecycle). The command line overrides both for one run with `-refresh=expired`, `-refresh=all` or `-refresh=false`. The setting is not an age of its own for a resource: only `$lifecycle: refresh` makes a plan read a resource that it does not look at.

A longer age means fewer calls to the providers for the records that a plan looks at, and a longer time for which a change made outside pravo can go unseen. [Choosing a refresh age](../concepts/plans.md#choosing-a-refresh-age) says how to pick one.

```cue file=main.cue
package demo

import "pravo.dev/pravo"

$settings: pravo.#Settings & {refresh: "soon"}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
$settings.refresh: 3 errors in empty disjunction:
$settings.refresh: conflicting values "always" and "soon":
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:87:12
    ./main.cue:5:12
    ./main.cue:5:40
$settings.refresh: conflicting values "never" and "soon":
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:87:23
    ./main.cue:5:12
    ./main.cue:5:40
$settings.refresh: invalid value "soon" (out of bound =~"^[0-9]+(ms|s|m|h|d)$"):
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:87:33
    ./cue.mod/gen/pravo.dev/pravo/settings_gen.cue:87:12
    ./main.cue:5:40
```

Without the schema, pravo checks the value itself:

```cue file=main.cue
package demo

$settings: refresh: "soon"
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.refresh: "soon" is not a refresh age; want "always", "never" or a duration such as "30m", "6h" or "7d"
```

## Unknown keys

`$settings` takes `state` and `refresh`. A plan policy is named with `--policy` by whoever runs pravo, and `$settings` has no key for it. See [Policies](policies-overview.md#naming-a-policy).

```cue file=main.cue
package demo

$settings: policy: "policy/plan.cue"
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.policy is not a setting (the block takes state and refresh)
```

## See also

- [Keep the state in a shared bucket](../how-to/remote-state.md) moves a root to S3.
- [Run one specification in several environments](../how-to/environments.md) computes the state path from a tag.
- [State format](state-format.md) describes the files that `state.path` holds, and [Plans](../concepts/plans.md#refresh-age) explains the refresh age.
