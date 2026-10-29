---
title: Start from the policy pack
description: Import the starter rules of pravo.dev/policies, restrict changes to a window, freeze them for a period, and try a rule at another time with policy test --at.
order: 8
minutes: 10
needs: B11 for the idea of a policy, and an empty directory.
done_when: "`pravo policy test hours --at` holds on a Wednesday morning and is refused with exit code 5 on a Saturday, and `pravo state verify` verifies the apply that stored the policy."
level: intermediate
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'sha256:[0-9a-f]{64}'
    replace: 'sha256:<digest>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '(hours@1:)[0-9a-f]{64}'
    replace: '$1<digest>'
  - pattern: '(?m)^  signed by ada, .*\n'
    replace: '  signed by ada\n'
group: Operate
---

`pravo.dev/policies` is a CUE package of starter rules. A policy names one of its rules and sets its parameters. This page adds a change window and a freeze, and tries them at other times than now, before they are stored.

Work in an empty directory. The root is `root/`, and its state is local and sealed to one reader, ada, because a policy version is signed. The root has one resource.

```cue file=root/main.cue title="root/main.cue"
package windowed

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh run
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo -C root init > /dev/null
pravo -C root protect init --encrypt --yes --allow-local > /dev/null
pravo -C root apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

## Add the rules

Import the package and declare two policies. `pravo init` writes the package to `cue.mod/gen/pravo.dev/policies`, beside `pravo.dev/pravo`, and the parameters of a rule are definitions such as `#days`.

```cue file=root/main.cue title="root/main.cue"
package windowed

import (
	"pravo.dev/pravo"
	"pravo.dev/policies"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 3}

hours: pravo.#Policy & {
	description: "Changes on Monday to Thursday, 08:00 to 16:00 UTC."
	rule: policies.#ChangeWindow & {
		#days:  ["Monday", "Tuesday", "Wednesday", "Thursday"]
		#from:  8
		#until: 16
	}
}

holidays: pravo.#Policy & {
	description: "No changes over the end of the year."
	rule: policies.#Freeze & {
		#from:  "2026-12-24T00:00:00Z"
		#until: "2027-01-04T00:00:00Z"
	}
}
```

The change of length replaces the pet. Store the plan. The two policies are new, so no policy judges this plan yet:

```sh run
pravo -C root plan --store 2>&1 | grep -E '^(Policies|Stored)'
```

```text out
Policies: +2 new, ~0 changed, -0 removed.
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
```

## Try them at other times

A policy judges a plan at a moment, which the plan document gives as `now`. At an apply, `now` is the start of the transaction. `pravo policy test` uses the time of the command, and `--at` takes another. 2026-11-04 is a Wednesday:

```sh run
pravo -C root policy test --at 2026-11-04T10:00:00Z
```

```text out
Policy holidays (the specification)
  ✓ plan-<id>  pending  holds
  1 of 1 judged plan hold.

Policy hours (the specification)
  ✓ plan-<id>  pending  holds
  1 of 1 judged plan hold.
```

A Saturday is refused, and so is the hour at which the window ends:

```sh run exit=5
pravo -C root policy test hours --at 2026-11-07T10:00:00Z
```

```text out
Policy hours (the specification)
  ✗ plan-<id>  pending  would be refused
      changes.0: server: replace outside the change window, Saturday 10:00 UTC
      change server
  0 of 1 judged plan hold.

Error: 1 plan would be refused
```

```sh run
pravo -C root policy test hours --at 2026-11-04T16:00:00Z | grep changes.0
```

```text out
      changes.0: server: replace outside the change window, Wednesday 16:00 UTC
```

The window is in UTC. It includes the hour `#from` and excludes the hour `#until`. The freeze holds from `#from` up to, and not including, `#until`:

```sh run exit=5
pravo -C root policy test holidays --at 2026-12-25T10:00:00Z
```

```text out
Policy holidays (the specification)
  ✗ plan-<id>  pending  would be refused
      changes.0: server: replace during the freeze from 2026-12-24T00:00:00Z to 2027-01-04T00:00:00Z
      change server
  0 of 1 judged plan hold.

Error: 1 plan would be refused
```

## Judge by the time of the apply

`pravo apply` of this plan stores both policies. From then on every plan that changes a resource is judged at the start of its apply, and the signed start holds that time. `pravo state verify` judges the plan again at that time, so the verdict it checks is the one the apply had. [The plan document in a policy](../reference/policies-plan-document.md#the-clock) says what `now` holds, and [The policy pack](../reference/policies-pack.md) lists every rule and its parameters.

## Recap

- A policy names a rule of `pravo.dev/policies` and sets its parameters, which are definitions such as `#days`.
- The rule is copied into the stored version, so the version is standalone and signed.
- A rule reads the time as `now` in the plan document. `pravo policy test --at` tries it at another time.
