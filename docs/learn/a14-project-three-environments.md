---
title: Project, three environments from one definition
description: Write one specification from a brief. It runs in dev, staging and prod, is stricter in prod, and refuses every other environment.
order: 14
group: Track A · CUE from zero
minutes: 20
needs: A13
learn: Can one specification serve three environments, with the strict rules of prod written once and checked before anything runs?
done_when: "`pravo eval -t env=prod` prints three encrypted buckets, and `pravo eval -t env=qa` is refused."
level: intermediate
---

Track A in one specification. You get a brief, four tasks with the output each must produce, and two hints per task. The reference files come after the tasks. Write your own first.

## The brief

You write a small estate as data and constraints. There is no provider and no cloud. The estate has three buckets, three availability zones and three environments. Work in an empty directory.

Everything the specification must do is in this table.

| Rule | The specification must |
|---|---|
| R1 | Offer the environments `dev`, `staging` and `prod`. `dev` is the default. Any other value is refused. |
| R2 | Name each bucket `estate-` plus its key. A name has 3 to 63 lowercase letters, digits or hyphens. |
| R3 | Give each bucket `retention` of 1 to 365 days, `encrypted` and `versioning` (both default `false`) and string `tags`. |
| R4 | Give each zone one subnet in `10.0.n.0/24`, named after the region and the zone. Every CIDR is valid. At least one exists. |
| R5 | Make `allow` the list of subnet CIDRs as JSON text. |
| R6 | Require `staging` to keep 30 days or more. Require `prod` to keep 90 days or more and to encrypt every bucket. `dev` adds no rule. |

The platform team sends the facts. Save them as `data.cue`.

```cue file=data.cue
package estate

buckets: {
	logs:    retention: 90
	assets:  retention: 365
	backups: retention: 180
}

zones: {a: 1, b: 2, c: 3}
```

You write four more files, one per task. Each task gives the names to use and a result to compare with. Two hints come before the answer.

## Tasks

:::exercise Select the environment
Write `env.cue`. It declares the field `env` for rule R1. The command line supplies the value with `-t env=...`.

Expected result:

- `pravo eval -e env` prints `"dev"`.
- `pravo eval -t env=prod -e env` prints `"prod"`.
- `pravo eval -t env=qa` is refused.

::hint
Mark `dev` as the default with `*`. End the line with `@tag(env)`.

::hint
Read [A6](a06-choices-and-defaults.md). The whole file is the package clause and one field.

::solution
The file `env.cue` is in [Your directory now](#your-directory-now).
:::

:::exercise Shape the buckets
Write `shapes.cue` for rules R2 and R3. Define `#Bucket` with these fields:

- `name`, required. It starts with `estate-` and has 3 to 63 lowercase letters, digits or hyphens.
- `encrypted`, a bool. The default is `false`.
- `retention`, an integer from 1 to 365.
- `versioning`, a bool. The default is `false`.
- `tags`, a struct of strings.

Then apply `#Bucket` to every entry of `buckets`, and set each name to `estate-` plus the key.

Expected result: `pravo eval -e buckets.logs` prints these five fields.

```text
name:       "estate-logs"
encrypted:  false
retention:  90
versioning: false
tags: {}
```

::hint
Join the checks with `&`. Use `strings.HasPrefix` for the prefix and `=~` for the characters. A pattern `[Name=string]` applies to every entry and names the key.

::hint
Read [A5](a05-constraints-narrow-a-value.md), [A8](a08-definitions-reject-typos.md), [A9](a09-patterns.md) and [A13](a13-packages-and-the-standard-library.md).

::solution
The file `shapes.cue` is in [Your directory now](#your-directory-now).
:::

:::exercise Generate the subnets
Write `network.cue` for rules R4 and R5.

- `region` is `"eu-west-1"`.
- `#Subnet` has a required `zone` and a required `cidr`. The `cidr` satisfies `net.IPCIDR`.
- `subnets` has one entry per entry of `zones`. Its key is the region followed by the zone key, as in `eu-west-1a`. Its `zone` equals the key.
- The `cidr` of zone number n is `10.0.n.0/24`.
- `cidrs` lists the CIDRs and holds at least one.
- `allow` is `cidrs` as JSON text.

Expected result:

- `pravo eval -e subnets` prints three entries, `eu-west-1a` to `eu-west-1c`, with the CIDRs `10.0.1.0/24` to `10.0.3.0/24`.
- `pravo eval -e allow` prints `"[\"10.0.1.0/24\",\"10.0.2.0/24\",\"10.0.3.0/24\"]"`.

::hint
A comprehension builds one entry per zone. A pattern `subnets: [Name=string]: #Subnet & {zone: Name}` sets each `zone` from its key. `net.AddIPCIDR("10.0.0.0/24", n)` is the n-th `/24`.

::hint
Read [A9](a09-patterns.md), [A10](a10-comprehensions.md) and [A13](a13-packages-and-the-standard-library.md). `json.Marshal` produces the text for `allow`.

::solution
The file `network.cue` is in [Your directory now](#your-directory-now).
:::

:::exercise Make prod stricter
Write `rules.cue` for rule R6. Keep the three sets of rules in one hidden struct with a key per environment. Apply the set that `env` selects to every bucket.

Expected result: this loop prints one line per environment.

```sh
for e in dev staging prod; do echo "$e: $(pravo eval -t env=$e -e '[for b in buckets {b.encrypted}]')"; done
```

```text
dev: [false, false, false]
staging: [false, false, false]
prod: [true, true, true]
```

::hint
A hidden struct holds the rules: `_rules: {dev: ..., staging: ..., prod: ...}`. The pattern `buckets: [string]: _rules[env]` adds the chosen set to every bucket.

::hint
Read [A11](a11-references-let-hidden.md) for hidden fields, [A9](a09-patterns.md) for the pattern and [A12](a12-conditions-and-strings.md) for a lookup by name.

::solution
The file `rules.cue` is in [Your directory now](#your-directory-now).
:::

## Your directory now

Compare your files with these. Each tab is one file. `data.cue` is the one above. Open the tabs after you have tried the tasks.

:::tabs Files of the estate
```cue file=env.cue
package estate

env: *"dev" | "staging" | "prod" @tag(env)
```

```cue file=shapes.cue
package estate

import "strings"

#Bucket: {
	name!:      string & strings.HasPrefix("estate-") & =~"^[a-z0-9-]{3,63}$"
	encrypted:  *false | bool
	retention:  int & >=1 & <=365
	versioning: *false | bool
	tags: [string]: string
}

buckets: [Name=string]: #Bucket & {name: "estate-\(Name)"}
```

```cue file=network.cue
package estate

import (
	"encoding/json"
	"list"
	"net"
)

#Subnet: {
	zone!: string
	cidr!: net.IPCIDR
}

region: "eu-west-1"

subnets: [Name=string]: #Subnet & {zone: Name}

subnets: {
	for z, n in zones {
		"\(region)\(z)": cidr: net.AddIPCIDR("10.0.0.0/24", n)
	}
}

cidrs: list.MinItems(1) & [for s in subnets {s.cidr}]
allow: json.Marshal(cidrs)
```

```cue file=rules.cue
package estate

_rules: {
	dev:     {}
	staging: {retention: >=30}
	prod:    {encrypted: true, retention: >=90}
}

buckets: [string]: _rules[env]
```
:::

The rules for all three environments live in one place. The pattern in `shapes.cue` and the pattern in `rules.cue` both apply to every bucket. [Unification](../reference/glossary.md#unification) merges them, and the files may be read in any order.

## Check your work

Run the expected results of tasks 1 to 4.

```sh run
pravo eval -e env
pravo eval -t env=prod -e env
pravo eval -e buckets.logs
```

```text out
"dev"
"prod"
name:       "estate-logs"
encrypted:  false
retention:  90
versioning: false
tags: {}
```

```sh run
pravo eval -e subnets
pravo eval -e allow
```

```text out
"eu-west-1a": {
	zone: "eu-west-1a"
	cidr: "10.0.1.0/24"
}
"eu-west-1b": {
	zone: "eu-west-1b"
	cidr: "10.0.2.0/24"
}
"eu-west-1c": {
	zone: "eu-west-1c"
	cidr: "10.0.3.0/24"
}
"[\"10.0.1.0/24\",\"10.0.2.0/24\",\"10.0.3.0/24\"]"
```

```sh run
for e in dev staging prod; do echo "$e: $(pravo eval -t env=$e -e '[for b in buckets {b.encrypted}]')"; done
```

```text out
dev: [false, false, false]
staging: [false, false, false]
prod: [true, true, true]
```

Now ask for an environment that does not exist.

```sh run exit=6
pravo eval -t env=qa
```

```text out
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./env.cue:3:7
env: conflicting values "prod" and "qa":
    ./env.cue:3:27
env: conflicting values "staging" and "qa":
    ./env.cue:3:15
```

The refusal comes from `env.cue`. `-t env=qa` meets the three choices, and none matches. The refusal happens during evaluation, before any provider starts.

## Run prod

```sh run
pravo eval -t env=prod
```

```text out
buckets: {
	logs: {
		name:       "estate-logs"
		encrypted:  true
		retention:  90
		versioning: false
		tags: {}
	}
	assets: {
		name:       "estate-assets"
		encrypted:  true
		retention:  365
		versioning: false
		tags: {}
	}
	backups: {
		name:       "estate-backups"
		encrypted:  true
		retention:  180
		versioning: false
		tags: {}
	}
}
env:    "prod"
...
zones: {
	a: 1
	b: 2
	c: 3
}
```

All three buckets are encrypted. Nothing in `data.cue` says so. The rule for `prod` adds `encrypted: true` to every bucket, and the default `false` gives way.

## Break prod

:::predict Change the retention of `backups` from 180 to 30 days. Which of dev, staging and prod refuse the estate?
Write the changed facts.

```cue file=data.cue status=unintended hl="6"
package estate

buckets: {
	logs:    retention: 90
	assets:  retention: 365
	backups: retention: 30
}

zones: {a: 1, b: 2, c: 3}
```

::reveal
Only `prod`. The rule for `staging` allows 30 days. The rule for `dev` allows any value from 1 to 365.

```sh run
for e in dev staging prod; do
  if pravo eval -t env=$e >/dev/null 2>&1; then echo "$e: accepted"; else echo "$e: refused"; fi
done
```

```text out
dev: accepted
staging: accepted
prod: refused
```

:::

Read the refusal.

```sh run exit=6
pravo eval -t env=prod
```

```text out
Error: evaluating .:
buckets.backups.retention: invalid value 30 (out of bound >=90):
    ./rules.cue:6:40
    ./data.cue:6:22
```

Read the error from the top.

- The path is `buckets.backups.retention`.
- The message is `invalid value 30 (out of bound >=90)`. The bound `>=90` is the rule.
- The first location is the rule, in `rules.cue`. The second is the value, in `data.cue`.

The error names the bucket, the number and the line in each file. The fix is a decision for the platform team: raise the retention or change the rule. Raise the retention.

```cue file=data.cue hl="6"
package estate

buckets: {
	logs:    retention: 90
	assets:  retention: 365
	backups: retention: 180
}

zones: {a: 1, b: 2, c: 3}
```

```sh run
pravo eval -t env=prod -e buckets.backups.retention
```

```text out
180
```

## The same estate in HCL

This is an HCL version of the same specification. It is shown for the line count and is not run on this page.

```hcl file=main.tf title="main.tf"
variable "env" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.env)
    error_message = "env must be dev, staging or prod."
  }
}

locals {
  region = "eu-west-1"
  zones  = { a = 1, b = 2, c = 3 }

  retention = {
    logs    = 90
    assets  = 365
    backups = 180
  }

  rules = {
    dev     = { encrypted = false, min_retention = 1 }
    staging = { encrypted = false, min_retention = 30 }
    prod    = { encrypted = true, min_retention = 90 }
  }
  rule = local.rules[var.env]

  buckets = {
    for k, days in local.retention : k => {
      name       = "estate-${k}"
      encrypted  = local.rule.encrypted
      retention  = days
      versioning = false
      tags       = {}
    }
  }

  subnets = {
    for z, n in local.zones : "${local.region}${z}" => {
      zone = "${local.region}${z}"
      cidr = cidrsubnet("10.0.0.0/16", 8, n)
    }
  }

  cidrs = [for s in local.subnets : s.cidr]
}

output "buckets" {
  value = local.buckets

  precondition {
    condition     = alltrue([for b in local.buckets : can(regex("^[a-z0-9-]{3,63}$", b.name))])
    error_message = "Bucket names must be 3 to 63 lowercase letters, digits or hyphens."
  }

  precondition {
    condition     = alltrue([for b in local.buckets : b.retention >= local.rule.min_retention && b.retention <= 365])
    error_message = "Retention must be at least ${local.rule.min_retention} days and at most 365 in ${var.env}."
  }
}

output "subnets" {
  value = local.subnets

  precondition {
    condition     = length(local.cidrs) >= 1
    error_message = "At least one subnet is required."
  }
}

output "allow" {
  value = jsonencode(local.cidrs)
}
```

Count the lines of both. Blank lines and `package` clauses are included. The data is part of both counts.

```sh run
echo "HCL: $(wc -l < main.tf | tr -d ' ') lines in 1 file"
echo "CUE: $(cat *.cue | wc -l | tr -d ' ') lines in $(ls *.cue | wc -l | tr -d ' ') files"
```

```text out
HCL: 73 lines in 1 file
CUE: 59 lines in 5 files
```

The counts are close. The difference is where the rules sit. In CUE each rule is a value on the field it judges, and the same definitions apply to every bucket. In HCL the rules are conditions in `precondition` blocks, written apart from the data they check.

## What you used

| Part | Constructs | Lesson |
|---|---|---|
| `env.cue` | A choice, a default and a tag | [A6](a06-choices-and-defaults.md) |
| `#Bucket` | A definition, bounds, a match, `strings.HasPrefix` | [A5](a05-constraints-narrow-a-value.md), [A8](a08-definitions-reject-typos.md), [A13](a13-packages-and-the-standard-library.md) |
| `buckets` rule | A pattern that names the key | [A9](a09-patterns.md) |
| `subnets` | A comprehension, a pattern, `net.IPCIDR`, `net.AddIPCIDR` | [A10](a10-comprehensions.md), [A13](a13-packages-and-the-standard-library.md) |
| `_rules` | A hidden field, a lookup by key, a pattern | [A11](a11-references-let-hidden.md), [A12](a12-conditions-and-strings.md) |
| `cidrs` and `allow` | `list.MinItems`, `json.Marshal` | [A13](a13-packages-and-the-standard-library.md) |

## Recap

- Constraints from several files unify. The strict rules of `prod` sit in one file and apply to every bucket.
- A value that breaks a rule is refused. The error gives the path, the value, the rule and a location in each file.
- `-t env=...` selects the environment, and a value outside the choices is refused.

:::check
Why does `pravo eval -t env=prod` print `encrypted: true` for a bucket that never says so?
::answer
The rule for `prod` is `encrypted: true`. It unifies with the default `false`, and the default gives way to the concrete value.

Why does the error for `backups` point at two files?
::answer
The bound `>=90` is written in `rules.cue` and the value `30` in `data.cue`. Both took part in the conflict.
:::
