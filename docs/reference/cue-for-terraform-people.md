---
title: CUE for Terraform people
description: The CUE that a pravo specification uses, on one page, for a reader who knows HCL. Values, unification, constraints, definitions, references, comprehensions, and what each HCL construct becomes.
order: 19
group: Language
keywords: "hcl variable locals count for_each conditional module interpolation syntax primer crash course"
---

A pravo specification is CUE. This page is the CUE that the specification pages assume, for a reader who writes HCL and has not written CUE. Each idea has a file and the value that `pravo eval` prints for it, which evaluates CUE and needs no provider. [Track A](../learn/index.md) teaches the same ideas in lessons with exercises, and the [CUE cheat sheet](../learn/cue-cheat-sheet.md) has the syntax on one page.

## Data first

CUE is a superset of JSON. A file is a struct of fields, and the quotes on labels, the commas and the outer braces are optional. Nesting can be written on one line: `a: b: c: 1`. The files of a directory that share a package clause are one value.

```cue file=bucket.cue
package demo

bucket: {
	name:   "app-logs"
	region: "eu-west-1"
	tags: team: "platform"
	versioning: true
}
```

```sh run
pravo eval -e bucket
```

```text out
name:   "app-logs"
region: "eu-west-1"
tags: {
	team: "platform"
}
versioning: true
```

| In HCL | In CUE |
|---|---|
| `name = "app-logs"` | `name: "app-logs"` |
| `tags = { team = "platform" }` | `tags: team: "platform"` |
| `ports = [80, 443]` | `ports: [80, 443]` |
| `enabled = true` | `enabled: true` |
| `"${var.env}-db"` | `"\(env)-db"` |
| `# comment` | `// comment` |

## A field is written as often as you like

HCL assigns a value once. CUE *unifies*: a field may be written in several places, and the value is what all of them agree on. The same value twice is one value, and two different values are an error that names both. This is how a file of defaults and a file of overrides combine without a merge function, and how a rule and the data it governs sit in different files.

```cue file=env.cue
package demo

bucket: region: "eu-west-1"
bucket: tags: env: "prod"
```

```sh run
pravo eval -e bucket.tags
```

```text out
env:  "prod"
team: "platform"
```

A second value for `region` conflicts:

```cue file=other.cue status=rejected
package demo

bucket: region: "us-east-1"
```

```sh run exit=6
pravo eval -e bucket.region
```

```text out
Error: evaluating .:
bucket.region: conflicting values "us-east-1" and "eu-west-1":
    ./bucket.cue:5:10
    ./other.cue:3:17
```

```sh run hidden
rm other.cue
```

## Types and constraints are values

`string`, `int`, `bool` and `>=1` are values too, and they unify with data. A field that holds a type is not complete until data gives it a concrete value; a field whose data breaks the constraint is an error. This is what HCL's `type` and `validation` blocks do, written in the same language as the data and placed wherever a value should be checked.

```cue file=shape.cue
package demo

bucket: {
	name:       string & =~"^[a-z][a-z0-9-]*$"
	region:     "eu-west-1" | "eu-central-1"
	versioning: bool
	retention:  int & >0 | *30
}
```

```sh run
pravo eval -e bucket.retention
```

```text out
30
```

| Written | Means |
|---|---|
| `string`, `int`, `float`, `bool`, `number` | A value of that type. |
| `>0`, `<=100`, `!=""` | A bound. Several combine with `&`. |
| `=~"^[a-z]"` | Matches a regular expression. |
| `"a" \| "b"` | One of the alternatives: a disjunction. |
| `*30 \| int` | A default. The value is `30` unless data says otherwise. |
| `[...string]` | A list of strings, any length. |
| `{...}` | A struct that accepts fields it does not name. |

`pravo validate` checks that every resource is concrete and satisfies its constraints before any provider starts. [Constraints narrow a value](../learn/a05-constraints-narrow-a-value.md) and [Choices and defaults](../learn/a06-choices-and-defaults.md) go further.

## Definitions reject typos

A struct is open: a field that nobody declared is accepted. A *definition*, written `#Name`, is closed: a value built from it may not add fields. The resource types that a provider package generates are definitions, which is why a misspelt attribute is an error at evaluation and not a surprise at apply.

```cue file=def.cue
package demo

#Bucket: {
	name:       string
	region:     string
	versioning: bool | *false
	tags: [string]: string
}

logs: #Bucket & {name: "logs", region: "eu-west-1"}
```

```sh run
pravo eval -e logs
```

```text out
name:       "logs"
region:     "eu-west-1"
versioning: false
tags: {}
```

```cue file=typo.cue status=rejected
package demo

logs: versionning: true
```

```sh run exit=6
pravo eval -e logs
```

```text out
Error: evaluating .:
logs.versionning: field not allowed:
    ./typo.cue:3:7
```

```sh run hidden
rm typo.cue
```

`[string]: string` is a pattern: every field of `tags`, whatever its name, is a string. A field marked `?` is optional, and one marked `!` is required, so that `name!: string` fails until something sets it. [Definitions reject typos](../learn/a08-definitions-reject-typos.md) and [Required, optional, regular](../learn/a07-required-optional-regular.md) show the three kinds of field.

## References need no quotes

A value refers to another by its path, and a reference in a string is `\( )`. There is no `var.`, `local.` or `module.` prefix: a name is looked up where it is written, then in the enclosing structs, then at the top of the package. A resource that reads an attribute of another resource depends on it, and pravo orders the plan by those references.

```cue file=refs.cue
package demo

env: "prod"

db: {
	name: "\(env)-db"
	port: 5432
}

app: {
	database_url: "postgres://\(db.name):\(db.port)/app"
	replicas:     3
}

_perReplica: 256
app: memory: _perReplica * app.replicas
```

```sh run
pravo eval -e app
```

```text out
database_url: "postgres://prod-db:5432/app"
memory:       768
replicas:     3
```

A field whose name starts with `_` is hidden: it is a value for the author and is not part of the output, which is what `locals` are for. [Naming a value once](../learn/a11-references-let-hidden.md) adds `let`.

## Loops are comprehensions

`count` and `for_each` become a comprehension: a `for` that produces fields or list elements, and an `if` that guards them. The result is a struct of resources, one for each entry, with the name of the entry as the name of the resource.

```cue file=loops.cue
package demo

regions: ["eu-west-1", "eu-central-1", "us-east-1"]

buckets: {
	for r in regions if r != "us-east-1" {
		"logs-\(r)": #Bucket & {name: "logs-\(r)", region: r}
	}
}

ports: [for p in [80, 443, 8080] if p < 1024 {p}]
```

```sh run
pravo eval -e ports
pravo eval -e buckets
```

```text out
[80, 443]
"logs-eu-west-1": {
	name:       "logs-eu-west-1"
	region:     "eu-west-1"
	versioning: false
	tags: {}
}
"logs-eu-central-1": {
	name:       "logs-eu-central-1"
	region:     "eu-central-1"
	versioning: false
	tags: {}
}
```

A conditional expression, `cond ? a : b`, is written with `if` fields or with a disjunction that data decides. [Generating fields from data](../learn/a10-comprehensions.md) and [Conditions and strings](../learn/a12-conditions-and-strings.md) show both.

## Inputs are tags

A value that a command supplies, where HCL has a variable, is a field with an attribute: `env: string @tag(env)`. `pravo plan -t env=prod` fills it, and a default keeps the plan runnable without the flag. [Tags and variables](spec-tags-and-variables.md) lists the forms.

```cue file=inputs.cue
package demo

size: *"small" | "large" @tag(size)
```

```sh run
pravo eval -e size
pravo eval -e size -t size=large
```

```text out
"small"
"large"
```

## Packages and the standard library

A directory is one package, named by its `package` clause, and `import` brings in another: a provider package that `pravo init` generates, the `pravo.dev/pravo` package with `#Output`, `#Policy` and the settings, or a package of the CUE standard library such as `strings` and `list`. The functions of HCL are mostly there: `strings.ToUpper`, `strings.Join`, `list.Concat`, `len`, `math.Round`. [Packages and the standard library](../learn/a13-packages-and-the-standard-library.md) tours them, and the [function map](../migrate/function-map.md) says where each HCL function went.

## From HCL to a specification

| HCL | In a pravo specification | Page |
|---|---|---|
| `provider "aws" {}` | `$aws: awsp.#Provider & {region: "eu-west-1"}` | [Providers](spec-providers.md) |
| `resource "aws_s3_bucket" "logs" {}` | `logs: $aws.#Resource.aws_s3_bucket & {...}` | [Resources](spec-resources.md) |
| `data "aws_ami" "base" {}` | `base: $aws.#Data.aws_ami & {...}` | [Resources](spec-resources.md#data-sources) |
| `variable "env" {}` | `env: string @tag(env)` | [Tags and variables](spec-tags-and-variables.md) |
| `locals { }` | A hidden field, `_name: ...`, or a plain field that no provider reads | [Naming a value once](../learn/a11-references-let-hidden.md) |
| `output "url" {}` | `url: pravo.#Output & {value: ...}` | [Outputs](spec-outputs.md) |
| `count`, `for_each` | A comprehension | [Resources](spec-resources.md) |
| `module "net" {}` | A definition, or a package that holds resources | [Modules](spec-modules.md) |
| `depends_on`, `lifecycle` | `$depends_on`, `$lifecycle` | [Meta fields](spec-meta-fields.md) |
| `moved {}`, `import {}` | `$from`, `$import` | [Moves and imports](spec-moves-and-imports.md) |
| `backend "s3" {}` | `$settings: state: {backend: "s3", ...}` | [Settings](spec-settings.md) |
| `validation {}`, Sentinel, OPA | A constraint on the provider definition, or a `pravo.#Policy` | [Policies](policies-overview.md) |
| `terraform.tfvars` | `-t` flags, or a file of tag values | [Tags and variables](spec-tags-and-variables.md) |
| Workspaces | A tag that selects the state | [Run one specification in several environments](../how-to/environments.md) |

[The concept map](../migrate/m2-concept-map.md) of the migration guide explains each row.

## Reading an error

An error of evaluation names a path, the values that conflict or the rule that failed, and the positions in the files. `bucket.region: conflicting values "us-east-1" and "eu-west-1"` means two places set `bucket.region` and they differ, and the lines below name both. `incomplete value` means something is still a type and no data filled it. `field not allowed` means a definition did not declare the field. [Reading a CUE error](../learn/c1-reading-a-cue-error.md) and [Why is my value incomplete?](../learn/c2-why-is-my-value-incomplete.md) read one of each line by line.

## See also

- [The specification](spec-overview.md) lists what pravo reads from a root.
- [Evaluation](../concepts/evaluation.md) explains how the files of a root become one value.
- [Track A](../learn/index.md) teaches CUE from zero, with exercises.
