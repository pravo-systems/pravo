---
title: Roll back
description: Where the migration can be undone by deleting the pravo files, and what to do after the first apply that changes an object.
order: 8
group: Runbook
gate: The first rollback is rehearsed
minutes: 20
needs: "[Convert the state](m5-convert-the-state.md) read."
learn: Until which step does a rollback mean deleting files, and what does it mean afterwards?
done_when: "You have rehearsed the first rollback, and you can name the step after which it does not apply."
level: intermediate
normalize:
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|Reading\.\.\.|Read complete|moved to )[^\n]*\n'
    replace: ""
  - pattern: "\\[id=[a-z-]+\\]"
    replace: "[id=<id>]"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "\\[txn [0-9TZ]+-[0-9a-f]+\\]"
    replace: "[txn <txn>]"
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
---

Write the rollback before you start, and rehearse it before you need it. The rollback depends on one fact: which commands have changed a real object.

## The boundary

pravo writes to its own state directory, `pravo.state`, and to the files of its own root. It reads the objects through their providers. It never writes the `.tfstate` file or the OpenTofu backend.

Until the first `pravo apply` that creates, updates, replaces or deletes an object, no object has changed. These write records and nothing else: `import-tfstate`, `import`, `drift` and the apply of a plan that changes no object, which includes the renames of `$from`. A `plan` writes nothing that you declared: it lists the moves and imports and leaves the state as it was. Deleting the pravo files undoes everything, and the OpenTofu state and the objects are what they were.

From the first apply that changes an object, the OpenTofu state is out of date. pravo does not write it, and pravo has no command that converts its state back into an OpenTofu state. Rolling back means making the OpenTofu state agree with the objects, or making the objects agree with the OpenTofu state.

:::figure The rollback depends on the first apply that changes an object.
<svg viewBox="0 0 700 200" role="img" aria-label="Steps of the migration on a line. Until the first apply that changes an object, rollback means deleting the pravo files. After it, rollback means reconciling OpenTofu with the objects." style="width:100%;height:auto;display:block;margin:0 auto">
  <g fill="none" stroke="currentColor" stroke-width="1.5">
    <path d="M30 105 H670"/>
    <circle cx="60" cy="105" r="7" fill="var(--bg)"/>
    <circle cx="170" cy="105" r="7" fill="var(--bg)"/>
    <circle cx="280" cy="105" r="7" fill="var(--bg)"/>
    <circle cx="390" cy="105" r="7" fill="var(--bg)"/>
    <circle cx="520" cy="105" r="7" fill="var(--bg)"/>
    <circle cx="640" cy="105" r="7" fill="var(--bg)"/>
    <path d="M455 30 V185" stroke-dasharray="5 4"/>
    <path d="M30 72 H435" opacity="0.6"/>
    <path d="M475 72 H670" opacity="0.6"/>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace" font-size="15" text-anchor="middle">
    <text x="60" y="138">translate</text>
    <text x="170" y="138">convert</text>
    <text x="280" y="138">plan</text><text x="280" y="157">empty</text>
    <text x="390" y="138">empty</text><text x="390" y="157">apply</text>
    <text x="520" y="138">changing</text><text x="520" y="157">apply</text>
    <text x="640" y="138">cut</text><text x="640" y="157">over</text>
    <text x="232" y="60">delete the pravo files</text>
    <text x="572" y="60">reconcile OpenTofu</text>
    <text x="455" y="20" opacity="0.7">first apply that changes an object</text>
  </g>
</svg>
:::

## Rehearse the first rollback

The workspace holds an OpenTofu root that OpenTofu 1.13.1 applied. It has the state file `terraform.tfstate` that OpenTofu wrote, the objects, and the pravo files of [M4](m4-translate-the-specification.md) and [M5](m5-convert-the-state.md). The page does not run OpenTofu. Rehearse in a copy, so that nothing is lost.

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

```cue file=moves.cue hidden
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


Take a checksum of the state file and of every object. Then run the migration up to the first apply, which is empty.

```sh run
rm -rf r1 && mkdir r1 && cp -R cue.mod banner.txt out terraform.tfstate *.cue r1/
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > before.txt
pravo -C r1 state import-tfstate terraform.tfstate > /dev/null
pravo -C r1 plan > /dev/null 2>&1
pravo -C r1 apply --auto-approve 2>&1 | grep '^Apply complete'
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > after.txt
cmp before.txt after.txt && echo "unchanged"
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
unchanged
```

The state file and the objects have the same checksums after the import, a plan and an apply that renames the records. Now roll back. Remove what pravo made and nothing else:

1. Stop every pravo job for the root.
2. Delete `pravo.state`.
3. Delete the pravo files: the `.cue` files, `cue.mod` and `pravo.lock.cue`.
4. Run `tofu plan -detailed-exitcode`. It is empty, because nothing it owns has changed.

```sh run
rm -rf r1/pravo.state r1/pravo.lock.cue r1/cue.mod r1/*.cue
ls r1
cksum r1/terraform.tfstate r1/out/hello.txt r1/out/notes/alpha.txt r1/out/notes/beta.txt > rolled.txt
cmp before.txt rolled.txt && echo "unchanged"
```

```text out
banner.txt
out
terraform.tfstate
unchanged
```

What remains is the OpenTofu root with its state and its objects. OpenTofu 1.13.1 agrees:

```text title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 0 (excerpt)"
...
No changes. Your infrastructure matches the configuration.
```

The provider cache in `$PRAVO_HOME` is shared by every project and holds no state. Leave it.

## After the boundary

Add a third region and apply it, as in [M5](m5-convert-the-state.md). The apply creates a real object. Convert the state in a second copy first.

```sh run
rm -rf r2 && mkdir r2 && cp -R cue.mod banner.txt out terraform.tfstate *.cue r2/
pravo -C r2 state import-tfstate terraform.tfstate > /dev/null
pravo -C r2 plan > /dev/null 2>&1
```

```cue file=r2/variables.cue title="r2/variables.cue" hl="4"
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us", "ap"]
```

```sh run
pravo -C r2 apply --auto-approve 2>&1 | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The transaction log says what the applies changed. Each apply lists its records with the action:

```sh run
pravo -C r2 state log | grep -E '^    ([0-9]+ added|(create|update|replace|delete) )'
```

```text out
    1 added, 0 changed, 0 destroyed
    create   region.ap  r0 -> r1
```

The OpenTofu state does not know the new object:

```sh run
grep -c '"ap"' r2/terraform.tfstate
pravo -C r2 state list | grep ap
```

```text out
0
region.ap
```

The count is 0, and pravo lists `region.ap`. The `.tfstate` file is the one you imported. Neither tool can see the other's changes.

Choose how to go back.

| What pravo did | Where the object is now | Undo with pravo | Reconcile OpenTofu |
|---|---|---|---|
| Created an object | It exists, and the OpenTofu state has no record. | Remove it from the specification, plan, and apply. | `tofu import <address> <id>` when the HCL has a block for it. |
| Updated an object in place | It has the new values. | Restore the old values in the specification, plan, and apply. | `tofu apply -refresh-only`, then edit the HCL to the new values. |
| Replaced an object | It has a new id. The old object is destroyed. | Not possible. | `tofu state rm <address>`, then `tofu import <address> <new id>`. |
| Deleted an object | It is gone. | Not possible. | `tofu apply` creates a new object. The data of the old one is lost. |

The id for `tofu import` is in `pravo state show <address>`. The import id of a type is in the provider's documentation. For many types it equals the `id` attribute. The `tofu` commands are not run by this page.

A provider may not implement import for a type. The `random_pet` of this sample is one. With `ap` added to the HCL, OpenTofu 1.13.1 plans to create `random_pet.region["ap"]`, and the import stops:

```text title="tofu import of random_pet.region ap, OpenTofu 1.13.1, exit code 1"
...
Error: Resource Import Not Implemented

This resource does not support import. Please contact the provider developer
for additional information.
```

For such a type, undo the change with pravo, as below.

Work through the changes in this order:

1. Stop the pravo pipeline and the OpenTofu jobs for the root.
2. List what the applies changed: `pravo state log`, as above.
3. Choose a row of the table for each object.
4. Undo with pravo, or reconcile OpenTofu. Do not mix the two for one object.
5. Run `tofu plan` and `pravo plan`. Both are empty.
6. Delete the pravo files, if the rollback is complete.

Undo a created object with pravo, so that the objects match the OpenTofu state again. Restore the old default of `regions`:

```cue file=r2/variables.cue title="r2/variables.cue"
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us"]
```

```sh run
pravo -C r2 plan --out undo.plan 2>&1 | grep -E '^(  # |Plan:)'
pravo -C r2 apply undo.plan 2>&1 | grep -E '^(Apply complete)'
pravo -C r2 plan --detailed-exitcode > /dev/null 2>&1
echo "exit=$?"
```

```text out
  # region.ap (random_pet) will be destroyed
Plan: 0 to add, 0 to change, 1 to destroy.
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
exit=0
```

The plan deletes the object that pravo made, and the next plan is empty. The objects are again the ones the OpenTofu state describes, so you can delete the pravo files as before.

## After the cut over

[M7](m7-cut-over.md) removes the addresses from the OpenTofu state. The rollback then has one more step at the front.

1. Stop the pravo pipeline for the root.
2. If an apply since the cut over changed an object, reconcile as in the table above and stop here. The archived state is older than the objects.
3. If no apply changed an object, restore the archived state: `tofu state push -force archive/platform-final.tfstate`.
4. Check out the HCL from the tag `opentofu-final`.
5. Run `tofu plan`. It is empty.
6. Re-enable the OpenTofu jobs. Delete the pravo files, and the pravo state in the bucket if M7 moved it there.

The archived file has a lower serial than the state that `tofu state rm` wrote, so a push without `-force` is refused. The page does not run these commands. The output is from OpenTofu 1.13.1 on the `platform` root of [M3](m3-worked-example.md), after the removal of [M7](m7-cut-over.md):

```text title="tofu state push archive/platform-final.tfstate, exit code 1"

Error: Failed to write the imported state

cannot import state with serial 2 over newer state with serial 9
```

`tofu state push -force` writes the archived file over the current OpenTofu state, even when its serial is lower. The state lists the seven resources again, and the plan is empty:

```text title="tofu plan -detailed-exitcode after the push -force, exit code 0 (excerpt)"
...
No changes. Your infrastructure matches the configuration.
```

## Recap

- Until the first `pravo apply` that changes an object, a rollback is deleting the pravo files. The OpenTofu state is never written.
- After it, the OpenTofu state is out of date. Undo a created or updated object with pravo, and reconcile a replaced or deleted object in OpenTofu.
- After the cut over, restore the archived state only when no apply has changed an object since.

:::check
Which pravo commands change no object?
::answer
`init`, `validate`, `plan`, `import`, `drift`, `eval`, the `state` subcommands and the apply of a plan that changes no object. `import` and `$import` only read the object.

Does pravo write an OpenTofu state?
::answer
No. pravo has no command that writes an OpenTofu state file. `tofu import` and `tofu apply -refresh-only` bring the OpenTofu state up to date.
:::

Next: [M9 Pitfalls](m9-pitfalls.md) lists the symptoms you may meet, with the messages and the fixes.
