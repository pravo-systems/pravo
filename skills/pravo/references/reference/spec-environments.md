<!-- Generated from docs/reference/spec-environments.md by tools/skillgen. Do not edit. -->

# Environments

$environments and $environment: the environments one specification is applied to, the rows of what differs, $after and $default, the checks at load, the gate of a following environment and the record each state keeps.

## Quick reference

| Name | Where | Meaning |
|---|---|---|
| `$environments` | A top-level field. | A struct with one field for each environment: the key is its name, the value its row. |
| a field of a row | Your own. | What differs between the environments: a size, a region, a domain. |
| `$after` | A row. | The environment whose release this one takes, or a list of them. Without it, the environment takes any release. |
| `$default` | A row. | `true` on the environment that a command without `--env` uses. At most one row has it. |
| `$environment` | Filled in by pravo; declare it for your editor ([Typing the rows](#typing-the-rows)). | The row of the selected environment, with `$name`, its name. |
| `--env NAME` | Every command that evaluates the specification. | Selects the environment. It is the tag `env`: `-t env=NAME` does the same. |

```cue
$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	staging: {workers: 2, $after: "dev"}
	prod: {workers: 3, $after: "staging"}
}

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}

server: workers: $environment.workers
```

`pravo.#Environments` checks the shape of the struct: a row is a struct, `$after` is a name or a list of names, `$default` is a boolean. Your fields are open. To give every row a field with a default, write a pattern: `$environments: [_]: {replicas: *1 | int}`.

## Typing the rows

pravo fills `$environment` while it loads, in a file that is not on disk, so an editor sees no declaration of it. Declare it yourself, with a definition of the row that `$environments` uses too. The editor then knows `$environment` and its fields, and a row that misses a field or gives it a value of the wrong type is refused:

```cue
#Row: pravo.#Environment & {
	workers: int & >=1
	region:  string
}

$environments: [string]: #Row
$environments: pravo.#Environments & {
	dev: {workers: 1, region: "eu-north-1", $default: true}
	prod: {workers: 3, region: "eu-north-1", $after: "dev"}
}

$environment: #Row
```

`pravo.#Environment` brings `$after`, `$default` and `$name`. pravo reads `#Row` and `$environment` with the rows, before the imports resolve, as it reads `$settings`, so they may use `pravo.` definitions and your own, and nothing else that comes from an import.

## Selecting an environment

`--env NAME` selects a row. Before the specification is evaluated, pravo fills `$environment` with that row and with `$name`. Every other field reads the row from there. Without `--env`, the row marked `$default: true` is used; a specification whose rows have no default stops and lists them. `--env NAME` is the tag `env`, so `-t env=NAME` selects the same row, and a command line where the two name different environments is refused.

`env` is not a tag of the specification. pravo takes it off the tags it gives CUE, and a field that carries `@tag(env)` is refused when `$environments` is declared, so that the environment has one source. The other tags (`-t image=1.4`) work as in [Tags and variables](spec-tags-and-variables.md).

A name is letters, digits, `.`, `-` and `_`, starts with a letter or a digit, and has at most 63 characters, so that it goes into a path, a bucket prefix and a command line without quoting. Quote a name with a dash in CUE: `"prod-eu": {...}`.

pravo keeps no current environment. Each command evaluates the specification with the tags it is given, so `plan`, `apply`, `destroy`, `output` and the `state` commands each take `--env NAME`. A saved plan or a stored plan carries the tags it was made with.

## What each environment has of its own

`$settings` is read once for each environment, so a setting written with `$environment.$name` differs between them.

| Setting | Use |
|---|---|
| `state` | A state of its own: `path: "state/\($environment.$name)"` or `prefix: "app/\($environment.$name)"`. Two environments with one state are refused. |
| `state.profile` | The AWS profile that the bucket of this environment is opened with. |
| `state.token_env` | The environment variable that holds the token of an HTTP state server. |
| `access.dir` | The directory of the readers file, so that a writer of staging is not a writer of prod. |

The names of a profile and of a variable are settings; a credential is not. [Settings](spec-settings.md) describes each one.

A [`pravo.#RemoteOutputs`](spec-env.md#remote-outputs) reads the state of another root. Write `$environment.$name` into its location, and each environment reads the outputs of the same environment there: `state: path: "../network/state/\($environment.$name)"`.

A [discovery](../concepts/discovery.md#with-environments) runs in the environment that `--env` names and keeps its findings in that environment's state. `pravo discover adopt` writes the resource that adopts an object inside a condition on `$environment.$name`.

## The promotion flow

`$after` orders the environments into a promotion flow. An environment may follow several, and several may follow one:

File `main.cue`:

```cue
package estate

import "pravo.dev/pravo"

$environments: pravo.#Environments & {
	dev: {zone: "a", $default: true}
	staging: {zone: "b", $after: "dev"}
	"prod-eu": {zone: "c", $after: "staging"}
	"prod-us": {zone: "d", $after: "staging"}
	audit: {zone: "e", $after: ["prod-eu", "prod-us"]}
}

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

```sh
pravo environments | head -n 1
```

Output:

```text
dev → staging → {prod-eu, prod-us} → audit
```

`audit` takes a release only when both `prod-eu` and `prod-us` run it.

## Checks

Every command that loads the specification checks the declaration first. It refuses:

- a `$environments` that is not a struct of structs, or that declares no environment;
- a name that is not valid;
- an `$after` that names the environment itself, names an environment that is not declared, names one twice, or is an empty list;
- environments that follow one another in a circle;
- two rows with `$default: true`;
- two environments whose state is in one place;
- a `--env NAME` that names no environment, and a field that carries `@tag(env)`.

`pravo validate` without `--env` evaluates the specification once for each environment and says which ones are valid. It also refuses a row that misses a field that another row has, and a field of a row that has no concrete value, which a plan of that environment would otherwise stop at. `pravo validate --env NAME` checks one.

File `main.cue`:

```cue
package estate

import "pravo.dev/pravo"

$environments: pravo.#Environments & {
	dev: {zone: "a", $default: true}
	staging: {zone: "b", $after: "dev"}
	"prod-eu": {zone: "c", $after: "stagign"}
}

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

```sh
pravo validate
```

Output:

```text
Error: $environments.prod-eu.$after names "stagign", which is not an environment (they are dev, staging and prod-eu)
```

## The gate

An environment with `$after` takes only a release that was promoted into it, once its state is sealed. The release is the specification of the directory, named by its fingerprint, and the values of its tags other than `env`. `pravo promote` signs a promotion into the state of the environment, carrying the signed notes that the environments before it run the release, and `pravo apply` checks it against the readers file of that state and each note against the readers file of the environment it comes from. [pravo promote](promote.md) lists the checks, and [Promote a release from one environment to the next](../how-to/promote-a-release.md) runs them.

A release that changes nothing in the environment passes the gate without a promotion: there is nothing to hold back. Its plan prints no `Promotion:` line, and its apply goes through without noting that the environment runs the release, so `pravo environments` still shows the release that was last promoted there, and an environment after it still waits for a promotion of the new one. A plan that defers a change to the next plan changes the environment, so it needs the promotion.

A state that is not sealed takes any release: it has no signatures to check. The plan and the apply of such an environment say so, on a line that starts with `Environment:`. An environment without `$after` takes any release.

## The record of an environment

Each state of an environment keeps a record of its place in the promotion flow, as a chain of signed versions below `environment/`: the environment it serves, the environments it follows, the lineage of each of their states, and, in a sealed state whose roles are stored, the pin of those roles (the `roles-genesis` line of its readers file, [Roles](roles.md)). The first apply writes it, and an apply fills in the pin of a record written before there was one.

An apply is refused with exit code 5 when:

- the record names another environment: the settings point the run at a state of another environment;
- the record says that the environment follows others than the specification says: an edit of `$after`, which would add or drop a gate;
- the record pins the roles of the state, and the readers file of the run pins other roles or none: an edit of `access.dir` that points the environment at a readers file whose roles are not the signed ones, which could make a writer an admin.

A plan is refused for the same reasons, and so is every other command that writes the records or the state: `import`, `state rm`, `state mv`, `state import-tfstate`, `discover`, and `secret set`, `rm` and `purge`.

The refusal names the command that makes the record agree, `pravo environments record --env NAME`, which an admin of the state runs ([pravo environments](environments.md#pravo-environments-record)). Once the record pins the roles, the admins who may change it are those of the roles it pins, whatever the readers file of the run says. A writer adds the lineage of an environment before when the first promotion reads it, and the pin of the roles when an apply finds none, and nothing else.

A promotion checks the notes of an environment before against that environment's readers file, with the roles its state holds when the file pins them. When the record of that state pins its roles and the file does not pin those roles, the promotion is refused, so pointing the environment before at another readers file does not make anyone a writer there.

Each apply also writes a note, below `runs/`, that says which release the state runs, the git revision it was applied from, and who applied it. `pravo environments` reads the newest note of each state, and `pravo promote` carries the notes of the environments before into a promotion. In a sealed state the record, the notes and the promotions are signed and checked against the readers file. [State format](state-format.md) lists the objects.
