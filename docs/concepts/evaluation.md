---
title: Evaluation
description: "How pravo turns the CUE files of a directory into one value, finds resources by identity and builds the dependency graph before any provider is asked anything."
order: 2
minutes: 11
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'took=\S+'
    replace: 'took=<time>'
---

Evaluation is the first half of every run. It reads files and nothing else. Its results are one value, the resources found in it, and a graph of the dependencies between them.

`pravo validate` and `pravo eval` stop when evaluation is done. `pravo plan` and `pravo apply` continue with the providers.

## One package, one value

The directory in which you run pravo is the root. pravo loads the CUE package in it. Every file of the package joins it, in any order, and CUE unifies the files into one value. A file in a subdirectory joins the package only when something imports it.

Work in an empty directory. The first two files hold data and no provider, so `pravo eval` runs without one. `pravo init` creates the CUE module that the package needs.

```cue file=base.cue
package estate

env:  *"dev" | "prod" @tag(env)
tags: owner: "platform"
```

```cue file=extra.cue
package estate

tags: stage: env
```

```sh run
pravo init
```

```text out
Providers:
Created cue.mod/module.cue

pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo eval
```

```text out
env: "dev"
tags: {
	owner: "platform"
	stage: "dev"
}
```

`stage` in the second file refers to `env` in the first. The result does not depend on the order of the files or of the lines.

A field with an `@tag()` attribute takes its value from the command line.

```sh run
pravo eval -t env=prod
```

```text out
env: "prod"
tags: {
	owner: "platform"
	stage: "prod"
}
```

A tag is part of the specification. A saved plan records the tags that it was made with. `-T` injects the system variables of CUE, such as `os` and `arch`.

The files of a package carry one package name. A file with another name, or with none, is an error that names the file.

```cue file=other.cue status=rejected
package other

size: 1
```

```sh run exit=6
pravo validate
```

```text out
Error: loading .:
other.cue is package other; the other files are package estate
```

```sh run
rm other.cue
```

## Provider definitions are part of the value

A provider contributes definitions to the value. `pravo init` reads the schema of each provider and writes a CUE package from it under `cue.mod/gen`. A resource is a struct that unifies with one definition from that package.

The definition lists every attribute of the type with its type. It is closed, so an attribute that the schema does not list is a conflict. Add a provider file and run `pravo init` again.

```cue file=providers.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$local:  loc.#Provider
```

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

A misspelt attribute fails at evaluation. The error names the path and the line.

```cue file=typo.cue status=rejected
package estate

oops: $random.#Resource.random_pet & {lenght: 2}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
oops.lenght: field not allowed:
    ./typo.cue:3:39
```

No provider process has started. The check needs the generated package and nothing else. The next block adds three resources, removes the typo and validates again with `-v`. The filter keeps the line that discovery prints and any line that reports a provider process.

```sh run
rm typo.cue
```

```cue file=resources.cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

// A plain field. It is not a resource, but web depends on port through it.
listen: port.result

web: $local.#Resource.local_file & {
	filename: "web-\(env).conf"
	content:  "port = \(listen)\n"
}

// A field that holds a whole resource is a second resource.
copy: port

// A definition and a hidden field are not searched.
#Pet:   $random.#Resource.random_pet
_draft: $random.#Resource.random_pet & {length: 9}
```

```sh run
pravo -v validate 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location)=[^ ]+//g' \
  | grep -E '^(loaded|provider started|Success)'
```

```text out
loaded managed=3 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0 took=<time>
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Discovery by identity

After evaluation, pravo walks the value and registers every struct that carries a `$kind` field. The generated definitions supply the field. `pravo eval -e` prints one expression of the value.

```sh run
pravo eval -e port | grep '^\$'
```

```text out
$kind:   "resource"
$type:   "random_integer"
$source: "registry.opentofu.org/hashicorp/random"
```

`$kind` says what the struct is. `$type` and `$source` say which provider type builds it. Nothing else decides. A struct is a resource because it carries these fields, not because of where it sits in a file.

| `$kind` | Written as | Found as |
|---|---|---|
| `resource` | `$random.#Resource.random_pet & {...}` | a managed resource |
| `data` | `$local.#Data.local_file & {...}` | a data source |
| `ephemeral` | `$tls.#Ephemeral.tls_private_key & {...}` | an ephemeral resource |
| `call` | `$k8s.#Function.manifest_decode & {...}` | a provider function call |
| `provider` | `rnd.#Provider & {...}` | a provider |
| `output` | `pravo.#Output & {...}` | an output |
| `env`, `secret_env` | `pravo.#Env & {...}`, `pravo.#SecretEnv & {...}` | an environment variable that pravo reads |
| `remote_outputs` | `pravo.#RemoteOutputs & {...}` | the outputs of another root |

The address of a found struct is its path. The walk follows fields, nested structs and list elements. It registers a struct and does not look inside it.

:::figure What discovery makes of the top-level fields of the example. Plain data is walked and holds nothing. The settings field, a definition and a hidden field are not searched.
<svg viewBox="0 0 780 338" role="img" aria-label="The top-level fields of an evaluated value, in order. Fields that carry a kind are found: two providers and three resources. Plain data is walked and nothing is found in it. A settings field, a definition and a hidden field are not searched." style="min-width:640px">
<text x="0" y="16" style="fill:var(--text-3);font-size:10.5px;letter-spacing:0.04em">field of the value</text>
<text x="190" y="16" style="fill:var(--text-3);font-size:10.5px;letter-spacing:0.04em">what discovery does</text>
<text x="440" y="16" style="fill:var(--text-3);font-size:10.5px;letter-spacing:0.04em">result</text>
<line x1="0" y1="26" x2="780" y2="26" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="0" y="30" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="47" style="fill:var(--text);font-size:12.5px;font-weight:500">env</text>
<rect x="188" y="34" width="168" height="18" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="272" y="46.5" text-anchor="middle" style="fill:var(--text-2);font-size:11.5px">walked</text>
<text x="440" y="47" style="fill:var(--text-3);font-size:12px">no $kind</text>
<text x="10" y="73" style="fill:var(--text);font-size:12.5px;font-weight:500">tags</text>
<rect x="188" y="60" width="168" height="18" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="272" y="72.5" text-anchor="middle" style="fill:var(--text-2);font-size:11.5px">walked</text>
<text x="440" y="73" style="fill:var(--text-3);font-size:12px">no $kind</text>
<rect x="0" y="82" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="99" style="fill:var(--text);font-size:12.5px;font-weight:500">$settings</text>
<rect x="188" y="86" width="168" height="18" rx="2" style="fill:none;stroke:var(--text-3);stroke-width:1" stroke-dasharray="4 3"/>
<text x="272" y="98.5" text-anchor="middle" style="fill:var(--text-3);font-size:11.5px">not searched</text>
<text x="440" y="99" style="fill:var(--text-3);font-size:12px">read for state and refresh, never searched</text>
<text x="10" y="125" style="fill:var(--text);font-size:12.5px;font-weight:500">$random</text>
<rect x="188" y="112" width="168" height="18" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1"/>
<text x="272" y="124.5" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">found: provider</text>
<text x="440" y="125" style="fill:var(--text);font-size:12px">address $random</text>
<rect x="0" y="134" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="151" style="fill:var(--text);font-size:12.5px;font-weight:500">$local</text>
<rect x="188" y="138" width="168" height="18" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1"/>
<text x="272" y="150.5" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">found: provider</text>
<text x="440" y="151" style="fill:var(--text);font-size:12px">address $local</text>
<text x="10" y="177" style="fill:var(--text);font-size:12.5px;font-weight:500">port</text>
<rect x="188" y="164" width="168" height="18" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1"/>
<text x="272" y="176.5" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">found: resource</text>
<text x="440" y="177" style="fill:var(--text);font-size:12px">address port</text>
<rect x="0" y="186" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="203" style="fill:var(--text);font-size:12.5px;font-weight:500">listen</text>
<rect x="188" y="190" width="168" height="18" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="272" y="202.5" text-anchor="middle" style="fill:var(--text-2);font-size:11.5px">walked</text>
<text x="440" y="203" style="fill:var(--text-3);font-size:12px">no $kind</text>
<text x="10" y="229" style="fill:var(--text);font-size:12.5px;font-weight:500">web</text>
<rect x="188" y="216" width="168" height="18" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1"/>
<text x="272" y="228.5" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">found: resource</text>
<text x="440" y="229" style="fill:var(--text);font-size:12px">address web</text>
<rect x="0" y="238" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="255" style="fill:var(--text);font-size:12.5px;font-weight:500">copy</text>
<rect x="188" y="242" width="168" height="18" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1"/>
<text x="272" y="254.5" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">found: resource</text>
<text x="440" y="255" style="fill:var(--text);font-size:12px">address copy</text>
<text x="10" y="281" style="fill:var(--text);font-size:12.5px;font-weight:500">#Pet</text>
<rect x="188" y="268" width="168" height="18" rx="2" style="fill:none;stroke:var(--text-3);stroke-width:1" stroke-dasharray="4 3"/>
<text x="272" y="280.5" text-anchor="middle" style="fill:var(--text-3);font-size:11.5px">not searched</text>
<text x="440" y="281" style="fill:var(--text-3);font-size:12px">a definition</text>
<rect x="0" y="290" width="780" height="26" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="10" y="307" style="fill:var(--text);font-size:12.5px;font-weight:500">_draft</text>
<rect x="188" y="294" width="168" height="18" rx="2" style="fill:none;stroke:var(--text-3);stroke-width:1" stroke-dasharray="4 3"/>
<text x="272" y="306.5" text-anchor="middle" style="fill:var(--text-3);font-size:11.5px">not searched</text>
<text x="440" y="307" style="fill:var(--text-3);font-size:12px">a hidden field</text>
<line x1="0" y1="316" x2="780" y2="316" style="stroke:var(--line-strong);stroke-width:1"/>
</svg>
:::

The plan lists what was found, in the order of the fields.

```sh run
pravo plan -t env=prod | grep '^  # '
```

```text out
  # port (random_integer) will be created
  # web (local_file) will be created
  # copy (random_integer) will be created
```

The three resources are `port`, `web` and `copy`. The two providers are found as well, and a provider is not a resource. The helper field `listen`, the definition `#Pet` and the hidden field `_draft` add nothing.

### What discovery does not search

- **Definitions.** A field whose name starts with `#` is a schema, not a value. Use it to say what a resource looks like. A resource built from it exists where you instantiate it.
- **Hidden fields.** A field whose name starts with `_` is private to the package.
- **Optional fields.** A field written `name?:` has no value to search.
- **The `$settings` field.** pravo reads it for the state location and the refresh age. It is the one top-level field with a meaning of its own.

### A resource does not contain a resource

A struct that is a resource cannot hold another one. pravo reports both paths.

```cue file=nested.cue status=rejected
package estate

import "pravo.dev/pravo"

both: pravo.#Output & {value: {first: port}}
```

```sh run exit=6
pravo validate
```

```text out
Error: both is an output, so it cannot contain both.value.first, which is a resource
```

```sh run
rm nested.cue
```

> [!PITFALL]
> `copy: port` makes a second resource. After `port` is applied, the copy carries the values that the provider computed for it, and the provider refuses them. Refer to an attribute, `port.result`, when you need a value.

## The dependency graph

pravo reads each resource for references to other resources. The rules are the rules of CUE references.

- A reference names a resource when some prefix of its path is the address of one. `api.config.filename` refers to the resource `api.config`.
- A reference counts inside an interpolation, a function call, an expression, a literal list and a comprehension: in the source after `in`, in the conditions and in the body.
- A reference passes through plain fields. `web` reads `listen`, which reads `port.result`, so `web` depends on `port`.
- `$depends_on: [port]` adds a dependency that no attribute implies.
- A reference in the configuration of a provider is a dependency of every resource built through that provider.

Inside a struct, a name resolves to the nearest field of that name. A resource named `proxy`, used inside a block that has its own `proxy` field, refers to the field. The leading `$` of a provider struct keeps its name apart from the attributes of a resource.

The plan document lists the dependencies of each change.

```sh run
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | {addr, dependencies: (.dependencies // [])}'
```

```text out
{"addr":"port","dependencies":[]}
{"addr":"web","dependencies":["port"]}
{"addr":"copy","dependencies":["port"]}
```

`copy` depends on `port` because it refers to it. `web` depends on `port` through `listen`. A resource runs after everything it depends on. Resources that do not depend on each other run together, up to ten at a time.

A resource that reads another one only in the source of a comprehension depends on it as well. `names` refers to `port` nowhere else:

```cue file=names.cue
package estate

import "strings"

names: $local.#Resource.local_file & {
	filename: "names.txt"
	content:  strings.Join([for k, v in {main: port.result} {"\(k)=\(v)"}], "\n")
}
```

```sh run
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | select(.addr == "names") | {addr, dependencies}'
rm names.cue
```

```text out
{"addr":"names","dependencies":["port"]}
```

A reference to a struct or a list that holds resources, and is not a resource itself, depends on every resource below it. `all` reads `ports` and none of its fields by name:

```cue file=all.cue
package estate

import "strings"

ports: {
	a: $random.#Resource.random_integer & {min: 1, max: 9, seed: "a"}
	b: $random.#Resource.random_integer & {min: 1, max: 9, seed: "b"}
}

all: $local.#Resource.local_file & {
	filename: "all.txt"
	content:  strings.Join([for k, v in ports {"\(k)=\(v.result)"}], "\n")
}
```

```sh run
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | select(.addr == "all") | {addr, dependencies}'
rm all.cue
```

```text out
{"addr":"all","dependencies":["ports.a","ports.b"]}
```

Resources that depend on each other in a circle have no first one. The run stops before it asks a provider for anything, and names the resources on the cycle: `dependency cycle between a, b, c`. See [Errors](../reference/errors.md#resources-and-values).

## Values before and after an apply

Before an apply, a computed attribute has no value. It evaluates to its type, or to null.

```sh run
pravo eval -t env=prod -e port.result
```

```text out
number | null
```

`copy` cannot be applied, so the specification drops it before the apply. The second file replaces the first.

```cue file=resources.cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

listen: port.result

web: $local.#Resource.local_file & {
	filename: "web-\(env).conf"
	content:  "port = \(listen)\n"
}
```

```sh run
pravo apply -t env=prod -auto-approve | tail -1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

`pravo eval` fills the recorded attributes of every resource into the value, so a value that depends on them becomes concrete.

```sh run
pravo eval -t env=prod -e port.result
```

```text out
26720
```

```sh run
pravo eval -t env=prod -e web.filename
```

```text out
"web-prod.conf"
```

During a run the same happens one resource at a time. When pravo has planned or applied a resource, it fills the known values into the value at the address of the resource. Every expression that reads them is evaluated again. [Plans](plans.md) describes the values that are still unknown.

## Recap

- The files of the directory unify into one value. Order does not matter, and a second package name is an error.
- A struct is a resource because it carries `$kind`. Definitions, hidden fields and `$settings` are not searched.
- Dependencies are the references between resources. A reference may pass through plain fields.

Next: [Resources and addresses](resources-and-addresses.md) explains what the address of a resource means.
