---
title: Large estate scenario
description: An estate of 2000 files, one value changed. The plan, and the number of resources that OpenTofu and pravo each read from their providers, with the default settings of both. The plans band of the landing page shows these counts.
capture: true
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]+\]'
    replace: '[txn <id>]'
---

This page is not published. `tools/capture` runs it, and `site/data/scale.json` records its output for the plans band of the landing page. The estate is 2000 `local_file` resources and one more that carries a version. The same estate is written for pravo and for OpenTofu, applied, and then one value changes. pravo runs with its default settings: the specification sets no refresh age.

```cue file=main.cue
package estate

import (
	"list"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

version: *"1" | string @tag(version)

files: {for i in list.Range(0, 2000, 1) {
	"f\(i)": $local.#Resource.local_file & {filename: "out/f\(i).txt", content: "item \(i)\n"}
}}

release: $local.#Resource.local_file & {filename: "out/release.txt", content: "version \(version)\n"}
```

```hcl file=tf/main.tf
terraform {
  required_providers {
    local = { source = "hashicorp/local", version = "2.9.1" }
  }
}

variable "version_tag" { default = "1" }

resource "local_file" "files" {
  count    = 2000
  filename = "out/f${count.index}.txt"
  content  = "item ${count.index}\n"
}

resource "local_file" "release" {
  filename = "out/release.txt"
  content  = "version ${var.version_tag}\n"
}
```

```sh hidden
mkdir -p out tf/out
pravo init
pravo apply --auto-approve | tail -n 1
tofu -chdir=tf init -no-color >/dev/null
tofu -chdir=tf apply -auto-approve -no-color | tail -n 1
```

Change the version to 2 in both and plan.

```sh run id=pravo
pravo plan -t version=2 | grep -E '^(Plan|Graph)'
```

```text out
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 2000 · refreshed 1 · not read 2000
```

```sh run id=tofu
tofu -chdir=tf plan -no-color -var version_tag=2 | grep -E '^Plan'
```

```text out
Plan: 1 to add, 0 to change, 1 to destroy.
```

pravo prints one `Refreshing state` line for each resource that it reads from a provider. It reads the one it plans.

```sh run id=pravo-reads
pravo plan -t version=2 | grep -c 'Refreshing state' || true
```

```text out
1
```

OpenTofu prints one `Refreshing state` line for each resource that it reads from a provider.

```sh run id=tofu-reads
tofu -chdir=tf plan -no-color -var version_tag=2 | grep -c 'Refreshing state'
```

```text out
2001
```

A plan that changes nothing reads nothing, and says how many records it did not read.

```sh run id=pravo-nochange
pravo plan -t version=1 | grep -E '^(No changes|Graph)'
```

```text out
No changes. The specification matches the state. 2001 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2001 · not read 2001
```

The apply of the change uses what the plan read. It locks the one resource it writes.

```sh run id=pravo-apply
pravo apply --auto-approve -v -t version=2 2>&1 | grep -E '^(Graph|Apply)|locked [0-9]+ resources' | sed 's/^+[0-9.]*s debug //'
```

```text out
Graph: planned 1 · skipped 2000 · refreshed 1 · not read 2000
locked 1 resources
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

```sh run id=tofu-apply
tofu -chdir=tf apply -auto-approve -no-color -var version_tag=2 | grep -c 'Refreshing state'
```

```text out
2001
```
