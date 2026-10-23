---
title: "Project: an estate in three environments"
description: Build three environments from one definition, with a policy, a secret, a drift check and a destroy. A script checks the outcomes.
order: 54
group: Track B · Labs and project
minutes: 60
needs: B10
learn: Can you combine modules, saved plans, moves, drift and policy without being told the steps?
done_when: "`sh check.sh` prints `All outcomes hold.` and exits 0."
level: advanced
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
---

This page lists eight outcomes and gives a script that checks them. It gives no steps. You build an estate, run the script, read the failure it names, and repeat.

The estate has three environments: dev, staging and prod. Each has three buckets. A bucket is a file, so nothing needs a cloud account, and a key stands in for a credential. Everything runs on the `local`, `random` and `tls` providers.

## The outcomes

Each outcome is a property of your estate. The number is the number in the script.

1. **Three environments.** The tag `env` takes the values `dev`, `staging` and `prod`. Any other value is refused before a provider starts.
2. **Buckets from one rule.** Each environment has the buckets `logs`, `assets` and `backups`. A bucket is the file `out/<env>/<name>.bucket` with a line `owner <team>` and a line `retention <days>`. One definition describes all of them. Each environment is applied from a saved plan and keeps its own state.
3. **Retention.** In prod, a bucket keeps data for at least 90 days. A bucket with 30 days is refused in prod and accepted in dev.
4. **Mode.** Every file has mode `0600`. A resource built through the provider `$files` with another mode is a conflict.
5. **Credentials.** The token in the variable `ESTATE_TOKEN` reaches a provider and never the state. The root does not run without the variable.
6. **Policy.** `policy/no-delete.cue` refuses plans that delete. `pravo destroy` of prod under it exits with 5 and removes nothing.
7. **Drift.** A file that someone deletes is found by `pravo drift`, which exits with 2. An apply restores it, and `pravo drift` then exits with 0.
8. **Destroy.** All three environments can be destroyed, and no file remains.

Two more outcomes are yours to confirm. The script does not look for them.

- The bucket is a definition in its own package under `estate/modules`. It takes its providers as inputs, as in [B6](b06-modules-providers-credentials.md).
- Renaming a bucket with `$from` plans `0 to add, 0 to change, 0 to destroy`. The plan says that the record moved, as in [B8](b08-renames-moves-imports.md).

## What the script relies on

The script runs your commands, so it needs names. These are the names.

| Name | Value |
|---|---|
| Root | The directory `estate`, with its own `cue.mod` and the package name `estate`. |
| Policy | `policy/no-delete.cue`, beside `estate`. |
| Environment | The tag `env`. |
| State of an environment | `estate/state/<env>`. |
| Buckets | A struct `buckets`. An entry of it has `owner` and `retention`. |
| Pinned provider | A provider struct named `$files`. |
| Secret | The variable `ESTATE_TOKEN`. |

The state path depends on `env`, so `pravo init` needs a default value for the tag. Write `env: *"dev" | "staging" | "prod" @tag(env)`.

## The check

Work in an empty directory. Save the script as `check.sh` and read it before you run it. It applies and destroys all three environments in `estate`.

```sh file=check.sh title="check.sh"
# check.sh: verifies the outcomes of the project. Run it from the directory
# that holds estate/ and policy/. It applies and destroys all three environments.
fail() { echo "FAIL $1: $2"; exit 1; }
pass() { echo "ok   $1: $2"; }
P="pravo -C estate"
POLICY=../policy/no-delete.cue
export ESTATE_TOKEN=check-secret-value

# 1. Three environments, and nothing else.
for e in dev staging prod; do
	$P validate -t env=$e >/dev/null 2>&1 || fail 1 "validate -t env=$e does not succeed"
done
$P validate -t env=qa >/dev/null 2>&1 && fail 1 "env=qa is accepted"
pass 1 "dev, staging and prod validate, and qa is refused"

# 2. Three buckets in each environment, each applied from a saved plan.
for e in dev staging prod; do
	$P plan -t env=$e --out $e.plan >/dev/null 2>&1 || fail 2 "plan -t env=$e does not succeed"
	$P apply $e.plan >/dev/null 2>&1 || fail 2 "apply $e.plan does not succeed"
	for b in logs assets backups; do
		f=estate/out/$e/$b.bucket
		test -f $f || fail 2 "$f does not exist"
		grep -q '^owner ' $f || fail 2 "$f has no owner line"
		grep -q '^retention [0-9]' $f || fail 2 "$f has no retention line"
	done
	test -d estate/state/$e || fail 2 "the state of $e is not in estate/state/$e"
done
pass 2 "nine bucket files, one state directory per environment"

# 3. Production keeps data for at least 90 days. Development does not.
probe() { printf 'package estate\n\nbuckets: probe: {owner: "check", retention: %s}\n' "$1" > estate/zz-probe.cue; }
probe 30
$P validate -t env=dev >/dev/null 2>&1 || { rm -f estate/zz-probe.cue; fail 3 "dev refuses retention 30"; }
$P validate -t env=prod >/dev/null 2>&1 && { rm -f estate/zz-probe.cue; fail 3 "prod accepts retention 30"; }
rm -f estate/zz-probe.cue
pass 3 "prod refuses retention 30 and dev accepts it"

# 4. Every file has mode 0600, and the provider refuses another mode.
test -z "$(find estate/out -type f ! -perm 600)" || fail 4 "a file does not have mode 0600"
printf 'package estate\n\nprobe: $files.#Resource.local_file & {filename: "probe.txt", content: "x", file_permission: "0644"}\n' > estate/zz-probe.cue
$P validate -t env=dev >/dev/null 2>&1 && { rm -f estate/zz-probe.cue; fail 4 "mode 0644 is accepted through \$files"; }
rm -f estate/zz-probe.cue
pass 4 "mode 0600 on disk, and 0644 is a conflict"

# 5. The token reaches a provider and never the state.
if grep -rq "$ESTATE_TOKEN" estate/state; then fail 5 "the token is in the state"; fi
( unset ESTATE_TOKEN; $P plan -t env=dev >/dev/null 2>&1 ) && fail 5 "plan succeeds without ESTATE_TOKEN"
pass 5 "the token is not in the state, and the root needs it"

# 6. The policy refuses to destroy production and changes nothing.
$P destroy -t env=prod --policy $POLICY >/dev/null 2>&1
test $? -eq 5 || fail 6 "destroy under the policy does not exit with 5"
test -f estate/out/prod/logs.bucket || fail 6 "the refused destroy removed a file"
$P plan -t env=prod --policy $POLICY >/dev/null 2>&1 || fail 6 "an empty plan is refused by the policy"
pass 6 "destroy of prod exits with 5, and a plain plan passes"

# 7. Drift is found and reconciled.
rm estate/out/prod/logs.bucket
$P drift -t env=prod >/dev/null 2>&1
test $? -eq 2 || fail 7 "drift does not exit with 2 after a file is deleted"
$P apply -auto-approve --refresh=all -t env=prod >/dev/null 2>&1 || fail 7 "apply does not restore the file"
test -f estate/out/prod/logs.bucket || fail 7 "the file is not back"
$P drift -t env=prod >/dev/null 2>&1 || fail 7 "drift is found after the reconcile"
pass 7 "drift exits with 2, then with 0 after an apply"

# 8. Everything is destroyed.
for e in dev staging prod; do
	$P destroy --auto-approve -t env=$e >/dev/null 2>&1 || fail 8 "destroy -t env=$e does not succeed"
	test -z "$(find estate/out/$e -type f 2>/dev/null)" || fail 8 "files remain in out/$e"
done
pass 8 "no file remains"
echo "All outcomes hold."
```

When every outcome holds, the script prints this and exits with 0.

```text
ok   1: dev, staging and prod validate, and qa is refused
ok   2: nine bucket files, one state directory per environment
ok   3: prod refuses retention 30 and dev accepts it
ok   4: mode 0600 on disk, and 0644 is a conflict
ok   5: the token is not in the state, and the root needs it
ok   6: destroy of prod exits with 5, and a plain plan passes
ok   7: drift exits with 2, then with 0 after an apply
ok   8: no file remains
All outcomes hold.
```

When one does not, it prints `FAIL`, the number and a sentence, and stops. The sentence names what the script observed. The script hides the output of the commands it runs. To read the error behind a `FAIL`, run the command that the sentence names. Set `ESTATE_TOKEN` in the shell first. An example is `pravo -C estate plan -t env=dev`.

:::exercise Build the estate
Make `sh check.sh` print `All outcomes hold.`

::hint
Build in layers, and run `pravo -C estate validate -t env=prod` after each one. Start with the tag and the state path. Add the buckets from one pattern, then the definition in its own package. Add the mode pin, the secret and the policy last.

::hint
The tag is in [B4](b04-inputs-tags-environments.md) and the pattern in [B5](b05-many-resources-from-one-rule.md). The module, the secret and the `tls` provider that carries it are in [B6](b06-modules-providers-credentials.md). The mode pin and the policy are in [B10](b10-policy.md).

::solution
One estate that meets the outcomes. The module comes first.

```cue file=estate/cue.mod/module.cue title="estate/cue.mod/module.cue"
module: "example.com/estate@v0"
language: version: "v0.14.0"
```

```cue file=estate/modules/bucket/bucket.cue title="estate/modules/bucket/bucket.cue"
package bucket

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

// A bucket is a file with an owner, a retention in days and a serial number.
#Bucket: {
	random!: rnd.#Provider
	local!:  loc.#Provider

	environment: string
	name:        string & =~"^[a-z][a-z0-9-]*$"
	owner!:      string
	retention:   int & >=1

	serial: random.#Resource.random_integer & {min: 1000, max: 9999, seed: "\(environment)-\(name)"}
	file: local.#Resource.local_file & {
		filename: "out/\(environment)/\(name).bucket"
		content:  "owner \(owner)\nretention \(retention)\nserial \(serial.result)\n"
	}
}
```

The root builds the buckets from one pattern. It pins the mode on `$files` and carries the token in the `tls` provider.

```cue file=estate/main.cue title="estate/main.cue"
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/modules/bucket"
)

env: *"dev" | "staging" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}

token: pravo.#SecretEnv & {name: "ESTATE_TOKEN"}

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files:  #Strict
$random: rnd.#Provider
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "deploy"
		password: token.value
	}]
}

signing: $tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}

minimum: {dev: 1, staging: 30, prod: 90}

buckets: [Name=string]: bucket.#Bucket & {
	random:      $random
	local:       $files
	environment: env
	name:        Name
	retention:   >=minimum[env]
}

buckets: {
	logs:    {owner: "platform", retention: 90}
	assets:  {owner: "web", retention: 365}
	backups: {owner: "platform", retention: 180}
}
```

The policy refuses deletes.

```cue file=policy/no-delete.cue title="policy/no-delete.cue"
// A plan may create and change. It may not delete.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && action == "delete" {
		error("\(addr): delete is not allowed")
	}
}]
```

```sh run
pravo -C estate init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
  tls = hashicorp/tls v4.4.2
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/tls (4 resource types, 2 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
sh check.sh
```

```text out
ok   1: dev, staging and prod validate, and qa is refused
ok   2: nine bucket files, one state directory per environment
ok   3: prod refuses retention 30 and dev accepts it
ok   4: mode 0600 on disk, and 0644 is a conflict
ok   5: the token is not in the state, and the root needs it
ok   6: destroy of prod exits with 5, and a plain plan passes
ok   7: drift exits with 2, then with 0 after an apply
ok   8: no file remains
All outcomes hold.
```

```sh run check
test -z "$(find estate/out -type f)"
```
:::

## What each outcome practises

| Outcome | Lesson |
|---|---|
| 1. Three environments | [B4](b04-inputs-tags-environments.md) |
| 2. Buckets from one rule | [B5](b05-many-resources-from-one-rule.md), [B6](b06-modules-providers-credentials.md), [B7](b07-changing-live-infrastructure.md) |
| 3. Retention | [A5](a05-constraints-narrow-a-value.md), [A14](a14-project-three-environments.md) |
| 4. Mode | [B10](b10-policy.md) |
| 5. Credentials | [B6](b06-modules-providers-credentials.md) |
| 6. Policy | [B10](b10-policy.md) |
| 7. Drift | [B9](b09-drift.md) |
| 8. Destroy | [B3](b03-apply-outputs-state.md) |

## Recap

- Outcomes are properties. Several designs meet them, and the script accepts all of them.
- A check script is an exercise whose verdict is a program. It runs your commands and reports what it observed.
- Faults and their exit codes are in [Lab 1](lab1-break-it-on-purpose.md). Concurrent applies are in [Lab 2](lab2-two-applies-at-once.md).

:::check
Which outcome do you confirm by reading a plan, and not by running the script?
::answer
The rename. A plan that renames a bucket with `$from` says that the record moved and changes nothing.

Why does the script pass `ESTATE_TOKEN` itself?
::answer
The root reads the variable on every plan and apply. The script sets it for its own commands and never writes it down.
:::

You have written a specification, planned it, applied it, moved it and watched it drift. You bound it with a policy and destroyed it. [Migrate](../migrate/index.md) applies the same steps to a project that already exists. The [How-to](../how-to/index.md) pages cover CI, remote state and publishing modules.
