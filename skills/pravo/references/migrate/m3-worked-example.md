<!-- Generated from docs/migrate/m3-worked-example.md by tools/skillgen. Do not edit. -->

# A worked example

One stack with a module, a for_each, a remote output, a sensitive value and a moved history, translated by hand, converted and planned to empty.

This page moves one small stack from OpenTofu to pravo. Every pravo command runs on your machine, and every pravo output below comes from a run. The stack ran in OpenTofu 1.13.1 once, and the page keeps the state files that OpenTofu wrote. The page does not run OpenTofu, so you need no OpenTofu binary and no cloud account.

Work in an empty directory.

## The stack

The stack has two roots, each in its own directory.

- `platform` calls a module once per service, with `for_each`. Each service gets a pet name and a configuration file. The root also holds an admin password, a `moved` block from an earlier rename, and two outputs. One output is sensitive.
- `gateway` reads the `endpoints` output of `platform` with `terraform_remote_state` and writes one file.

The CUE on this page uses [definitions](https://pravo.systems/learn/a08-definitions-reject-typos.html), [comprehensions](https://pravo.systems/learn/a10-comprehensions.html) and [references](https://pravo.systems/learn/a11-references-let-hidden.html). Read those lessons first if any of them is new to you.

## Step 1: run the stack in OpenTofu

The HCL of both roots is in step 2, next to its CUE. This step shows OpenTofu 1.13.1 running it. The page does not run these commands. The output is excerpted, and a line that reads `...` marks a cut.

`platform` was applied with the password named `root`. A `moved` block then renamed the password to `admin`. A `moved` block that has not been applied is a change, so the plan exits with code 2 until an apply has carried it out. `gateway` was applied once, after `platform`.

```sh
tofu apply
```

```text
...
Apply complete! Resources: 7 added, 0 changed, 0 destroyed.

Outputs:

admin_password = <sensitive>
endpoints = tolist([
  "api-whole-tarpon:8080",
  "web-ethical-humpback:8081",
  "worker-capital-elk:8082",
])
```

```sh
tofu plan -detailed-exitcode
```

```text
...
OpenTofu will perform the following actions:

  # random_password.root has moved to random_password.admin
    resource "random_password" "admin" {
        id          = "none"
        # (12 unchanged attributes hidden)
    }

Plan: 0 to add, 0 to change, 0 to destroy.
```

After the second `tofu apply` of `platform`, both roots plan to nothing. This is the gate of [M1](m1-before-you-start.md#task-1-make-the-old-plan-empty): the old plan is empty before you start.

```sh
tofu plan -detailed-exitcode
```

```text
...
No changes. Your infrastructure matches the configuration.
```

Each root left a `terraform.tfstate`. The page keeps both files whole, as OpenTofu wrote them, and does not edit them. It prints the parts that the conversion reads, and a line that reads `...` marks a cut. The files are in state format 4. The password in the first file is a random value that the random provider generated.

```json
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 2,
  "lineage": "e815e6f2-7f86-229c-a995-99c0754c10e9",
  "outputs": {
    "admin_password": {
      "value": "LvJ3A220jgeoq7ozAmhqaOyy",
      "type": "string",
      "sensitive": true
    },
...
    {
      "module": "module.service[\"api\"]",
      "mode": "managed",
      "type": "local_file",
      "name": "config",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "host = api-whole-tarpon\nport = 8080\n",
...
            "filename": "out/api.conf",
...
          "sensitive_attributes": [
...
          "dependencies": [
            "module.service.random_pet.host"
          ]
```

```json
  "resources": [
    {
      "mode": "data",
      "type": "terraform_remote_state",
      "name": "platform",
      "provider": "provider[\"terraform.io/builtin/terraform\"]",
      "instances": [
...
            "outputs": {
              "value": {
                "admin_password": "LvJ3A220jgeoq7ozAmhqaOyy",
                "endpoints": [
                  "api-whole-tarpon:8080",
                  "web-ethical-humpback:8081",
                  "worker-capital-elk:8082"
                ]
              },
```

Four things in the files matter to the conversion.

- Providers sit on the registry that OpenTofu used: `registry.opentofu.org/hashicorp/random`. The built-in provider sits on `terraform.io/builtin/terraform`.
- A resource in a module carries the module instance: `module.service["api"]`. The `dependencies` of `local_file.config` name `module.service.random_pet.host`, without the key.
- `gateway` holds a `terraform_remote_state` record. It copies every output of `platform`, `admin_password` in the clear.
- The files hold fields the converter ignores: `lineage`, `serial`, `terraform_version`, `sensitive_attributes` and `check_results`.

The state describes four files. Write them, then record a checksum of every file that matters.

```sh
mkdir -p platform/out gateway/out
printf 'host = api-whole-tarpon\nport = 8080\n' > platform/out/api.conf
printf 'host = web-ethical-humpback\nport = 8081\n' > platform/out/web.conf
printf 'host = worker-capital-elk\nport = 8082\n' > platform/out/worker.conf
printf 'api-whole-tarpon:8080\nweb-ethical-humpback:8081\nworker-capital-elk:8082\n' > gateway/out/upstreams.conf
cksum platform/terraform.tfstate gateway/terraform.tfstate platform/out/* gateway/out/*
```

Output:

```text
3519162698 8340 platform/terraform.tfstate
1970167571 3025 gateway/terraform.tfstate
4045921612 36 platform/out/api.conf
3677381612 40 platform/out/web.conf
1286036499 38 platform/out/worker.conf
402797924 72 gateway/out/upstreams.conf
```

The four `.conf` files are the real infrastructure of this example. The `cksum` lines are your record. You compare them again at the end.

## Step 2: translate the specification by hand

pravo has no converter for HCL. You translate it yourself, one construct at a time. Each pair below shows the HCL that OpenTofu ran on the left and the CUE on the right. The [concept map](m2-concept-map.md) lists every construct.

### Providers

```hcl
terraform {
  required_providers {
    local  = { source = "hashicorp/local", version = "~> 2.5" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}
```

File `platform/providers.cue`:

```cue
package platform

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local: loc.#Provider & {$version: "~> 2.5"}
$random: rnd.#Provider & {$version: "~> 3.6"}
```

- `required_providers` and the `provider` block become one struct per provider. `$version` holds the constraint.
- The import path names the provider. `pravo init` generates the package it points to.

### The module

```hcl
variable "name" {
  type = string
}

variable "port" {
  type = number

  validation {
    condition     = var.port > 1024 && var.port < 65536
    error_message = "port must be between 1025 and 65535."
  }
}

resource "random_pet" "host" {
  prefix = var.name
  length = 2
}

resource "local_file" "config" {
  filename = "out/${var.name}.conf"
  content  = "host = ${random_pet.host.id}\nport = ${var.port}\n"
}

output "endpoint" {
  value = "${random_pet.host.id}:${var.port}"
}
```

File `platform/service.cue`:

```cue
package platform

#Service: {
	name: string
	port: int & >1024 & <65536

	host: $random.#Resource.random_pet & {
		prefix: name
		length: 2
	}
	config: $local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "host = \(host.id)\nport = \(port)\n"
	}
	endpoint: "\(host.id):\(port)"
}
```

- A module is a definition that holds resources. `#Service` is the module.
- The variables are fields. The `validation` block is the constraint on `port`.
- The module output is an ordinary field: `endpoint`.
- Nothing wires the module into the root. Writing an instance is enough.
- `#Service` builds its resources through the providers of its own package. A module shared by several roots takes the provider as an input, as [M4](m4-translate-the-specification.md) shows.
- The address of a resource inside an instance is the path of the instance plus the field: `services.api.host`.

### The root

```hcl
locals {
  ports = {
    api    = 8080
    web    = 8081
    worker = 8082
  }
}

module "service" {
  source   = "./modules/service"
  for_each = local.ports

  name = each.key
  port = each.value
}

resource "random_password" "admin" {
  length  = 24
  special = false
}

moved {
  from = random_password.root
  to   = random_password.admin
}

output "endpoints" {
  value = sort([for svc in module.service : svc.endpoint])
}

output "admin_password" {
  value     = random_password.admin.result
  sensitive = true
}
```

File `platform/main.cue`:

```cue
package platform

import (
	"list"
	"pravo.dev/pravo"
)

_ports: {api: 8080, web: 8081, worker: 8082}

services: {
	for k, p in _ports {
		(k): #Service & {name: k, port: p}
	}
}

admin: $random.#Resource.random_password & {
	length:  24
	special: false
}

endpoints: pravo.#Output & {
	value: list.SortStrings([for _, s in services {s.endpoint}])
}

admin_password: pravo.#Output & {
	value:     admin.result
	sensitive: true
}
```

- `locals.ports` becomes the hidden field `_ports`.
- `module "service"` with `for_each` becomes a struct comprehension. The key becomes the last part of the address: `services.api`.
- `sort(...)` becomes `list.SortStrings(...)`.
- `sensitive = true` becomes `sensitive: true`. The provider also marks `result` as sensitive in its schema.
- The `moved` block has no CUE counterpart. OpenTofu applied it, so the state already holds `random_password.admin`. A `moved` block that is still pending becomes `$from`, as step 6 shows.

### The second root

```hcl
terraform {
  required_providers {
    local = { source = "hashicorp/local", version = "~> 2.5" }
  }
}

data "terraform_remote_state" "platform" {
  backend = "local"

  config = {
    path = "../platform/terraform.tfstate"
  }
}

resource "local_file" "upstreams" {
  filename = "out/upstreams.conf"
  content  = "${join("\n", data.terraform_remote_state.platform.outputs.endpoints)}\n"
}
```

File `gateway/main.cue`:

```cue
package gateway

import (
	"strings"
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider & {$version: "~> 2.5"}

platform: pravo.#RemoteOutputs & {
	state: path: "../platform/pravo.state"
}

upstreams: $local.#Resource.local_file & {
	filename: "out/upstreams.conf"
	content:  strings.Join(platform.outputs.endpoints, "\n") + "\n"
}
```

- `terraform_remote_state` becomes `pravo.#RemoteOutputs`. It reads the `outputs.json` of the other root's pravo state and nothing else.
- `platform.outputs.endpoints` is the value of the `endpoints` output of `platform`.
- An output that `platform` marks sensitive is not passed on.

The HCL blocks above hold 84 lines. The CUE blocks hold 71 lines in four files. The conversion in steps 6 and 8 adds 15 lines of scaffolding, which you delete at the end.

## Step 3: initialise and validate

`pravo init` installs the providers, writes `pravo.lock.cue` and generates the CUE packages under `cue.mod`. In a directory without `pravo.lock.cue`, it contacts the provider registry. The first line of its output is normal: the files import packages that `init` generates.

`pravo validate` evaluates the specification and checks every resource against its provider schema. It reads neither the state nor your objects.

```sh
pravo -C platform init
pravo -C gateway init
pravo -C platform validate
pravo -C gateway validate
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v<version>
  random = hashicorp/random v<version>
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v<version>
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 7 resources, 0 data sources, 0 ephemeral, 0 function calls, 2 outputs across 2 providers.
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

## Step 4: convert the state of platform

`pravo state import-tfstate` reads the file you name and writes pravo's own state in `platform/pravo.state`. It does not write the `.tfstate` file.

```sh
pravo -C platform state import-tfstate terraform.tfstate
```

Output:

```text
Imported 7 resources and 2 outputs from terraform.tfstate into ./platform/pravo.state

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  module.service["api"].local_file.config -> local_file.service_api_config
  module.service["web"].local_file.config -> local_file.service_web_config
  module.service["worker"].local_file.config -> local_file.service_worker_config
  module.service["api"].random_pet.host -> random_pet.service_api_host
  module.service["web"].random_pet.host -> random_pet.service_web_host
  module.service["worker"].random_pet.host -> random_pet.service_worker_host
```

Seven records and two outputs are in the new state. Six instances were renamed. A module path and an instance key become part of the name, joined with underscores. Any character other than an ASCII letter, an ASCII digit, `_` or `-` becomes `_`, so a dot in a key becomes `_`.

| OpenTofu address | Address in pravo's state | Resource in CUE |
|---|---|---|
| `module.service["api"].random_pet.host` | `random_pet.service_api_host` | `services.api.host` |
| `module.service["api"].local_file.config` | `local_file.service_api_config` | `services.api.config` |
| `random_password.admin` | `random_password.admin` | `admin` |

The `web` and `worker` instances follow the first two rows. The address of a resource in pravo's state is where the resource sits in the specification. The conversion keeps the OpenTofu name. Step 6 supplies the rest.

Show one record to see what the conversion kept.

```sh
pravo -C platform state show random_pet.service_api_host
```

Output:

```text
// random_pet.service_api_host (provider registry.opentofu.org/hashicorp/random as random, schema version 0, revision 1)
// refreshed never, applied never, config hash none
// needed by local_file.service_api_config
{
	id:        "api-whole-tarpon"
	keepers:   null
	length:    2
	prefix:    "api"
	separator: "-"
}
```

The provider is `registry.opentofu.org/hashicorp/random`, the host the state names. A state that legacy Terraform wrote names its providers on `registry.terraform.io`. The conversion reads that host as the OpenTofu registry and keeps every other host as written. [M5](m5-convert-the-state.md#get-the-state-file) shows both.

The record also lists the resource that needs it. OpenTofu wrote the dependency of `local_file.config` as `module.service.random_pet.host`, without the key of the module instance. The conversion points each dependency at the record of the same instance: `local_file.service_api_config` needs `random_pet.service_api_host` and no other pet.

What the conversion carries:

- every managed resource and data source, with its attributes, schema version, provider-private data and the dependencies between those records;
- every output value, with its `sensitive` mark;
- the provider source, and the alias as part of the provider key.

What it does not carry:

- `taint`. The command prints a warning for each tainted instance and records the object as healthy. Use `pravo plan --replace ADDR` to replace it.
- Deposed objects. The command prints a warning and skips them. pravo keeps one object per resource.
- Records of the built-in provider, such as `terraform_remote_state`. The command prints a warning and skips them. Step 8 shows one.
- `lineage`, `serial`, `terraform_version` and `check_results`. The converter does not read them.
- `sensitive_attributes`. The converter does not read them, and a record keeps every attribute as the provider returned it. `pravo state show` prints `result` of `random_password.admin` in the clear.

The last item is visible in the record of the password. The command prints the two attributes that OpenTofu marked sensitive:

```sh
pravo -C platform state show random_password.admin | grep -E '^\s+(bcrypt_hash|result):'
```

Output:

```text
	bcrypt_hash:      "$2a$10$mOHC9Xd0OpZYDPSZ.yhknuBaZau/e/qADCGx0kbHzbguaP/52qVM."
	result:           "LvJ3A220jgeoq7ozAmhqaOyy"
```

An output that OpenTofu marked sensitive stays sensitive. The list hides `admin_password`.

```sh
pravo -C platform output
```

Output:

```text
admin_password: (sensitive)
endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]
```

The list hides the value. A request for the output by name prints it.

```sh
pravo -C platform output admin_password
```

Output:

```text
"LvJ3A220jgeoq7ozAmhqaOyy"
```

> [!PITFALL]
> `pravo output NAME` and `pravo output --json` print a sensitive output in the clear. Do not paste their output into a ticket or a chat. The state directory holds the value too, as `{"$sensitive": VALUE}`.

## Step 5: make a mistake

The specification does not yet say which record belongs to which resource. Predict what pravo does about that, then run the plan.

Status: unintended.

```sh
pravo -C platform plan --detailed-exitcode
```

Output:

```text
...
pravo will perform the following actions:

  # services.api.host (random_pet) will be created
  + services: api: host: {
  +     id:        (known after apply)
  +     length:    2
  +     prefix:    "api"
  +     separator: "-"
    }
...
  # random_pet.service_worker_host (random_pet) will be destroyed
  - random_pet: service_worker_host: {
  -     id:        "worker-capital-elk"
  -     length:    2
  -     prefix:    "worker"
  -     separator: "-"
    }

Plan: 7 to add, 0 to change, 7 to destroy.
Graph: planned 7 · removed 7 · refreshed 7

Outputs:
  admin_password: (sensitive)
  endpoints: (known after apply)
```

The plan is valid. It is not what you meant. Read the summary line first: seven to add and seven to destroy. List the headings to see the pairs.

```sh
pravo -C platform plan 2>&1 | grep '^  # '
```

Output:

```text
  # services.api.host (random_pet) will be created
  # services.api.config (local_file) will be created
  # services.web.host (random_pet) will be created
  # services.web.config (local_file) will be created
  # services.worker.host (random_pet) will be created
  # services.worker.config (local_file) will be created
  # admin (random_password) will be created
  # local_file.service_api_config (local_file) will be destroyed
  # local_file.service_web_config (local_file) will be destroyed
  # local_file.service_worker_config (local_file) will be destroyed
  # random_password.admin (random_password) will be destroyed
  # random_pet.service_api_host (random_pet) will be destroyed
  # random_pet.service_web_host (random_pet) will be destroyed
  # random_pet.service_worker_host (random_pet) will be destroyed
```

Every resource is created at its new address. Every record is destroyed at its old one.

Applied, this plan creates each file and then deletes it. The new resource and the old record name the same path. It also draws a new pet name for every service and a new password.

> [!WARNING]
> Stop rule. During a migration a plan may contain moves and nothing else. A create, a change, a replacement or a destroy means: do not apply. Fix the specification and plan again.

The flag `--detailed-exitcode` makes the rule mechanical. Exit code 2 means the plan has changes, and the exit code above is 2. The [exit codes](../reference/exit-codes.md) list the others.

## Step 6: say where each record went

`$from` names the address a record has now. You write it on the resource that holds the record. Put all of them in one file, so that you can delete the file later.

File `platform/migrate.cue`:

```cue
package platform

// Scaffolding for the state conversion. Delete this file after the first apply.
admin: $from: "random_password.admin"

for k, _ in _ports {
	services: (k): {
		host: $from:   "random_pet.service_\(k)_host"
		config: $from: "local_file.service_\(k)_config"
	}
}
```

The `for` repeats the two lines for each service. A root without a module needs one `$from` per resource. Plan again.

```sh
pravo -C platform plan --detailed-exitcode
```

Output:

```text
random_password.admin: moved to admin
random_pet.service_api_host: moved to services.api.host
local_file.service_api_config: moved to services.api.config
random_pet.service_web_host: moved to services.web.host
local_file.service_web_config: moved to services.web.config
random_pet.service_worker_host: moved to services.worker.host
local_file.service_worker_config: moved to services.worker.config
...
pravo will perform the following actions:

  # random_password.admin was moved to admin (record renamed; no change to the object)

  # random_pet.service_api_host was moved to services.api.host (record renamed; no change to the object)

  # local_file.service_api_config was moved to services.api.config (record renamed; no change to the object)

  # random_pet.service_web_host was moved to services.web.host (record renamed; no change to the object)

  # local_file.service_web_config was moved to services.web.config (record renamed; no change to the object)

  # random_pet.service_worker_host was moved to services.worker.host (record renamed; no change to the object)

  # local_file.service_worker_config was moved to services.worker.config (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 7 · refreshed 7 · moved 7

Outputs:
  admin_password: (sensitive)
  endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]
```

Every change is a move: `record renamed; no change to the object`. The summary has no adds, changes or destroys, and the exit code is 0. This is the plan you wait for.

The plan renames the records in pravo's state at once. Plan again and the moves are gone. The records have no configuration hash yet, so the plan asked each provider to plan all seven resources: `planned 7`.

## Step 7: apply the empty plan

The first apply changes no object. It records a configuration hash and the provider version for every resource. It also records the outputs again, from the specification.

```sh
pravo -C platform apply --auto-approve
pravo -C platform output
```

Output:

```text
...
No changes. Your infrastructure matches the configuration.
Graph: planned 7 · refreshed 7

Outputs:
  admin_password: (sensitive)
  endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]
...
admin_password: (sensitive)
endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]
```

`pravo output` lists `admin_password` as `(sensitive)`. The next plan skips every resource whose configuration and provider version have not changed.

## Step 8: convert the second root

`gateway` reads `platform/pravo.state`. Convert the root that publishes an output before the root that reads it. A plan of `gateway` before `platform` has recorded outputs stops with `has no recorded outputs; apply that root first`.

```sh
pravo -C gateway state import-tfstate terraform.tfstate
pravo -C gateway state list
```

Output:

```text
Imported 1 resources and 0 outputs from terraform.tfstate into ./gateway/pravo.state
Warning: data.terraform_remote_state.platform: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)
local_file.upstreams
```

The `terraform_remote_state` data source is not in the state. pravo has no such data source. The conversion skips the record of the built-in provider `terraform.io/builtin/terraform` and says so in a warning. The record holds a copy of every output of `platform`, `admin_password` included, and the conversion does not carry it. `gateway/main.cue`, written in step 2, reads the outputs of `platform` with `pravo.#RemoteOutputs`.

Add the `$from` for the file and plan.

File `gateway/migrate.cue`:

```cue
package gateway

// Scaffolding for the state conversion. Delete this file after the first apply.
upstreams: $from: "local_file.upstreams"
```

```sh
pravo -C gateway plan --detailed-exitcode
pravo -C gateway apply --auto-approve
```

Output:

```text
local_file.upstreams: moved to upstreams
upstreams: Refreshing state... [id=a80f4be062d20967b1ee08bdcac01ef45725e8cb]

pravo will perform the following actions:

  # local_file.upstreams was moved to upstreams (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 1 · refreshed 1 · moved 1
upstreams: Refreshing state... [id=a80f4be062d20967b1ee08bdcac01ef45725e8cb]

No changes. Your infrastructure matches the configuration.
Graph: planned 1 · refreshed 1
```

## Step 9: delete the scaffolding

The records are in their places. The `$from` fields have nothing left to do. Delete the two files and plan both roots.

```sh
rm platform/migrate.cue gateway/migrate.cue
pravo -C platform plan --detailed-exitcode
pravo -C gateway plan --detailed-exitcode
pravo -C platform state list
```

Output:

```text
No changes. The specification matches the state. 7 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 7 · not read 7

Outputs:
  admin_password: (sensitive)
  endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]

No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.
Graph: planned 0 · skipped 1 · not read 1
admin
services.api.config
services.api.host
services.web.config
services.web.host
services.worker.config
services.worker.host
```

Both plans report no changes. `not read 7` means that pravo trusted the seven records and read none of their objects, so the plan says that the specification matches the state, not the infrastructure. `planned 0` means that nothing in the specification had changed, so it planned none. The addresses in `state list` are now the CUE paths.

Check the promise. The state files and the four objects are the files they were at the start. List what else the directories hold.

```sh
cksum platform/terraform.tfstate gateway/terraform.tfstate platform/out/* gateway/out/*
ls -A platform gateway
```

Output:

```text
3519162698 8340 platform/terraform.tfstate
1970167571 3025 gateway/terraform.tfstate
4045921612 36 platform/out/api.conf
3677381612 40 platform/out/web.conf
1286036499 38 platform/out/worker.conf
402797924 72 gateway/out/upstreams.conf
gateway:
cue.mod
main.cue
out
pravo.lock.cue
pravo.state
terraform.tfstate

platform:
cue.mod
main.cue
out
pravo.lock.cue
pravo.state
providers.cue
service.cue
terraform.tfstate
```

The numbers match step 1. OpenTofu's state and your objects were not touched. In each root pravo wrote `cue.mod`, `pravo.lock.cue` and `pravo.state`, and nothing else.

Delete `platform/queue.cue` before you continue.

## What this example leaves out

The example uses providers that need no credentials. A provider such as `hashicorp/aws` adds one requirement: `pravo plan` reads real objects and stops without credentials. `pravo validate` needs none.

The example has no `count`, no taint, no `TF_VAR_` secret and no script that checks the plan JSON. [M4](m4-translate-the-specification.md) translates each construct. [M5](m5-convert-the-state.md) explains a plan that is not empty after conversion. [M10](m10-not-in-pravo.md) lists what has no counterpart.

## Recap

- You translate by hand; `pravo state import-tfstate` converts the state and leaves the `.tfstate` file as it was.
- A converted record sits at an OpenTofu-style address; `$from` moves it to the CUE path of its resource.
- A plan during migration may hold moves and nothing else; exit code 2 means stop.

Next: [translate the specification](m4-translate-the-specification.md) construct by construct.
