---
title: Write and roll out policies
description: Write a policy rule, test it on fixtures with the matching cue version, try it on the plans you have, roll a new rule out as advice, exempt by type, apply under the policies of the state, ask for approvals and retire a policy.
order: 2
minutes: 25
needs: B10 for the idea of a policy. `cue` at the version that `pravo version` prints on its second line, `jq`, and an empty directory.
done_when: "`sh policy/test.sh` passes, a plan that replaces a pet is refused with exit code 5, and `pravo state verify` verifies the apply job."
level: advanced
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
  - pattern: 'sha256:[0-9a-f]{64}'
    replace: 'sha256:<digest>'
  - pattern: 'sha256:[0-9a-f]{12}'
    replace: 'sha256:<digest>'
  - pattern: '<sealed:[0-9a-f]+>'
    replace: '<sealed>'
  - pattern: '(guard@[0-9]+):[0-9a-f]{64}'
    replace: '$1:<digest>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} +[0-9.]+(µs|ms|s)'
    replace: '<date> <time> <took>'
group: Operate
---

A policy is a node of the root whose rule is a CUE struct. The first apply that carries it writes it to the state, and from then on every plan that changes a resource is judged by it. This page writes a rule, tests it, adds a rule without blocking anyone, and applies under the policies of the state.

Work in an empty directory. The root is `root/`, and its state is local and sealed to one reader, ada, because a policy version is signed. Every pravo command uses `-C root`.

```cue file=root/main.cue title="root/main.cue"
package guarded

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}
```

```sh run
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo -C root init > /dev/null
pravo -C root protect init --encrypt --yes --allow-local > /dev/null
pravo -C root apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## 1. Write the policy

The rule is evaluated against the plan document, the data that `pravo plan --json` prints. This one forbids a managed resource to be deleted or replaced and limits the size of a plan. It sits in a file of the root, beside the resources:

```cue file=root/policy.cue title="root/policy.cue"
package guarded

import "pravo.dev/pravo"

guard: pravo.#Policy & {
	description: "Rules for every plan."
	rule: {
		changes: [...{
			addr:   string
			mode:   string
			action: string

			// A managed resource is neither deleted nor replaced. A replacement is an
			// action of its own, so both are named.
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}
		}]

		// One plan adds at most five resources.
		summary: add: <=5
	}
}
```

The template declares every field that a guard reads, because CUE resolves names lexically. [B10](../learn/b10-policy.md) walks through the parts, and [Writing policies](../reference/policies-writing.md) lists the shapes that work and the ones that check nothing.

## 2. Test it on fixtures

A fixture is a plan document that the rule must accept or refuse. Make it from a throwaway copy of the root, so the real state stays as it is. The copy carries the state, so the plan is the one a real change produces. The policy is not applied yet, so the copy judges nothing.

```sh run
mkdir -p policy/fixtures
cp -r root scratch
printf 'package guarded\n\nextra: $random.#Resource.random_pet & {length: 2, prefix: "app"}\n' > scratch/extra.cue
pravo -C scratch plan --json 2>/dev/null > policy/fixtures/accept-create.json
rm -r scratch

cp -r root scratch
sed 's/length: 2/length: 3/' root/main.cue > scratch/main.cue
pravo -C scratch plan --json 2>/dev/null > policy/fixtures/refuse-replace.json
rm -r scratch
ls policy/fixtures
```

```text out
accept-create.json
refuse-replace.json
```

`pravo eval -e guard.rule --raw` prints the rule as it is written, comprehensions included, so the output is the file. `cue vet -c` judges a fixture with that file the way pravo judges a plan: it requires the result to be concrete.

```sh run
pravo -C root eval -e guard.rule --raw > policy/guard.cue
cue vet -c policy/guard.cue policy/fixtures/accept-create.json
echo "exit=$?"
cue vet -c policy/guard.cue policy/fixtures/refuse-replace.json
echo "exit=$?"
```

```text out
exit=0
changes.0: server: replace is not allowed:
    ./policy/guard.cue:9:3
exit=1
```

The message is the one pravo prints. The path `changes.0` is a position in the `changes` list. Then comes the text of `error()`, and then the line of the rule that wrote it.

The CUE inside the `pravo` binary evaluates a rule, so test it with the same version. `pravo version` prints that version on its second line.

```sh run
pravo version
cue version | head -n 1
```

```text out
pravo 0.1.0-dev
cue v0.17.1
cue version v0.17.1
```

With a Go toolchain, `go install cuelang.org/go/cmd/cue@v0.17.1` installs it. Use the version that `pravo version` prints. Put the checks in one script, so that CI runs what you run:

```sh file=policy/test.sh title="policy/test.sh"
#!/bin/sh
# Every rule has a plan it must accept and a plan it must refuse.
want=$(pravo version | awk 'NR==2 {print $2}')
have=$(cue version | awk 'NR==1 {print $3}')
[ "$want" = "$have" ] || { echo "cue $have does not match the cue $want inside pravo"; exit 1; }

pravo -C root eval -e guard.rule --raw > policy/guard.cue
for f in policy/fixtures/accept-*.json; do
	cue vet -c policy/guard.cue "$f" || { echo "refused, should be accepted: $f"; exit 1; }
done
for f in policy/fixtures/refuse-*.json; do
	if cue vet -c policy/guard.cue "$f" > /dev/null 2>&1; then
		echo "accepted, should be refused: $f"
		exit 1
	fi
done
echo "policy tests pass"
```

```sh run
sh policy/test.sh
```

```text out
policy tests pass
```

> [!PITFALL]
> A plan document is not masked. Sensitive attributes appear in it, in a fixture as in a refusal message. Do not write rules over secrets, and do not commit a fixture that has not been scrubbed.

## 3. Test the test

A misspelt literal in a guard forbids nothing, and pravo does not warn. Misspell `replace` and run the script.

```sh run exit=1
sed -i 's/action == "replace"/action == "relace"/' root/policy.cue
sh policy/test.sh
```

```text out
accepted, should be refused: policy/fixtures/refuse-replace.json
```

The fixture that must be refused was accepted, and the script says so. This is what fixtures are for. Other misspellings behave differently. A misspelt field with a concrete value, such as `summary: destory: 0`, is satisfied by every plan. A misspelt bound, type or pattern, such as `summary: ad: <=5`, is refused when the rule is added, because the document of an empty plan has no such field. Restore the correct file.

```sh run
sed -i 's/action == "relace"/action == "replace"/' root/policy.cue
sh policy/test.sh
```

```text out
policy tests pass
```

## 4. Add the policy

The apply writes the policy to the state. The plan that adds it is not judged by it. From then on a plan that replaces `server` is refused:

```sh run
pravo -C root plan | sed -n '/^Policies/,/^Graph/p'
pravo -C root apply -auto-approve | grep '^Apply complete'
sed -i 's/length: 2/length: 3/' root/main.cue
pravo -C root plan 2>&1 | sed -n '/^Error/,$p'
sed -i 's/length: 3/length: 2/' root/main.cue
```

```text out
Policies: +1 new, ~0 changed, -0 removed.
  + guard (sha256:<digest>)
Graph: planned 0 · skipped 2 · not read 2
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
Error: the plan violates the policy guard@1 (sha256:<digest>):
changes.0: server: replace is not allowed:
    guard:9:3
```

## 5. Roll a new rule out as advice

A new rule starts in a file of its own, outside the root. Evaluate it outside pravo, on a plan that already exists, in a step that the pipeline allows to fail. The rule: a pet that is created has a prefix.

```cue file=policy/advice.cue title="policy/advice.cue"
// A rule on trial: a pet that is created has a prefix.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: prefix: string & !=""
	}
}]
```

Add a pet without a prefix to the root and save the plan. The policy in force is satisfied. The advisory file judges the saved plan.

```cue file=root/extra.cue title="root/extra.cue"
package guarded

extra: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo -C root plan --out change.plan | grep -E '^(Plan|Policies)'
pravo -C root show change.plan --json | jq .plan | cue vet -c policy/advice.cue json: - 2>&1 | head -n 1
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Policies: 1 satisfied.
changes.0.after.prefix: conflicting values null and string (mismatched types null and string):
```

The policy in force is satisfied, and the advice names a pet that is created without a prefix. The plan job goes on. The rule judges what a plan does, so it does not report the pet that already exists. `pravo state query` evaluates a CUE expression over the state and lists the current violators.

```sh run
pravo -C root state query '[for a, r in resources if r.type == "random_pet" if r.attributes.prefix == null {a}]'
```

```text out
["server"]
```

`pravo policy test` asks the same question without `cue`, of a plan file or of the plans stored in the state, and says which plans the rule would refuse. It exits with 5 when one would be, so a pipeline can gate a draft on the plans the team has. The paths are relative to the root, as `-C` runs the command there:

```sh run exit=5
pravo -C root policy test ../policy/advice.cue --plan change.plan
```

```text out
Policy advice (../policy/advice.cue)
  ✗ change.plan  file  would be refused
      changes.0.after.prefix: conflicting values null and string (mismatched types null and string)
      change extra
  0 of 1 judged plan hold.

Error: 1 plan would be refused
```

The viewer asks the same question with an editor. Its policy studio, `s` in `pravo serve`, holds a rule against every stored plan, or against every resource of the state as if a plan updated it, and the verdict updates as you type. Nothing is applied or stored. [Explore an estate in a browser](explore-your-estate.md#11-see-the-policies-in-force-and-try-a-rule) has the picture, and [`pravo policy`](../reference/policy.md) the flags.

`server` must be replaced to get a prefix, and the policy refuses a replacement. Section 7 covers exceptions. Fix the new pet first.

```cue file=root/extra.cue title="root/extra.cue" hl="3"
package guarded

extra: $random.#Resource.random_pet & {length: 2, prefix: "app"}
```

```sh run
pravo -C root plan --out change.plan | grep -E '^(Plan|Policies)'
pravo -C root show change.plan --json | jq .plan | cue vet -c policy/advice.cue json: -
echo "exit=$?"
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Policies: 1 satisfied.
exit=0
```

## 6. Make it blocking

When the advice is quiet, move the rule into the policy and add the fixture that proves it. The edit is a change of the policy, so the apply that carries it is not judged by the new text: it creates `extra` under the version in force, and writes version 2.

```cue file=root/policy.cue title="root/policy.cue" hl="9,19-22"
package guarded

import "pravo.dev/pravo"

guard: pravo.#Policy & {
	description: "Rules for every plan."
	rule: {
		changes: [...{
			addr:   string
			mode:   string
			type:   string
			action: string

			// A managed resource is neither deleted nor replaced. A replacement is an
			// action of its own, so both are named.
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}

			// A pet that is created has a prefix.
			if type == "random_pet" && action == "create" {
				after: prefix: string & !=""
			}
		}]

		// One plan adds at most five resources.
		summary: add: <=5
	}
}
```

```sh run
cp -r root scratch
printf 'package guarded\n\nextra2: $random.#Resource.random_pet & {length: 2}\n' > scratch/extra2.cue
pravo -C scratch plan --json 2>/dev/null | jq -s '.[0]' > policy/fixtures/refuse-no-prefix.json
rm -r scratch
sh policy/test.sh
rm policy/advice.cue
pravo -C root apply --auto-approve | grep -E '^(Policies|Apply complete)'
```

```text out
policy tests pass
Policies: +0 new, ~1 changed, -0 removed.
Policies: +0 new, ~1 changed, -0 removed.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The test passes: the plan that creates `extra` with a prefix is accepted, and the plan that creates a pet without one is refused. Now the apply of a pet without a prefix is refused by version 2:

```sh run
printf 'package guarded\n\nextra2: $random.#Resource.random_pet & {length: 2}\n' > root/extra2.cue
pravo -C root plan 2>&1 | sed -n '/^Error/,$p'
rm root/extra2.cue
```

```text out
Error: the plan violates the policy guard@2 (sha256:<digest>):
changes.0.after.prefix: conflicting values null and !="" (mismatched types null and string):
    guard:14:2
    guard:16:12
    plan:1:115
```

## 7. Exempt by type

The specification cannot exempt a resource, and no flag waives a policy. An exemption is an edit to the rule, reviewed like any other edit. Key it to what does not move: the type, the provider or the recorded id. Do not key it to `addr` or `name`, which are labels the author chooses and which change with `$from`.

Allow `random_integer` resources to be replaced.

```cue file=root/policy.cue title="root/policy.cue" hl="14-15"
package guarded

import "pravo.dev/pravo"

guard: pravo.#Policy & {
	description: "Rules for every plan."
	rule: {
		changes: [...{
			addr:   string
			mode:   string
			type:   string
			action: string

			// A managed resource is neither deleted nor replaced, except a random_integer.
			if mode == "managed" && type != "random_integer" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}

			// A pet that is created has a prefix.
			if type == "random_pet" && action == "create" {
				after: prefix: string & !=""
			}
		}]

		// One plan adds at most five resources.
		summary: add: <=5
	}
}
```

```sh run
cp -r root scratch
sed 's/min: 8000/min: 7000/' root/main.cue > scratch/main.cue
pravo -C scratch plan --json 2>/dev/null | jq -s '.[0]' > policy/fixtures/accept-replace-port.json
jq -c '.changes[] | {addr, type, action}' policy/fixtures/accept-replace-port.json
rm -r scratch
sh policy/test.sh
pravo -C root apply --auto-approve | grep -E '^(Policies|Apply complete)'
```

```text out
{"addr":"port","type":"random_integer","action":"replace"}
policy tests pass
Policies: +0 new, ~1 changed, -0 removed.
Policies: +0 new, ~1 changed, -0 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
```

The replacement of `port` is accepted, and the replacement of a pet is still refused. A rule that has to be lifted for one run is retired: see section 9.

## 8. Apply under the policies

The plan job saves a plan. The apply job applies that file. Both are judged by the policies in the state, and neither names a policy.

```sh run
sed -i 's/min: 8000/min: 7000/' root/main.cue
pravo -C root plan --out change.plan --actor ci --reason "move the port" | grep -E '^(Plan|Policies|Saved)'
pravo -C root apply change.plan --actor ci | grep -E '^(Policies|Apply)'
```

```text out
Plan: 1 to add, 0 to change, 1 to destroy.
Policies: 1 satisfied.
Saved the plan to change.plan. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

The transaction log records the command line that was run. The signed start of the transaction lists the versions that judged the plan:

```sh run
pravo -C root state log | head -n 3
pravo -C root state log --json | jq -r '.[0].policies[]'
```

```text out
<txn>  apply    applied   <date> <time> <took>
    pravo -C root apply change.plan --actor <sealed>
    actor ci  move the port
guard@3:<digest>
```

A plan that is stored in the state is kept while its receipt may be checked. Store a plan, apply it by its id, and verify the transaction. The command checks the signatures, finds the plan, and judges it again with the listed versions:

```sh run
sed -i 's/min: 7000/min: 6000/' root/main.cue
pravo -C root plan --store | grep '^Stored'
ID=$(pravo -C root plan list | awk 'NR==1 {print $1}')
pravo -C root apply "$ID" | grep '^Apply complete'
pravo -C root state verify "$(pravo -C root state log --json | jq -r '.[0].id')"
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
Transaction <txn>
  signed by ada, pravo 0.1.0-dev
  plan      sha256:<digest>
  judged by 1 policy versions:
    guard@3:<digest>
Verified.
```

## 9. Retire a policy

No flag lifts a policy. A rule that has to go is removed from the root. The apply that removes it is not judged, and it writes a signed removal. With [roles](give-people-roles.md), that apply is an admin's: a writer adds and edits a policy, and only an admin removes one, or changes one that asks for approvals.

```sh run
rm root/policy.cue
pravo -C root plan | sed -n '/^Policies/,/^Graph/p'
pravo -C root apply --auto-approve | grep '^Apply complete'
ls root/pravo.state/policy
```

```text out
Policies: +0 new, ~0 changed, -1 removed.
  - guard (version 3)
Graph: planned 0 · skipped 3 · not read 3
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
guard@00000001.json
guard@00000002.json
guard@00000003.json
guard@00000004.json
```

## 10. Ask for approvals instead of refusing

A rule can ask for a second pair of eyes rather than say no. A rule that sets `approvals` on the plans it matches makes the apply of such a plan wait until enough other members have approved it with `pravo plan approve`:

```cue
// A plan that destroys a resource is applied once another writer has approved it.
careful_destroys: pravo.#Policy & {
	rule: {
		{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1}}
	}
}
```

The approvals belong to a stored plan, so a plan that such a rule matches is made with `pravo plan --store` and applied by its id, and an admin needs none. [Approve a plan](approve-a-plan.md) sets it up with three people, and [Operating policies](../reference/policies-operations.md#ask-for-approvals) lists the fields.

## What a policy does not bind

A policy binds the runs of pravo that read the state. A laptop, a shell that an agent holds and a job that calls the cloud API directly are not checked. [The trust anchor](../concepts/policies.md#the-trust-anchor) names the rights that decide what a policy guarantees, and [What a policy does not guarantee](../concepts/policies.md#what-a-policy-does-not-guarantee) lists the limits.

Review every change to the files that declare policies: the signed version names who applied it, and it does not say whether the change was reviewed. Define the apply job where authors of the roots cannot edit it, and hold the credentials that change infrastructure in that job only.

## What next

- [Approve a plan](approve-a-plan.md) makes a plan wait for the approval of a colleague.
- [Run pravo in a pipeline](ci.md) puts the plan and apply jobs in a pipeline with the exit codes.
- [Let an agent plan and apply](agents-and-mcp.md) shows the tool server under the same policies.
- [Explore an estate in a browser](explore-your-estate.md#11-see-the-policies-in-force-and-try-a-rule) shows the policies in force, their versions and the studio.
