---
title: Secrets and ephemeral values
description: "Which values pravo records in state, saved plans and outputs, what an ephemeral value is, where it may go, and what hiding a value does and does not do."
order: 9
minutes: 14
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
---

pravo handles three kinds of value that need care. A provider marks some attributes sensitive. Some values must never be recorded, and pravo calls them ephemeral. Some values come from the environment. Each travels through plans, the state and outputs in a different way. This page says where each one goes.

## Where a value is recorded

| Value | State | Saved plan | Printed plan | Plan JSON and policy document |
|---|---|---|---|---|
| An attribute of a resource or data source | Recorded | Recorded | Printed | Present |
| An attribute that the provider marks sensitive | Recorded as returned | Recorded | `(sensitive)` | Present, not masked |
| A value read with `pravo.#Env` | Wherever it is used | Wherever it is used | Where it is used | Where it is used |
| An output | Recorded | Recorded when it is known | Printed | Present |
| An output with `sensitive: true` | Recorded, wrapped as `$sensitive` | Recorded when it is known | `(sensitive)` | Present, not masked |
| An ephemeral value | Never | Never | Never | Never |

Hiding a value is a matter of display. `(sensitive)` keeps a value out of a terminal, a log and a review. It does not keep the value out of the state, the saved plan or the plan document. An ephemeral value is the one kind that pravo keeps out of all of them.

## Ephemeral values

An **ephemeral resource** is a provider object that exists for the length of one run. It is built through a provider like any other resource, from its `#Ephemeral.TYPE` definition. pravo opens it when the first resource that needs it runs. It renews it on the schedule that the provider asks for. It closes every open one when the run ends, whether the run succeeded or failed.

Plan and apply are separate runs. An ephemeral resource that a plan opened is closed at the end of the plan, and the apply opens it again. Nothing about it is written to the state or to a saved plan. The result lives in the running process. The `Graph:` line counts how many were opened.

Work in an empty directory. The specification below runs a command and uses its output as the password of a provider.

```cue file=main.cue
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

```sh run
pravo init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo plan -out p.plan | grep -E '^(Plan|Graph)'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 1, 1 ephemeral opened.
```

The plan opened `token`. The apply of the saved plan opens it again.

```sh run
pravo apply p.plan | grep -E '^(Apply|token)'
```

```text out
token: Opening...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The marker `hunter2-marker` is nowhere in the state or in the saved plan.

```sh run
grep -r hunter2-marker pravo.state p.plan || echo "not found"
```

```text out
not found
```

### Where an ephemeral value may go

Everything that a managed resource or a data source returns is recorded in the state, and so is every output. An ephemeral value may therefore be used in only three places.

- The configuration of a provider, which is the fields of a provider struct.
- A write-only attribute of a managed resource. The provider accepts it and never stores it.
- Another ephemeral resource.

:::figure Where an ephemeral value may go. Every refused place leads to the state.
<svg viewBox="0 0 780 290" role="img" aria-label="An ephemeral value on the left. Three arrows lead to accepted places: provider configuration, a write-only attribute and another ephemeral resource. Three arrows lead to refused places: an ordinary attribute, an output and an argument of a provider function, because each of them is recorded in the state." style="min-width:680px">
<rect x="0" y="100" width="168" height="74" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.5"/>
<text x="84" y="128" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">ephemeral value</text>
<text x="84" y="146" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">ephemeral resource</text>
<text x="84" y="160" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo.#SecretEnv</text>
<line x1="168" y1="137" x2="220" y2="137" style="stroke:var(--text-3);stroke-width:1.5"/>
<line x1="220" y1="27" x2="220" y2="261" style="stroke:var(--text-3);stroke-width:1.5"/>
<line x1="220" y1="27" x2="262" y2="27" style="stroke:var(--accent);stroke-width:1.5"/>
<polygon points="262,27 257,24.0 257,30.0" style="fill:var(--accent)"/>
<rect x="264" y="10" width="236" height="34" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.5"/>
<text x="278" y="31.5" style="fill:var(--text);font-size:12.5px;font-weight:600">provider configuration</text>
<text x="516" y="31.5" style="fill:var(--text-2);font-size:11.5px">accepted: fields of a provider struct</text>
<line x1="220" y1="69" x2="262" y2="69" style="stroke:var(--accent);stroke-width:1.5"/>
<polygon points="262,69 257,66.0 257,72.0" style="fill:var(--accent)"/>
<rect x="264" y="52" width="236" height="34" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.5"/>
<text x="278" y="73.5" style="fill:var(--text);font-size:12.5px;font-weight:600">write-only attribute</text>
<text x="516" y="73.5" style="fill:var(--text-2);font-size:11.5px">accepted: the provider never stores it</text>
<line x1="220" y1="111" x2="262" y2="111" style="stroke:var(--accent);stroke-width:1.5"/>
<polygon points="262,111 257,108.0 257,114.0" style="fill:var(--accent)"/>
<rect x="264" y="94" width="236" height="34" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.5"/>
<text x="278" y="115.5" style="fill:var(--text);font-size:12.5px;font-weight:600">another ephemeral resource</text>
<text x="516" y="115.5" style="fill:var(--text-2);font-size:11.5px">accepted: also lives for one run</text>
<line x1="220" y1="177" x2="262" y2="177" style="stroke:var(--conflict);stroke-width:1.5"/>
<polygon points="262,177 257,174.0 257,180.0" style="fill:var(--conflict)"/>
<rect x="264" y="160" width="236" height="34" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="278" y="181.5" style="fill:var(--text);font-size:12.5px;font-weight:600">ordinary attribute</text>
<text x="516" y="181.5" style="fill:var(--text-2);font-size:11.5px">error: recorded in the state</text>
<line x1="220" y1="219" x2="262" y2="219" style="stroke:var(--conflict);stroke-width:1.5"/>
<polygon points="262,219 257,216.0 257,222.0" style="fill:var(--conflict)"/>
<rect x="264" y="202" width="236" height="34" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="278" y="223.5" style="fill:var(--text);font-size:12.5px;font-weight:600">output</text>
<text x="516" y="223.5" style="fill:var(--text-2);font-size:11.5px">error: recorded in the state</text>
<line x1="220" y1="261" x2="262" y2="261" style="stroke:var(--conflict);stroke-width:1.5"/>
<polygon points="262,261 257,258.0 257,264.0" style="fill:var(--conflict)"/>
<rect x="264" y="244" width="236" height="34" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="278" y="265.5" style="fill:var(--text);font-size:12.5px;font-weight:600">provider function argument</text>
<text x="516" y="265.5" style="fill:var(--text-2);font-size:11.5px">error: a result can be recorded</text>
<line x1="0" y1="140" x2="0" y2="140" style="stroke:var(--line);stroke-width:1"/>
</svg>
:::

A reference from any other attribute is an error that names the attribute and the ephemeral resource.

```cue file=leak.cue status=rejected
package estate

leak: $local.#Resource.local_file & {
	filename: "leak.txt"
	content:  token.stdout
}
```

pravo checks the rule when it converts the body of a resource, before it asks the provider about that resource. `pravo validate` does not run the check.

```sh run
pravo validate
```

```text out
Success! 2 resources, 0 data sources, 1 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

```sh run exit=1
pravo plan
```

```text out
token: Opening...

Error: leak.content: references token, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

```sh run
rm leak.cue
```

An output is recorded in the state, so an ephemeral value cannot be an output. pravo checks the outputs before it plans.

```cue file=leakout.cue status=rejected
package estate

import "pravo.dev/pravo"

leaked: pravo.#Output & {value: token.stdout}
```

```sh run exit=1
pravo plan
```

```text out
Error: output leaked references token; outputs are recorded in state, so ephemeral values cannot be used in them
```

```sh run
rm leakout.cue
```

A provider function does not accept an ephemeral value, because the result of a call can end up in the state. When the configuration of an ephemeral resource is unknown during a plan, the open waits for the apply. It is unknown when it refers to a resource that does not exist yet.

### Environment variables

pravo reads an environment variable itself, with no provider, on every plan and apply. There are two kinds. A `pravo.#Env` is an ordinary value. It is recorded wherever it is used. A `pravo.#SecretEnv` is an ephemeral value. The same rules decide where it may go.

```cue file=env.cue
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

```sh run exit=1
env -u API_KEY pravo plan
```

```text out
Error: api_key: the environment variable API_KEY is not set
```

The value of `region` is an ordinary attribute of `noted`, so the state holds it.

```sh run
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
grep -c "eu-west-1" pravo.state/resources/noted.json
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
1
```

The kind is chosen where the variable is declared.

### Write-only attributes

A write-only attribute is an attribute that the provider reads from the configuration and never stores. The generated schema marks it `@pravo(write_only)`.

```sh run
pravo schema tls_self_signed_cert | grep write_only
```

```text out
	private_key_pem_wo?: string @pravo(sensitive,write_only)
```

An ephemeral value may be the value of a write-only attribute, because nothing about it reaches the state. The rule above treats a write-only attribute as the one position in a managed resource that accepts one.

```cue file=cert.cue
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

```sh run
pravo validate
```

```text out
Success! 3 resources, 0 data sources, 3 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

The provider plans a write-only attribute as null, so the plan has no line for `private_key_pem_wo`. The version attribute next to it is an ordinary attribute, and the plan shows it.

```sh run
API_KEY=not-recorded pravo plan | grep -E 'private_key|^Plan'
```

```text out
  +     private_key_pem_wo_version: 1
Plan: 1 to add, 0 to change, 0 to destroy.
```

The apply hands the key to the provider with the rest of the configuration, and the provider signs the certificate with it.

```sh run
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The state holds null for the attribute, and the key is nowhere in it.

```sh run
pravo state show cert | grep private_key
grep -q 'PRIVATE KEY' pravo.state/resources/cert.json || echo "not found"
```

```text out
	private_key_pem:            null
	private_key_pem_wo:         null
	private_key_pem_wo_version: 1
not found
```

A `pravo.#SecretEnv` is accepted in the same place. [Keep secrets out of the state](../how-to/secrets.md) shows one. Remove the certificate before the next section.

```sh run
rm cert.cue
API_KEY=not-recorded pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <id>]
```

## Sensitive attributes and the state

A provider marks an attribute sensitive in its schema. pravo prints it as `(sensitive)` in a plan. The state holds the attribute as the provider returned it. Here is the private key of `deploy`, in the record.

```sh run
pravo state show deploy | grep -c 'BEGIN OPENSSH PRIVATE KEY'
```

```text out
1
```

The printed plan hides it. The plan document does not.

```sh run
pravo plan -destroy | grep private_key
```

```text out
  # deploy (tls_private_key) will be destroyed
  -     private_key_openssh:           (sensitive)
  -     private_key_pem:               (sensitive)
  -     private_key_pem_pkcs8:         (sensitive)
```

```sh run
pravo plan -destroy --json 2>/dev/null | grep -c 'BEGIN OPENSSH PRIVATE KEY'
```

```text out
1
```

The plan document is the document that policies are judged against, and the document that `--json` hands to a program. A refusal by a policy quotes the offending value. [Policies](policies.md) says what that means for a rule over a sensitive attribute.

The files of the state are written with mode 0644, as the umask allows. A saved plan is written with mode 0600, and it holds the prior and planned values of every change. Treat both as sensitive.

```sh run
ls -l p.plan | cut -c1-10
```

```text out
-rw-------
```

### Outputs

An output with `sensitive: true` is hidden where outputs are listed. That is a plan, the summary of an apply, and `pravo output` without a name. The state still holds the value, wrapped as `{"$sensitive": VALUE}`. `pravo output NAME` and `--json` print it.

```sh run
pravo output | head -1
```

```text out
private: (sensitive)
```

```sh run
pravo output private | grep -c 'BEGIN PRIVATE KEY'
```

```text out
1
```

Another root reads outputs with `pravo.#RemoteOutputs`. It reads `outputs.json` alone, and an output that the producing root marked sensitive is not given out.

## Recap

- A value is recorded in the state, the saved plan and the plan document unless it is ephemeral. Sensitive hides a value on a screen and nowhere else.
- An ephemeral value exists for one run. It may go into provider configuration, a write-only attribute and another ephemeral resource, and nowhere else.
- `pravo.#SecretEnv` is an ephemeral environment variable, and `pravo.#Env` is an ordinary one.

Back to [Concepts](index.md), or to the guide on [secrets](../how-to/secrets.md).
