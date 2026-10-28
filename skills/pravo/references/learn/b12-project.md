<!-- Generated from docs/learn/b12-project.md by tools/skillgen. Do not edit. -->

# Project: an estate in three environments

Build three environments from one definition, with a policy, a secret, a drift check and a destroy. A script checks the outcomes.

This page lists eight outcomes and gives a script that checks them. It gives no steps. You build an estate, run the script, read the failure it names, and repeat.

The estate has three environments: dev, staging and prod. Each has three buckets. A bucket is a file, so nothing needs a cloud account, and a key stands in for a credential. Everything runs on the `local`, `random` and `tls` providers.

## The outcomes

Each outcome is a property of your estate. The number is the number in the script.

1. **Three environments.** The tag `env` takes the values `dev`, `staging` and `prod`. Any other value is refused before a provider starts.
2. **Buckets from one rule.** Each environment has the buckets `logs`, `assets` and `backups`. A bucket is the file `out/<env>/<name>.bucket` with a line `owner <team>` and a line `retention <days>`. One definition describes all of them. Each environment is applied from a saved plan and keeps its own state.
3. **Retention.** In prod, a bucket keeps data for at least 90 days. A bucket with 30 days is refused in prod and accepted in dev.
4. **Mode.** Every file has mode `0600`. A resource built through the provider `$files` with another mode is a conflict.
5. **Credentials.** The token in the variable `ESTATE_TOKEN` reaches a provider and never the state. The root does not run without the variable.
6. **Policy.** The policy `no_delete` refuses plans that delete. The states are sealed so that a policy can be signed. `pravo destroy` of prod under it exits with 5 and removes nothing.
7. **Drift.** A file that someone deletes is found by `pravo drift`, which exits with 2. An apply restores it, and `pravo drift` then exits with 0.
8. **Destroy.** All three environments can be destroyed, and no file remains.

Two more outcomes are yours to confirm. The script does not look for them.

- The bucket is a definition in its own package under `estate/lib`. It takes its providers as inputs, as in [B6](b06-reuse-providers-credentials.md).
- Renaming a bucket with `$from` plans `0 to add, 0 to change, 0 to destroy`. The plan says that the record will move, as in [B8](b08-renames-moves-imports.md).

## What the script relies on

The script runs your commands, so it needs names. These are the names.

| Name | Value |
|---|---|
| Root | The directory `estate`, with its own `cue.mod` and the package name `estate`. |
| Policy | A `pravo.#Policy` named `no_delete`, in the file `estate/policy.cue`. |
| Environment | The tag `env`. |
| State of an environment | `estate/state/<env>`. |
| Buckets | A struct `buckets`. An entry of it has `owner` and `retention`. |
| Pinned provider | A provider struct named `$files`. |
| Secret | The variable `ESTATE_TOKEN`. |

The state path depends on `env`, so `pravo init` needs a default value for the tag. Write `env: *"dev" | "staging" | "prod" @tag(env)`.

## The check

Work in an empty directory. Save the script as `check.sh` and read it before you run it. It applies and destroys all three environments in `estate`.

File `check.sh`:

```sh
# check.sh: verifies the outcomes of the project. Run it from the directory
# that holds estate/. It seals, applies and destroys all three environments.
fail() { echo "FAIL $1: $2"; exit 1; }
pass() { echo "ok   $1: $2"; }
P="pravo -C estate"
export ESTATE_TOKEN=check-secret-value
export PRAVO_PASSPHRASE="a project passphrase" PRAVO_CONFIG_DIR=$PWD/people/ada

# 0. A policy version is signed, so each state is sealed to one reader.
mkdir -p estate/.pravo/access
pravo -C estate id new --name ada | grep '^  ada ssh' | sed 's/^  //' > estate/.pravo/access/readers.txt
for e in dev staging prod; do
	$P protect init --encrypt --yes --allow-local -t env=$e >/dev/null 2>&1 || fail 0 "the state of $e is not sealed"
done

# 1. Three environments, and nothing else.
for e in dev staging prod; do
	$P validate -t env=$e >/dev/null 2>&1 || fail 1 "validate -t env=$e does not succeed"
done
$P validate -t env=qa >/dev/null 2>&1 && fail 1 "env=qa is accepted"
pass 1 "dev, staging and prod validate, and qa is refused"

# 2. Three buckets in each environment, each applied from a saved plan.
for e in dev staging prod; do
	$P plan -t env=$e --out $e.plan >/dev/null 2>&1 || fail 2 "plan -t env=$e does not succeed"
	$P apply -t env=$e $e.plan >/dev/null 2>&1 || fail 2 "apply $e.plan does not succeed"
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
$P destroy --auto-approve -t env=prod >/dev/null 2>&1
test $? -eq 5 || fail 6 "destroy under the policy does not exit with 5"
test -f estate/out/prod/logs.bucket || fail 6 "the refused destroy removed a file"
$P plan -t env=prod >/dev/null 2>&1 || fail 6 "a plan without changes is refused by the policy"
pass 6 "destroy of prod exits with 5, and a plain plan passes"

# 7. Drift is found and reconciled.
rm estate/out/prod/logs.bucket
$P drift -t env=prod >/dev/null 2>&1
test $? -eq 2 || fail 7 "drift does not exit with 2 after a file is deleted"
$P apply -auto-approve --refresh=all -t env=prod >/dev/null 2>&1 || fail 7 "apply does not restore the file"
test -f estate/out/prod/logs.bucket || fail 7 "the file is not back"
$P drift -t env=prod >/dev/null 2>&1 || fail 7 "drift is found after the reconcile"
pass 7 "drift exits with 2, then with 0 after an apply"

# 8. Everything is destroyed. The policy refuses a destroy, so it goes first.
mv estate/policy.cue policy.cue.off
for e in dev staging prod; do
	$P apply --auto-approve -t env=$e >/dev/null 2>&1 || fail 8 "apply -t env=$e does not remove the policy"
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

## What each outcome practises

| Outcome | Lesson |
|---|---|
| 1. Three environments | [B4](b04-inputs-tags-environments.md) |
| 2. Buckets from one rule | [B5](b05-many-resources-from-one-rule.md), [B6](b06-reuse-providers-credentials.md), [B7](b07-changing-live-infrastructure.md) |
| 3. Retention | [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html), [A14](https://pravo.systems/learn/a14-project-three-environments.html) |
| 4. Mode | [B11](https://pravo.systems/learn/b11-policy.html) |
| 5. Credentials | [B6](b06-reuse-providers-credentials.md) |
| 6. Policy | [B11](https://pravo.systems/learn/b11-policy.html) |
| 7. Drift | [B9](b09-drift.md) |
| 8. Destroy | [B3](b03-apply-outputs-state.md) |

## Recap

- Outcomes are properties. Several designs meet them, and the script accepts all of them.
- A check script is an exercise whose verdict is a program. It runs your commands and reports what it observed.
- Faults and their exit codes are in [Lab 1](https://pravo.systems/learn/lab1-break-it-on-purpose.html). Concurrent applies are in [Lab 2](https://pravo.systems/learn/lab2-two-applies-at-once.html). What a copy of the state bucket shows, and what sealing changes, is in [Lab 3](https://pravo.systems/learn/lab3-the-leaked-bucket.html).

You have written a specification, planned it, applied it, moved it and watched it drift. You bound it with a policy and destroyed it. [Migrate](https://pravo.systems/migrate/index.html) applies the same steps to a project that already exists. The [How-to](https://pravo.systems/how-to/index.html) pages cover CI, remote state and publishing CUE modules.
