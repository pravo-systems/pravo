---
title: Run pravo in a pipeline
description: Validate without credentials, plan under a policy, apply a saved or a stored plan, wait for an approval, find drift, test offline and keep a trace of each job, with the exit code of every step.
order: 1
minutes: 15
needs: pravo and `jq` on the runner. B7 teaches saved plans and B11 teaches policies.
done_when: "A plan job saves a plan under a policy, an apply job applies that file, and each step ends with the exit code in the table."
level: intermediate
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: 'created [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8}'
    replace: 'created <time>'
  - pattern: 'sha256:[0-9a-f]{12}'
    replace: 'sha256:<digest>'
group: Operate
keywords: "github actions gitlab ci pipeline exit code"
---

A pipeline needs four answers: does the specification evaluate, what changes, what exactly was applied and did anything change outside pravo. Each is a command with an exit code.

Work in an empty directory. It stands for the repository. The root is `app/`. It declares a policy, `no_destroy`, in `app/policy.cue`, and its state is sealed to one reader so that the policy can be signed. Every command uses `-C app`, which runs pravo as if it started in `app/`.

```cue file=app/main.cue title="app/main.cue"
package ci

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

// Pinned, so that a pipeline cannot pick up a new major version unnoticed.
$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider & {$version: "~> 2.5"}

server: $random.#Resource.random_pet & {length: 2}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "\(server.id)\n"
}
```

```cue file=app/policy.cue title="app/policy.cue"
package ci

import "pravo.dev/pravo"

no_destroy: pravo.#Policy & {
	rule: {
		// A managed resource is neither deleted nor replaced.
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}
		}]
	}
}
```

## 1. Check without credentials

`pravo init` installs the providers and generates the packages that `validate`, `plan` and `apply` need. `pravo validate` evaluates the specification against them. It starts no provider and needs no credentials.

```sh run
test -z "$(pravo -C app fmt)" && echo "formatted"
pravo -C app init > /dev/null
pravo -C app validate
```

```text out
formatted
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

`pravo fmt` prints the name of each file it rewrites, so a clean tree prints nothing and `test -z` passes. A mistake in the specification gives exit code 6 from `validate`.

```cue file=app/bad.cue title="app/bad.cue" status=rejected
package ci

server: length: "two"
```

```sh run exit=6
pravo -C app validate
```

```text out
Error: evaluating ./app:
server.length: conflicting values "two" and 2 (mismatched types string and int):
    ./app/bad.cue:3:17
    ./app/main.cue:12:49
```

`plan` and `apply` report the same error with exit code 6. A pipeline tells a mistake in the code from a failed plan by the code.

```sh run
rm app/bad.cue
```

```sh run hidden
mkdir -p app/.pravo/access
pravo -C app id new --name ada | grep '^  ada ssh' | sed 's/^  //' > app/.pravo/access/readers.txt
pravo -C app protect init --encrypt --yes --allow-local
```

## 2. Ask whether a change is pending

Apply once. Then `plan -detailed-exitcode` exits 0 when the plan is empty and 2 when it has changes.

```sh run
pravo -C app apply -auto-approve | grep '^Apply complete'
pravo -C app plan -detailed-exitcode > /dev/null
echo "exit=$?"
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
exit=0
```

Add a resource.

```cue file=app/extra.cue title="app/extra.cue"
package ci

extra: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo -C app plan -detailed-exitcode > /dev/null
echo "exit=$?"
```

```text out
exit=2
```

A job reads the code from a command that is not in a pipe, because a pipe hides it.

Exit code 0 means that the plan found no change among the resources it looked at: the ones a change reaches and the ones they read. It does not mean that every resource matches its object. A plan does not read the other records, unless it is asked to with `-refresh=expired` or `-refresh=all` or a resource sets a refresh age of its own that has run out, so it is not a drift detector. Step 5 finds drift.

## 3. Read a plan as data

Every command accepts `--json`. Standard output carries the data, and progress goes to standard error. `plan` prints one document.

```sh run
pravo -C app plan --json 2>/dev/null | jq -c '.summary, [.changes[] | select(.action != "no-op") | {addr, action}]'
```

```text out
{"add":1,"change":0,"destroy":0,"read":0,"blast_radius":1}
[{"addr":"extra","action":"create"}]
```

The `addr` of a change is the address of the resource. A pipeline can post the summary on a pull request or compare a number with a budget. `apply` prints one JSON event per line and ends with a summary. A plain `apply --json` that has changes to make needs `-auto-approve`, because there is no prompt.

## 4. Plan, then apply the file

The plan job saves a plan. The apply job applies that file, and the apply judges it with the policies in the state.

```sh run
pravo -C app plan -out change.plan --actor ci --reason "build 42" | grep -E '^(Plan|Policies|Saved)'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Policies: 1 satisfied.
Saved the plan to change.plan. To apply exactly these actions, run:
```

`--actor` and `--reason` are recorded on the plan and on the transaction. They are labels that the caller supplies and pravo does not verify. `pravo show` prints the plan with its label, and `--json` gives the same as data.

```sh run
pravo -C app show change.plan | head -n 1
```

```text out
Saved plan created <time> by "pravo -C app plan --out change.plan --actor ci --reason build 42" (actor ci)
```

A saved plan is checked against the specification, the provider versions and the state it touches before anything runs. Change the specification after the plan was made.

```cue file=app/extra.cue title="app/extra.cue" hl="3"
package ci

extra: $random.#Resource.random_pet & {length: 3}
```

```sh run exit=3
pravo -C app apply change.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (extra.cue changed)
```

Exit code 3 means that the plan is stale. Plan again and apply the new file.

```sh run
pravo -C app plan -out change.plan --actor ci --reason "build 43" > /dev/null
pravo -C app apply change.plan
```

```text out
extra: Creating...
extra: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The apply judged the file under the locks, and the signed start of its transaction lists the policy versions that judged it. A plain `pravo apply` judges the plan it builds. [Write and roll out policies](policies.md) covers the policy.

A plan file travels between jobs as an artifact. When the jobs share a state in a bucket, the plan job can store the plan in the state, and the apply job can name it by its id. With `--json`, the id is `stored.id` in the one document that the plan job prints. A plan with no changes is not stored and has no `stored` field.

```text
plan job       pravo plan --store --json --actor ci --reason "$REV" > plan.json
               jq -r .stored.id plan.json
apply job      pravo apply "$PLAN_ID"
```

Both jobs need the same checkout, because the apply compares the files, and the same `-t` and `-T`. A stored plan expires after seven days, or after the `--ttl` of the plan job. A stored plan is also where a person comes in: the team lists it with `pravo plan list` or in the viewer, and where a policy asks for approvals, a member approves it with `pravo plan approve ID` from their own machine. The apply job runs once the approvals are there, and exit code 5 with `needs N approvals` before that means that it runs again later. [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) follows a stored plan from the plan to the apply, and [Approve a plan](approve-a-plan.md) adds the approval.

## 5. Find drift on a schedule

`pravo drift` reads every resource from its provider, ignores refresh ages and exits 2 when something changed outside pravo. Delete the file behind pravo's back.

```sh run exit=2
rm app/note.txt
pravo -C app drift
```

```text out
Drift. 1 of 4 resources changed outside pravo:
  note: create
    - refresh found the real state differs from the state (drift)
```

With `-webhook <url>` the report is also posted as JSON, so a scheduler can hand drift to whoever triages it. With `--plan --store` the job also stores the plan that puts the drift right, and the webhook receives an event with its id: [Let drift propose the fix](let-drift-propose-the-fix.md).

`pravo drift` reads every record, and an estate of many records takes long to read. Two other jobs read fewer.

- `pravo drift --due --limit N` reads the records that are past their refresh age, the oldest first, at most `N` of them, and plans nothing. Each run costs what it reads. A job that runs it every few minutes reads the whole estate in turn, and a record that was never read goes first. It exits 2 when it finds drift.
- `pravo plan -refresh=expired` reads every record that is past its refresh age and plans the specification as well. Use it where a job must see both.

A read that finds drift leaves a mark on the record. The next plan, saved plan and apply act on it, whichever job found it, and it stays until an apply writes the resource.

## 6. Test without providers or credentials

A job that only asks whether a change to the specification still produces the expected plan needs no provider and no credentials. Record the responses once. Replay them in every run.

Record a plan from an empty state in a scratch copy of the root. The copy has no sealed state, so it leaves the policy out.

```sh run
mkdir record
cp app/*.cue record
rm record/policy.cue
pravo -C record init > /dev/null
PRAVO_RECORD=recordings pravo -C record plan | grep -E '^Plan'
ls record/recordings
```

```text out
Plan: 4 to add, 0 to change, 0 to destroy.
local
random
```

The `recordings` directory holds the provider's responses, one directory per provider. A response carries the attributes of the objects the provider returned, the sensitive ones included, and the result of an ephemeral resource. A recording is as sensitive as state, so handle it as you handle state. pravo creates its directories with mode 0700 and its files with mode 0600. It does not write the values of a request, so the credentials in a provider configuration and write-only attributes are not in it.

Version control keeps no file modes. Commit the recording with the specification only when no resource in it returns a secret. Otherwise keep it where the pipeline keeps state, and fetch it before the job. A fresh clone of the repository with an empty provider store replays the recordings.

```sh run
mkdir clone empty-home
cp record/*.cue clone
cp -r record/recordings clone
PRAVO_HOME=$PWD/empty-home PRAVO_REPLAY=recordings pravo -C clone init
PRAVO_HOME=$PWD/empty-home PRAVO_REPLAY=recordings pravo -C clone plan -detailed-exitcode | grep -E '^Plan'
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Plan: 4 to add, 0 to change, 0 to destroy.
```

The clone has an empty provider store, and replay answers from the recordings. No provider binary was downloaded:

```sh run
test -z "$(ls -A empty-home)" && echo "the provider store is empty"
```

```text out
the provider store is empty
```

A change that alters what pravo asks the provider is a missing recording.

```sh run exit=1
sed 's/length: 3/length: 5/' clone/extra.cue > clone/extra.tmp
mv clone/extra.tmp clone/extra.cue
PRAVO_HOME=$PWD/empty-home PRAVO_REPLAY=recordings pravo -C clone plan
```

```text out
Error: extra: planning: PlanResourceChange random_pet: no recording for this request (replaying from recordings/random)
```

When the change is intended, record again and store the new recordings where the old ones are. When it is not, you caught it before a real provider saw it. Replay shows that pravo and the specification still agree on the requests. It does not show that a real provider behaves the same; `pravo drift` on a schedule covers that.

## 7. Refuse a replacement

Change `server` so that the provider has to replace it.

```cue file=app/main.cue title="app/main.cue" hl="12"
package ci

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

// Pinned, so that a pipeline cannot pick up a new major version unnoticed.
$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider & {$version: "~> 2.5"}

server: $random.#Resource.random_pet & {length: 3}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "\(server.id)\n"
}
```

```sh run
pravo -C app plan -out replace.plan > plan.txt
echo "exit=$?"
grep -E '^(Plan|Error|changes)' plan.txt
test -f app/replace.plan || echo "no plan file"
```

```text out
exit=5
Plan: 2 to add, 0 to change, 1 to destroy.
Error: the plan violates the policy no_destroy@1 (sha256:<digest>):
changes.0: server: replace is not allowed:
no plan file
```

The plan is printed first and judged afterwards. Exit code 5 means that a policy refused the run, and no plan file was written. `apply` judges the same way, under the locks, and applies nothing. With `--json`, standard output carries the plan document and then the error object, so a consumer takes the object whose `kind` is `error`.

## Exit codes

| Code | Meaning | Where it appears |
|---|---|---|
| 0 | Success. For `plan -detailed-exitcode`, no changes. | Every command. |
| 1 | An error: a missing recording, `apply --json` without `-auto-approve`, a provider or state error. | Step 6. |
| 2 | Changes pending (`plan -detailed-exitcode`) or drift found (`drift`). | Steps 2 and 5. |
| 3 | A saved plan is stale, or a node that an apply writes or reads moved after the plan was made. A stored plan that expired is refused with this code too. A job plans again and retries. | Step 4. |
| 4 | Rejected: a node is locked or reserved by another actor. A job retries later. | [Let an agent plan and apply](agents-and-mcp.md). |
| 5 | Refused: by a policy, by a CUE module that does not match its lock, by a role that does not allow the command, or for the approvals a stored plan still needs. | Steps 4 and 7. |
| 6 | A command that loads the specification found that it does not load or evaluate. | Step 1. |
| 7 | The state failed verification: a record does not match its name. A job stops and reports it. | [Seal the state](seal-the-state.md). |

With `--json`, an error also prints `{"code":N,"kind":"error","message":"..."}` on standard output. The lookup page is [Exit codes](../reference/exit-codes.md).

## A job that reads a sealed state

When the state is [sealed](seal-the-state.md), a job that plans or applies opens it with an identity of its own. Make one with `pravo id new --ci --name deploy-bot --out deploy-bot.identity`, add its public line to `.pravo/access/readers.txt`, run `pravo protect rewrap`, and store the contents of the file as the secret `PRAVO_IDENTITY` of the job. pravo reads the variable once, removes it from its environment, and starts providers without it. A job that has no identity can run `pravo state list` and gets exit code 5 from any command that needs a value. The job checks out the repository, because a command that writes a sealed state reads the readers file from it.

Give each job the smallest [role](give-people-roles.md) its work needs, as the fifth word of its line: the plan job is a reader, `role:reader`, which plans and stores and cannot apply, and the apply job is a writer, `role:writer`. No job is an admin, because an admin changes who reads the state and applies without the approvals a policy asks for. A job that holds a reader's identity and tries to apply gets exit code 5, as a job without an identity does.

## Keep a trace of each job

A job that fails leaves a log, and a job that is slow leaves a number. `PRAVO_TRACES=on` keeps more: the trace of each command, every stage, resource, provider call and request to the state with its timing, in `.pravo/traces` of the checkout. Save that directory as an artifact of the job, and `pravo serve` on any checkout of the repository shows the run as a timeline, with the span that failed in red. A team that has a collector sets `OTEL_EXPORTER_OTLP_ENDPOINT` instead, and the `TRACEPARENT` that the CI system sets makes each command a child of the job's span. [Record what a run does](record-what-a-run-does.md) shows both.

## One pipeline

The commands of this page, in the jobs that run them:

```text
pull request   pravo init; test -z "$(pravo fmt)"; pravo validate
               PRAVO_REPLAY=recordings pravo plan --json > plan.json
plan job       pravo plan -out change.plan --actor ci --reason "$REV"
               or, with a shared state: pravo plan --store --json --actor ci --reason "$REV" | jq -r .stored.id
apply job      pravo apply change.plan
               or, once the approvals a policy asks for are there: pravo apply "$PLAN_ID"
schedule       pravo drift --due --limit 500 --actor drift-cron
```

`validate`, `plan` and `apply` need the generated packages under `cue.mod/gen`. A job runs `pravo init` first, or the repository commits `cue.mod/gen`. A policy is a node of the root, so a change to it is a change that review sees, and the version it writes is signed by whoever applies it. The apply job is defined where the authors of the roots cannot change it unreviewed.

## What next

- [Write and roll out policies](policies.md) writes a policy and tests it.
- [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) stores a plan in the state, where a team can list it and apply it by its id, and [Approve a plan](approve-a-plan.md) makes it wait for a colleague.
- [Give people roles](give-people-roles.md) gives the plan job and the apply job their identities.
- [Let an agent plan and apply](agents-and-mcp.md) adds reservations and the tool server.
- [Record what a run does](record-what-a-run-does.md) keeps the trace of a job, and [Find out why a run fails](debugging.md) reads the log of one that failed.
