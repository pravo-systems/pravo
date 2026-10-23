<!-- Generated from docs/learn/b06-modules-providers-credentials.md by tools/skillgen. Do not edit. -->

# Modules, providers, credentials

Share one definition between instances, pass a provider in as an input, and keep a credential out of the state.

Two questions meet in a module. How does it get its provider? Where does a credential go?

In OpenTofu a module reaches its provider through a `providers` map on the module call. The module's inputs sit in another place. A `sensitive` variable is hidden from the plan output. It is still written to the state when a resource attribute uses it.

```hcl
provider "local" {
  alias = "primary"
}

module "api" {
  source    = "./modules/service"
  providers = { local = local.primary }
  name      = "api"
  port      = 8080
}
```

```cue
api: service.#Service & {
	local: $local
	name:  "api"
	port:  8080
}
```

A module in pravo is a definition that contains resources. A provider is a value, so the module takes it as an input field, next to the other inputs. The module marks the field required with `!`. The caller fills the field with a provider.

A credential belongs to the provider, and pravo checks where it goes. A secret that reaches provider configuration is never recorded. A secret that reaches a resource attribute is refused at plan. This lesson builds a module and passes it providers. Then it puts a secret in each of the two places and shows what pravo does.

*Figure: The root owns the providers. Each one goes into an input field of the module instance. The module builds its resources through the inputs.*

## Write the module

Work in an empty directory. Imports of your own packages start with the module name in `cue.mod/module.cue`. `pravo init` writes that file with a name taken from the directory. Here you write it first, so that the name is `example.com/estate`.

File `cue.mod/module.cue`:

```cue
module: "example.com/estate@v0"
language: version: "v0.14.0"
```

The module is a definition in its own package.

File `modules/service/service.cue`:

```cue
package service

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider

	name: string & =~"^[a-z][a-z0-9-]*$"
	port: int & >1024 & <65536

	serial: random.#Resource.random_integer & {min: 1000, max: 9999, seed: name}
	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\nport \(port)\nserial \(serial.result)\n"
	}
}
```

Three parts matter here.

- `random` and `local` are provider inputs. Their type is the generated `#Provider` of each package. The `!` makes each one required: an instance that leaves one out is an error.
- `name` and `port` are inputs with constraints, as in [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html).
- `serial` and `config` are resources, built through the provider inputs. The seed makes the serial repeatable. `config` reads `serial`, so pravo creates `serial` first.

The root decides the version and the configuration of each provider it passes in.

## Use it twice

The root declares each provider once and passes it in.

File `main.cue`:

```cue
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	"example.com/estate/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api:    service.#Service & {random: $random, local: $local, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, name: "worker", port: 9090}
```

```sh
pravo init
pravo validate
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 4 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

Apply the root and list what the state holds.

```sh
pravo apply --auto-approve
pravo state list
```

Output:

```text
pravo will perform the following actions:

  # api.serial (random_integer) will be created
  + api: serial: {
  +     id:     (known after apply)
  +     max:    9999
  +     min:    1000
  +     result: (known after apply)
  +     seed:   "api"
    }
...
Plan: 4 to add, 0 to change, 0 to destroy.
Graph: planned 4 of 4.
...
Apply complete! Resources: 4 added, 0 changed, 0 destroyed. [txn <txn>]
api.config
api.serial
worker.config
worker.serial
```

Each instance wrote its own file.

```sh
cat out/api.conf out/worker.conf
```

Output:

```text
service api
port 8080
serial 2680
service worker
port 9090
serial 2481
```

Each instance has its own name, port and serial. They share the definition and nothing else.

## Break it: a wrong provider

A provider input is typed. This root passes the random provider where the module expects the local one. The block carries `status=rejected`: pravo refuses it before any provider starts.

File `main.cue`:

```cue
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	"example.com/estate/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api:    service.#Service & {random: $random, local: $random, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, name: "worker", port: 9090}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
api.local.$source: conflicting values "registry.opentofu.org/hashicorp/local" and "registry.opentofu.org/hashicorp/random":
    ./cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue:23:11
    ./cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue:23:11
    ./main.cue:12:9
    ./main.cue:12:53
    ./modules/service/service.cue:10:11
```

The path is `api.local.$source`, the identity of the provider. The two values are the sources of `local` and `random`. The locations name the generated packages, the call in `main.cue` and the input in the module.

An input constraint fails in the same way. Restore the provider and set a port below 1025.

File `main.cue`:

```cue
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	"example.com/estate/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api:    service.#Service & {random: $random, local: $local, name: "api", port: 80}
worker: service.#Service & {random: $random, local: $local, name: "worker", port: 9090}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
api.port: invalid value 80 (out of bound >1024):
    ./modules/service/service.cue:13:14
    ./main.cue:12:80
```

The error points at the bound in the module and at the value in the root. Write the working root again to continue.

File `main.cue`:

```cue
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	"example.com/estate/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api:    service.#Service & {random: $random, local: $local, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, name: "worker", port: 9090}
```

## Credentials belong to the provider

Put a credential on the provider in the root, not in the module. The `tls` provider has no account, but its `proxy` block takes a user name and a password. It stands in for a cloud provider here.

`pravo.#SecretEnv` reads a variable of the environment. Its value is ephemeral: it lives for one run. It may go into provider configuration and nowhere that is recorded.

Add a third provider input to the module, and a key that uses it.

File `modules/service/service.cue`:

```cue
package service

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider
	tls!:    tlsp.#Provider

	name: string & =~"^[a-z][a-z0-9-]*$"
	port: int & >1024 & <65536

	serial: random.#Resource.random_integer & {min: 1000, max: 9999, seed: name}
	key:    tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}
	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\nport \(port)\nserial \(serial.result)\n"
	}
}
```

The root builds the `tls` provider with the secret and passes it to both instances.

File `main.cue`:

```cue
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/modules/service"
)

token: pravo.#SecretEnv & {name: "REGISTRY_TOKEN"}

$random: rnd.#Provider
$local:  loc.#Provider
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "deploy"
		password: token.value
	}]
}

api:    service.#Service & {random: $random, local: $local, tls: $tls, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, tls: $tls, name: "worker", port: 9090}
```

The new import needs the generated package of the `tls` provider.

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
  tls = hashicorp/tls v4.4.2
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/tls (4 resource types, 2 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

The variable is not set. The run stops and names it.

```sh
pravo plan
```

Output:

```text
Error: token: the environment variable REGISTRY_TOKEN is not set
```

Set the variable for one command and apply. The value is a placeholder.

```sh
REGISTRY_TOKEN=placeholder-secret pravo apply --auto-approve
```

Output:

```text
...
  # api.key (tls_private_key) will be created
  + api: key: {
  +     algorithm:                     "ECDSA"
  +     ecdsa_curve:                   "P256"
  +     id:                            (known after apply)
  +     private_key_openssh:           (sensitive)
  +     private_key_pem:               (sensitive)
  +     private_key_pem_pkcs8:         (sensitive)
  +     public_key_fingerprint_md5:    (known after apply)
  +     public_key_fingerprint_sha256: (known after apply)
  +     public_key_openssh:            (known after apply)
  +     public_key_pem:                (known after apply)
  +     rsa_bits:                      2048
    }
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 6, 4 unchanged and skipped, 4 not read.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

The run read the variable and used it in the provider configuration. Search the state directory for the value.

```sh
grep -r placeholder-secret pravo.state || echo "not found"
```

Output:

```text
not found
```

## Break it: a leaked secret

Write the secret into a file. The block carries `status=rejected`.

File `main.cue`:

```cue
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/modules/service"
)

token: pravo.#SecretEnv & {name: "REGISTRY_TOKEN"}

$random: rnd.#Provider
$local:  loc.#Provider
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "deploy"
		password: token.value
	}]
}

api:    service.#Service & {random: $random, local: $local, tls: $tls, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, tls: $tls, name: "worker", port: 9090}
leak:   $local.#Resource.local_file & {filename: "out/leak.txt", content: token.value}
```

```sh
REGISTRY_TOKEN=placeholder-secret pravo plan
```

Output:

```text
Error: leak.content: references token, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

The message names the field, the secret and the rule. The content of a file is recorded in the state, so an ephemeral value cannot go there. A write-only attribute is one that a provider accepts and never returns, so nothing is recorded for it. An output is recorded too, so the same rule stops it. Delete the `leak` line to continue.

## Break it: a provider that was not passed

An input marked `!` has no default. Add a third instance and leave out `tls`. The block carries `status=rejected`.

File `main.cue`:

```cue
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/modules/service"
)

token: pravo.#SecretEnv & {name: "REGISTRY_TOKEN"}

$random: rnd.#Provider
$local:  loc.#Provider
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "deploy"
		password: token.value
	}]
}

api:    service.#Service & {random: $random, local: $local, tls: $tls, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, tls: $tls, name: "worker", port: 9090}
docs:   service.#Service & {random: $random, local: $local, name: "docs", port: 8081}
```

```sh
pravo validate
pravo plan
```

Output:

```text
Error: provider docs.tls was not supplied: docs requires a hashicorp/tls provider in its field tls. Pass one, for example
	docs: ... & {tls: $tls}

Error: provider docs.tls was not supplied: docs requires a hashicorp/tls provider in its field tls. Pass one, for example
	docs: ... & {tls: $tls}
```

The message names the instance, `docs`, and the field, `tls`. The specification does not load, so no provider starts, and every command that loads it stops with the same error. Pass the provider to the instance.

File `main.cue`:

```cue
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/modules/service"
)

token: pravo.#SecretEnv & {name: "REGISTRY_TOKEN"}

$random: rnd.#Provider
$local:  loc.#Provider
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "deploy"
		password: token.value
	}]
}

api:    service.#Service & {random: $random, local: $local, tls: $tls, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, tls: $tls, name: "worker", port: 9090}
docs:   service.#Service & {random: $random, local: $local, tls: $tls, name: "docs", port: 8081}
```

```sh
REGISTRY_TOKEN=placeholder-secret pravo apply --auto-approve
```

Output:

```text
...
  # docs.key (tls_private_key) will be created
...
Plan: 3 to add, 0 to change, 0 to destroy.
Graph: planned 3 of 9, 6 unchanged and skipped, 6 not read.
...
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <txn>]
```

The state names the provider that built each key.

```sh
pravo state show api.key | head -n 1
pravo state show docs.key | head -n 1
```

Output:

```text
// api.key (provider registry.opentofu.org/hashicorp/tls v4.4.2 as tls, schema version 1, revision 1)
// docs.key (provider registry.opentofu.org/hashicorp/tls v4.4.2 as tls, schema version 1, revision 1)
```

Both keys were built through `tls`, the one provider struct of the root.

> [!PITFALL]
> Write `!` on every provider input. A provider field without it is a provider struct of its own, and an instance that leaves it out builds through a second provider with no configuration. Nothing fails. [Pitfalls](../migrate/m9-pitfalls.md#a-provider-that-was-not-passed) shows the run.

## Exercise

## Clean up

```sh
REGISTRY_TOKEN=placeholder-secret pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 9 destroyed. [txn <txn>]
```

## Recap

- A module is a definition that contains resources. The field that holds an instance is the prefix of its addresses.
- A provider is a required input, `provider!: pkg.#Provider`. The root passes it in, and the module builds its resources through it. An instance that leaves it out stops the load and the message names the instance and the field.
- A credential belongs to the provider in the root. `pravo.#SecretEnv` reaches provider configuration and never the state.

Cloud providers follow the same pattern: configure the provider in the root and pass it in. The how-to pages cover credentials, registries and publishing: [Secrets](../how-to/secrets.md), [Providers from a registry](../how-to/providers-from-a-registry.md), [Modules in a repository](../how-to/modules-in-a-repository.md) and [Publishing a module](../how-to/publishing-a-module.md).

[B7](b07-changing-live-infrastructure.md) saves a plan and applies it in stages.
