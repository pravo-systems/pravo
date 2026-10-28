<!-- Generated from docs/concepts/evaluation.md by tools/skillgen. Do not edit. -->

# Evaluation

How pravo turns the CUE files of a directory into one value, finds resources by identity and builds the dependency graph before any provider is asked anything.

Evaluation is the first half of every run. It reads files and nothing else. Its results are one value, the resources found in it, and a graph of the dependencies between them.

`pravo validate` and `pravo eval` stop when evaluation is done. `pravo plan` and `pravo apply` continue with the providers.

## One package, one value

The directory in which you run pravo is the root. pravo loads the CUE package in it. Every file of the package joins it, in any order, and CUE unifies the files into one value. A file in a subdirectory joins the package only when something imports it.

Work in an empty directory. The first two files hold data and no provider, so `pravo eval` runs without one. `pravo init` creates the CUE module that the package needs.

File `base.cue`:

```cue
package estate

env:  *"dev" | "prod" @tag(env)
tags: owner: "platform"
```

File `extra.cue`:

```cue
package estate

tags: stage: env
```

```sh
pravo init
```

Output:

```text
Providers:
Created cue.mod/module.cue

pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh
pravo eval
```

Output:

```text
env: "dev"
tags: {
	owner: "platform"
	stage: "dev"
}
```

`stage` in the second file refers to `env` in the first. The result does not depend on the order of the files or of the lines.

A field with an `@tag()` attribute takes its value from the command line.

```sh
pravo eval -t env=prod
```

Output:

```text
env: "prod"
tags: {
	owner: "platform"
	stage: "prod"
}
```

A tag is part of the specification. A saved plan records the tags that it was made with. `-T` injects the system variables of CUE, such as `os` and `arch`.

The files of a package carry one package name. A file with another name, or with none, is an error that names the file.

File `other.cue`:

```cue
package other

size: 1
```

```sh
pravo validate
```

Output:

```text
Error: loading .:
other.cue is package other; the other files are package estate
```

```sh
rm other.cue
```

## Provider definitions are part of the value

A provider contributes definitions to the value. `pravo init` reads the schema of each provider and writes a CUE package from it under `cue.mod/gen`. A resource is a struct that unifies with one definition from that package.

The definition lists every attribute of the type with its type. It is closed, so an attribute that the schema does not list is a conflict. Add a provider file and run `pravo init` again.

File `providers.cue`:

```cue
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

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

A misspelt attribute fails at evaluation. The error names the path and the line.

File `typo.cue`:

```cue
package estate

oops: $random.#Resource.random_pet & {lenght: 2}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
oops.lenght: field not allowed:
    ./typo.cue:3:39
```

No provider process has started. The check needs the generated package and nothing else. The next block adds three resources, removes the typo and validates again with `-v`. The filter keeps the line that discovery prints and any line that reports a provider process.

```sh
rm typo.cue
```

File `resources.cue`:

```cue
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

```sh
pravo -v validate 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location)=[^ ]+//g' \
  | grep -E '^(loaded|provider started|Success)'
```

Output:

```text
loaded managed=3 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0 took=<time>
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Discovery by identity

After evaluation, pravo walks the value and registers every struct that carries a `$kind` field. The generated definitions supply the field. `pravo eval -e` prints one expression of the value.

```sh
pravo eval -e port | grep '^\$'
```

Output:

```text
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
| `secret`, `secret_version` | `pravo.#Secret & {...}`, `pravo.#SecretVersion & {...}` | a secret that the sealed state keeps, or the number of its version |
| `remote_outputs` | `pravo.#RemoteOutputs & {...}` | the outputs of another root |

The address of a found struct is its path. The walk follows fields, nested structs and list elements. It registers a struct and does not look inside it.

*Figure: What discovery makes of the top-level fields of the example. Plain data is walked and holds nothing. The settings field, a definition and a hidden field are not searched.*

The plan lists what was found, in the order of the fields.

```sh
pravo plan -t env=prod | grep '^  # '
```

Output:

```text
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

File `nested.cue`:

```cue
package estate

import "pravo.dev/pravo"

both: pravo.#Output & {value: {first: port}}
```

```sh
pravo validate
```

Output:

```text
Error: both is an output, so it cannot contain both.value.first, which is a resource
```

```sh
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

```sh
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | {addr, dependencies: (.dependencies // [])}'
```

Output:

```text
{"addr":"port","dependencies":[]}
{"addr":"web","dependencies":["port"]}
{"addr":"copy","dependencies":["port"]}
```

`copy` depends on `port` because it refers to it. `web` depends on `port` through `listen`. A resource runs after everything it depends on. Resources that do not depend on each other run together, up to ten at a time.

A resource that reads another one only in the source of a comprehension depends on it as well. `names` refers to `port` nowhere else:

File `names.cue`:

```cue
package estate

import "strings"

names: $local.#Resource.local_file & {
	filename: "names.txt"
	content:  strings.Join([for k, v in {main: port.result} {"\(k)=\(v)"}], "\n")
}
```

```sh
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | select(.addr == "names") | {addr, dependencies}'
rm names.cue
```

Output:

```text
{"addr":"names","dependencies":["port"]}
```

A reference to a struct or a list that holds resources, and is not a resource itself, depends on every resource below it. `all` reads `ports` and none of its fields by name:

File `all.cue`:

```cue
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

```sh
pravo plan -t env=prod --json 2>/dev/null | jq -c '.changes[] | select(.addr == "all") | {addr, dependencies}'
rm all.cue
```

Output:

```text
{"addr":"all","dependencies":["ports.a","ports.b"]}
```

Resources that depend on each other in a circle have no first one. The run stops before it asks a provider for anything, and names the resources on the cycle: `dependency cycle between a, b, c`. See [Errors](../reference/errors.md#resources-and-values).

## Values before and after an apply

Before an apply, a computed attribute has no value. It evaluates to its type, or to null.

```sh
pravo eval -t env=prod -e port.result
```

Output:

```text
number | null
```

`copy` cannot be applied, so the specification drops it before the apply. The second file replaces the first.

File `resources.cue`:

```cue
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

```sh
pravo apply -t env=prod -auto-approve | tail -1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

`pravo eval` fills the recorded attributes of every resource into the value, so a value that depends on them becomes concrete.

```sh
pravo eval -t env=prod -e port.result
```

Output:

```text
26720
```

```sh
pravo eval -t env=prod -e web.filename
```

Output:

```text
"web-prod.conf"
```

During a run the same happens one resource at a time. When pravo has planned or applied a resource, it fills the known values into the value at the address of the resource. Every expression that reads them is evaluated again. [Plans](plans.md) describes the values that are still unknown.

## Recap

- The files of the directory unify into one value. Order does not matter, and a second package name is an error.
- A struct is a resource because it carries `$kind`. Definitions, hidden fields and `$settings` are not searched.
- Dependencies are the references between resources. A reference may pass through plain fields.

Next: [Resources and addresses](resources-and-addresses.md) explains what the address of a resource means.
