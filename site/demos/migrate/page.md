---
title: Migration demo
description: The runs behind the migration demo. A state file in format 4, written by OpenTofu 1.13.1, is converted and planned.
capture: true
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|moved to )[^\n]*\n'
    replace: ""
---

This page is not published. `tools/capture` runs it, and `site/data/migrate.json` records its output for the migration demo, which the page "The gate is an empty plan" embeds. `site/demos/migrate/derive.go` reads that file and this page and computes what the band embeds.

The OpenTofu side of the demo was run once. OpenTofu 1.13.1 applied `main.tf` below with `tofu apply`, and `site/demos/migrate/tofu/record.sh` keeps the result. `terraform.tfstate` is the state file that OpenTofu wrote, together with the three files that its resources created. This page does not run OpenTofu, so it needs no OpenTofu binary. pravo reads the state file, the providers read the files. `site/demos/migrate/fixtures.go` checks that the blocks of this page equal the recorded files.

The configuration that the state file belongs to, in HCL. It is shown in the demo and is not run.

```hcl title="main.tf"
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

resource "random_pet" "server" {
  length = 2
}

resource "local_file" "motd" {
  filename = "motd.txt"
  content  = "serving ${random_pet.server.id}\n"
}

resource "local_file" "note" {
  for_each = toset(["alpha", "beta"])
  filename = "notes/${each.key}.txt"
  content  = "${each.key}\n"
}
```

The same stack in CUE. Each resource names the address its record has in the converted state with `$from`.

```cue file=stack.cue title="stack.cue"
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider & {$version: "~> 2.5"}

server: $random.#Resource.random_pet & {
	$from:  "random_pet.server"
	length: 2
}

motd: $local.#Resource.local_file & {
	$from:    "local_file.motd"
	filename: "motd.txt"
	content:  "serving \(server.id)\n"
}

note: {
	for k in ["alpha", "beta"] {
		"\(k)": $local.#Resource.local_file & {
			$from:    "local_file.note_\(k)"
			filename: "notes/\(k).txt"
			content:  "\(k)\n"
		}
	}
}
```

The objects that the state file describes.

```text file=motd.txt hidden
serving saved-tetra
```

```text file=notes/alpha.txt hidden
alpha
```

```text file=notes/beta.txt hidden
beta
```

```json file=terraform.tfstate hidden
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 1,
  "lineage": "f93e90bf-4a32-d352-ae93-7cae029d34dd",
  "outputs": {},
  "resources": [
    {
      "mode": "managed",
      "type": "local_file",
      "name": "motd",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "serving saved-tetra\n",
            "content_base64": null,
            "content_base64sha256": "z2LJydqAgs5XMAB7Yj4POtQLRY664HGI8kZ2erhKpeU=",
            "content_base64sha512": "QH78My4aImNunDlv9d0Y0yN6wK6RObSKcHE4pixQNv+D97PY4V/7PQTgvAzSmdjI2oYHtmRiqdIB2DW1KsRJjA==",
            "content_md5": "e050f29aaa7511263649fcab2d892c47",
            "content_sha1": "a335b76bfedd3f13b948473b4757cc4862fee26a",
            "content_sha256": "cf62c9c9da8082ce5730007b623e0f3ad40b458ebae07188f246767ab84aa5e5",
            "content_sha512": "407efc332e1a22636e9c396ff5dd18d3237ac0ae9139b48a707138a62c5036ff83f7b3d8e15ffb3d04e0bc0cd299d8c8da8607b66462a9d201d835b52ac4498c",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "motd.txt",
            "id": "a335b76bfedd3f13b948473b4757cc4862fee26a",
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
            "random_pet.server"
          ]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "local_file",
      "name": "note",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "index_key": "alpha",
          "schema_version": 0,
          "attributes": {
            "content": "alpha\n",
            "content_base64": null,
            "content_base64sha256": "tqmNnOmi2RSSiPo99C03fD5Cc3r9za9xTjPAoQC1EGA=",
            "content_base64sha512": "YtB5HSL4ce9LTo9voTdAkfbVQLpePpvCOw5v0uPWU0+Qh7jBlWNMdif8JqM/F1drThB9pKtCHUhqzCY2U4u1jw==",
            "content_md5": "9f9f90dbe3e5ee1218c86b8839db1995",
            "content_sha1": "d046cd9b7ffb7661e449683313d41f6fc33e3130",
            "content_sha256": "b6a98d9ce9a2d9149288fa3df42d377c3e42737afdcdaf714e33c0a100b51060",
            "content_sha512": "62d0791d22f871ef4b4e8f6fa1374091f6d540ba5e3e9bc23b0e6fd2e3d6534f9087b8c195634c7627fc26a33f17576b4e107da4ab421d486acc2636538bb58f",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "notes/alpha.txt",
            "id": "d046cd9b7ffb7661e449683313d41f6fc33e3130",
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
          ]
        },
        {
          "index_key": "beta",
          "schema_version": 0,
          "attributes": {
            "content": "beta\n",
            "content_base64": null,
            "content_base64sha256": "8sgt7N1xgc+YlFkppiWY235rR34R9uDrCulwIO/xUa0=",
            "content_base64sha512": "jziRL10BJFnStgpQu6WaVVWm0lfhg/o/r7wC3WU3LBmnP/Tr27C9XYgDc/9eT/Ntgh3Je5vRsAGPMfXRvg6uuQ==",
            "content_md5": "f0cf2a92516045024a0c99147b28f05b",
            "content_sha1": "6c007a14875d53d9bf0ef5a6fc0257c817f0fb83",
            "content_sha256": "f2c82decdd7181cf98945929a62598db7e6b477e11f6e0eb0ae97020eff151ad",
            "content_sha512": "8f38912f5d012459d2b60a50bba59a5555a6d257e183fa3fafbc02dd65372c19a73ff4ebdbb0bd5d880373ff5e4ff36d821dc97b9bd1b0018f31f5d1be0eaeb9",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "notes/beta.txt",
            "id": "6c007a14875d53d9bf0ef5a6fc0257c817f0fb83",
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
          ]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "random_pet",
      "name": "server",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "saved-tetra",
            "keepers": null,
            "length": 2,
            "prefix": null,
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    }
  ],
  "check_results": null
}
```

```sh hidden
pravo init
```

## Convert the state

```sh run
pravo state import-tfstate terraform.tfstate
```

```text out
Imported 4 resources and 0 outputs from terraform.tfstate into ./pravo.state

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  local_file.note["alpha"] -> local_file.note_alpha
  local_file.note["beta"] -> local_file.note_beta
```

## Plan

The first plan lists the moves that `$from` declares and writes nothing. An apply carries them out, and then the next plan has nothing to do.

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # random_pet.server will be moved to server (record renamed; no change to the object)

  # local_file.motd will be moved to motd (record renamed; no change to the object)

  # local_file.note_alpha will be moved to note.alpha (record renamed; no change to the object)

  # local_file.note_beta will be moved to note.beta (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 4 to move.
Graph: planned 4 · refreshed 4 · to move 4
```

```sh hidden
pravo apply --auto-approve
```

```sh run
pravo plan
```

```text out
No changes. The specification matches the state. 4 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 4 · not read 4
```

## Facts for the band

```sh run
pravo validate
```

```text out
Success! 4 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

```sh run
pravo state list
```

```text out
motd
note.alpha
note.beta
server
```
