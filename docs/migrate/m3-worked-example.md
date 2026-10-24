---
title: A worked example
description: One stack with a module, a for_each, a remote output, a sensitive value and a moved history, translated by hand, converted and planned to empty.
order: 3
group: Runbook
gate: The example plans empty
minutes: 35
needs: "[Quick start](../how-to/quick-start.md#1-install-pravo). The [concept map](m2-concept-map.md) helps."
learn: What does each part of a real stack become in CUE, what does the state conversion do to it, and what does a mistake look like?
done_when: "`pravo plan` reports no changes in both roots, and `cksum` prints the numbers it printed at the start."
level: intermediate
keywords: "hcl terraform tofu for_each count example"
normalize:
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
---

This page moves one small stack from OpenTofu to pravo. Every pravo command runs on your machine, and every pravo output below comes from a run. The stack ran in OpenTofu 1.13.1 once, and the page keeps the state files that OpenTofu wrote. The page does not run OpenTofu, so you need no OpenTofu binary and no cloud account.

Work in an empty directory.

## The stack

The stack has two roots, each in its own directory.

- `platform` calls a module once per service, with `for_each`. Each service gets a pet name and a configuration file. The root also holds an admin password, a `moved` block from an earlier rename, and two outputs. One output is sensitive.
- `gateway` reads the `endpoints` output of `platform` with `terraform_remote_state` and writes one file.

The CUE on this page uses [definitions](../learn/a08-definitions-reject-typos.md), [comprehensions](../learn/a10-comprehensions.md) and [references](../learn/a11-references-let-hidden.md). Read those lessons first if any of them is new to you.

## Step 1: run the stack in OpenTofu

The HCL of both roots is in step 2, next to its CUE. This step shows OpenTofu 1.13.1 running it. The page does not run these commands. The output is excerpted, and a line that reads `...` marks a cut.

`platform` was applied with the password named `root`. A `moved` block then renamed the password to `admin`. A `moved` block that has not been applied is a change, so the plan exits with code 2 until an apply has carried it out. `gateway` was applied once, after `platform`.

```sh title="platform, first apply"
tofu apply
```

```text title="OpenTofu 1.13.1"
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

```sh title="platform, after the rename"
tofu plan -detailed-exitcode
```

```text title="OpenTofu 1.13.1, exit code 2"
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

```sh title="platform, then gateway"
tofu plan -detailed-exitcode
```

```text title="OpenTofu 1.13.1, exit code 0 in both roots"
...
No changes. Your infrastructure matches the configuration.
```

Each root left a `terraform.tfstate`. The page keeps both files whole, as OpenTofu wrote them, and does not edit them. It prints the parts that the conversion reads, and a line that reads `...` marks a cut. The files are in state format 4. The password in the first file is a random value that the random provider generated.

```json title="platform/terraform.tfstate (excerpt)"
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

```json title="gateway/terraform.tfstate (excerpt)"
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

```json file=platform/terraform.tfstate hidden
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
    "endpoints": {
      "value": [
        "api-whole-tarpon:8080",
        "web-ethical-humpback:8081",
        "worker-capital-elk:8082"
      ],
      "type": [
        "list",
        "string"
      ]
    }
  },
  "resources": [
    {
      "mode": "managed",
      "type": "random_password",
      "name": "admin",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 3,
          "attributes": {
            "bcrypt_hash": "$2a$10$mOHC9Xd0OpZYDPSZ.yhknuBaZau/e/qADCGx0kbHzbguaP/52qVM.",
            "id": "none",
            "keepers": null,
            "length": 24,
            "lower": true,
            "min_lower": 0,
            "min_numeric": 0,
            "min_special": 0,
            "min_upper": 0,
            "number": true,
            "numeric": true,
            "override_special": null,
            "result": "LvJ3A220jgeoq7ozAmhqaOyy",
            "special": false,
            "upper": true
          },
          "sensitive_attributes": [
            [
              {
                "type": "get_attr",
                "value": "bcrypt_hash"
              }
            ],
            [
              {
                "type": "get_attr",
                "value": "result"
              }
            ]
          ]
        }
      ]
    },
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
            "content_base64": null,
            "content_base64sha256": "PYOlZ1yK2BP5Wy7T7StN51HI90ebGIlcvHRGPZY7U6k=",
            "content_base64sha512": "yWfae0wyKW6rXvofzZED0GqXObUtzxS3xOW1AQ0KH5wvturN6KJY99MU7j3ScSZu7U7WBPjoVv/zwPAHK6wViA==",
            "content_md5": "20349bdf8cc108f89790be94465f200e",
            "content_sha1": "2d90dc036c332486001227484af84855018d9763",
            "content_sha256": "3d83a5675c8ad813f95b2ed3ed2b4de751c8f7479b18895cbc74463d963b53a9",
            "content_sha512": "c967da7b4c32296eab5efa1fcd9103d06a9739b52dcf14b7c4e5b5010d0a1f9c2fb6eacde8a258f7d314ee3dd271266eed4ed604f8e856fff3c0f0072bac1588",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/api.conf",
            "id": "2d90dc036c332486001227484af84855018d9763",
            "sensitive_content": null,
            "source": null
          },
          "sensitive_attributes": [
            [
              {
                "type": "get_attr",
                "value": "sensitive_content"
              }
            ]
          ],
          "dependencies": [
            "module.service.random_pet.host"
          ]
        }
      ]
    },
    {
      "module": "module.service[\"api\"]",
      "mode": "managed",
      "type": "random_pet",
      "name": "host",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "api-whole-tarpon",
            "keepers": null,
            "length": 2,
            "prefix": "api",
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.service[\"web\"]",
      "mode": "managed",
      "type": "local_file",
      "name": "config",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "host = web-ethical-humpback\nport = 8081\n",
            "content_base64": null,
            "content_base64sha256": "GiE3bzNXlyp1NdZRp93jiCfaXwidILalUktWuekeX1w=",
            "content_base64sha512": "H7uf345h8hJ0z5uB8BTiaHi0FKEHzp1VjFewqa4Lz6ebOaPO29AWMpq4FkkifVELhs/HYBX0/iqlrcSUHTBRvA==",
            "content_md5": "8d0194f739e2cb2178a8a4357eb540da",
            "content_sha1": "fee05d3d445f409a78d444b593c6b4d579db2658",
            "content_sha256": "1a21376f3357972a7535d651a7dde38827da5f089d20b6a5524b56b9e91e5f5c",
            "content_sha512": "1fbb9fdf8e61f21274cf9b81f014e26878b414a107ce9d558c57b0a9ae0bcfa79b39a3cedbd016329ab81649227d510b86cfc76015f4fe2aa5adc4941d3051bc",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/web.conf",
            "id": "fee05d3d445f409a78d444b593c6b4d579db2658",
            "sensitive_content": null,
            "source": null
          },
          "sensitive_attributes": [
            [
              {
                "type": "get_attr",
                "value": "sensitive_content"
              }
            ]
          ],
          "dependencies": [
            "module.service.random_pet.host"
          ]
        }
      ]
    },
    {
      "module": "module.service[\"web\"]",
      "mode": "managed",
      "type": "random_pet",
      "name": "host",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "web-ethical-humpback",
            "keepers": null,
            "length": 2,
            "prefix": "web",
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.service[\"worker\"]",
      "mode": "managed",
      "type": "local_file",
      "name": "config",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "host = worker-capital-elk\nport = 8082\n",
            "content_base64": null,
            "content_base64sha256": "D8N+WtG5QJQEfVuQ/+LSxXTCeiuQhchb7hSAXXzR1wU=",
            "content_base64sha512": "5/WVg7c4eSi7e5DotfSPLzQIUbJyJiJjJpV1zfsLuA8VTsSC8146M5yp1w9ERsG6+60pcWX4lw/ZSetW8TmJ+Q==",
            "content_md5": "7efe8ad32eec8f28f604ef80535799b0",
            "content_sha1": "7507605ba091e0a20f9f52f934251e773830367d",
            "content_sha256": "0fc37e5ad1b94094047d5b90ffe2d2c574c27a2b9085c85bee14805d7cd1d705",
            "content_sha512": "e7f59583b7387928bb7b90e8b5f48f2f340851b272262263269575cdfb0bb80f154ec482f35e3a339ca9d70f4446c1bafbad297165f8970fd949eb56f13989f9",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/worker.conf",
            "id": "7507605ba091e0a20f9f52f934251e773830367d",
            "sensitive_content": null,
            "source": null
          },
          "sensitive_attributes": [
            [
              {
                "type": "get_attr",
                "value": "sensitive_content"
              }
            ]
          ],
          "dependencies": [
            "module.service.random_pet.host"
          ]
        }
      ]
    },
    {
      "module": "module.service[\"worker\"]",
      "mode": "managed",
      "type": "random_pet",
      "name": "host",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "worker-capital-elk",
            "keepers": null,
            "length": 2,
            "prefix": "worker",
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    }
  ],
  "check_results": [
    {
      "object_kind": "var",
      "config_addr": "module.service.var.port",
      "status": "pass",
      "objects": [
        {
          "object_addr": "module.service[\"worker\"].var.port",
          "status": "pass"
        },
        {
          "object_addr": "module.service[\"api\"].var.port",
          "status": "pass"
        },
        {
          "object_addr": "module.service[\"web\"].var.port",
          "status": "pass"
        }
      ]
    }
  ]
}
```

```json file=gateway/terraform.tfstate hidden
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 1,
  "lineage": "7ea0ecb1-04f8-0d9a-58c8-ce9a7eb76f00",
  "outputs": {},
  "resources": [
    {
      "mode": "data",
      "type": "terraform_remote_state",
      "name": "platform",
      "provider": "provider[\"terraform.io/builtin/terraform\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "backend": "local",
            "config": {
              "value": {
                "path": "../platform/terraform.tfstate"
              },
              "type": [
                "object",
                {
                  "path": "string"
                }
              ]
            },
            "defaults": null,
            "outputs": {
              "value": {
                "admin_password": "LvJ3A220jgeoq7ozAmhqaOyy",
                "endpoints": [
                  "api-whole-tarpon:8080",
                  "web-ethical-humpback:8081",
                  "worker-capital-elk:8082"
                ]
              },
              "type": [
                "object",
                {
                  "admin_password": "string",
                  "endpoints": [
                    "list",
                    "string"
                  ]
                }
              ]
            },
            "workspace": null
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "mode": "managed",
      "type": "local_file",
      "name": "upstreams",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "api-whole-tarpon:8080\nweb-ethical-humpback:8081\nworker-capital-elk:8082\n",
            "content_base64": null,
            "content_base64sha256": "NQWwlkhpnuRpCuZgpZnA98WbmiVy9bZhpMPxWbe9zKM=",
            "content_base64sha512": "/oYt1jGf8LWFfrQ9JmXZCXxAx/uiYKA4oJPrDcGZ4PEz3BcKkFMmfPEkaEZNHE52U+CjhR5VIb+vmiaVkoWvEA==",
            "content_md5": "ced60d0d74493832ff4a6c6ccf4a8058",
            "content_sha1": "a80f4be062d20967b1ee08bdcac01ef45725e8cb",
            "content_sha256": "3505b09648699ee4690ae660a599c0f7c59b9a2572f5b661a4c3f159b7bdcca3",
            "content_sha512": "fe862dd6319ff0b5857eb43d2665d9097c40c7fba260a038a093eb0dc199e0f133dc170a9053267cf12468464d1c4e7653e0a3851e5521bfaf9a26959285af10",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/upstreams.conf",
            "id": "a80f4be062d20967b1ee08bdcac01ef45725e8cb",
            "sensitive_content": null,
            "source": null
          },
          "sensitive_attributes": [
            [
              {
                "type": "get_attr",
                "value": "sensitive_content"
              }
            ]
          ],
          "dependencies": [
            "data.terraform_remote_state.platform"
          ]
        }
      ]
    }
  ],
  "check_results": null
}
```

Four things in the files matter to the conversion.

- Providers sit on the registry that OpenTofu used: `registry.opentofu.org/hashicorp/random`. The built-in provider sits on `terraform.io/builtin/terraform`.
- A resource in a module carries the module instance: `module.service["api"]`. The `dependencies` of `local_file.config` name `module.service.random_pet.host`, without the key.
- `gateway` holds a `terraform_remote_state` record. It copies every output of `platform`, `admin_password` in the clear.
- The files hold fields the converter ignores: `lineage`, `serial`, `terraform_version`, `sensitive_attributes` and `check_results`.

The state describes four files. Write them, then record a checksum of every file that matters.

```sh run
mkdir -p platform/out gateway/out
printf 'host = api-whole-tarpon\nport = 8080\n' > platform/out/api.conf
printf 'host = web-ethical-humpback\nport = 8081\n' > platform/out/web.conf
printf 'host = worker-capital-elk\nport = 8082\n' > platform/out/worker.conf
printf 'api-whole-tarpon:8080\nweb-ethical-humpback:8081\nworker-capital-elk:8082\n' > gateway/out/upstreams.conf
cksum platform/terraform.tfstate gateway/terraform.tfstate platform/out/* gateway/out/*
```

```text out
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

:::compare
```hcl title="platform/main.tf, lines 1-6"
terraform {
  required_providers {
    local  = { source = "hashicorp/local", version = "~> 2.5" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}
```

```cue file=platform/providers.cue
package platform

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local: loc.#Provider & {$version: "~> 2.5"}
$random: rnd.#Provider & {$version: "~> 3.6"}
```
:::

- `required_providers` and the `provider` block become one struct per provider. `$version` holds the constraint.
- The import path names the provider. `pravo init` generates the package it points to.

### The module

:::compare
```hcl title="platform/modules/service/main.tf"
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

```cue file=platform/service.cue
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
:::

- A module is a definition that holds resources. `#Service` is the module.
- The variables are fields. The `validation` block is the constraint on `port`.
- The module output is an ordinary field: `endpoint`.
- Nothing wires the module into the root. Writing an instance is enough.
- `#Service` builds its resources through the providers of its own package. A module shared by several roots takes the provider as an input, as [M4](m4-translate-the-specification.md) shows.
- The address of a resource inside an instance is the path of the instance plus the field: `services.api.host`.

### The root

:::compare
```hcl title="platform/main.tf, lines 8-41"
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

```cue file=platform/main.cue
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
:::

- `locals.ports` becomes the hidden field `_ports`.
- `module "service"` with `for_each` becomes a struct comprehension. The key becomes the last part of the address: `services.api`.
- `sort(...)` becomes `list.SortStrings(...)`.
- `sensitive = true` becomes `sensitive: true`. The provider also marks `result` as sensitive in its schema.
- The `moved` block has no CUE counterpart. OpenTofu applied it, so the state already holds `random_password.admin`. A `moved` block that is still pending becomes `$from`, as step 6 shows.

### The second root

:::compare
```hcl title="gateway/main.tf"
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

```cue file=gateway/main.cue
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
:::

- `terraform_remote_state` becomes `pravo.#RemoteOutputs`. It reads the `outputs.json` of the other root's pravo state and nothing else.
- `platform.outputs.endpoints` is the value of the `endpoints` output of `platform`.
- An output that `platform` marks sensitive is not passed on.

The HCL blocks above hold 84 lines. The CUE blocks hold 71 lines in four files. The conversion in steps 6 and 8 adds 15 lines of scaffolding, which you delete at the end.

## Step 3: initialise and validate

`pravo init` installs the providers, writes `pravo.lock.cue` and generates the CUE packages under `cue.mod`. In a directory without `pravo.lock.cue`, it contacts the provider registry. The first line of its output is normal: the files import packages that `init` generates.

`pravo validate` evaluates the specification and checks every resource against its provider schema. It reads neither the state nor your objects.

```sh run
pravo -C platform init
pravo -C gateway init
pravo -C platform validate
pravo -C gateway validate
```

```text out
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

```sh run
pravo -C platform state import-tfstate terraform.tfstate
```

```text out
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

:::mapping
| OpenTofu address | Address in pravo's state | Resource in CUE |
|---|---|---|
| `module.service["api"].random_pet.host` | `random_pet.service_api_host` | `services.api.host` |
| `module.service["api"].local_file.config` | `local_file.service_api_config` | `services.api.config` |
| `random_password.admin` | `random_password.admin` | `admin` |
:::

The `web` and `worker` instances follow the first two rows. The address of a resource in pravo's state is where the resource sits in the specification. The conversion keeps the OpenTofu name. Step 6 supplies the rest.

Show one record to see what the conversion kept.

```sh run
pravo -C platform state show random_pet.service_api_host
```

```text out
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

```sh run
pravo -C platform state show random_password.admin | grep -E '^\s+(bcrypt_hash|result):'
```

```text out
	bcrypt_hash:      "$2a$10$mOHC9Xd0OpZYDPSZ.yhknuBaZau/e/qADCGx0kbHzbguaP/52qVM."
	result:           "LvJ3A220jgeoq7ozAmhqaOyy"
```

An output that OpenTofu marked sensitive stays sensitive. The list hides `admin_password`.

```sh run
pravo -C platform output
```

```text out
admin_password: (sensitive)
endpoints: ["api-whole-tarpon:8080", "web-ethical-humpback:8081", "worker-capital-elk:8082"]
```

The list hides the value. A request for the output by name prints it.

```sh run
pravo -C platform output admin_password
```

```text out
"LvJ3A220jgeoq7ozAmhqaOyy"
```

> [!PITFALL]
> `pravo output NAME` and `pravo output --json` print a sensitive output in the clear. Do not paste their output into a ticket or a chat. The state directory holds the value too, as `{"$sensitive": VALUE}`.

## Step 5: make a mistake

The specification does not yet say which record belongs to which resource. Predict what pravo does about that, then run the plan.

:::predict The state holds seven records and the specification declares seven resources, none with $from. What does the plan do?
::reveal
It creates seven resources and destroys seven. Each record sits at an address that no resource has, such as `random_pet.service_api_host`. Each resource sits at an address that no record has, such as `services.api.host`.
:::

```sh run exit=2 status=unintended
pravo -C platform plan --detailed-exitcode
```

```text out
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

```sh run
pravo -C platform plan 2>&1 | grep '^  # '
```

```text out
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

```cue file=platform/migrate.cue hl="4,8-9"
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

```sh run
pravo -C platform plan --detailed-exitcode
```

```text out
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

```sh run
pravo -C platform apply --auto-approve
pravo -C platform output
```

```text out
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

```sh run
pravo -C gateway state import-tfstate terraform.tfstate
pravo -C gateway state list
```

```text out
Imported 1 resources and 0 outputs from terraform.tfstate into ./gateway/pravo.state
Warning: data.terraform_remote_state.platform: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)
local_file.upstreams
```

The `terraform_remote_state` data source is not in the state. pravo has no such data source. The conversion skips the record of the built-in provider `terraform.io/builtin/terraform` and says so in a warning. The record holds a copy of every output of `platform`, `admin_password` included, and the conversion does not carry it. `gateway/main.cue`, written in step 2, reads the outputs of `platform` with `pravo.#RemoteOutputs`.

Add the `$from` for the file and plan.

```cue file=gateway/migrate.cue
package gateway

// Scaffolding for the state conversion. Delete this file after the first apply.
upstreams: $from: "local_file.upstreams"
```

```sh run
pravo -C gateway plan --detailed-exitcode
pravo -C gateway apply --auto-approve
```

```text out
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

```sh run
rm platform/migrate.cue gateway/migrate.cue
pravo -C platform plan --detailed-exitcode
pravo -C gateway plan --detailed-exitcode
pravo -C platform state list
```

```text out
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

```sh run
cksum platform/terraform.tfstate gateway/terraform.tfstate platform/out/* gateway/out/*
ls -A platform gateway
```

```text out
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

:::exercise Add a fourth service
Add a service `queue` on port 8083 without editing an existing file. Predict how many resources the plan adds. Then run `pravo -C platform plan --detailed-exitcode`.

::hint
`_ports` is a struct. A second file in the same package can add a field to it.

::hint
Lesson [A4](../learn/a04-order-does-not-matter.md) explains why a second file may add to a field of the first.

::solution
```cue file=platform/queue.cue
package platform

_ports: queue: 8083
```

```sh run exit=2
pravo -C platform plan --detailed-exitcode
```

```text out
...
pravo will perform the following actions:

  # services.queue.host (random_pet) will be created
...
  # services.queue.config (local_file) will be created
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 · skipped 7 · not read 7
...
```

```sh run check
pravo -C platform plan --detailed-exitcode | grep -q 'Plan: 2 to add, 0 to change, 0 to destroy.'
```
:::

Delete `platform/queue.cue` before you continue.

## What this example leaves out

The example uses providers that need no credentials. A provider such as `hashicorp/aws` adds one requirement: `pravo plan` reads real objects and stops without credentials. `pravo validate` needs none.

The example has no `count`, no taint, no `TF_VAR_` secret and no script that checks the plan JSON. [M4](m4-translate-the-specification.md) translates each construct. [M5](m5-convert-the-state.md) explains a plan that is not empty after conversion. [M10](m10-not-in-pravo.md) lists what has no counterpart.

## Recap

- You translate by hand; `pravo state import-tfstate` converts the state and leaves the `.tfstate` file as it was.
- A converted record sits at an OpenTofu-style address; `$from` moves it to the CUE path of its resource.
- A plan during migration may hold moves and nothing else; exit code 2 means stop.

:::check
What does a plan contain when you forget `$from` on every resource?
::answer
One create for each resource and one destroy for each record. No record sits at the address of a resource.

Why does `gateway` hold one record after the conversion, when the OpenTofu state held two?
::answer
The conversion skips the record of the built-in provider and prints a warning. The specification reads the outputs of the other root with `pravo.#RemoteOutputs`, which keeps no copy of them.
:::

Next: [translate the specification](m4-translate-the-specification.md) construct by construct.
