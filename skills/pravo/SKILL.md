---
name: pravo
description: Use when writing, reviewing, running or debugging infrastructure with pravo, the CUE-based tool that drives OpenTofu and Terraform providers. Covers CUE specifications with provider structs and resources, the pravo commands (init, validate, plan, apply, destroy, drift, why, state, import, eval, schema, mod, serve, mcp), plan policies, state and locks, modules, migrating from OpenTofu or Terraform, and reading pravo errors and exit codes. Apply when a directory has a pravo.lock.cue or cue.mod/gen, when the user mentions pravo, pravo.state or pravo.dev/providers, or asks for CUE instead of HCL.
---

# pravo

pravo reads a CUE package, compares it with a graph of recorded resources and drives unmodified OpenTofu providers over the provider plugin protocol. A specification is CUE: there is no HCL, no `terraform` block and no `tofu` binary. The state is a directory with one record per resource. A plan looks at what a change reaches and leaves the rest alone.

This skill is the short guide. The full documentation is in `references/`, one file per page of the pravo documentation, and `references/INDEX.md` says what each file covers. Read the file that answers the question. Do not answer from what you know about Terraform: pravo differs in most details.

## Before anything else

1. Run `pravo version`. The references describe the version that installed this skill; a newer pravo may differ, and `pravo COMMAND --help` is always current.
2. A **root** is a directory holding one CUE package, inside a CUE module (a `cue.mod` directory at or above it). Work in the root, or pass `-C DIR` to any command.
3. Never guess the attributes of a resource. Ask the provider schema: `pravo schema aws_s3_bucket` prints the CUE definition with the provider's documentation as comments, and `pravo schema --required aws_s3_bucket` prints the attributes that must be set. In a directory with no specification, name the provider: `pravo schema hashicorp/random/random_pet`.
4. Never edit `cue.mod/gen/` or `pravo.lock.cue`. `pravo init` writes both. Run it after a checkout, after adding an import of a provider, and with `--upgrade` to take newer provider versions.

## What a specification looks like

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {state: path: "pravo.state"}

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

hello: $local.#Resource.local_file & {
	filename: "hello.txt"
	content:  "Hello, \(pet.id)!\n"
}

file: pravo.#Output & {value: hello.filename}
```

- A **provider** is a struct from the generated package: `$aws: awsp.#Provider & {$version: "~> 5.0", region: "eu-west-1"}`. Its own fields are its configuration. Every resource is built through a declared provider. A provider is imported from `pravo.dev/providers/NAMESPACE/TYPE`, as in `import awsp "pravo.dev/providers/hashicorp/aws"`, never from `hashicorp/aws`; `pravo init` generates the package.
- A **resource** is `$provider.#Resource.TYPE & {...}`; a data source is `#Data`, an ephemeral resource `#Ephemeral`, a provider function call `#Function`.
- The **address** of a resource is the CUE path where its struct sits: `pet`, `api.config`, `pets.eu`. `pravo state list` prints them. Flags and commands that name a resource take the address.
- References between resources are plain CUE references (`pet.id`). They are the dependencies. `$depends_on` and `$lifecycle` carry the rest.
- An **output** is `pravo.#Output & {value: ...}`. There is no top-level `output` field.
- A **module** is a definition that contains resources, or a package of them. Instantiate it with `&`. There is no module block and nothing to wire: every field of the definition is an input, and CUE constraints are the validation.

More: `references/reference/spec-overview.md`, `spec-resources.md`, `spec-providers.md`, `spec-modules.md`, `spec-meta-fields.md`, `spec-outputs.md`.

## The things that go wrong when HCL habits are used

1. **No HCL constructs.** `count` and `for_each` are CUE comprehensions: `for r in regions {"\(r)": $random.#Resource.random_pet & {prefix: r}}`. There are no variables blocks, no `locals`, no workspaces, no provisioners and no `-target`. `references/reference/not-in-pravo.md` lists each with its replacement.
2. **Inputs are tagged fields.** `env: *"dev" | "prod" @tag(env)` and `pravo plan -t env=prod`. There is no variables file. Secrets never go in `-t`, which is written into saved and stored plans: read them with `pravo.#SecretEnv`.
3. **Two values for one field are an error.** CUE unifies; it does not override. A definition is closed, so a misspelt field is an error. Constraints such as `int & >1024` fail before any provider starts (exit code 6). Read `references/learn/c1-reading-a-cue-error.md` before guessing at a fix.
4. **Renaming destroys and recreates** unless the move is declared. A rename is `$from: "old.address"` on the resource that has the new name. An existing object is adopted with `$import: "id"` or `pravo import ADDR ID`. See `references/reference/spec-moves-and-imports.md`.
5. **A plan is not read-only.** It installs providers, writes the lock file, refreshes records whose refresh age ran out and marks drift. It creates, changes and destroys no infrastructure. Only `apply` and `destroy` do.
6. **A plan looks at what the change reaches.** A resource it did not reach is "skipped" or "not read", and its real state was not asked. Drift in it is found by `pravo drift` (every record) or `pravo drift --due --limit N` (the oldest records past their refresh age). See `references/concepts/plans.md`.
7. **A policy is named by whoever runs pravo**, with `--policy FILE` on `plan`, `apply`, `destroy` or `mcp`. The specification cannot name or remove it. A plan that violates it exits with code 5.
8. **A saved plan is applied exactly as it was reviewed.** `pravo plan --out plan.pravo`, then `pravo show plan.pravo`, then `pravo apply plan.pravo`. A plan that no longer matches exits with code 3. A policy is judged against the saved plan, so `apply` without a file or an id is refused under `--policy`. A plan can also be stored in the state, so that a team sees it before anyone applies it: see the workflow.
9. **State is a directory of records** (`pravo.state/` by default) or an S3-compatible bucket. Do not edit the files. Use `pravo state show|list|mv|rm|query|log|locks|unlock`. `pravo state import-tfstate FILE` converts an OpenTofu or Terraform state file once.
10. **`Success! 0 resources` means that pravo read nothing.** A specification written with HCL names (`provider`, `resource`, `variable`) is valid CUE and declares nothing. `validate` and `plan` print a hint when they see such fields. Build every resource through a provider struct.
11. **Locks are per resource.** A command that needs a resource held by another command is rejected with exit code 4. `pravo state locks` lists the holders; `pravo state unlock ADDR` is for a lock left by a crashed command, never for a running one.

## The workflow

```sh
pravo init                          # providers, lock file, generated CUE packages
pravo validate                      # evaluate and check against the provider schemas
pravo plan --out plan.pravo         # what would change; saves the plan
pravo show plan.pravo               # read it again, with its age and actor
pravo apply plan.pravo              # apply exactly that plan
pravo output                        # the recorded outputs
pravo destroy                       # destroys everything recorded; asks first
```

- `pravo apply` without a file plans, asks for approval and applies. `--auto-approve` skips the question: use it only when the user has asked for an unattended apply.
- `pravo apply plan.pravo --only ADDR` applies a closed part of a plan that was reviewed whole.
- `pravo plan --store` writes the plan into the state instead of a file, with `--actor` as its author and `--ttl 36h` as the time it is kept (7 days by default). `pravo plan list` shows the stored plans that can be applied (`--all` adds stale, applied and expired ones, `--touching ADDR` keeps those that change one resource). `pravo plan show plan-ID`, or `pravo show plan-ID`, prints one. `pravo apply plan-ID` applies it with the checks of a plan file, and a stored plan that is stale or expired exits with code 3. `pravo plan pull plan-ID plan.pravo` writes it to a file. `pravo plan gc` removes the plans that nobody needs, and `--dry-run` only says which. A plan that creates, changes or destroys no resource is not stored; `pravo apply` applies it. `plan show` and `apply` need the `-t` and `-T` the plan was made with.
- `pravo plan --replace ADDR` replaces a resource that did not change. `--destroy` plans a destroy.
- `pravo eval` prints the evaluated specification with the recorded state filled in. `pravo eval -e EXPR` evaluates one expression. `pravo fmt` formats the CUE files.
- `pravo why ADDR` explains why a plan creates, changes, replaces or skips a resource. `-v` logs what pravo decides, `-vv` adds the providers' own log.
- `--json` makes a command print machine-readable output on standard output and progress on standard error. `--actor NAME` and `--reason TEXT` are labels recorded on transactions, locks and plans; they are not verified.
- `pravo serve` opens a read-only viewer of the specification, the state and a saved plan in a browser (`--plan FILE`). It lists the plans stored in the state, with who made them and which resources they share, and `pravo serve --plan plan-ID` lays one over the graph. It applies nothing.

Exit codes: 0 success, 1 error, 2 changes (`plan --detailed-exitcode`, `drift`), 3 stale saved plan or stale or expired stored plan, 4 locked or reserved, 5 refused by a policy or a module lock, 6 invalid specification. `references/reference/exit-codes.md` has the full table.

## Safety for an agent

- Run `pravo validate` and `pravo plan` before saying that a change is right, and quote the plan's summary line. A change that was not planned has not been checked.
- Do not run `pravo apply`, `pravo destroy`, `pravo state rm`, `pravo state push`, `pravo state unlock` or `pravo plan gc` unless the user asked for that action on that root. Save the plan and show it.
- Treat the plan as the thing to review: look for `destroy`, `replace` and `-/+` actions on resources that hold data.
- A stored plan, like a plan file, holds sensitive values: do not print one in full. Whoever can read the state can read its plans.
- Do not print values of sensitive attributes or outputs, and do not put secrets in a specification. `pravo.#SecretEnv` reads one from the environment without recording it.
- An agent that calls pravo through tools should use `pravo mcp --policy FILE`. The server applies only from saved plans, a plan file or the id of a stored plan with its `tags` (`pravo_plan` with `store` stores one, `pravo_plan_list` lists them), and refuses arguments that start with `-`. `references/how-to/agents-and-mcp.md` states what that does and does not protect.
- `--actor` is a label that the caller types. It is not an identity.

## Where to look

| The question | Read |
|---|---|
| How is a resource, provider, module, output or setting written? | `references/reference/spec-overview.md` and the `spec-*.md` pages |
| What does a command do and which flags does it take? | `references/reference/plan.md`, `apply.md`, `state.md`, `validate-eval-fmt-gen.md`, `global-flags.md`, or `pravo COMMAND --help` |
| What does an error mean? | `references/reference/errors.md` (grep the message), `exit-codes.md`, `references/how-to/debugging.md` |
| How does evaluation, a plan, state or a provider behave? | `references/concepts/` |
| First plan and apply | `references/how-to/quick-start.md` |
| Several environments, secrets, remote state | `references/how-to/environments.md`, `secrets.md`, `remote-state.md` |
| Share code between roots or publish a module | `references/how-to/modules-in-a-repository.md`, `using-a-module.md`, `publishing-a-module.md` |
| Policies | `references/how-to/policies.md`, `references/reference/policies-writing.md`, `policies-plan-document.md` |
| CI and pipelines | `references/how-to/ci.md` |
| Sharing a plan with a team before it is applied | `references/how-to/share-a-plan-before-you-apply-it.md`, `references/reference/plan.md` |
| Rename, move or adopt resources | `references/how-to/importing-and-moving.md` |
| Drift and refresh age | `references/reference/why-and-drift.md`, `references/concepts/plans.md` |
| Moving from OpenTofu or Terraform | `references/migrate/` (start with `m1-before-you-start.md`; constructs in `construct-matrix.md`, commands in `command-map.md`) |
| CUE itself | `references/learn/cue-cheat-sheet.md`, `references/learn/c1-reading-a-cue-error.md` |
| The viewer | `references/reference/serve.md` |

If a file does not answer it, `grep -r` the references: every error message pravo prints is in `references/reference/errors.md`. The documentation is also at https://pravo.systems, and https://pravo.systems/llms-full.txt has all of it in one file.
