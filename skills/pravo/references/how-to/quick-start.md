<!-- Generated from docs/how-to/quick-start.md by tools/skillgen. Do not edit. -->

# Quick start

Twelve minutes from install to a first plan, an apply, a conflict and a destroy. No account and no cloud.

pravo specifies infrastructure in CUE and drives the OpenTofu providers you already use, unchanged.

OpenTofu merges values by precedence. A module default, a `tfvars` file and a `-var` flag can each set one argument. The last one set takes effect. pravo merges values by agreement. Two values for one field must agree, and a contradiction is an error. Step 8 causes one.

On this page you write a CUE file, plan it, apply it and read the result. Then you add a rule in a second file. You read a plan that skips what the rule does not touch. You write a value that contradicts the rule. Last, you destroy what you made. The providers run on your machine. You need no account and no cloud.

The commands are for a POSIX shell.

## 1. Install pravo

pravo is one file, and it needs no Go installation. It starts the provider binaries itself, so OpenTofu is not needed. `pravo init` fetches providers from `registry.opentofu.org`, so the machine needs network access to it once.

On macOS and Linux, the install script downloads the build for your system, checks it against the published checksums and puts `pravo` in `~/.local/bin`:

```sh
curl -fsSL https://pravo.systems/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://pravo.systems/install.ps1 | iex
```

Both take a version from `PRAVO_VERSION`, such as `v0.1.0`, and a directory from `PRAVO_INSTALL_DIR`. Each build is also on the [releases page](https://github.com/pravo-systems/pravo/releases) as an archive, with a `SHA256SUMS` file and a signed provenance attestation. A browser download on macOS carries the quarantine flag that blocks an unsigned program; `xattr -d com.apple.quarantine pravo` clears it. The install script does not set the flag.

If `pravo` is not found after the install, add the directory that the script names to `PATH`.

To build pravo from its source tree instead, with Go 1.26 or newer, run this in the root of the tree:

```sh
go build -o bin/pravo ./cmd/pravo
```

The result is the single file `bin/pravo`. Put its directory on `PATH`:

```sh
export PATH="$PWD/bin:$PATH"
```

Change to an empty directory for the rest of this page, and check the install:

```sh
pravo version
```

Output:

```text
pravo 0.1.0-dev
cue v0.17.1
```

The first line is the version of pravo that you installed, which a release build prints as `pravo 0.1.0`. The second is the CUE evaluator built into it. You do not install `cue` separately.

If the shell answers `command not found: pravo`, the directory that holds the binary is not on `PATH`. Add it, as the install script or the `export` line above says.

## 2. Write the specification

A specification is the set of `.cue` files in one directory. Create `main.cue`:

File `main.cue`:

```cue
package start

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

config: $local.#Resource.local_file & {
	filename: "server.env"
	content:  "NAME=\(pet.id)\nPORT=8080\n"
}
```

Read it from the top.

- `package start` names the package. Every file in the directory begins with the same line.
- The two imports name CUE packages that `pravo init` generates from the providers' schemas.
- `$random` and `$local` are providers. By convention a provider name starts with `$`.
- `pet` and `config` are resources. A resource is built through a provider, here `$random` and `$local`.
- `#Provider` and `#Resource` are definitions: shapes that the generated packages declare. `&` combines the shape of a resource type with the values you give. [A8](https://pravo.systems/learn/a08-definitions-reject-typos.html) teaches definitions.
- The name of a resource is its address. `pet` and `config` are two addresses.
- `\(pet.id)` puts the name of the pet into the file. That reference is the dependency: pravo creates `pet` first. There is no `depends_on`.

*Figure: The two resources. config uses pet.id, so pravo creates pet first.*

## 3. Initialise

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

The first line is not an error. `main.cue` imports packages that do not exist until `init` generates them.

`pravo init` does three things.

- It installs the providers. On a first run it downloads each one and prints a `Downloading` line for it.
- It writes `pravo.lock.cue`, which pins the provider versions. Commit this file.
- It generates one CUE package per provider under `cue.mod/gen/`. These are the packages `main.cue` imports.

```sh
ls -1
```

Output:

```text
cue.mod
main.cue
pravo.lock.cue
```

Run `pravo init` before any other command. Without it, the imports in `main.cue` do not resolve. See the [error catalogue](../reference/errors.md).

## 4. Validate

```sh
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

`pravo validate` evaluates the specification and checks every resource against the schema of its provider. It starts no provider and changes nothing. Run it after every edit.

## 5. Read the plan

```sh
pravo plan
```

Output:

```text
pravo will perform the following actions:

  # pet (random_pet) will be created
  + pet: {
  +     id:        (known after apply)
  +     length:    2
  +     separator: "-"
    }

  # config (local_file) will be created
  + config: {
  +     content:              (known after apply)
  +     content_base64sha256: (known after apply)
  +     content_base64sha512: (known after apply)
  +     content_md5:          (known after apply)
  +     content_sha1:         (known after apply)
  +     content_sha256:       (known after apply)
  +     content_sha512:       (known after apply)
  +     directory_permission: "0777"
  +     file_permission:      "0777"
  +     filename:             "server.env"
  +     id:                   (known after apply)
    }

Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
```

The plan changes nothing. Four things to read in it:

- `+` marks a resource to create. Each block is headed by its address and its type.
- `(known after apply)` marks a value that only the provider can supply. `content` is unknown because it uses `pet.id`.
- `Plan:` counts the actions. `Graph:` says how many resources the plan covers: here `planned 2`.
- `file_permission: "0777"` is the default of the provider. It lets every user read, write and run the file, less what your umask removes. Step 7 pins a stricter mode.

## 6. Apply

```sh
pravo apply -auto-approve
```

Output:

```text
pravo will perform the following actions:
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
pet: Creating... 
pet: Creation complete after 0s [id=unique-coral]
config: Creating... 
config: Creation complete after 0s [id=261ce15616ccd7d5df87e363a6975949dbddb862]

Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn 20261019T101500Z-3f9c2a]
```

`apply` prints the plan again, then runs it. `-auto-approve` skips the question. Without it, pravo waits until you type `yes`.

Read the result:

```sh
cat server.env
```

Output:

```text
NAME=unique-coral
PORT=8080
```

pravo created the pet, then wrote the file with the pet's name in it. Your name differs: the pet is random. Open `server.env` in an editor to see the same two lines.

pravo records every resource it manages in the directory `pravo.state/`:

```sh
pravo state list
```

Output:

```text
config
pet
```

## 7. Change it

Write a rule for `local_file`. Create `rules.cue`:

File `rules.cue`:

```cue
package start

$local: #Resource: local_file: file_permission: "0600"
```

A colon between names nests them. The line reads: in `$local`, in `#Resource`, in `local_file`, the field `file_permission` is `"0600"`. [A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html) teaches the shorthand.

This file is in the same package, so CUE merges it with `main.cue`. It says: every `local_file` built through `$local` has the permission `0600`. `main.cue` stays as it is. [B10, Policy on providers and on plans](../learn/b10-policy.md) covers rules of this kind.

Apply the change and look at the mode of the file:

```sh
pravo apply -auto-approve
```

Output:

```text
...
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2
...
config: Destroying... [id=261ce15616ccd7d5df87e363a6975949dbddb862]
config: Creating replacement...
config: Replacement complete after 0s [id=261ce15616ccd7d5df87e363a6975949dbddb862]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn 20261019T101500Z-3f9c2a]
```

```sh
ls -l server.env | cut -c1-10
```

Output:

```text
-rw-------
```

The first ten characters of `ls -l` are the file type and mode. Only the owner can read and write the file.

*Figure: What the plan reached. config is replaced. pet is skipped.*

## 8. Break the rule

This step ends in an error that you cause.

In OpenTofu, a file named `override.tf` replaces an argument that another file sets. In CUE a value in another file does not replace anything. It has to agree. Add a file that asks for a different permission:

```hcl
resource "local_file" "config" {
  file_permission = "0644"
}
```

File `override.cue`:

```cue
package start

config: file_permission: "0644"
```

Status: rejected.

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
config.file_permission: 2 errors in empty disjunction:
config.file_permission: conflicting values "0600" and "0644":
    ./main.cue:13:9
    ./override.cue:3:26
    ./rules.cue:3:49
config.file_permission: conflicting values "0644" and null (mismatched types string and null):
    ./cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue:97:29
    ./main.cue:13:9
    ./override.cue:3:26
```

Read the error in four parts.

- **Path.** `config.file_permission` is the field that disagrees.
- **Values.** `"0600"` and `"0644"` cannot both hold. `"0600"` is the rule. `"0644"` is the new value.
- **Locations.** Each indented line is `file:line:column`. `rules.cue:3:49` is the rule, `override.cue:3:26` is the new value, and `main.cue:13:9` is the resource they both apply to. Your terminal prints the full path of each file. This site shortens it to `./`.
- **Alternatives.** The schema allows a string or `null` for this field. pravo reports each alternative. The first error is the one to read.

No provider started and nothing changed. `pravo plan` and `pravo apply` print the same error and stop. [A3, Writing a field twice](https://pravo.systems/learn/a03-writing-a-field-twice.html) teaches this in full. [Reading a CUE error](../learn/c1-reading-a-cue-error.md) explains the shape of the message.

Remove the file and validate again:

```sh
rm override.cue
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## 9. Destroy

```sh
pravo destroy -auto-approve
```

Output:

```text
config: Refreshing state... [id=261ce15616ccd7d5df87e363a6975949dbddb862]
pet: Refreshing state... [id=unique-coral]

pravo will perform the following actions:

  # config (local_file) will be destroyed
  - config: {
  -     content:              "NAME=unique-coral\nPORT=8080\n"
...
  -     file_permission:      "0600"
  -     filename:             "server.env"
...
    }

  # pet (random_pet) will be destroyed
  - pet: {
  -     id:        "unique-coral"
  -     length:    2
  -     separator: "-"
    }

Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state · refreshed 2
config: Destroying... [id=261ce15616ccd7d5df87e363a6975949dbddb862]
config: Destruction complete after 0s
pet: Destroying... [id=unique-coral]
pet: Destruction complete after 0s

Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn 20261019T101500Z-3f9c2a]
```

pravo destroys in reverse order of dependency: the file first, then the pet. `server.env` is gone. Your specification and the record of what pravo did remain:

```sh
ls -1
```

Output:

```text
cue.mod
main.cue
pravo.lock.cue
pravo.state
rules.cue
```

## Recap

- A resource is a CUE struct built through a provider. Its name is its address, and a reference to another resource is the dependency.
- A plan lists every action before it happens and counts what it skipped: `planned 1`.
- Two values for one field must agree. A conflict is an error that names the path, both values and both places, before any provider starts.

The reference pages for the commands on this page: [init](../reference/init.md), [validate](../reference/validate-eval-fmt-gen.md), [plan](../reference/plan.md), [apply](../reference/apply.md), [destroy](../reference/destroy.md) and [state](../reference/state.md).

## Where next

### I run OpenTofu or Terraform

Providers stay the same. Translate the specification, convert the state, and cut over when the plan is empty.

[Migrate](https://pravo.systems/migrate/index.html)

### I have never seen CUE

Track A teaches CUE in fourteen lessons of 6 to 20 minutes each. You need only the `pravo` binary.

[Start Track A at A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html)

### Show me the reference

One page per command and per specification topic, each opening with a table.

[Reference](https://pravo.systems/reference/index.html)
