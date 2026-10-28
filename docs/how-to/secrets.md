---
title: Keep secrets out of the state
description: Read a secret from the environment with pravo.#SecretEnv, hand it to a provider and see where pravo allows the value and where it refuses.
order: 5
minutes: 10
needs: B6 for providers and credentials. An empty directory.
done_when: "A secret reaches a provider's configuration, no file in the root contains it, and a use of it in an output or an attribute is refused."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
group: Operate
keywords: "sensitive ephemeral secret password"
---

pravo reads an environment variable with `pravo.#Env` or with `pravo.#SecretEnv`. You choose, because most variables are not secret. The difference is where the value may go.

| Where the value is used | `pravo.#Env` | `pravo.#SecretEnv` |
|---|---|---|
| Provider configuration | allowed | allowed |
| A write-only attribute of a resource | allowed | allowed |
| Another ephemeral resource | allowed | allowed |
| An ordinary attribute of a resource | allowed, and recorded in the state | refused |
| An output | allowed, and recorded in the state | refused |
| A default | `default` field | none; an unset variable stops the run |

A `pravo.#SecretEnv` is an ephemeral value. It exists for the run and is never written to the state or to a saved plan. pravo checks where it is used before any provider runs.

Work in an empty directory.

## 1. Read a secret into a provider

The `tls` provider takes a proxy password in its configuration. The root reads it from `DB_PASSWORD` and builds a key through the provider.

```cue file=main.cue title="main.cue" hl="8,14"
package secrets

import (
	"pravo.dev/pravo"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

db: pravo.#SecretEnv & {name: "DB_PASSWORD"}

$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "pravo"
		password: db.value
	}]
}

key: $tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}
```

A secret has no default. Without the variable the run stops.

```sh run exit=1
pravo init > /dev/null
pravo plan
```

```text out
Error: db: the environment variable DB_PASSWORD is not set
```

With the variable, plan to a file and apply it. The value is a test value.

```sh run
DB_PASSWORD=correct-horse pravo plan -out change.plan | grep -E '^(Plan|Saved)'
DB_PASSWORD=correct-horse pravo apply change.plan | grep '^Apply complete'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Saved the plan to change.plan. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

Search the root, the plan and the state for the value.

```sh run exit=1
grep -r correct-horse .
```

```text out
```

No file contains it: not the state, not the saved plan, not the generated packages. `grep` exits 1 when it finds nothing.

## 2. Know what the state does hold

A provider decides what it returns, and everything a resource returns is recorded. The key that the provider generated is in the state in clear.

```sh run
grep -l 'PRIVATE KEY' pravo.state/resources/key.json
ls -l change.plan | cut -c1-10
```

```text out
pravo.state/resources/key.json
-rw-------
```

Protect the state as you protect the secrets in it. A saved plan is created with owner-only permissions, because its values can include secrets. A plan that `pravo plan --store` writes into the state holds the same values, and whoever can read the state can read it. `pravo serve` withholds them from a stored plan as it does from a plan file.

## 3. Break it: a secret in a recorded place

An output is recorded in the state, so a `pravo.#SecretEnv` value may not be an output.

```cue file=leak.cue title="leak.cue" status=rejected hl="5"
package secrets

import "pravo.dev/pravo"

shown: pravo.#Output & {value: db.value}
```

```sh run exit=1
DB_PASSWORD=correct-horse pravo plan
```

```text out
Error: output shown references db; outputs are recorded in state, so ephemeral values cannot be used in them
```

An ordinary attribute is recorded too.

```cue file=leak.cue title="leak.cue" status=rejected hl="7"
package secrets

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2, prefix: db.value}
```

```sh run exit=1
pravo init > /dev/null
DB_PASSWORD=correct-horse pravo plan
```

```text out
Error: pet.prefix: references db, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

Both messages name the attribute and the secret. `pravo validate` does not read the environment, so it does not report this: `plan` and `apply` do, before any provider runs.

## 4. Use #Env for what is not secret

A variable that is not secret is an ordinary value. It can go anywhere, and it is recorded where it is used.

```cue file=leak.cue title="leak.cue" hl="10,12"
package secrets

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

region: pravo.#Env & {name: "REGION", default: "eu-west-1"}

pet: $random.#Resource.random_pet & {length: 2, prefix: region.value}
```

```sh run
DB_PASSWORD=correct-horse pravo apply -auto-approve | grep '^Apply complete'
grep -l eu-west-1 pravo.state/resources/pet.json
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
pravo.state/resources/pet.json
```

An unset `pravo.#Env` takes its `default`. Without a default, the run stops with `the environment variable REGION is not set, and there is no default`.

## 5. Keep secrets out of tags and logs

A value that goes through `-t` is written into saved plans and stored plans, and feeds the configuration hash. Tags are for configuration such as an environment name.

```cue file=tag.cue title="tag.cue"
package secrets

token: string @tag(token)
```

```sh run
DB_PASSWORD=correct-horse pravo plan -t token=hunter2 -out tagged.plan > /dev/null
grep -l hunter2 tagged.plan
rm tag.cue
```

```text out
tagged.plan
```

The debug log names a variable and says whether it is set. It never prints the value.

```sh run
DB_PASSWORD=correct-horse pravo plan -v 2>&1 | grep 'environment variable' | cut -d' ' -f2- | sort
```

```text out
debug environment variable addr=db name=DB_PASSWORD set=true
debug environment variable addr=region name=REGION set=false
```

## 6. Know what sensitive hides

`sensitive: true` on an output hides the value where outputs are listed. It does not keep the value out of the state.

```cue file=sensitive.cue title="sensitive.cue" hl="5"
package secrets

import "pravo.dev/pravo"

fingerprint: pravo.#Output & {value: key.public_key_fingerprint_sha256, sensitive: true}
```

```sh run
DB_PASSWORD=correct-horse pravo apply -auto-approve > /dev/null
pravo output
grep -c '\$sensitive' pravo.state/outputs.json
```

```text out
fingerprint: (sensitive)
1
```

An output that should never be recorded must not be an output.

## 7. Hand a secret to a write-only attribute

A write-only attribute is an attribute that the provider reads from the configuration and never stores. It is the one attribute of a resource that accepts a `pravo.#SecretEnv` value. The `tls` provider signs a certificate with a private key that it is given in `private_key_pem_wo`. The key is read from the environment.

```cue file=cert.cue title="cert.cue" hl="5,8"
package secrets

import "pravo.dev/pravo"

signing_key: pravo.#SecretEnv & {name: "SIGNING_KEY_PEM"}

cert: $tls.#Resource.tls_self_signed_cert & {
	private_key_pem_wo:         signing_key.value
	private_key_pem_wo_version: 1
	subject: [{common_name: "example.test"}]
	validity_period_hours: 24
	allowed_uses: ["digital_signature"]
}
```

Any PEM private key works. This block makes one with `openssl` and passes it to the run. No file holds it.

```sh run
export SIGNING_KEY_PEM="$(openssl ecparam -name prime256v1 -genkey -noout)"
DB_PASSWORD=correct-horse pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The state holds null for the attribute, and no private key.

```sh run
pravo state show cert | grep private_key_pem_wo
grep -q 'PRIVATE KEY' pravo.state/resources/cert.json || echo "not found"
```

```text out
	private_key_pem_wo:         null
	private_key_pem_wo_version: 1
not found
```

The provider keeps no copy of the key, so it cannot see that a key changed. Its description of `private_key_pem_wo_version` in `pravo schema tls_self_signed_cert` says to increment that number when the key is rotated.

## What next

- [Concepts: secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) describes how pravo orders ephemeral resources.
- [Environments](environments.md) uses a tag for the environment name, which is configuration.
- [Environment variables and remote outputs](../reference/spec-env.md) is the reference for `pravo.#Env` and `pravo.#SecretEnv`.
- [Keep the state in a shared store](keep-the-state-in-a-shared-store.md) covers who may read the bucket that holds the state.
- [Seal the state](seal-the-state.md) encrypts the values in the state to a list of readers.
