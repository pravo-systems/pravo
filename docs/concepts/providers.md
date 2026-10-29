---
title: Providers
description: How pravo finds, installs, locks, starts and talks to a provider, how a provider becomes a CUE struct, and how an instance is named.
order: 7
minutes: 14
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'v[0-9]+\.[0-9]+\.[0-9]+'
    replace: 'v<v>'
  - pattern: '/[0-9]+\.[0-9]+\.[0-9]+/'
    replace: '/<v>/'
  - pattern: '(darwin|linux|windows)_(arm64|amd64)'
    replace: '<os>_<arch>'
  - pattern: 'sha256:[0-9a-f]{64}'
    replace: 'sha256:<hash>'
  - pattern: '(version:\s+)"[0-9]+\.[0-9]+\.[0-9]+"'
    replace: '${1}"<v>"'
  - pattern: '(?m)^[^\n]*: Refreshing state\.\.\.[^\n]*\n'
    replace: ''
---

A provider is a program. pravo runs the providers of the OpenTofu registry as they are published. It starts the program, asks it for its schema, and sends it the requests of the provider plugin protocol over gRPC. It never modifies a provider.

## From a registry to a process

A provider reaches a run through a chain of files. `pravo init` builds the first half. Every run uses the second.

:::figure The chain from a registry to a provider process. The generated package is what the specification imports.
<svg viewBox="0 0 780 300" role="img" aria-label="Two rows. The first row is pravo init: a registry, the archive store, the schema cache, the generated CUE package, and beside them the lock file. The second row is what happens in a run: the package is imported, a struct is built from its provider definition, the struct names an instance, and the instance is a provider process that the state records by key." style="min-width:700px">
<text x="0" y="14" style="fill:var(--text-3);font-size:11px;letter-spacing:0.06em">pravo init</text>
<rect x="0" y="26" width="150" height="74" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="75.0" y="48" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">registry</text>
<text x="75.0" y="66" text-anchor="middle" style="fill:var(--text-3);font-size:10px">HTTPS, or an OCI</text>
<text x="75.0" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10px">repository</text>
<rect x="176" y="26" width="150" height="74" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="251.0" y="48" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">provider store</text>
<text x="251.0" y="66" text-anchor="middle" style="fill:var(--text-3);font-size:10px">$PRAVO_HOME/providers/</text>
<text x="251.0" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10px">archive checked, unpacked</text>
<rect x="352" y="26" width="150" height="74" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="427.0" y="48" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">schema cache</text>
<text x="427.0" y="66" text-anchor="middle" style="fill:var(--text-3);font-size:10px">$PRAVO_HOME/schemas/</text>
<text x="427.0" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10px">one start, GetProviderSchema</text>
<rect x="528" y="26" width="226" height="74" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.3"/>
<text x="641.0" y="48" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">generated package</text>
<text x="641.0" y="66" text-anchor="middle" style="fill:var(--text-3);font-size:10px">cue.mod/gen/pravo.dev/</text>
<text x="641.0" y="80" text-anchor="middle" style="fill:var(--text-3);font-size:10px">providers/NS/TYPE/TYPE_gen.cue</text>
<line x1="152" y1="63" x2="174" y2="63" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="174,63 168,59.4 168,66.6" style="fill:var(--text-3)"/>
<line x1="328" y1="63" x2="350" y2="63" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="350,63 344,59.4 344,66.6" style="fill:var(--text-3)"/>
<line x1="504" y1="63" x2="526" y2="63" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="526,63 520,59.4 520,66.6" style="fill:var(--text-3)"/>
<rect x="176" y="120" width="150" height="52" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3" stroke-dasharray="4 3"/>
<text x="251.0" y="142" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">pravo.lock.cue</text>
<text x="251.0" y="160" text-anchor="middle" style="fill:var(--text-3);font-size:10px">version, constraint, hashes</text>
<line x1="251" y1="100" x2="251" y2="119" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="251,119 248.0,114 254.0,114" style="fill:var(--text-3)"/>
<line x1="0" y1="196" x2="780" y2="196" style="stroke:var(--line-strong);stroke-width:1" stroke-dasharray="2 4"/>
<text x="0" y="216" style="fill:var(--text-3);font-size:11px;letter-spacing:0.06em">each run</text>
<rect x="0" y="228" width="150" height="58" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="75.0" y="250" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">import</text>
<text x="75.0" y="268" text-anchor="middle" style="fill:var(--text-3);font-size:10px">"pravo.dev/providers/..."</text>
<rect x="176" y="228" width="150" height="58" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="251.0" y="250" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">provider struct</text>
<text x="251.0" y="268" text-anchor="middle" style="fill:var(--text-3);font-size:10px">$aws: awsp.#Provider &amp; {...}</text>
<rect x="352" y="228" width="150" height="58" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="427.0" y="250" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">instance key</text>
<text x="427.0" y="268" text-anchor="middle" style="fill:var(--text-3);font-size:10px">aws, or aws.$eu</text>
<text x="427.0" y="282" text-anchor="middle" style="fill:var(--text-3);font-size:10px">when there are several</text>
<rect x="528" y="228" width="150" height="58" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="603.0" y="250" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">provider process</text>
<text x="603.0" y="268" text-anchor="middle" style="fill:var(--text-3);font-size:10px">started on first use</text>
<text x="603.0" y="282" text-anchor="middle" style="fill:var(--text-3);font-size:10px">gRPC, protocol 5 or 6</text>
<line x1="152" y1="257" x2="174" y2="257" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="174,257 168,253.4 168,260.6" style="fill:var(--text-3)"/>
<line x1="328" y1="257" x2="350" y2="257" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="350,257 344,253.4 344,260.6" style="fill:var(--text-3)"/>
<line x1="504" y1="257" x2="526" y2="257" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="526,257 520,253.4 520,260.6" style="fill:var(--text-3)"/>
<line x1="678" y1="257" x2="700" y2="257" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="700,257 694,253.4 694,260.6" style="fill:var(--text-3)"/>
<text x="740" y="254" text-anchor="middle" style="fill:var(--text);font-size:12px;font-weight:600">state</text>
<text x="740" y="269" text-anchor="middle" style="fill:var(--text-3);font-size:10px">provider_key</text>
</svg>
:::

`pravo init` does five things.

1. It reads the import lines of every `.cue` file in the CUE module. It also reads the literal `$version`, `$path`, `$protocol` and `$address` of each provider struct written as `alias.#Provider & {...}`.
2. It resolves each constraint against the registry. It chooses the newest version that satisfies it, has a build for your platform and speaks plugin protocol 5 or 6.
3. It downloads the archive, verifies its SHA-256 against the registry's answer, and unpacks it under `$PRAVO_HOME/providers/`. `$PRAVO_HOME` is `~/.pravo` unless you set it. A binary that the OpenTofu plugin cache already holds is used instead of a download.
4. It starts each provider once, asks for the schema, and caches the schema as JSON under `$PRAVO_HOME/schemas/`.
5. It writes `cue.mod/module.cue` when no module exists, the generated packages, and `pravo.lock.cue`.

It writes no file into the root package. `plan`, `apply` and the other commands install a missing provider and update the lock file on their own. They need the generated packages to exist, because the specification imports them.

## A provider is a struct

A provider struct is built from the `#Provider` definition of a generated package. Work in an empty directory. The specification below has three provider structs and two kinds of provider.

```cue file=providers.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:    rnd.#Provider & {$version: "~> 3.6"}
$primary:   loc.#Provider
$secondary: loc.#Provider
```

```cue file=resources.cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}

one: $primary.#Resource.local_file & {
	filename: "one.txt"
	content:  "one\n"
}

// A definition that takes a provider as an input.
#Pair: {
	provider!: loc.#Provider
	name:      string
	file: provider.#Resource.local_file & {
		filename: "\(name).txt"
		content:  "\(name)\n"
	}
}

two: #Pair & {provider: $secondary, name: "two"}
```

```sh run
pravo init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

The fields of a provider struct that start with `$` belong to pravo. Every other field is the configuration of the provider, typed by the definition. The configuration may refer to resources, data sources and ephemeral resources. Whatever a provider struct refers to is a dependency of every resource that uses the provider.

| Field | Meaning |
|---|---|
| `$kind`, `$source` | Identity. The definition supplies both. `$source` is the registry address of the provider. |
| `$version` | A version constraint such as `"~> 3.6"`. Without it, any version is accepted. |
| `$path` | A provider binary, or a directory that holds one. Nothing is downloaded or verified for it. |
| `$protocol`, `$address` | How pravo reaches the provider. See [the plugin protocol](#the-plugin-protocol). |
| `$from` | The address that the provider had before. See [providers from a container registry](#providers-from-a-container-registry). |

A provider struct can sit anywhere in the specification, in the root or inside a definition. Its name is a convention. A leading `$` keeps it apart from the attributes of a resource.

## The generated package

`pravo init` writes one package for each provider and one for the settings of pravo.

```sh run
find cue.mod/gen -type f | sort
```

```text out
cue.mod/gen/pravo.dev/policies/policies_gen.cue
cue.mod/gen/pravo.dev/pravo/settings_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
```

The path of a provider package is `pravo.dev/providers/NAMESPACE/TYPE`. A registry other than the default adds its host in front of the namespace. The first line of the file names the provider and the version that the package was made from.

```sh run
sed -n 1,2p cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
```

```text out
// Provider registry.opentofu.org/hashicorp/random v<v>
// Code generated by pravo. DO NOT EDIT.
```

The package exports one definition, `#Provider`. The definitions that resources are built from are its members.

```sh run
grep -E '^[[:space:]]?#(Provider|Resource|Data|Ephemeral|Function): ' cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
```

```text out
#Provider: {
	#Resource: _#Resource
	#Data: _#Data
	#Ephemeral: _#Ephemeral
	#Function: _#Function
```

| Member | Contents |
|---|---|
| `#Resource` | One definition for each managed resource type. |
| `#Data` | One for each data source type. |
| `#Ephemeral` | One for each ephemeral resource type. |
| `#Discovery` | One for each list resource type, when the provider can list. See [Discovery](discovery.md). |
| `#Function` | One for each provider function, with `args` and `result`. |

Each member refers to a set of definitions that the file writes once, after `#Provider`, as `_#Resource`, `_#Data`, `_#Ephemeral`, `_#Discovery` and `_#Function`. CUE copies what a struct holds each time the struct is unified, so a provider that held its definitions would carry thousands of them into every provider struct and every definition that takes one. A reference is shared. A specification does not use the hidden names. It writes `$aws.#Resource.aws_s3_bucket`.

The provider-level fields of `#Provider` come first in the file.

```sh run
grep -E '^[[:space:]]\$' cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
```

```text out
	$kind:   "provider"
	$source: "registry.opentofu.org/hashicorp/random"
	$version?: __string
	$path?: __string
	$protocol?: "tfplugin" | "tfplugin+reattach"
	$address?: __string
	$from?: __string
```

The package holds every type that the provider offers, not only the ones that a root uses. A resource, a data source or a function is a struct that unifies with one definition, and the definition is closed. A misspelt attribute is a conflict that CUE reports before any provider runs. The definitions also carry the documentation of the provider as comments, so an editor shows it.

A run does not load all of it. The package of a large provider is megabytes of CUE, and evaluating it is most of the time that a small root takes. When pravo loads a root, it reads the CUE of the root and its packages for the types that it names, such as `$aws.#Resource.aws_s3_bucket`, and loads a copy of each large generated package in which the other types are blanked. The lines keep their numbers, so a position in a message is a position in the file on disk. The file is not changed, and an editor still sees the whole package. A package is loaded whole when the module uses a catalogue as a whole, as in `for k, v in $aws.#Resource`, or names a type with an expression, and when the load fails, which is how a type that the scan did not find is found. `PRAVO_NO_PRUNE` loads whole packages.

A computed attribute is a regular field of the type `T | null`. Referring to it before it is known gives a non-concrete value, which pravo sends to the provider as unknown. An attribute that is both optional and computed has the same shape. A sensitive, deprecated or write-only attribute carries an `@pravo(...)` attribute.

The package name is the provider type with `-` and `.` replaced by `_`. A name that is a CUE keyword gets a trailing underscore, and a name that begins with a digit gets a `p`. The import then carries the name after a colon, and you choose the alias.

| Provider type | Package name | Import |
|---|---|---|
| `random` | `random` | `"pravo.dev/providers/hashicorp/random"` |
| `null` | `null_` | `"pravo.dev/providers/hashicorp/null:null_"` |
| `my-cloud` | `my_cloud` | `"pravo.dev/providers/acme/my-cloud:my_cloud"` |

Choose an alias that differs from the names of provider structs and from every field in scope. A top-level field with the name of an import is an error, `random redeclared as imported package name`. Inside a struct, the name reads as the field, and the import is hidden.

When the version in the lock file differs from the version that a package was made from, every command warns and carries on. `pravo init` and `pravo gen` rewrite the package.

## A resource uses the provider it was built through

A resource is built by selecting a definition from a provider struct: `$primary.#Resource.local_file`. pravo reads the provider from that path. It follows plain fields: after `#Bucket: $aws.#Resource.aws_s3_bucket & {...}`, the struct `b: #Bucket & {...}` is built through `$aws`. It does not follow computed selection, such as an index into a list of providers.

A resource that was not built through a declared provider is an error that shows how to build one.

```cue file=orphan.cue status=rejected
package estate

import rnd "pravo.dev/providers/hashicorp/random"

lonely: rnd.#Provider.#Resource.random_pet & {length: 2}
```

```sh run exit=6
pravo validate
```

```text out
Error: lonely was not built through a provider. Declare a provider, for example
	$aws: awsp.#Provider & {...}
and build the resource through it: $aws.#Resource.random_pet & {...}
```

```sh run
rm orphan.cue
```

A definition can take a provider as an input. `#Pair` above declares `provider!: loc.#Provider` and builds its resource through it. The caller writes `two: #Pair & {provider: $secondary, ...}`. The field of the instance is another name for `$secondary`. It is not a second provider.

The `!` makes the input required. An instance that leaves it out does not load, and the error names the instance and the field. [Reuse](../reference/spec-reuse.md#an-instance-that-passes-no-provider) shows it.

## Instances and keys

An instance is one configured provider process. Each provider struct is an instance, except a struct that is built from another struct, which shares its instance. Resources that are built through one struct use one process.

The key of an instance is recorded in the state as `provider_key`. It is the local name of the provider when the specification has one struct of that source. It is `NAME.PATH` of the struct when there are several.

```sh run
pravo apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
```

```sh run
pravo state query '{for a, r in resources {"\(a)": r.provider_key}}'
```

```text out
{
	one:        "local.$primary"
	port:       "random"
	"two.file": "local.$secondary"
}
```

`one` was built through `$primary`, and `two.file` through `$secondary`. `port` is the only `random` struct, so its key is the local name.

```sh run
pravo state show two.file | head -1
```

```text out
// two.file (provider registry.opentofu.org/hashicorp/local v<v> as local.$secondary, schema version 0, revision 1)
```

Adding a second struct of one source changes the key that is recorded for the first.

```cue file=providers.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:    rnd.#Provider & {$version: "~> 3.6"}
$spare:     rnd.#Provider
$primary:   loc.#Provider
$secondary: loc.#Provider
```

The key is not part of the configuration that the provider sees, so the plan proposes no change to the object. `pravo why` reports the new key as the reason.

```sh run
pravo why port
```

```text out
port: no-op
  - provider instance changed (random -> random.$random)
  - refreshed because the record is older than its refresh age; no drift
```

The next apply records the key. It reports no changes, and the object stays as it is.

```sh run
pravo apply -auto-approve | grep '^No changes'
pravo state show port | head -1
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
// port (provider registry.opentofu.org/hashicorp/random v<v> as random.$random, schema version 0, revision 2)
```

The local name is the provider type, made unique with the namespace when two sources share a type. If a struct is removed while resources that used it still exist, pravo destroys them through a default instance with no configuration. A provider that needs configuration, such as a region, cannot do that. Keep the struct until its resources are gone.

## Versions and the lock file

`$version` uses the constraint syntax of the providers. `"~> 3.6"` accepts `3.6.x` and later minor versions below `4.0`. `">= 3.5, < 4.0"` is the explicit form. A specification has one version of each source. Structs of one source with different constraints are an error.

```cue file=providers.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:    rnd.#Provider & {$version: "~> 3.6"}
$primary:   loc.#Provider & {$version: "~> 2.5"}
$secondary: loc.#Provider & {$version: ">= 2.9"}
```

```sh run exit=6
pravo validate
```

```text out
Error: $secondary: $version ">= 2.9" conflicts with "~> 2.5" on another hashicorp/local provider; one version of a provider is used
```

Restore the file. `pravo init` records the selected versions in `pravo.lock.cue` next to the specification.

```cue file=providers.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:    rnd.#Provider & {$version: "~> 3.6"}
$primary:   loc.#Provider
$secondary: loc.#Provider
```

```sh run
cat pravo.lock.cue
```

```text out
// This file is maintained by "pravo init". Commit it to version control.

providers: "registry.opentofu.org/hashicorp/local": {
	version: "<v>"
	hashes: {
		"<os>_<arch>": "sha256:<hash>"
	}
}
providers: "registry.opentofu.org/hashicorp/random": {
	version:     "<v>"
	constraints: "~> 3.6"
	hashes: {
		"<os>_<arch>": "sha256:<hash>"
	}
}
```

The key is the registry address of the provider, host included. A provider archive differs for each platform, so `hashes` has one entry for each `OS_ARCH`. The hash is the SHA-256 of the archive.

A later `pravo init` installs exactly the locked versions. When it downloads an archive and the lock pins a hash for the platform, the archive must match it. A mismatch is an error and leaves nothing installed. A platform with no hash gets the hash of what was downloaded. `pravo init -upgrade` ignores the lock and chooses the newest versions that satisfy the constraints.

`pravo providers lock` pins the hashes of other platforms, such as the Linux runners of a CI system when you work on a Mac. The hashes come from the registry's listing. Nothing is downloaded.

`pravo providers` lists the providers of the root with their versions and where their binaries are.

```sh run
pravo providers
```

```text out
local = hashicorp/local v<v>
    ~/.pravo/providers/registry.opentofu.org/hashicorp/local/<v>/<os>_<arch>/terraform-provider-local
random = hashicorp/random v<v>
    ~/.pravo/providers/registry.opentofu.org/hashicorp/random/<v>/<os>_<arch>/terraform-provider-random
```

## The plugin protocol

pravo speaks the provider plugin protocol, version 5 or 6, over gRPC. It detects the version when it starts the provider. A provider that serves one of them is started and called for the operations that [Compatibility](../reference/compatibility.md#provider-protocol) lists.

pravo starts a provider the first time a resource needs it. It starts one process for each instance and configures it with the current evaluation of its struct. It stops every process when the command ends. A provider can receive requests from several resources at once.

| What pravo does | Request |
|---|---|
| Fetch the schema | Once, by `pravo init`, and again when the cache is missing |
| Validate and apply the configuration of the provider | When the first resource of an instance runs |
| Validate the configuration of a resource | Each resource that a plan reaches |
| Upgrade a record to the current schema | Each record that a plan reaches, and each record it reads as an input |
| Refresh | Each record that a plan looks at when its refresh age has run out. `-refresh=expired` and `-refresh=all` read more. |
| Plan a change | Each resource that a plan reaches, and again at apply |
| Apply a change | Each resource that an apply changes |
| Import | A `$import` declaration, and `pravo import` |
| Read a data source | A data source that is read |
| Open, renew and close an ephemeral resource | An ephemeral resource, for the length of the run |
| Call a function | A function call. It needs no configuration, so the process stays unconfigured. |
| Stop | The end of the command |

### Reaching a provider that is already running

A provider struct has a `$protocol`. The default is `tfplugin`: pravo installs the binary, starts it and stops it. The other value is `tfplugin+reattach`: the provider is already running, and pravo connects to the `$address` and never starts, stops or signals it.

```cue title="providers.cue"
$dev: dev.#Provider & {
	$protocol: "tfplugin+reattach"
	$address:  "unix:///tmp/dev-provider.sock"
}
```

`$address` is `unix:///PATH` or `tcp://127.0.0.1:PORT`. TCP is accepted on the loopback interface only. The connection carries configuration, state and write-only values without encryption. pravo tries version 6 of the protocol and then version 5.

pravo does not install, select or lock what it connects to. `$version` and `$path` are errors on a reattached provider. It has no entry in `pravo.lock.cue`, its schema is not cached, and the records that it writes carry the version `reattach`.

Debuggers and provider test programs print an environment variable, `TF_REATTACH_PROVIDERS`. It selects reattaching for the providers it names, whatever their declaration says. pravo says so when it uses it.

A provider under development is reached the other way with `$path`. The source of the package names the provider in the state. Switching between a development binary and a registry build changes no address.

## Providers from a container registry

A provider can live in a container registry: GitHub Packages, ECR, Artifact Registry, Harbor, or any registry that stores OCI artifacts. Its source is `oci://HOST/PATH/TYPE`. The generated package path is `pravo.dev/providers/oci/HOST/PATH/TYPE`, with a port in the host written after an underscore.

```cue title="providers.cue"
import aws "pravo.dev/providers/oci/ghcr.io/acme/providers/aws"

$aws: aws.#Provider & {$version: "~> 5.0"}
```

Each provider is a repository and each version is a tag. The tag holds an image index with one manifest for each platform. Each manifest has one layer, the zip archive that a provider registry serves. The content is the same, and only the address differs. `pravo init` lists the tags and takes the newest that satisfies `$version` and has a build for your platform. It records the digest of the archive in the lock file. An archive that does not match its digest is rejected. Authentication is the one that container tools use: `~/.docker/config.json` and its credential helpers.

`pravo providers copy hashicorp/aws 5.82.0 oci://ghcr.io/acme/providers/aws` publishes a version from a provider registry or from another container registry. A published version is never replaced. pravo checks the copy against the checksums of its source. It does not keep or verify the signature of the original release. After the copy, you trust the digest in the lock file and whoever can write to the repository.

Identity follows the source. A record names the provider that wrote it, and a plan stops when the specification names another.

```text
Error: server was written by provider registry.opentofu.org/hashicorp/random, but the provider is now oci://localhost:5001/acme/providers/random; add $from: "registry.opentofu.org/hashicorp/random" to the provider
```

`$from` on the provider declares the address that it had before, as `$from` on a resource declares an earlier address. The plan relabels the records in memory, so the resources are refreshed through the new source, and lists nothing to replace. The apply writes the new label. Nothing is replaced.

```cue title="providers.cue"
$random: rnd.#Provider & {
	$version: "~> 3.6"
	$from:    "registry.opentofu.org/hashicorp/random"
}
```

```text
server: provider registry.opentofu.org/hashicorp/random -> oci://localhost:5001/acme/providers/random
```

`$from` is a claim that the two builds are the same provider. pravo cannot check it.

## Recap

- A provider is a program that pravo starts, configures and stops. Its schema becomes a generated CUE package that the specification imports.
- A provider struct is an instance. A resource uses the provider that it was built through, and the state records the instance key.
- The lock file pins the version and the archive hash of each provider for each platform.

Next: [Constraints and policies](policies.md) describes the two places where a rule about a resource can live.
