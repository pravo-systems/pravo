<!-- Generated from docs/concepts/secrets-and-ephemeral-values.md by tools/skillgen. Do not edit. -->

# Secrets and ephemeral values

Which values pravo records in state, saved plans and outputs, what an ephemeral value is, where it may go, and what hiding a value does and does not do.

pravo handles four kinds of value that need care. A provider marks some attributes sensitive. Some values must never be recorded, and pravo calls them ephemeral. Some values come from the environment. Some are secrets that the sealed state keeps. Each travels through plans, the state and outputs in a different way. This page says where each one goes.

## Where a value is recorded

| Value | State | Saved plan | Printed plan | Plan JSON and policy document |
|---|---|---|---|---|
| An attribute of a resource or data source | Recorded | Recorded | Printed | Present |
| An attribute that the provider marks sensitive | Recorded as returned | Recorded | `(sensitive)` | Present, not masked |
| A value read with `pravo.#Env` | Wherever it is used | Wherever it is used | Where it is used | Where it is used |
| The number of a version, read with `pravo.#SecretVersion` | Wherever it is used | Wherever it is used | Where it is used | Where it is used |
| An output | Recorded | Recorded when it is known | Printed | Present |
| An output with `sensitive: true` | Recorded, wrapped as `$sensitive` | Recorded when it is known | `(sensitive)` | Present, not masked |
| An ephemeral value | Never | Never | Never | Never |

Hiding a value is a matter of display. `(sensitive)` keeps a value out of a terminal, a log and a review. It does not keep the value out of the state, the saved plan or the plan document. An ephemeral value is the one kind that pravo keeps out of all of them.

## Ephemeral values

An **ephemeral resource** is a provider object that exists for the length of one run. It is built through a provider like any other resource, from its `#Ephemeral.TYPE` definition. pravo opens it when the first resource that needs it runs. It renews it on the schedule that the provider asks for. It closes every open one when the run ends, whether the run succeeded or failed.

Plan and apply are separate runs. An ephemeral resource that a plan opened is closed at the end of the plan, and the apply opens it again. Nothing about it is written to the state or to a saved plan. The result lives in the running process. The `Graph:` line counts how many were opened.

Work in an empty directory. The specification below runs a command and uses its output as the password of a provider.

File `main.cue`:

```cue
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$local: loc.#Provider

// An ephemeral resource: opened for the run, never recorded.
token: $local.#Ephemeral.local_command & {
	command:   "printf"
	arguments: ["hunter2-marker"]
}

// Provider configuration may use an ephemeral value.
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://127.0.0.1:1"
		username: "pravo"
		password: token.stdout
	}]
}

deploy: $tls.#Resource.tls_private_key & {algorithm: "ED25519"}

public: pravo.#Output & {value: deploy.public_key_openssh}
private: pravo.#Output & {value: deploy.private_key_pem, sensitive: true}
```

```sh
pravo init | tail -1
```

Output:

```text
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh
pravo plan -out p.plan | grep -E '^(Plan|Graph)'
```

Output:

```text
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · opened 1
```

The plan opened `token`. The apply of the saved plan opens it again.

```sh
pravo apply p.plan | grep -E '^(Apply|token)'
```

Output:

```text
token: Opening...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The marker `hunter2-marker` is nowhere in the state or in the saved plan.

```sh
grep -r hunter2-marker pravo.state p.plan || echo "not found"
```

Output:

```text
not found
```

### Where an ephemeral value may go

Everything that a managed resource or a data source returns is recorded in the state, and so is every output. An ephemeral value may therefore be used in only three places.

- The configuration of a provider, which is the fields of a provider struct.
- A write-only attribute of a managed resource. The provider accepts it and never stores it.
- Another ephemeral resource.

*Figure: Where an ephemeral value may go. Every refused place leads to the state.*

A reference from any other attribute is an error that names the attribute and the ephemeral resource.

File `leak.cue`:

```cue
package estate

leak: $local.#Resource.local_file & {
	filename: "leak.txt"
	content:  token.stdout
}
```

pravo checks the rule when it converts the body of a resource, before it asks the provider about that resource. `pravo validate` does not run the check.

```sh
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 1 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

```sh
pravo plan
```

Output:

```text
token: Opening...

Error: leak.content: references token, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

```sh
rm leak.cue
```

An output is recorded in the state, so an ephemeral value cannot be an output. pravo checks the outputs before it plans.

File `leakout.cue`:

```cue
package estate

import "pravo.dev/pravo"

leaked: pravo.#Output & {value: token.stdout}
```

```sh
pravo plan
```

Output:

```text
Error: output leaked references token; outputs are recorded in state, so ephemeral values cannot be used in them
```

```sh
rm leakout.cue
```

A provider function does not accept an ephemeral value, because the result of a call can end up in the state. When the configuration of an ephemeral resource is unknown during a plan, the open waits for the apply. It is unknown when it refers to a resource that does not exist yet.

### Environment variables

pravo reads an environment variable itself, with no provider, on every plan and apply. There are two kinds. A `pravo.#Env` is an ordinary value. It is recorded wherever it is used. A `pravo.#SecretEnv` is an ephemeral value. The same rules decide where it may go.

File `env.cue`:

```cue
package estate

import "pravo.dev/pravo"

region:  pravo.#Env & {name: "REGION", default: "eu-west-1"}
api_key: pravo.#SecretEnv & {name: "API_KEY"}

noted: $local.#Resource.local_file & {
	filename: "noted.txt"
	content:  "region = \(region.value)\n"
}
```

A variable that is not set and has no default is an error. A `#SecretEnv` has no default.

```sh
env -u API_KEY pravo plan
```

Output:

```text
Error: api_key: the environment variable API_KEY is not set
```

The value of `region` is an ordinary attribute of `noted`, so the state holds it.

```sh
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
grep -c "eu-west-1" pravo.state/resources/noted.json
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
1
```

The kind is chosen where the variable is declared.

### Secrets of the sealed state

A `pravo.#Secret` is an ephemeral value that comes from the [sealed state](sealed-state.md) of the root instead of the environment of the process. [`pravo secret set`](../reference/secret.md) keeps it there as a named secret with numbered versions, sealed to the same readers as the records. A run reads the latest version when it starts, or the version that `version` names, and stops before any provider starts when the secret is missing or was removed. The same rules decide where the value may go: provider configuration, a write-only attribute and another ephemeral resource.

```cue
db_password: pravo.#Secret & {name: "prod/db_password"}
db_rev:      pravo.#SecretVersion & {name: "prod/db_password"}
```

A `pravo.#SecretVersion` is the number of the version that the run reads. The number is not secret, so it is an ordinary value that goes anywhere and is recorded where it is used. Wire it to the version attribute that sits next to a write-only attribute, such as `password_wo_version`, and a new version of the secret changes that attribute, which is what makes the provider take the new value. A run that uses both reads one version of the secret. `pravo validate`, `pravo fmt` and `pravo eval` do not open the state, so the value is unknown there, as it is for a `pravo.#SecretEnv`. [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) shows a secret from `set` to a rotation.

### Write-only attributes

A write-only attribute is an attribute that the provider reads from the configuration and never stores. The generated schema marks it `@pravo(write_only)`.

```sh
pravo schema tls_self_signed_cert | grep write_only
```

Output:

```text
	private_key_pem_wo?: string @pravo(sensitive,write_only)
```

An ephemeral value may be the value of a write-only attribute, because nothing about it reaches the state. The rule above treats a write-only attribute as the one position in a managed resource that accepts one.

File `cert.cue`:

```cue
package estate

key: $tls.#Ephemeral.tls_private_key & {algorithm: "ED25519"}

cert: $tls.#Resource.tls_self_signed_cert & {
	private_key_pem_wo:         key.private_key_pem
	private_key_pem_wo_version: 1
	subject: [{common_name: "example.test"}]
	validity_period_hours: 24
	allowed_uses: ["digital_signature"]
}
```

`pravo validate` accepts the specification.

```sh
pravo validate
```

Output:

```text
Success! 3 resources, 0 data sources, 3 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

The provider plans a write-only attribute as null, so the plan has no line for `private_key_pem_wo`. The version attribute next to it is an ordinary attribute, and the plan shows it.

```sh
API_KEY=not-recorded pravo plan | grep -E 'private_key|^Plan'
```

Output:

```text
  +     private_key_pem_wo_version: 1
Plan: 1 to add, 0 to change, 0 to destroy.
```

The apply hands the key to the provider with the rest of the configuration, and the provider signs the certificate with it.

```sh
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The state holds null for the attribute, and the key is nowhere in it.

```sh
pravo state show cert | grep private_key
grep -q 'PRIVATE KEY' pravo.state/resources/cert.json || echo "not found"
```

Output:

```text
	private_key_pem:            null
	private_key_pem_wo:         null
	private_key_pem_wo_version: 1
not found
```

A `pravo.#SecretEnv` and a `pravo.#Secret` are accepted in the same place. [Keep secrets out of the state](../how-to/secrets.md) shows the first, and [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) the second. Remove the certificate before the next section.

```sh
rm cert.cue
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
```

## Sensitive attributes and the state

A provider marks an attribute sensitive in its schema. pravo prints it as `(sensitive)` in a plan. The state holds the attribute as the provider returned it, unless the state is [sealed](sealed-state.md), when it holds the attribute encrypted to a list of readers. Here is the private key of `deploy`, in the record.

```sh
pravo state show deploy | grep -c 'BEGIN OPENSSH PRIVATE KEY'
```

Output:

```text
1
```

The printed plan hides it. The plan document does not.

```sh
pravo plan -destroy | grep private_key
```

Output:

```text
  # deploy (tls_private_key) will be destroyed
  -     private_key_openssh:           (sensitive)
  -     private_key_pem:               (sensitive)
  -     private_key_pem_pkcs8:         (sensitive)
```

```sh
pravo plan -destroy --json 2>/dev/null | grep -c 'BEGIN OPENSSH PRIVATE KEY'
```

Output:

```text
1
```

The plan document is the document that policies are judged against, and the document that `--json` hands to a program. A refusal by a policy quotes the offending value. [Policies](policies.md) says what that means for a rule over a sensitive attribute.

The files of the state are written with mode 0644, as the umask allows. A saved plan is written with mode 0600, and it holds the prior and planned values of every change. Treat both as sensitive.

```sh
ls -l p.plan | cut -c1-10
```

Output:

```text
-rw-------
```

### Outputs

An output with `sensitive: true` is hidden where outputs are listed. That is a plan, the summary of an apply, and `pravo output` without a name. The state still holds the value, wrapped as `{"$sensitive": VALUE}`. `pravo output NAME` and `--json` print it.

```sh
pravo output | head -1
```

Output:

```text
private: (sensitive)
```

```sh
pravo output private | grep -c 'BEGIN PRIVATE KEY'
```

Output:

```text
1
```

Another root reads outputs with `pravo.#RemoteOutputs`. It reads `outputs.json` alone, and an output that the producing root marked sensitive is not given out.

## Recap

- A value is recorded in the state, the saved plan and the plan document unless it is ephemeral. Sensitive hides a value on a screen and nowhere else.
- An ephemeral value exists for one run. It may go into provider configuration, a write-only attribute and another ephemeral resource, and nowhere else.
- `pravo.#SecretEnv` is an ephemeral environment variable, and `pravo.#Env` is an ordinary one. `pravo.#Secret` is an ephemeral value that the sealed state keeps, and `pravo.#SecretVersion` is its number.

Back to [Concepts](https://pravo.systems/concepts/index.html), or to the guide on [secrets](../how-to/secrets.md).
