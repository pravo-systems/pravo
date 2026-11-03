<!-- Generated from docs/reference/environments.md by tools/skillgen. Do not edit. -->

# pravo environments

Show what the state of each environment runs and how the release of this directory stands there, compare two environments, and record the place of a state in the promotion flow.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo environments [-t key=value]...`, `pravo environments diff A B`, `pravo environments record --env NAME` |
| Alias | `pravo envs` |
| Changes infrastructure | No. |
| Starts providers | No. `diff` reads the schemas that `pravo init` cached. |
| Writes | `record` only: a version of the record of an environment, below `environment/` of its state. |
| Requires | A specification that declares `$environments` ([Environments](spec-environments.md)). No key: a sealed state is read without one. |
| Taught in | [Run one specification in several environments](../how-to/environments.md) and [Promote a release from one environment to the next](../how-to/promote-a-release.md). |

## pravo environments

`pravo environments` reads the state of each environment, in the order of the promotion flow, and prints a row for each:

| Column | Meaning |
|---|---|
| `ENV` | The environment. |
| `FOLLOWS` | The environments its row names in `$after`, or `-`. |
| `RUNS` | The id of the release that its newest run note names: the fingerprint of the specification, cut to 12 characters. |
| `GIT` | The git revision the release was applied from, with `-dirty` for a working tree with changes. |
| `SINCE`, `BY` | When it was applied, and the name that the readers file of that environment gives the key that signed the note. A state that is not sealed signs nothing, and shows `-`. |
| the last column | How the release of this directory stands there, or what stands in the way. |

The last column says one or more of:

| Text | Meaning |
|---|---|
| `runs this directory` | Its newest note is for the release of this directory, with the same tags. |
| `runs this directory in part` | Its newest note is for the release, and its apply [deferred changes to the next plan](../concepts/plans.md#changes-deferred-to-the-next-plan). The next step is an apply there, and the environments after it are not ready. |
| `promoted by NAME`, `waived by NAME` | The release was promoted into it, or waived, and is not applied yet. |
| `ready to promote` | Every environment it follows runs the release. The next step is `pravo promote`. |
| `nothing applied yet` | The state has no note. |
| `runs this specification with other tags` | Same fingerprint, other values of the tags. |
| `not sealed, takes any release` | It follows another, and its state is not sealed, so nothing holds it to what that one runs. |
| a problem | The state cannot be read here, its record disagrees with the specification, or its note does not verify. |

Then it prints the release of this directory, with its tags, and the next command to type: an apply in the first environment that does not run the release and may take it, or the promotion into it. An environment whose settings or state cannot be read here, such as a bucket that these credentials cannot read, is shown as such, and the others still are.

| Flag | Default | Meaning |
|---|---|---|
| `-t`, `--tag key=value` | none | The tags of the release. Repeatable. |
| `--env NAME` | none | Does not narrow the list: every environment is read. With `record`, the environment whose record is written. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE. |
| `--access-dir DIR` | none | The directory of the readers file of the default environment, or of the one `--env` names. Every other environment reads the file that its `$settings.access.dir` names, else `.pravo/access`. |

With `--json` it prints `{"flow", "release", "environments", "next"}`: the promotion flow as text, the release (`fingerprint` and the names of its `tags`), one object for each environment (`name`, `after`, `default`, `state`, `sealed`, `runs`, `release`, `promoted`, `problem`) and the next command.

## pravo environments diff

`pravo environments diff A B` evaluates the specification once for each environment and compares the resources it declares: the ones only one of them has, and the fields whose values differ, by their dotted path. It compares the specification, not the states: `pravo plan --env NAME` compares one environment with what it runs. A field that the provider marks sensitive, or whose name says that it is a secret, is compared and shown as `differs (sensitive)`. A value known only at apply, such as the id of another resource, is shown as `(known at apply)`. The tags (`-t`) are those of both.

With `--json` it prints `{"a", "b", "resources", "same"}`, where each resource is `{"address", "only", "fields"}` and each field `{"path", "a", "b", "sensitive"}`.

## pravo environments record

The first apply in the state of an environment records which environment the state serves and which ones it follows, with the lineage of their states ([The record of an environment](spec-environments.md#the-record-of-an-environment)). An apply for another environment, or one whose row in `$environments` follows other environments, is refused with exit code 5, so that a state that a setting points at by mistake is not written over and a gate is not dropped by an edit of the specification. So is an apply whose readers file does not pin the roles that the record pins, so that an edit of `access.dir` does not change who is an admin. When the change is meant, `record` makes the record say what the specification and the readers file say:

```sh
pravo environments record --env prod
```

It prints `Recorded: this state serves prod, which follows staging.` When the state has a record, an admin of the state runs it: an admin of the roles the record pins, once it pins them. For a specification that declares no environments, it records that the state serves none. A state that does not exist yet is refused: its first apply records what it serves.

## Example

File `main.cue`:

```cue
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$environments: pravo.#Environments & {
	dev: {replicas: 1, $default: true}
	prod: {replicas: 3, $after: "dev"}
}

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}

$local: loc.#Provider

conf: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "replicas \($environment.replicas)\n"
}
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | grep '^Apply complete'
pravo environments
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
dev → prod

ENV   FOLLOWS  RUNS          GIT  SINCE   BY
dev   -        <release-id>       0s ago  -   runs this directory
prod  dev      -                              nothing applied yet; not sealed, takes any release

This directory: <release-id>
Next: pravo apply --env prod
```

```sh
pravo environments diff dev prod
```

Output:

```text
Comparing dev with prod:

  ~ conf
      content: "replicas 1\n" → "replicas 3\n"
      filename: "dev.conf" → "prod.conf"

1 resource differs; 0 are the same.
```

## Exit codes

| Code | When |
|---|---|
| 0 | The promotion flow was printed, even when an environment cannot be read. `diff` and `record` succeeded. |
| 1 | An error, such as a flag or a state that `record` cannot write. |
| 3 | `record` found that another command changed the record first. Run it again. |
| 5 | `record` refused: the state does not exist, or the identity is not an admin of a state that has a record. |
| 6 | The specification is not valid, declares no environments, or `diff` names one that it does not declare. |
| 7 | `record` found that the roles of the state do not verify. |
