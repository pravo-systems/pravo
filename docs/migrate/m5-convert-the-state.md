---
title: Convert the state
description: Convert the OpenTofu state into pravo records, point each record at its address, and plan until nothing changes.
order: 5
group: Runbook
gate: "`pravo plan` says No changes"
minutes: 30
needs: "Step 4 done: [translate the specification](m4-translate-the-specification.md)."
learn: How does an OpenTofu state become pravo records, and when is the first plan allowed to be applied?
done_when: "`pravo plan` prints `No changes.`, the first apply has finished, and a new resource has been created."
level: intermediate
normalize:
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|Reading\.\.\.|Read complete|moved to )[^\n]*\n'
    replace: ""
  - pattern: "refreshed [0-9]+s ago"
    replace: "refreshed <age> ago"
  - pattern: "\\[id=[a-z-]+\\]"
    replace: "[id=<id>]"
  - pattern: "after [0-9]+s"
    replace: "after 0s"
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
  - pattern: "\\[txn [0-9TZ]+-[0-9a-f]+\\]"
    replace: "[txn <txn>]"
  - pattern: "config hash [0-9a-f]{16}"
    replace: "config hash <hash>"
keywords: "terraform state tfstate import convert"
---

This is step 5 of the runbook. You convert the OpenTofu state once, tell pravo where each record now sits, and plan until the plan is empty. An empty plan is the gate: it says that the specification and the real objects agree.

Every command up to the first apply changes the pravo state directory and nothing else. The `.tfstate` file is read and never written. The real objects are read and never changed. Data sources and ephemeral resources run during a plan, as they do in OpenTofu.

For a cloud root, the first `pravo plan` reads every object through its provider, because a converted record has no configuration hash yet. Run it with the credentials and the provider configuration that OpenTofu used.

> [!WARNING]
> Stop rule: never apply a plan that replaces or destroys a resource during the migration. Such a plan means that the specification does not yet describe the object. Fix the specification, or the `$from` fields, and plan again.

The outputs on this page come from a sample root: the estate of [M4](m4-translate-the-specification.md), the objects that OpenTofu 1.13.1 created for it, and the state file `terraform.tfstate` that OpenTofu wrote. The page does not run OpenTofu. The sample files are not printed. Your project has its own. Run the same commands in your root. The addresses and the counts differ. [M3](m3-worked-example.md) prints complete state files that OpenTofu wrote.

```cue file=providers.cue hidden
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:  rnd.#Provider & {$version: "~> 3.6"}
$local:   loc.#Provider & {$version: "~> 2.5"}
$archive: loc.#Provider
```

```cue file=variables.cue hidden
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us"]
```

```cue file=locals.cue hidden
package estate

_prefix: "acme-\(env)"
```

```cue file=resources.cue hidden
package estate

banner: $local.#Data.local_file & {filename: "banner.txt"}

server: $random.#Resource.random_pet & {
	prefix: _prefix
	length: 2
}

hello: $local.#Resource.local_file & {
	filename: "out/hello.txt"
	content:  "\(banner.content)Hello, \(server.id)!\n"
}
```

```cue file=hello.cue hidden
package estate

hello: {
	$depends_on: [ports[0], ports[1]]
	$lifecycle: ignore_changes: ["file_permission"]
}
```

```cue file=repeat.cue hidden
package estate

import "list"

ports: [
	for i in list.Range(0, 2, 1) {
		$random.#Resource.random_integer & {min: 8000, max: 8999}
	},
]

region: {
	for r in regions {
		"\(r)": $random.#Resource.random_pet & {prefix: r, length: 2}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: $random.#Resource.random_pet & {length: 1}
}
```

```cue file=notes.cue hidden
package estate

import loc "pravo.dev/providers/hashicorp/local"

#Notes: {
	provider!: loc.#Provider
	dir:       string
	names:     [...string]

	files: {
		for n in names {
			"\(n)": provider.#Resource.local_file & {
				filename: "\(dir)/\(n).txt"
				content:  "\(n)\n"
			}
		}
	}
	paths: [for n in names {files[n].filename}]
}
```

```cue file=instances.cue hidden
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

```cue file=outputs.cue hidden
package estate

import "pravo.dev/pravo"

server_name: pravo.#Output & {value: server.id}
note_paths:  pravo.#Output & {value: notes.paths}

port_numbers: pravo.#Output & {
	value:     [for p in ports {p.result}]
	sensitive: true
}
```

```text file=banner.txt hidden
estate
```

```text file=out/hello.txt hidden
estate
Hello, acme-dev-charmed-buzzard!
```

```text file=out/notes/alpha.txt hidden
alpha
```

```text file=out/notes/beta.txt hidden
beta
```

```json file=terraform.tfstate hidden
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 1,
  "lineage": "f796198c-333d-d541-da82-c3c1788f205b",
  "outputs": {
    "note_paths": {
      "value": [
        "out/notes/alpha.txt",
        "out/notes/beta.txt"
      ],
      "type": [
        "tuple",
        [
          "string",
          "string"
        ]
      ]
    },
    "port_numbers": {
      "value": [
        8111,
        8974
      ],
      "type": [
        "tuple",
        [
          "number",
          "number"
        ]
      ],
      "sensitive": true
    },
    "server_name": {
      "value": "acme-dev-charmed-buzzard",
      "type": "string"
    }
  },
  "resources": [
    {
      "mode": "data",
      "type": "local_file",
      "name": "banner",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "estate\n",
            "content_base64": "ZXN0YXRlCg==",
            "content_base64sha256": "B2K4gBS6bSNOY4Qb3Irinf2nSR/7kn66kZHzkum7FUE=",
            "content_base64sha512": "Tl90siz9Jq4jVwR2hoq5sh+/gdrp0boArd3pRtcQnVeob1etCkWMJ9PpGfdJro7R48TWSm/ToLZI0z9U+BrxJA==",
            "content_md5": "7e35b47b4de9f993c8ca4161f2260100",
            "content_sha1": "fd9c765da1ef3d530b673c2069003cd07dd3c4da",
            "content_sha256": "0762b88014ba6d234e63841bdc8ae29dfda7491ffb927eba9191f392e9bb1541",
            "content_sha512": "4e5f74b22cfd26ae23570476868ab9b21fbf81dae9d1ba00addde946d7109d57a86f57ad0a458c27d3e919f749ae8ed1e3c4d64a6fd3a0b648d33f54f81af124",
            "filename": "banner.txt",
            "id": "fd9c765da1ef3d530b673c2069003cd07dd3c4da"
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "mode": "managed",
      "type": "local_file",
      "name": "hello",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "content": "estate\nHello, acme-dev-charmed-buzzard!\n",
            "content_base64": null,
            "content_base64sha256": "MjVB04BOFJK3lc9AJWbljKtQi8J0cUFbEA5PhMeoOH0=",
            "content_base64sha512": "0bmo3AT1Sk7B04Y3xUzShISEqQm2/p82ueoH6K8Azq3O3LbLfdVaR+BOYPS+VW53aQCKa0hH1GaJMp8WKw9C3Q==",
            "content_md5": "ed6b0f8497246c1f7099b9f8fbaa3cae",
            "content_sha1": "16fa07316cd5759c13c1380c5503af94b58ef1cc",
            "content_sha256": "323541d3804e1492b795cf402566e58cab508bc27471415b100e4f84c7a8387d",
            "content_sha512": "d1b9a8dc04f54a4ec1d38637c54cd2848484a909b6fe9f36b9ea07e8af00ceadcedcb6cb7dd55a47e04e60f4be556e7769008a6b4847d46689329f162b0f42dd",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/hello.txt",
            "id": "16fa07316cd5759c13c1380c5503af94b58ef1cc",
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
            "data.local_file.banner",
            "random_integer.port",
            "random_pet.server"
          ]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "random_integer",
      "name": "port",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "index_key": 0,
          "schema_version": 0,
          "attributes": {
            "id": "8111",
            "keepers": null,
            "max": 8999,
            "min": 8000,
            "result": 8111,
            "seed": null
          },
          "sensitive_attributes": []
        },
        {
          "index_key": 1,
          "schema_version": 0,
          "attributes": {
            "id": "8974",
            "keepers": null,
            "max": 8999,
            "min": 8000,
            "result": 8974,
            "seed": null
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "mode": "managed",
      "type": "random_pet",
      "name": "region",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "index_key": "eu",
          "schema_version": 0,
          "attributes": {
            "id": "eu-just-salmon",
            "keepers": null,
            "length": 2,
            "prefix": "eu",
            "separator": "-"
          },
          "sensitive_attributes": []
        },
        {
          "index_key": "us",
          "schema_version": 0,
          "attributes": {
            "id": "us-exciting-barnacle",
            "keepers": null,
            "length": 2,
            "prefix": "us",
            "separator": "-"
          },
          "sensitive_attributes": []
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
            "id": "acme-dev-charmed-buzzard",
            "keepers": null,
            "length": 2,
            "prefix": "acme-dev",
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.notes",
      "mode": "managed",
      "type": "local_file",
      "name": "note",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"].archive",
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
            "filename": "out/notes/alpha.txt",
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
            "filename": "out/notes/beta.txt",
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
    }
  ],
  "check_results": [
    {
      "object_kind": "var",
      "config_addr": "var.env",
      "status": "pass",
      "objects": [
        {
          "object_addr": "var.env",
          "status": "pass"
        }
      ]
    }
  ]
}
```

```sh hidden
pravo init
```


## Get the state file

`pravo state import-tfstate` reads a file. For a local backend that file is `terraform.tfstate`. For a remote backend, write the current state to a file first and keep a second copy.

```sh
tofu state pull > terraform.tfstate
cp terraform.tfstate terraform.tfstate.backup
```

The command accepts state format 4, which OpenTofu and Terraform 0.12 or newer write. OpenTofu names its providers on `registry.opentofu.org`. A state from legacy Terraform names them on `registry.terraform.io`. pravo reads each as the same provider on the OpenTofu registry, and keeps a provider on any other host as written.

The state in this workspace holds six resource blocks. Its shape:

```json title="terraform.tfstate (excerpt)"
{
  "version": 4,
  "terraform_version": "1.13.1",
  "resources": [
    {
      "module": "module.notes",
      "mode": "managed",
      "type": "local_file",
      "name": "note",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"].archive",
      "instances": [
        { "index_key": "alpha", "attributes": { "filename": "out/notes/alpha.txt", "...": "..." } },
        { "index_key": "beta", "attributes": { "filename": "out/notes/beta.txt", "...": "..." } }
      ]
    },
    "..."
  ]
}
```

To see the host rule, rewrite the host in a copy of the file and convert the copy in a throwaway root. The copy stands in for a state that legacy Terraform wrote.

```sh run
rm -rf legacy && mkdir legacy && cp -R cue.mod *.cue legacy/
grep -c 'registry.opentofu.org/' terraform.tfstate
sed 's#registry.opentofu.org/#registry.terraform.io/#' terraform.tfstate > legacy.tfstate
grep -c 'registry.terraform.io/' legacy.tfstate
pravo -C legacy state import-tfstate ../legacy.tfstate | head -n 1
pravo -C legacy state show random_pet.server | head -n 1
rm -r legacy legacy.tfstate
```

```text out
6
6
Imported 9 resources and 3 outputs from ../legacy.tfstate into ./legacy/pravo.state
// random_pet.server (provider registry.opentofu.org/hashicorp/random as random, schema version 0, revision 1)
```

The record names `registry.opentofu.org/hashicorp/random`, as it does for the original file.

## Import it

```sh run
pravo state import-tfstate terraform.tfstate
```

```text out
Imported 9 resources and 3 outputs from terraform.tfstate into ./pravo.state

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  module.notes.local_file.note["alpha"] -> local_file.notes_note_alpha
  module.notes.local_file.note["beta"] -> local_file.notes_note_beta
  random_integer.port[0] -> random_integer.port_0
  random_integer.port[1] -> random_integer.port_1
  random_pet.region["eu"] -> random_pet.region_eu
  random_pet.region["us"] -> random_pet.region_us
```

The first line counts what was converted: eight managed resources, one data source and three outputs. Each instance is one record in the new state directory, `pravo.state`.

An address in pravo is the place of a resource in the specification. The records keep the OpenTofu address until you say where each one went. Instances with a key, and resources in modules, have no plain address. The command renames them and prints every rename:

- An instance of `count` or `for_each` becomes `<name>_<key>`.
- A resource in a module takes the module path as a prefix: `module.notes.local_file.note["alpha"]` becomes `local_file.notes_note_alpha`.
- Any character other than a letter, a digit, `_` or `-` becomes `_`.

:::mapping
| In the OpenTofu state | Record in the pravo state | Place in the specification |
|---|---|---|
| `data.local_file.banner` | `data.local_file.banner` | `banner` |
| `random_pet.server` | `random_pet.server` | `server` |
| `local_file.hello` | `local_file.hello` | `hello` |
| `random_integer.port[0]` | `random_integer.port_0` | `ports[0]` |
| `random_pet.region["eu"]` | `random_pet.region_eu` | `region.eu` |
| `module.notes.local_file.note["alpha"]` | `local_file.notes_note_alpha` | `notes.files.alpha` |
:::

The command also prints a warning for each thing it cannot carry over, and skips the records of the built-in provider, such as `terraform_remote_state`. [M10](m10-not-in-pravo.md) lists them.

## A root with workspaces

pravo has no workspaces. A tag selects the environment, and the state path follows it, as in [B4](../learn/b04-inputs-tags-environments.md). Each workspace of the OpenTofu root has a state of its own, so each one is converted into the state path of its environment. The tag names the environment, and `import-tfstate` writes to the state path that the tag selects.

The sample is a root in the directory `ws` with the workspaces `dev` and `prod`. The pet takes its prefix from the workspace: `prefix = terraform.workspace`. OpenTofu 1.13.1 listed the workspaces and pulled the state of each. The page does not run these commands.

```sh
tofu workspace list
tofu workspace select dev
tofu state pull > dev.tfstate
tofu workspace select prod
tofu state pull > prod.tfstate
```

```text title="tofu workspace list, OpenTofu 1.13.1"
  default
  dev
* prod
```

The workspace `default` holds no resource, so it has nothing to convert. The two files below are the output of `tofu state pull`, unedited. The pull prints a state on one line.

```cue file=ws/main.cue title="ws/main.cue"
package ws

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

env: *"dev" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {$from: "random_pet.pet", length: 1, prefix: env}
```

```json file=ws/dev.tfstate title="ws/dev.tfstate"
{"version":4,"terraform_version":"1.13.1","serial":1,"lineage":"73892dda-414b-edc9-6a58-654fdd6613cc","outputs":{},"resources":[{"mode":"managed","type":"random_pet","name":"pet","provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[{"schema_version":0,"attributes":{"id":"dev-flounder","keepers":null,"length":1,"prefix":"dev","separator":"-"},"sensitive_attributes":[]}]}],"check_results":null}
```

```json file=ws/prod.tfstate title="ws/prod.tfstate"
{"version":4,"terraform_version":"1.13.1","serial":1,"lineage":"2849ce56-ff2d-cb00-33d9-0e23a6b5eab3","outputs":{},"resources":[{"mode":"managed","type":"random_pet","name":"pet","provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[{"schema_version":0,"attributes":{"id":"prod-chimp","keepers":null,"length":1,"prefix":"prod","separator":"-"},"sensitive_attributes":[]}]}],"check_results":null}
```

Convert each state with its own tag, and plan each environment with the same tag:

```sh run
pravo -C ws init > /dev/null
pravo -C ws state import-tfstate dev.tfstate | head -n 1
pravo -C ws state import-tfstate prod.tfstate -t env=prod | head -n 1
pravo -C ws plan | grep -E '^(  # |Plan:)'
pravo -C ws plan -t env=prod | grep -E '^(  # |Plan:)'
ls ws/state
```

```text out
Imported 1 resources and 0 outputs from dev.tfstate into ./ws/state/dev
Imported 1 resources and 0 outputs from prod.tfstate into ./ws/state/prod
  # random_pet.pet was moved to pet (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy.
  # random_pet.pet was moved to pet (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy.
dev
prod
```

The specification and its `$from` fields are shared. Each state moves its own record. Work through the rest of this page once for each environment, and pause the pipeline for every workspace of the root before the cut-over of [M7](m7-cut-over.md).

## Plan before you point

Run the plan now, with the policy that enforces the stop rule. The policy refuses any plan that deletes or replaces a managed resource.

```cue file=policy/no-destroy.cue title="policy/no-destroy.cue"
// During the migration a plan may not delete or replace a managed resource.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
```

The specification does not mention the old addresses yet. pravo cannot tell that `random_pet.server` and `server` are one object.

```sh run status=unintended
pravo plan --policy policy/no-destroy.cue > plan.txt 2>&1
echo "exit=$?"
grep -E '^(  # |Plan:|Error:)' plan.txt
```

```text out
exit=5
  # notes.files.alpha (local_file) will be created
  # notes.files.beta (local_file) will be created
  # ports[0] (random_integer) will be created
  # ports[1] (random_integer) will be created
  # region.eu (random_pet) will be created
  # region.us (random_pet) will be created
  # server (random_pet) will be created
  # hello (local_file) will be created
  # local_file.hello (local_file) will be destroyed
  # local_file.notes_note_alpha (local_file) will be destroyed
  # local_file.notes_note_beta (local_file) will be destroyed
  # random_integer.port_0 (random_integer) will be destroyed
  # random_integer.port_1 (random_integer) will be destroyed
  # random_pet.region_eu (random_pet) will be destroyed
  # random_pet.region_us (random_pet) will be destroyed
  # random_pet.server (random_pet) will be destroyed
  # data.local_file.banner (local_file) will be destroyed
Plan: 8 to add, 0 to change, 9 to destroy.
Error: the plan violates the policy policy/no-destroy.cue (sha256:5fa3c22880d1):
```

The plan creates every resource at its new address and deletes every record at its old one. The exit code is 5, the code for a policy refusal. The policy did its work: nothing here may be applied. The plan changed no object. The only effect was a refresh of the records that it plans to delete.

## Point each record at its address

`$from` on a resource names the address its record has now. At the next plan the record moves to the resource. The field belongs in the specification, so a reviewer can read it.

Put every `$from` in one file. pravo unifies `moves.cue` with the declarations in the other files, and the migration adds one file to the repository. A loop in the specification gets a loop in `moves.cue`.

```cue file=moves.cue title="moves.cue"
package estate

import "list"

banner: $from: "data.local_file.banner"
server: $from: "random_pet.server"
hello:  $from: "local_file.hello"

ports: [for i in list.Range(0, 2, 1) {$from: "random_integer.port_\(i)"}]

region: {
	for r in regions {
		"\(r)": $from: "random_pet.region_\(r)"
	}
}

notes: files: {
	for n in notes.names {
		"\(n)": $from: "local_file.notes_note_\(n)"
	}
}
```

Take the old names from the `Renamed instances` list and from the table above. A resource without a rename keeps its OpenTofu address: `server` names `random_pet.server`.

The same move written on the resource itself reads like this. Use one form for each resource.

```cue title="resources.cue, the move written on the resource"
server: $random.#Resource.random_pet & {
	$from:  "random_pet.server"
	prefix: _prefix
	length: 2
}
```

`pravo state mv random_pet.server server` does the same once, without a declaration. It does not check the new address against the specification. Use `$from` for a migration, because the next environment needs the same change.

```sh run
pravo plan --policy policy/no-destroy.cue
```

```text out
pravo will perform the following actions:

  # data.local_file.banner was moved to banner (record renamed; no change to the object)

  # random_pet.server was moved to server (record renamed; no change to the object)

  # local_file.hello was moved to hello (record renamed; no change to the object)

  # random_integer.port_0 was moved to ports[0] (record renamed; no change to the object)

  # random_integer.port_1 was moved to ports[1] (record renamed; no change to the object)

  # random_pet.region_eu was moved to region.eu (record renamed; no change to the object)

  # random_pet.region_us was moved to region.us (record renamed; no change to the object)

  # local_file.notes_note_alpha was moved to notes.files.alpha (record renamed; no change to the object)

  # local_file.notes_note_beta was moved to notes.files.beta (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 9 of 9, 9 refreshed, 9 moved.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
Policy policy/no-destroy.cue (sha256:5fa3c22880d1) is satisfied.
```

Read the plan. Each line says that a record was renamed and that the object did not change. The summary adds nothing and destroys nothing. The `Policy` line confirms that the stop rule held.

The `Graph:` line says `planned 9 of 9`. Imported records carry no configuration hash yet, so this plan asks the provider about every one of them, once.

A plan carries out the moves: it renames the records in the pravo state. They sit at their new addresses now:

```sh run
pravo state list
```

```text out
banner
hello
notes.files.alpha
notes.files.beta
ports[0]
ports[1]
region.eu
region.us
server
```

Plan again. This plan has nothing left to do.

```sh run
pravo plan --detailed-exitcode --policy policy/no-destroy.cue
echo "exit=$?"
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 9 of 9, 9 refreshed.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
Policy policy/no-destroy.cue (sha256:5fa3c22880d1) is satisfied.
exit=0
```

With `--detailed-exitcode` the exit code is 0 for an empty plan and 2 when the plan has changes. A CI job uses it as the gate. Moves count as no change.

This plan read all nine objects, as `9 refreshed` says. After the first apply the records carry hashes, and a plan reads only the records it looks at. A gate that must read every object runs `pravo plan -refresh=all --detailed-exitcode`.

## When the plan is not empty

Read the first non-empty plan from the top. The line that differs from the object names the cause.

| Symptom | Cause | Fix |
|---|---|---|
| `# random_pet.server (random_pet) will be destroyed` and `# server (random_pet) will be created` | The resource has no `$from`, or `$from` names an address that is not in the state. | Add `$from` with the name from `Renamed instances`. |
| One resource is created and one destroyed, and the rest moved. | `$from` uses the OpenTofu spelling `random_pet.region["eu"]`. The record is `random_pet.region_eu`. | Write the name that the import printed. |
| `Error: $from local_file.hello -> server: the record is a managed local_file but server is declared as a managed random_pet; the type must stay the same` | `$from` names a record of another type. | Correct the address. |
| `Error: $from random_integer.port_0 -> ports[0]: both addresses exist in state; remove one with "pravo state rm"` | Both records exist, for example after `pravo import` of an object that the converted state holds. | Keep the converted record. Run `pravo state rm 'ports[0]'`, then plan again. |
| `# server (random_pet) must be replaced` with `~ length: 2 -> 3 # forces replacement` | The translated value differs from the object. | Set the specification to the value that `pravo state show server` prints. |
| A change on a resource that you did not touch, such as a new default. | The provider is newer than the one OpenTofu applied with. | Pin `$version` to the version in `.terraform.lock.hcl`, run `pravo init`, and plan again. |
| A progress line says that `hello` does not exist and will be created again, and the plan has `# hello (local_file) will be created`. | The provider cannot find the object. The file is missing, the working directory is another one, or the provider reads another region, account or endpoint. | Run from the directory OpenTofu used. Give the provider the same configuration and credentials. |
| `Graph: planned 9 of 9` and no change | Imported records have no hash yet. | None. The first apply writes it. |

Do not apply a plan that matches any row except the last. Apply only after the fix in the table has produced an empty plan. The deep dive below runs five of them.

:::deep-dive Reproduce each symptom
Each trial copies the project to a directory, edits the copy and plans in it. The copy has its own `pravo.state`, so the project is not touched.

A resource without `$from`: the broken plan shown earlier. The next trial writes the OpenTofu spelling of a key.

```sh run
rm -rf t1 && mkdir t1 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t1/
cat > t1/moves.cue <<'CUE'
package estate

banner: $from: "data.local_file.banner"
server: $from: "random_pet.server"
hello:  $from: "local_file.hello"
region: eu: $from: "random_pet.region[\"eu\"]"
region: us: $from: "random_pet.region_us"
ports: [{$from: "random_integer.port_0"}, {$from: "random_integer.port_1"}]
notes: files: alpha: $from: "local_file.notes_note_alpha"
notes: files: beta: $from: "local_file.notes_note_beta"
CUE
pravo -C t1 state import-tfstate terraform.tfstate > /dev/null
pravo -C t1 plan 2>&1 | grep -E '^(  # .* will be|Plan:)'
```

```text out
  # region.eu (random_pet) will be created
  # random_pet.region_eu (random_pet) will be destroyed
Plan: 1 to add, 0 to change, 1 to destroy.
```

A `$from` that names a record of another type:

```sh run
rm -rf t2 && mkdir t2 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t2/
printf 'package estate\n\nserver: $from: "local_file.hello"\n' > t2/moves.cue
pravo -C t2 state import-tfstate terraform.tfstate > /dev/null
pravo -C t2 plan 2>&1 | grep '^Error'
```

```text out
Error: $from local_file.hello -> server: the record is a managed local_file but server is declared as a managed random_pet; the type must stay the same
```

A value in the specification that differs from the object:

```sh run
rm -rf t3 && mkdir t3 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t3/
cp moves.cue t3/ 2>/dev/null || true
sed 's/length: 2/length: 3/' t3/resources.cue > t3/resources.new && mv t3/resources.new t3/resources.cue
pravo -C t3 state import-tfstate terraform.tfstate > /dev/null
pravo -C t3 plan 2>&1 | grep -E '(must be replaced|forces replacement)'
```

```text out
  # server (random_pet) must be replaced
  ~     length:    2 -> 3 # forces replacement
  # hello (local_file) must be replaced
  ~     content:              "estate\nHello, acme-dev-charmed-buzzard!\n" -> (known after apply) # forces replacement
```

An object that the provider cannot find:

```sh run
rm -rf t4 && mkdir t4 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t4/
rm t4/out/hello.txt
pravo -C t4 state import-tfstate terraform.tfstate > /dev/null
pravo -C t4 plan 2>&1 | grep -E '(no longer exists|will be created)'
```

```text out
hello: no longer exists; it will be created again
  # hello (local_file) will be created
```

Both records exist, because the object was also imported by hand:

```sh run
rm -rf t5 && mkdir t5 && cp -R cue.mod banner.txt out terraform.tfstate *.cue t5/
pravo -C t5 state import-tfstate terraform.tfstate > /dev/null
pravo -C t5 import 'ports[0]' 8111,8000,8999 > /dev/null
pravo -C t5 plan 2>&1 | grep '^Error'
pravo -C t5 state rm 'ports[0]'
pravo -C t5 plan 2>&1 | grep '^Plan'
```

```text out
Error: $from random_integer.port_0 -> ports[0]: both addresses exist in state; remove one with "pravo state rm"
Removed ports[0]
Plan: 0 to add, 0 to change, 0 to destroy.
```
:::


## The first apply

An apply of an empty plan changes no object. It finalises the records: it writes the configuration hash, the provider version and the instance key of every record. The records of the converted state show the difference.

```sh run
pravo state show hello | head -n 3
pravo output
```

```text out
// hello (provider registry.opentofu.org/hashicorp/local as local, schema version 0, revision 6)
// refreshed <age> ago, applied never, config hash none
// depends on banner, ports[0], ports[1], server
note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
port_numbers: (sensitive)
server_name: "acme-dev-charmed-buzzard"
```

The import copies the output values and their `sensitive` mark. `pravo output` lists `port_numbers` as `(sensitive)`.

```sh run
pravo apply
pravo state show hello | head -n 3
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 9 of 9, 9 refreshed.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
// hello (provider registry.opentofu.org/hashicorp/local v<version> as local.$local, schema version 0, revision 7)
// refreshed <age> ago, applied never, config hash <hash>
// depends on banner, ports[0], ports[1], server
```

`config hash none` became a hash, and the provider version and the instance key `local.$local` appeared. `applied never` stays, because pravo created and changed nothing. The apply left the objects and the OpenTofu state as they were, so `tofu plan -detailed-exitcode` on the root still exits 0 in OpenTofu 1.13.1. The next plan skips every record whose hash and provider version match, so it is faster.

The first apply records the outputs again, from the specification. An output marked `sensitive` there stays hidden in the list.

## A first real change

The empty plan proved that pravo can read the objects. A small change proves that it can write them. Add a third region. The only edit is the default of `regions`.

```cue file=variables.cue title="variables.cue" hl="4"
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us", "ap"]
```

Save the plan to a file and read it before you apply.

```sh run
pravo plan -out change.plan --policy policy/no-destroy.cue
```

```text out
pravo will perform the following actions:

  # region.ap (random_pet) will be created
  + region: ap: {
  +     id:        (known after apply)
  +     length:    2
  +     prefix:    "ap"
  +     separator: "-"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 10, 8 unchanged and skipped, 1 refreshed, 8 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
Policy policy/no-destroy.cue (sha256:5fa3c22880d1) is satisfied.

Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

The plan creates `region.ap` and touches no other resource. Applying the saved plan performs exactly these actions.

```sh run
pravo apply change.plan
pravo state list
```

```text out
region.ap: Creating...
region.ap: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
banner
hello
notes.files.alpha
notes.files.beta
ports[0]
ports[1]
region.ap
region.eu
region.us
server
```

The `$from` fields have done their work. Delete `moves.cue` once every environment has applied. The plan after the deletion is still empty.

```sh run
rm moves.cue
pravo plan
```

```text out
No changes. The specification matches the state. 9 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 1 of 10, 9 unchanged and skipped, 1 refreshed, 9 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-charmed-buzzard"
```

## Recap

- `pravo state import-tfstate` converts the state once. Each instance is a record, renamed when it has a key or a module path.
- `$from` points a record at its resource. Plan until the plan is empty, and refuse any plan that deletes or replaces.
- The first apply of an empty plan finalises the records. A small saved plan proves that pravo can write.

:::check
Why does the first plan after the import say `planned 9 of 9`?
::answer
Imported records have no configuration hash. pravo cannot tell that nothing changed, so it asks the provider about each record once.

What does the exit code of `pravo plan --detailed-exitcode` tell a CI job?
::answer
0 means the plan found nothing to do among the records it read. 2 means it has changes. A moved record is not a change.
:::

Next: [M6 Rewrite the checks on plans](m6-rewrite-plan-checks.md) turns the CI checks that read the OpenTofu plan JSON into plan policies.
