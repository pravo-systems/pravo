---
title: Promote a release from one environment to the next
description: Hold prod to what dev runs. Give each environment its own readers, promote a release with a signed note, withdraw a promotion, and waive the check when dev cannot be read.
order: 6
minutes: 15
needs: "[Run one specification in several environments](environments.md) and a sealed state, as in [Seal the state](seal-the-state.md). The two people are two directories on one machine."
done_when: "`pravo apply -t env=prod` is refused with exit code 5 until `pravo promote prod` has signed the release that dev runs."
level: intermediate
group: Share and protect
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'AAAAC3NzaC1lZDI1NTE5AAAA[A-Za-z0-9+/]{44}'
    replace: '<ssh-key>'
  - pattern: 'x25519:[A-Za-z0-9_-]{43}'
    replace: 'x25519:<key>'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\b[0-9a-f]{12}\b'
    replace: '<release-id>'
  - pattern: '\b[0-9]s ago'
    replace: '0s ago'
keywords: "promotion promote release gate staging prod pipeline approval waive revoke ladder environments"
---

An environment whose row names another in `$after` takes only a release that was promoted into it, once its state is sealed. A promotion is signed by a writer of that environment, and it carries the signed note that the environment before runs the release. `pravo apply` checks both: the promotion against the readers of the state it writes, and the note against the readers of the environment it comes from.

Each environment has a readers file of its own, so a writer of dev is not a writer of prod.

| Person | dev | prod |
|---|---|---|
| ada | admin | admin |
| dan | writer | reader |

> [!NOTE]
> The states on this page are directories, so that the page runs on one machine, and the two people are two identity directories that `PRAVO_CONFIG_DIR` picks. A team keeps each state in a bucket ([Keep the state in a shared store](keep-the-state-in-a-shared-store.md)). A command without a prefix is ada's.

## 1. Declare the ladder

Prod follows dev. `access.dir` gives each environment the directory of its readers file.

```cue file=main.cue title="main.cue" hl="9-10,16"
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	prod: {workers: 3, $after: "dev"}
}

// Each environment has its own state and its own readers file.
$settings: pravo.#Settings & {
	state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"
}

$local: loc.#Provider

conf: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "workers \($environment.workers)\nimage 1.0\n"
}
```

## 2. Give each environment its readers

Ada and dan each make an identity. The readers file of dev makes dan a writer, the one of prod a reader.

```sh run hidden
mkdir -p .pravo/access/dev .pravo/access/prod
pravo id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan > /dev/null
ADA=$(pravo id show | tail -n 1 | sed 's/^ *//')
DAN=$(PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//')
printf '%s role:admin\n%s role:writer\n' "$ADA" "$DAN" > .pravo/access/dev/readers.txt
printf '%s role:admin\n%s role:reader\n' "$ADA" "$DAN" > .pravo/access/prod/readers.txt
```

```sh run
cat .pravo/access/dev/readers.txt .pravo/access/prod/readers.txt
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:writer
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:reader
```

Seal each state to its readers. `-t env=NAME` selects the state, and with it the readers file.

```sh run
pravo init > /dev/null
pravo protect init --encrypt --allow-local --yes -t env=dev | grep '^The state'
pravo protect init --encrypt --allow-local --yes -t env=prod | grep '^The state'
```

```text out
The state is sealed to ada dan.
The state is sealed to ada dan.
```

## 3. Apply dev

Dan applies dev, the default environment.

```sh run
PRAVO_CONFIG_DIR=people/dan pravo apply --auto-approve | grep '^Apply complete'
pravo environments
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
dev → prod

ENV   FOLLOWS  RUNS          GIT  SINCE   BY
dev   -        <release-id>       0s ago  dan  runs this directory
prod  dev      -                               nothing applied yet; ready to promote

This directory: <release-id>
Next: pravo promote prod
```

The release is the specification of this directory with its tags, named by its id. Every apply writes a note into its state, signed by whoever applied it, that says which release the state runs. The row of dev says that it runs this one.

## 4. Prod refuses an apply that was not promoted

```sh run
pravo apply -t env=prod --auto-approve > apply.txt 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' apply.txt
```

```text out
exit=5
Error: prod only takes what was promoted from dev, and nobody has promoted this release yet.

Run these, skipping a step that is already done:

    pravo apply -t env=dev
    pravo promote prod
```

Ada is an admin of prod, and the apply is refused all the same: the role decides who may promote, and the promotion decides what prod takes.

## 5. Promote the release

`pravo promote prod` reads the note of dev, checks it against the readers file of dev, signs a promotion into the state of prod and applies it there. Whoever promotes needs to be a writer of prod. Dan is a reader there:

```sh run
PRAVO_CONFIG_DIR=people/dan pravo promote prod --auto-approve; echo "exit=$?"
```

```text out
Error: your identity is a reader in .pravo/access/prod/readers.txt, and a writer is needed to change the state.
exit=5
```

Ada promotes. `--note` keeps a line of text with the promotion.

```sh run
pravo promote prod --note "smoke tests passed" --auto-approve | grep -E '^(Promoted|Promotion|Apply)'
pravo environments | sed -n '3,5p'
```

```text out
Promoted to prod: dev runs this release, applied by dan less than a minute ago.
Promotion: dev ran this release, and ada promoted it less than a minute ago.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
ENV   FOLLOWS  RUNS          GIT  SINCE   BY
dev   -        <release-id>       0s ago  dan  runs this directory
prod  dev      <release-id>       0s ago  ada  runs this directory
```

## 6. A new release starts in dev

Change the image. The directory is a new release, which dev does not run yet, and prod refuses to take it.

```sh run
sed -i.bak 's/image 1.0/image 1.1/' main.cue && rm main.cue.bak
pravo promote prod; echo "exit=$?"
```

```text out
Error: dev does not run this release: it runs <release-id>, and this directory is <release-id>.

Run these, skipping a step that is already done:

    pravo apply -t env=dev
    pravo promote prod
exit=5
```

Dan applies dev. Ada signs the promotion with `--sign-only` and applies it later, which is the split a CI job uses: one step promotes, another applies. A plan of prod names the promotion it will be applied under.

```sh run
PRAVO_CONFIG_DIR=people/dan pravo apply --auto-approve | grep '^Apply complete'
pravo promote prod --sign-only
pravo plan -t env=prod | grep -E '^(Plan|Promotion)'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
Promoted to prod: dev runs this release, applied by dan less than a minute ago.
Apply it there with:

    pravo apply -t env=prod
Plan: 1 to add, 0 to change, 1 to destroy.
Promotion: dev ran this release, and ada promoted it less than a minute ago.
```

## 7. Withdraw a promotion

The smoke tests of 1.1 fail before prod is applied. An admin of prod withdraws the promotion, with the reason. Every promotion of the release signed before it stops counting.

```sh run
pravo promote prod --revoke "1.1 fails its smoke test"
pravo apply -t env=prod --auto-approve > apply.txt 2>&1; echo "exit=$?"
grep '^Error' apply.txt
```

```text out
Withdrawn: prod takes this release again only after a new promotion. The reason is kept in its state.
exit=5
Error: prod only takes what was promoted from dev, and its promotion was withdrawn by ada less than a minute ago: 1.1 fails its smoke test.
```

A new promotion of the same release lifts it.

## 8. Waive the check when dev cannot be read

A promotion needs the note of dev. When the state of dev cannot be read, an admin of prod can promote without it, with a reason. The waiver holds for this one release, and the reason is written into the note of the apply. Here the state of dev is moved away, as a bucket that does not answer would be.

```sh run
mv state/dev state/dev.away
sed -i.bak 's/image 1.1/image 1.2/' main.cue && rm main.cue.bak
pravo promote prod --waive "dev is being restored; hotfix for the outage" --auto-approve | grep -E '^(Waived|Promotion|Apply)'
mv state/dev.away state/dev
```

```text out
Waived: prod may take this release although dev was not checked. The reason is kept with the promotion and with the note of the apply.
Promotion: waived by ada less than a minute ago, without a check of dev: dev is being restored; hotfix for the outage
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

A writer who is not an admin cannot waive. Dev is behind prod now, and the next release starts there again.

## What next

- [pravo promote](../reference/promote.md) lists every flag, the checks of the apply and the exit codes.
- [Environments](../reference/spec-environments.md) describes `$after`, a list of environments to follow and the record that each state keeps of its place in the ladder.
- [Give people roles](give-people-roles.md) pins the roles of each readers file in its state.
- [CI](ci.md) runs one job per environment.
