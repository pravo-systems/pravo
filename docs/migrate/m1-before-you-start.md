---
title: Before you start
description: Make the OpenTofu plan empty, back up the state and the code, scan for constructs that need a decision, preview the conversion and write the recovery plan.
order: 1
group: Runbook
gate: The OpenTofu plan is empty
minutes: 25
needs: A working OpenTofu or Terraform project and a pravo installation.
learn: What must be true before the first pravo command, and which constructs in your project need a decision?
done_when: "`tofu plan -detailed-exitcode` exits 0, the backups are checked, the scan is read line by line, and the recovery plan is written."
level: intermediate
---

Nothing on this page changes your infrastructure or your OpenTofu state. The one exception is the apply in task 1, which OpenTofu runs. Do the five tasks for each root, in order.

The page uses a sample project, so that the output below is real. In your project, run the same commands in the root directory. Work in an empty directory if you follow the sample.

The OpenTofu output on this page comes from OpenTofu 1.13.1, run once on a sample root called `platform`. The page does not run OpenTofu. Every pravo command on this page is run.

## Task 1: make the old plan empty

pravo compares your specification with the objects that exist. An unapplied OpenTofu change shows up in the first pravo plan as a change you did not make. When the old plan is empty, every later difference comes from the translation.

```sh
tofu plan -detailed-exitcode
```

Exit code 0 means no changes. Exit code 2 means changes are pending. Exit code 1 means an error.

The plan counts more than edits to the HCL. A `moved` block that no apply has carried out is a change. A tainted instance is a replacement. A deposed object, left over from a replacement that failed halfway, is a destroy. The sample root `platform` has the last two:

```text title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 2"
...
OpenTofu will perform the following actions:

  # local_file.web[1] (deposed object 6d47a1a2) will be destroyed
  # (left over from a partially-failed replacement of this instance)
...
  # null_resource.gate is tainted, so it must be replaced
...
  # random_pet.logs is tainted, so it must be replaced
...
Plan: 2 to add, 0 to change, 3 to destroy.
```

Run `tofu apply` until the plan exits 0. The apply replaces each tainted instance and destroys each deposed object. Then pause the pipeline that applies this root. Merge nothing into it until the migration ends.

```text title="tofu plan -detailed-exitcode after the apply, OpenTofu 1.13.1, exit code 0"
...
No changes. Your infrastructure matches the configuration.
```

## Task 2: back up the state and the code

Copy the state first. The copy is what you restore from.

Local state keeps the file next to the code. OpenTofu also writes `terraform.tfstate.backup`, the state as it was before the last write, from the second write on. A root that has been applied once has no backup file.

```sh
mkdir -p backup
cp -p terraform.tfstate backup/
cp -p terraform.tfstate.backup backup/
cksum backup/terraform.tfstate terraform.tfstate
```

The `cp` of the backup file fails when the file does not exist. The two `cksum` lines must match.

Remote state needs a pull. Enable object versioning on the bucket as well, so that every later write keeps the previous version:

```sh
mkdir -p backup
tofu state pull > backup/platform.tfstate
aws s3api put-bucket-versioning --bucket acme-tfstate --versioning-configuration Status=Enabled
aws s3api get-bucket-versioning --bucket acme-tfstate
```

A root can have more than one state. Run `tofu workspace list`. Each workspace has a state of its own, so back up each one: select the workspace, pull its state to a file named after it, and repeat. A local root keeps the state of every workspace except `default` in `terraform.tfstate.d/NAME/terraform.tfstate`.

Other backends need the same two things: a pulled copy, and the backend's own way to keep the previous version of the object. `tofu state pull` works for every backend.

Work on a branch, and commit the code before you add a CUE file:

```sh
git switch -c migrate-platform
```

The pravo state is a directory of plain files. It holds the attributes of every resource, including the ones a provider marks sensitive. Keep it out of version control before pravo creates it:

```sh
printf 'pravo.state/\n' >> .gitignore
```

The pravo state path is a directory. The default is `pravo.state`, next to the specification. A state path that names a file, such as the `terraform.tfstate` that OpenTofu uses, is an error, and pravo changes nothing. See [Pitfalls](m9-pitfalls.md#a-state-path-that-is-a-file).

## Task 3: scan for constructs that need a decision

Some constructs have no one-line translation. Some have none at all. The script below counts them. Each row is one `grep`. A count of 0 needs no decision.

First, the sample project.

```hcl file=main.tf
data "terraform_remote_state" "net" {
  backend = "s3"

  config = {
    bucket = "acme-tfstate"
    key    = "net/terraform.tfstate"
  }
}

resource "aws_instance" "web" {
  count         = 2
  ami           = "ami-0abc1234"
  instance_type = "t3.micro"

  provisioner "local-exec" {
    command = "echo ${self.id} >> hosts.txt"
  }
}

moved {
  from = aws_instance.server
  to   = aws_instance.web
}

variable "db_password" {
  type      = string
  sensitive = true
}
```

```sh file=ci/plan.sh
export TF_VAR_db_password="$DB_PASSWORD"
tofu plan -out=change.plan -target=module.db
tofu show -json change.plan > plan.json
conftest test plan.json
```

Then the script. Save it as `preflight.sh` in the root of your project.

```sh file=preflight.sh
#!/bin/sh
# Count the constructs that need a decision before you translate.
# Run it in the root of an OpenTofu project.
count() {
	case $1 in
	tf)  grep -rEn --include='*.tf' --exclude-dir=.terraform -e "$2" . ;;
	all) grep -rEn --exclude-dir=.terraform --exclude-dir=.git --exclude=preflight.sh -e "$2" . ;;
	esac | wc -l | tr -d ' '
}
row() { printf '%-24s %s\n' "$1" "$(count "$2" "$3")"; }

row provisioners       tf  '^[[:space:]]*provisioner[[:space:]]+"'
row workspaces         tf  'terraform\.workspace'
row target-flags       all '-target[ =]'
row remote-state       tf  'terraform_remote_state'
row check-blocks       tf  '^[[:space:]]*check[[:space:]]+"'
row lifecycle-checks   tf  '(precondition|postcondition|replace_triggered_by)'
row moved-blocks       tf  '^[[:space:]]*moved[[:space:]]*\{'
row import-blocks      tf  '^[[:space:]]*import[[:space:]]*\{'
row dynamic-blocks     tf  '^[[:space:]]*dynamic[[:space:]]+"'
row count              tf  '^[[:space:]]*count[[:space:]]*='
row provider-passing   tf  '^[[:space:]]*providers[[:space:]]*='
row sensitive-vars     tf  'sensitive[[:space:]]*=[[:space:]]*true'
row tf-var-env         all 'TF_VAR_'
row plan-json-checks   all 'show[[:space:]]+-json|conftest|opa[[:space:]]+(eval|exec)'
```

```sh run
sh preflight.sh
```

```text out
provisioners             1
workspaces               0
target-flags             1
remote-state             1
check-blocks             0
lifecycle-checks         0
moved-blocks             1
import-blocks            0
dynamic-blocks           0
count                    1
provider-passing         0
sensitive-vars           1
tf-var-env               1
plan-json-checks         2
```

Every row above 0 is a decision. The table says what each construct becomes and which page explains it.

| Script row | In pravo | Read |
|---|---|---|
| `provisioners` | No equivalent. A provider resource or `user_data` does the work. | [M10](m10-not-in-pravo.md) |
| `workspaces` | No equivalent. A tag selects the environment and the state path follows it. | [M10](m10-not-in-pravo.md), [environments](../how-to/environments.md) |
| `target-flags` | No equivalent. A plan reaches only what changed. `pravo apply PLANFILE --only ADDR` applies part of a saved plan. | [M10](m10-not-in-pravo.md), [plans](../concepts/plans.md) |
| `remote-state` | `pravo.#RemoteOutputs`. The conversion skips the data source record and prints a warning. | [M3](m3-worked-example.md), [environment values](../reference/spec-env.md) |
| `check-blocks` | No equivalent. A constraint fails evaluation. A plan policy refuses a plan. | [M10](m10-not-in-pravo.md) |
| `lifecycle-checks` | No equivalent. `$lifecycle` takes `create_before_destroy`, `prevent_destroy`, `ignore_changes` and `refresh`. | [M10](m10-not-in-pravo.md) |
| `moved-blocks`, `import-blocks` | `$from` and `$import` on the resource, or `pravo import`. | [M5](m5-convert-the-state.md), [importing and moving](../how-to/importing-and-moving.md) |
| `dynamic-blocks` | A comprehension in the body of the resource. | [M4](m4-translate-the-specification.md) |
| `count` | A comprehension. Addresses change from `[0]` to `_0` in the converted state. | [M4](m4-translate-the-specification.md), [M5](m5-convert-the-state.md) |
| `provider-passing` | A module input of type `#Provider`. | [M4](m4-translate-the-specification.md), [modules](../reference/spec-modules.md) |
| `sensitive-vars`, `tf-var-env` | `pravo.#SecretEnv` for a secret and `pravo.#Env` for the rest. | [M4](m4-translate-the-specification.md), [secrets](../how-to/secrets.md) |
| `plan-json-checks` | A plan policy, a CUE file named with `--policy`. | [M6](m6-rewrite-plan-checks.md), [policies](../how-to/policies.md) |

The `workspaces` row counts the places where the code reads the workspace name. It cannot see a workspace that a pipeline selects with `tofu workspace select`. The `tofu workspace list` of task 2 does. A root with workspaces converts each state into its own pravo state, as [M5](m5-convert-the-state.md#a-root-with-workspaces) shows.

Three more checks need more than `grep`. First, every provider must exist on the OpenTofu registry. The lock file lists them. This loop asks the registry about each one:

```sh
grep -h '^provider ' .terraform.lock.hcl | sed 's/provider "\(.*\)" {/\1/' | while read -r p; do
  ns_type=${p#*/}
  curl -fsS -o /dev/null "https://registry.opentofu.org/v1/providers/$ns_type/versions" && echo "found   $ns_type" || echo "missing $ns_type"
done
```

On the `platform` root, OpenTofu 1.13.1 wrote a lock file with two providers, and the registry has both:

```text title="the loop on platform, OpenTofu 1.13.1 and the OpenTofu registry"
found   hashicorp/local
found   hashicorp/random
```

A provider that reports `missing` cannot be installed from the OpenTofu registry. See [providers from a registry](../how-to/providers-from-a-registry.md).

Second, note the version of every provider. `.terraform.lock.hcl` records the version that OpenTofu applied with. pravo chooses a version from the constraint you write, so a looser constraint can pick a newer provider and plan changes that the translation did not cause. [M4](m4-translate-the-specification.md#step-1-providers) pins the versions.

```sh
grep -A1 '^provider ' .terraform.lock.hcl
```

```text title="the lock file of platform, OpenTofu 1.13.1"
provider "registry.opentofu.org/hashicorp/local" {
  version     = "2.9.1"
--
provider "registry.opentofu.org/hashicorp/random" {
  version     = "3.9.1"
```

Third, taint, deposed objects and renamed instances live in the state, not in the code. Task 4 finds them.

## Task 4: preview the conversion on a copy

`pravo state import-tfstate` prints a warning for every tainted instance, every deposed object and every record it skips. It lists every instance it renames. Run it on the backup, in a throwaway directory, before you write any specification. The run also proves that the backup is a readable state file.

The sample state is `backup/platform.tfstate`, the state of the `platform` root above. OpenTofu 1.13.1 wrote it, and the page does not print it. It was copied before the apply of task 1, so that it still holds a deposed object and two tainted instances. After task 1, those warnings do not appear. The file also holds a counted resource, a module and a `terraform_remote_state` data source.

```json file=backup/platform.tfstate hidden
{
  "version": 4,
  "terraform_version": "1.13.1",
  "serial": 3,
  "lineage": "791dc012-9f7c-7282-2d70-326f318ea913",
  "outputs": {},
  "resources": [
    {
      "mode": "data",
      "type": "terraform_remote_state",
      "name": "net",
      "provider": "provider[\"terraform.io/builtin/terraform\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "backend": "local",
            "config": {
              "value": {
                "path": "../net/terraform.tfstate"
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
                "vpc": "vpc-quick-stingray"
              },
              "type": [
                "object",
                {
                  "vpc": "string"
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
      "name": "web",
      "provider": "provider[\"registry.opentofu.org/hashicorp/local\"]",
      "instances": [
        {
          "index_key": 0,
          "schema_version": 0,
          "attributes": {
            "content": "web 0\n",
            "content_base64": null,
            "content_base64sha256": "2Iv0pVV3XhnqPmjMYPRRwbTa1gCEbYiRlGcwTZbCHMo=",
            "content_base64sha512": "fdckqG3JPY5DQL6InZDMLuj/92R7aIIrVLGt8GVYzPyQrsq9Sp/P+f14qNPU/6U4AAhUvADscCzwZhcmaTHBwQ==",
            "content_md5": "d46ad41703dd13f4d2e4c22b0ce5831f",
            "content_sha1": "57415a3c8fdc1eff74651f6f5e7b686fee43ec65",
            "content_sha256": "d88bf4a555775e19ea3e68cc60f451c1b4dad600846d88919467304d96c21cca",
            "content_sha512": "7dd724a86dc93d8e4340be889d90cc2ee8fff7647b68822b54b1adf06558ccfc90aecabd4a9fcff9fd78a8d3d4ffa538000854bc00ec702cf06617266931c1c1",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/web0-1.txt",
            "id": "57415a3c8fdc1eff74651f6f5e7b686fee43ec65",
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
          "create_before_destroy": true
        },
        {
          "index_key": 1,
          "schema_version": 0,
          "attributes": {
            "content": "web 1\n",
            "content_base64": null,
            "content_base64sha256": "OC5+eWPSpOJiDCUeih4h8of79H80KNf05MaB0vP9Bz8=",
            "content_base64sha512": "mris9VuBFyzh4f69pLlOZfrlFVC4LQI6ZLt4dAKFysCIwGeqIhEUirQZxopR69M8K2tjKQ7TXZejzeCOCPvxwQ==",
            "content_md5": "186c9637117f3fd5b9b1297dc8da0a55",
            "content_sha1": "68f1cd2e0b1d04d5cf751daf0b84253f90249fe5",
            "content_sha256": "382e7e7963d2a4e2620c251e8a1e21f287fbf47f3428d7f4e4c681d2f3fd073f",
            "content_sha512": "9ab8acf55b81172ce1e1febda4b94e65fae51550b82d023a64bb78740285cac088c067aa2211148ab419c68a51ebd33c2b6b63290ed35d97a3cde08e08fbf1c1",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/web1-2.txt",
            "id": "68f1cd2e0b1d04d5cf751daf0b84253f90249fe5",
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
          "create_before_destroy": true
        },
        {
          "index_key": 1,
          "deposed": "6d47a1a2",
          "schema_version": 0,
          "attributes": {
            "content": "web 1\n",
            "content_base64": null,
            "content_base64sha256": "OC5+eWPSpOJiDCUeih4h8of79H80KNf05MaB0vP9Bz8=",
            "content_base64sha512": "mris9VuBFyzh4f69pLlOZfrlFVC4LQI6ZLt4dAKFysCIwGeqIhEUirQZxopR69M8K2tjKQ7TXZejzeCOCPvxwQ==",
            "content_md5": "186c9637117f3fd5b9b1297dc8da0a55",
            "content_sha1": "68f1cd2e0b1d04d5cf751daf0b84253f90249fe5",
            "content_sha256": "382e7e7963d2a4e2620c251e8a1e21f287fbf47f3428d7f4e4c681d2f3fd073f",
            "content_sha512": "9ab8acf55b81172ce1e1febda4b94e65fae51550b82d023a64bb78740285cac088c067aa2211148ab419c68a51ebd33c2b6b63290ed35d97a3cde08e08fbf1c1",
            "directory_permission": "0777",
            "file_permission": "0777",
            "filename": "out/web1-1.txt",
            "id": "68f1cd2e0b1d04d5cf751daf0b84253f90249fe5",
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
          "create_before_destroy": true
        }
      ]
    },
    {
      "mode": "managed",
      "type": "null_resource",
      "name": "gate",
      "provider": "provider[\"registry.opentofu.org/hashicorp/null\"]",
      "instances": [
        {
          "status": "tainted",
          "schema_version": 0,
          "attributes": {
            "id": "4294265792588050385",
            "triggers": {
              "web": "57415a3c8fdc1eff74651f6f5e7b686fee43ec65,68f1cd2e0b1d04d5cf751daf0b84253f90249fe5"
            }
          },
          "sensitive_attributes": [],
          "dependencies": [
            "local_file.web"
          ]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "random_pet",
      "name": "logs",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "status": "tainted",
          "schema_version": 0,
          "attributes": {
            "id": "logs-powerful-ringtail",
            "keepers": null,
            "length": 2,
            "prefix": "logs",
            "separator": "-"
          },
          "sensitive_attributes": []
        }
      ]
    },
    {
      "module": "module.db",
      "mode": "managed",
      "type": "random_pet",
      "name": "main",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "db-positive-glowworm",
            "keepers": null,
            "length": 2,
            "prefix": "db",
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

```sh run
mkdir preview
printf 'package preview\n' > preview/main.cue
pravo -C preview init
pravo -C preview state import-tfstate ../backup/platform.tfstate
pravo -C preview state list
```

```text out
Providers:
Created cue.mod/module.cue

pravo has been initialised. Run "pravo plan" to see what it would do.
Imported 5 resources and 0 outputs from ../backup/platform.tfstate into ./preview/pravo.state
Warning: data.terraform_remote_state.net: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)
Warning: local_file.web[1]: deposed object 6d47a1a2 skipped (pravo keeps one object per resource; destroy it with tofu first)
Warning: null_resource.gate: tainted in the source state; pravo has no taint, the object is recorded as healthy
Warning: random_pet.logs: tainted in the source state; pravo has no taint, the object is recorded as healthy

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  local_file.web[0] -> local_file.web_0
  local_file.web[1] -> local_file.web_1
  module.db.random_pet.main -> random_pet.db_main
local_file.web_0
local_file.web_1
null_resource.gate
random_pet.db_main
random_pet.logs
```

Read the output as a worklist:

- A warning about a tainted instance or a deposed object means that task 1 is not finished. `tofu plan` still replaces the first and destroys the second. Run `tofu apply`, take the backup again, and run the preview again. A tainted instance that you convert is recorded as healthy, and `pravo plan --replace ADDR` replaces it.
- A record of the built-in provider is skipped. The warning names the replacement. For `terraform_remote_state` it is `pravo.#RemoteOutputs`.
- Each line under `Renamed instances` is a `$from` that you write in [M4](m4-translate-the-specification.md) and [M5](m5-convert-the-state.md).

The conversion stops with an error when the file is not in state format 4. It stops on a state with legacy flat attributes, and on two instances that would get one name. All three errors belong here, not in the middle of the migration.

Delete the throwaway directory.

```sh run
rm -r preview
```

The preview writes only inside `preview`. Your state file and your objects are as they were.

## Task 5: write the recovery plan

Write one paragraph per root before you run any other pravo command. Put it where the whole team can read it. This is a template:

```text
Recovery plan: root platform
Written: <date>, before any pravo command.
Owner: <name>. Window: <start> to <end>.

Backups
  State:  backup/platform.tfstate, cksum <number>
  Code:   branch migrate-platform at commit <hash>
  Bucket: versioning enabled on acme-tfstate
  Kept:   until <date>, then deleted

Before the first apply that changes an object
  Stop:   a plan with a create, a change, a replacement or a destroy.
  Undo:   delete pravo.state, pravo.lock.cue, cue.mod and the .cue files.
  Check:  tofu plan -detailed-exitcode exits 0.

After it
  Follow M8. Restore backup/platform.tfstate, then tofu plan exits 0.
```

The undo step is complete because pravo writes nothing else in the root. [M3](m3-worked-example.md) lists the files and shows checksums that do not change. The rollback after the first applied change is in [M8](m8-rollback.md).

## Recap

- Make the OpenTofu plan empty first, so that every later difference is yours to explain.
- Back up the state, test the copy, and write the recovery plan before the first pravo command.
- Scan the code, preview the conversion, and read each row above 0 before you translate.

:::check
Why must the OpenTofu plan be empty before the first pravo plan?
::answer
An unapplied OpenTofu change appears in the pravo plan as a change you did not make. With an empty old plan, every difference comes from the translation.

Which command lists the tainted and deposed objects in your state without touching it?
::answer
`pravo state import-tfstate` on a copy of the state, in a throwaway directory. It prints a warning for each tainted instance, each deposed object and each skipped record.
:::

Next: the [concept map](m2-concept-map.md) lists what each construct, command and function becomes.
