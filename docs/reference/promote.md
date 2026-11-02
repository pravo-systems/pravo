---
title: pravo promote
description: Bring the release that the environments before one run into it, apply it there, withdraw a promotion or waive the check. Flags, checks, roles and exit codes.
order: 23
group: Commands
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
keywords: "promotion release gate pipeline staging prod waive revoke sign-only ladder"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo promote ENVIRONMENT [-t key=value]... [--note TEXT] [--sign-only] [--auto-approve]`, `--waive REASON`, `--revoke REASON` |
| Changes infrastructure | Yes, unless `--sign-only` or `--revoke`: it applies the release as `pravo apply -t env=ENVIRONMENT` does. |
| Starts providers | For the apply only. Reading the environments before needs none. |
| Writes | A promotion below `promotions/` of the state of ENVIRONMENT, then what the apply writes. |
| Requires | A row of `$environments` with `$after`, and a sealed state for ENVIRONMENT. A writer of that state; an admin for `--waive` and `--revoke`. |
| Taught in | [Promote a release from one environment to the next](../how-to/promote-a-release.md). |

An environment whose row in `$environments` names others in `$after` takes only a release that was promoted into it, once its state is sealed ([Environments](spec-environments.md#the-gate)). The release is the specification of the directory and the values of its tags, other than `env`. Its id is the fingerprint of the specification, as [`pravo environments`](environments.md) shows it.

`promote` reads the newest note of each environment that ENVIRONMENT follows. A note is written by each apply into the state it applies, signed by whoever applied, and says which release that state runs. Reading it needs no key and no provider. When every one runs the release of this directory, `promote` signs a promotion that carries those notes into the state of ENVIRONMENT, and applies it there. The other tags are those of the release:

```sh
pravo apply   -t env=staging -t image=1.4
pravo promote prod -t image=1.4
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--note TEXT` | none | A line of text, kept with the promotion. |
| `--sign-only` | off | Sign the promotion and stop. `pravo apply -t env=ENVIRONMENT` applies it later, by anyone who may write the state. |
| `--auto-approve` | off | Apply without asking. |
| `--waive REASON` | none | Promote without the notes of the environments before. An admin gives the reason. |
| `--revoke REASON` | none | Withdraw the promotions of this release that were signed before. An admin gives the reason. Nothing is applied. |
| `-t`, `--tag key=value` | none | The tags of the release. `-t env=NAME`, when given, names ENVIRONMENT again. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE. |
| `--access-dir DIR` | see below | The directory of the readers file of ENVIRONMENT. |
| `--refresh`, `--full` | as for `plan` | How the apply reads the resources, as in [pravo plan](plan.md). |

`--waive` and `--revoke` exclude each other. The readers file of ENVIRONMENT is the one that `--access-dir`, `PRAVO_ACCESS_DIR`, `$settings.access.dir` of that environment or `.pravo/access` names, in this order. The readers file of each environment before it is the one that its own `$settings.access.dir` names, else `.pravo/access`.

## What is checked

Before it signs, `promote` checks:

| Check | Refused when |
|---|---|
| The environment | ENVIRONMENT is not declared, has no `$after`, has no state yet or a state that is not sealed, or `-t env` names another one. |
| The record | The record in the state of ENVIRONMENT says that it serves another environment, or follows others ([Environments](spec-environments.md#the-record-of-an-environment)). |
| The role | The identity is not a writer of the state of ENVIRONMENT, or not an admin for `--waive` and `--revoke`. |
| Each environment before | Its state cannot be read, is not sealed, has no note, or its newest note is for another release, is signed by a key that its readers file does not make a writer, or is dated more than five minutes ahead. |

An apply of ENVIRONMENT, by `promote` or by `pravo apply`, checks again, under the lock of the state: the promotion against the readers file of ENVIRONMENT, and each note it carries against the readers file of the environment it comes from, with the lineage of that state that the record holds. A promotion signed before the newest withdrawal of the release does not count. A refusal lists the commands that move the release along:

```text
Error: prod only takes what was promoted from dev, and nobody has promoted this release yet.

Run these, skipping a step that is already done:

    pravo apply -t env=dev
    pravo promote prod
```

The plan of an environment that follows another says how its release stands, on a line that starts with `Promotion:`. Before a promotion it lists the same commands as the refusal of an apply. A state that is not sealed takes any release, and the plan and the apply say so on a line that starts with `Environment:`.

## A waiver

When the state of an environment before cannot be read, an admin of ENVIRONMENT promotes with `--waive "reason"`. The promotion is signed without the notes, holds for this release only, and its reason is written into the note of the apply, so that the environments after ENVIRONMENT see it. A writer who is not an admin is refused with exit code 5.

## A withdrawal

`--revoke "reason"` writes a withdrawal of the release into the state of ENVIRONMENT. Every promotion of the release signed before it stops counting, and an apply is refused with the reason until a new promotion. A release that ENVIRONMENT already runs stays applied: a withdrawal stops the next apply, and a fix is a new release.

## Example

One person, ada, is an admin of both environments, and one readers file serves both, which `promote` points out.

```cue file=main.cue title="main.cue"
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$environments: pravo.#Environments & {
	dev: {$default: true}
	prod: {$after: "dev"}
}

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}

$local: loc.#Provider

conf: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "image 1.0\n"
}
```

```sh run hidden
mkdir -p .pravo/access
pravo id new --name ada > /dev/null
echo "$(pravo id show | tail -n 1 | sed 's/^ *//') role:admin" > .pravo/access/readers.txt
pravo init > /dev/null
pravo protect init --encrypt --allow-local --yes -t env=dev > /dev/null
pravo protect init --encrypt --allow-local --yes -t env=prod > /dev/null
pravo apply --auto-approve > /dev/null
```

```sh run
pravo promote prod --sign-only --note "smoke tests passed"
```

```text out
Promoted to prod: dev runs this release, applied by ada less than a minute ago.
Note: dev and prod read one readers file, so a writer of dev may promote into prod. $settings.access.dir gives prod a file of its own.
Apply it there with:

    pravo apply -t env=prod
```

```sh run
pravo apply -t env=prod --auto-approve | grep -E '^(Promotion|Apply)'
```

```text out
Promotion: dev ran this release, and ada promoted it less than a minute ago.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

With `--json`, `promote` prints one line, `{"kind": "promotion", "promotion": {...}}`, with the kind (`promoted`, `waived` or `revoked`), the environment, the fingerprint and the tags of the release, the notes it carries (`from`: the environment, the lineage of its state, the fingerprint, the transaction and the time of each), the time, the signer, its key and the note. The lines of the apply follow it.

## Exit codes

| Code | When |
|---|---|
| 0 | The release was promoted, and applied unless `--sign-only` or `--revoke`. |
| 1 | An error: a flag, a state that cannot be opened, a failure of the apply. |
| 5 | Refused: an environment before does not run the release or its note does not hold, a state is not sealed, a role is too low, the record of the state disagrees with the specification. |
| 6 | The specification is not valid, ENVIRONMENT is not one of its environments, or `--waive` and `--revoke` are given together. |
| 7 | The state of ENVIRONMENT or its roles do not verify. |

The codes are those of [Exit codes](exit-codes.md).
