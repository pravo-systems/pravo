<!-- Generated from docs/how-to/publishing-a-module.md by tools/skillgen. Do not edit. -->

# Publish a module

Create a module in a git repository, try it from an example, and publish a version to a registry that others can use.

A published module is a CUE module in a git repository, stored in a container registry under a version tag. Other roots add it with `pravo mod get`.

> [!NOTE]
> Every command below runs against `tools/fakeoci`, the in-memory registry in pravo's source, on port 5078. For ghcr.io, step 4 and the registry in the output of step 5 change.

Work in an empty directory. It is the repository of the module.

## 1. Create the module

```sh
git init -q -b main
pravo mod init example.com/acme/pravo-service@v0
pravo mod edit --source git
cat cue.mod/module.cue
```

Output:

```text
module: "example.com/acme/pravo-service@v0"
language: {
	version: "v0.17.1"
}
source: {
	kind: "git"
}
```

The module path is its permanent name.

- The first element contains a dot. To publish to ghcr.io, start the path with `github.com/<owner>/`.
- `@v0` is the major version. The tags you publish match it: `v0.1.0`, `v0.2.0`.
- `source: kind: "git"` ties a published version to a commit. `pravo mod publish` refuses a tree with uncommitted changes.

## 2. Write the package

A module is a definition that contains resources. It takes each provider as an input and builds its resources through it.

File `service/service.cue`:

```cue
// Package service describes a small service: validated inputs, the
// resources that realise it and an endpoint.
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	// The providers the service is built through. The caller passes them in.
	random!: rnd.#Provider
	local!:  loc.#Provider

	// name is used in every resource and file name.
	name: string & =~"^[a-z][a-z0-9-]*$"
	// port the service listens on.
	port: int & >1024 & <65536
	// owner is the team that answers for the service.
	owner: string

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

- **Constrain inputs.** A pattern, a bound or a choice turns a mistake into an evaluation error that names the field.
- **Comment fields.** Editors show the comments on hover.
- **Give a default to what has one** (`*"standard" | "premium"`) and leave the rest required.
- **Take each provider as a required input** (`provider!: loc.#Provider`). A published module cannot see the consumer's provider structs, and it does not choose their version or configuration. The input is another name for the struct that the caller passes. An instance that leaves it out does not load, and the module may not configure the provider it was given.
- **Import provider definitions** from `pravo.dev/providers/<namespace>/<type>`. They type the inputs. The consumer's `pravo init` generates the packages, so the module ships none.
- **Keep resources as siblings.** A resource does not contain another resource.

## 3. Try it from an example in the repository

An example is a root below the module root. It imports the module by its own path, so no registry is involved.

File `examples/basic/main.cue`:

```cue
package basic

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/acme/pravo-service/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api: service.#Service & {random: $random, local: $local, name: "api", port: 8080, owner: "platform"}

endpoint: pravo.#Output & {value: api.endpoint}
```

The example plays the consumer: it declares the providers and passes them in.

```sh
pravo -C examples/basic init > /dev/null
pravo -C examples/basic validate
pravo -C examples/basic plan | grep -E '^(  #|Plan|  endpoint)'
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 1 outputs across 2 providers.
  # api.codename (random_pet) will be created
  # api.config (local_file) will be created
Plan: 2 to add, 0 to change, 0 to destroy.
  endpoint: "http://api.internal:8080"
```

Keep what is generated or local out of git.

File `.gitignore`:

```text
cue.mod/gen/
pravo.state/
out/
```

## 4. Choose a registry

### A local registry

`CUE_REGISTRY` maps a module path to a registry. The in-memory registry forgets everything when it stops. Start it in a second terminal from a checkout of pravo's source:

```sh
go run ./tools/fakeoci -addr 127.0.0.1:5078
```

and set the mapping in the shell that runs `pravo mod publish`. The suffix `+insecure` selects plain HTTP.

```sh
export CUE_REGISTRY='example.com=localhost:5078+insecure'
```

### ghcr.io

When `CUE_REGISTRY` is not set, pravo maps every `github.com/<owner>` module to `ghcr.io/<owner>/cue`. Nothing else needs configuring. Log in to the registry with a token that can write packages, as `docker login ghcr.io` does, then publish a module whose path starts with `github.com/<owner>/`.

A first push to ghcr.io is private. The package settings page of the registry changes its visibility. Until then an anonymous pull of the module fails.

The directory `templates/module` in pravo's source holds a template repository. Its workflow publishes a version whenever a version tag is pushed.

## 5. Publish

`pravo mod publish` checks before it publishes. A tree with uncommitted changes is refused.

```sh
pravo mod publish v0.1.0
```

Output:

```text
Error: the working tree has uncommitted changes; a published version must match a commit:
?? .gitignore
?? cue.mod/
?? examples/
?? service/
```

Commit, then publish.

```sh
git add -A
git commit -q -m "service module"
pravo mod publish v0.1.0
```

Output:

```text
published example.com/acme/pravo-service@v0.1.0 to localhost:5078/example.com/acme/pravo-service:v0.1.0
```

`pravo mod publish` also refuses a module without a `source` and a version that does not start with `v`.

## 6. Publish the next version

Add an optional input with a default. Existing users keep working, so this is a minor version.

File `service/service.cue`:

```cue
// Package service describes a small service: validated inputs, the
// resources that realise it and an endpoint.
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	// The providers the service is built through. The caller passes them in.
	random!: rnd.#Provider
	local!:  loc.#Provider

	// name is used in every resource and file name.
	name: string & =~"^[a-z][a-z0-9-]*$"
	// port the service listens on.
	port: int & >1024 & <65536
	// owner is the team that answers for the service.
	owner: string
	// tier selects the service class.
	tier: *"standard" | "premium"

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\ntier \(tier)\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

```sh
git commit -q -am "tier input"
pravo mod publish v0.2.0
```

Output:

```text
published example.com/acme/pravo-service@v0.2.0 to localhost:5078/example.com/acme/pravo-service:v0.2.0
```

## 7. Rules for versions

- Publish a new version for every change. A consumer's lock file records a hash of each version and refuses a version whose content changed.
- Tags are semantic versions that match the major version. Adding an optional input with a default is a minor change. Removing or renaming an input, or renaming a resource, is a breaking change.
- A resource's address is its path. Renaming a field that holds a resource changes the address in every consumer's state. Treat the names of resource fields as part of the interface. A definition whose instances accept `$from` embeds `pravo.#Movable`; [Rename, move and adopt resources](importing-and-moving.md) shows both.

## What next

- [Use a published module](using-a-module.md) shows the consumer's side.
- [Run pravo in a pipeline](ci.md) records provider responses once, so that a module's test runs without credentials.
- [pravo mod](../reference/mod.md) lists every subcommand.
