---
title: Exit codes
description: The exit status of every pravo command, what each code means, and how --json reports an error.
order: 52
group: Data and formats
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'pravo.local/[A-Za-z0-9_.-]+'
    replace: 'pravo.local/ws'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
---

| Code | Name | Meaning |
|---|---|---|
| 0 | success | The command did what it was asked. For `plan --detailed-exitcode`: no changes. For `drift`: no drift. For `serve`: a signal stopped the viewer. |
| 1 | error | Any failure that has no code of its own: an unknown flag, a stray argument, a missing output, a provider or state error, an `apply --json` that has changes and no `--auto-approve`, an answer other than `yes`. |
| 2 | changes | `plan --detailed-exitcode` found changes, and a new, changed or removed policy counts as one. `drift` found drift. `discover show --exit-code` found an unmanaged object. This is a report and not a failure. |
| 3 | stale | A saved plan cannot be applied: the configuration, a provider or a node changed after it was made. An apply that finds a resource different from the plan, saved or printed by the same command, stops with this code too. A plan that adds, changes or removes a policy is refused with this code when the state holds another version of that policy. A plan stored in the state is refused with this code when it was not applied and its time to live ended, when a collection is removing it, or when it is [partly stale](apply.md#a-partly-stale-plan) and the apply includes a change that conflicts, as the whole plan does. No other failure to claim a stored plan has this code. |
| 4 | rejected | A node is locked by a running command, or reserved by another actor: before the apply changes anything, or, with `lock lost`, in the middle of an apply that another command took a lock from. The lock on the recorded outputs is held by a command that did not release it in time. A command that writes a sealed state is refused while a seal or an unseal is in progress. |
| 5 | refused | A policy refused the plan, the root declares a policy and the state is not sealed, or a module does not match its lock. In a sealed state: you are not a reader, or the readers file is missing for a command that writes the state ([`pravo protect`](protect.md#exit-codes) uses 6 for a missing file). In a repository that has a readers file: the state is not sealed, or does not exist yet, for a command that writes the state. In a sealed state with a recovery recipient: the recovery file and the state name different keys, or the file is missing, for a command that writes the state; and shares that do not rebuild the recovery key, for `pravo protect recover`. |
| 6 | invalid | A command that loads the specification found that it does not load or evaluate: a syntax error, a conflict, a file outside the package, a setting that is not allowed, resources that depend on each other in a circle. A plan that adds or changes a policy found that its rule cannot be used. `pravo validate` also exits 6 when a provider cannot be installed. `pravo schema` was asked for a type of a provider that is not declared. In a sealed state, a readers file that is invalid, and for `pravo protect` one that is missing. |
| 7 | untrusted | The state failed verification: an immutable record does not match its name, a pointer does not match its record, or a sealed value does not open where it is. A policy version has no signature or one that does not verify, or the numbers of a policy have a gap. `pravo state verify` found a signature, a plan digest or a listed version that does not hold. `pravo protect status` reports it and exits 7. A command that reads a record that fails the same check stops with this code. So do `pravo state log` and `pravo protect status` when the signature of a transaction of a sealed state does not verify. See [7: untrusted](#7-untrusted). |

In `--json` mode an error also prints `{"code":N,"kind":"error","message":"..."}` on standard output. The mismatch report of `pravo mod verify --json` is the exception: it prints `{"ok":false,"problems":[...]}` alone.

Work in an empty directory. The root is `infra`.

```cue file=infra/main.cue
package infra

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

hello: $local.#Resource.local_file & {filename: "hello.txt", content: "hello\n"}
```

```sh run
pravo -C infra init > /dev/null
```

## 0 and 2: success and changes

Without `--detailed-exitcode`, `plan` exits 0 whether or not it found changes. With the flag, it exits 2 when the plan has changes and 0 when it has none. Reads, ephemeral resources, function calls and moves are not changes.

```sh run
pravo -C infra plan --detailed-exitcode > /dev/null; echo "plan, one resource to create: $?"
pravo -C infra apply --auto-approve > /dev/null
pravo -C infra plan --detailed-exitcode > /dev/null; echo "plan, nothing to do: $?"
pravo -C infra drift > /dev/null; echo "drift, none found: $?"
echo changed > infra/hello.txt
pravo -C infra drift; echo "drift, found: $?"
```

```text out
plan, one resource to create: 2
plan, nothing to do: 0
drift, none found: 0
Drift. 1 of 1 resources changed outside pravo:
  hello: create
    - refresh found the real state differs from the state (drift)
drift, found: 2
```

A job reads the code with `case`:

```sh run
pravo -C infra plan --detailed-exitcode --out change.plan > /dev/null 2>&1
case $? in
  0) echo "nothing to apply" ;;
  2) echo "review change.plan, then apply it" ;;
  *) echo "the plan failed" ;;
esac
```

```text out
review change.plan, then apply it
```

## 1: error

```sh run
pravo -C infra output nope; echo "exit=$?"
pravo -C infra plan --nope; echo "exit=$?"
pravo -C infra apply --json 2>/dev/null | tail -n 1
```

```text out
Error: output "nope" is not recorded in state (run "pravo apply")
exit=1

Error: unknown flag: --nope
exit=1
{"code":1,"kind":"error","message":"apply with --json needs -auto-approve, or apply a reviewed plan file"}
```

A command that takes no argument refuses one. A flag whose value is optional, such as `-refresh`, takes it only in the form `-refresh=false`:

```sh run
pravo -C infra plan stray; echo "exit=$?"
pravo -C infra plan -refresh false; echo "exit=$?"
```

```text out
Error: pravo plan takes no arguments, but got "stray"
exit=1

Error: pravo plan takes no arguments, but got "false"; write the value of -refresh with =, as in -refresh=false
exit=1
```

## 3: stale

A saved plan is refused when the configuration changed, the state has another lineage, a provider has another version, or a node was written after the plan was made. A plan stored in the state with `pravo plan --store` is refused for the same reasons, and when it was not applied and its time to live ended. A stored plan is judged change by change: when only some of its changes conflict with the state, it lists as `partly-stale`, the apply of the whole plan is refused with this code, and `--only` with the set that `pravo plan list` prints applies the rest. See [A partly stale plan](apply.md#a-partly-stale-plan).

```sh run
echo 'extra: 1' >> infra/main.cue
pravo -C infra apply change.plan; echo "exit=$?"
sed -i.bak '$d' infra/main.cue
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
exit=3
```

A stored plan that was not applied and whose time to live ended is refused with the same code, until a collection removes it. After that its id is unknown, and the exit code is 1.

```sh run
pravo -C infra plan --store --ttl 2s --actor ci > /dev/null
sleep 3
id=$(pravo -C infra plan list --all --json | jq -r '.plans[0].id')
pravo -C infra apply $id; echo "exit=$?"
pravo -C infra plan gc > /dev/null
pravo -C infra apply $id; echo "exit=$?"
```

```text out
Error: plan-<id> expired a moment ago; plans are kept for a limited time: run "pravo plan --store" again
exit=3

Error: no such stored plan: plan-<id> (see: pravo plan list --all)
exit=1
```

## 4: rejected

A node that a running command holds, or that another actor has reserved, is refused to every other actor. The refusal comes before anything changes.

```sh run
pravo -C infra plan --reserve 15m --actor ci > /dev/null 2>&1
pravo -C infra plan --actor bob 2>&1 | grep '^Error' | cut -c1-70
pravo -C infra plan --actor bob > /dev/null 2>&1; echo "exit=$?"
pravo -C infra state unlock hello
```

```text out
Error: plan rejected: hello is reserved by ci (pravo -C infra plan --r
exit=4
Unlocked hello
```

## 5: refused

A plan that violates a policy is refused after it is printed. A root that declares a policy needs a sealed state, and a state that is not sealed is refused. Work in a second root, `guarded`, with a policy that refuses every change to a resource:

```cue file=guarded/main.cue
package guarded

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

hello: $local.#Resource.local_file & {filename: "hello.txt", content: "hello\n"}

freeze: pravo.#Policy & {rule: "changes: []"}
```

```sh run
pravo -C guarded init > /dev/null
pravo -C guarded plan 2>&1; echo "unsealed state: $?"
mkdir -p guarded/.pravo/access
pravo -C guarded id new --name ada | grep '^  ada ssh' | sed 's/^  //' > guarded/.pravo/access/readers.txt
pravo -C guarded protect init --encrypt --yes --allow-local > /dev/null
pravo -C guarded apply --auto-approve > /dev/null 2>&1
sed -i 's/hello\\n/changed\\n/' guarded/main.cue
pravo -C guarded plan > violation.txt 2>&1; echo "violation: $?"
grep '^Error' violation.txt
```

```text out
Error: the root declares policies, and a policy version is signed: the state must be sealed (pravo protect init --encrypt)
unsealed state: 5
violation: 5
Error: the plan violates the policy freeze@1 (sha256:cd6fab17493b):
```

A module that does not match the lock is exit 5 as well. Add an entry to the lock file for a module that nothing loads:

```sh run
echo 'modules: "example.com/x@v0": {version: "v0.1.0", hash: "h1:abc"}' >> infra/pravo.lock.cue
pravo -C infra mod verify; echo "exit=$?"
pravo -C infra mod verify --json; echo "exit=$?"
```

```text out
  - example.com/x@v0 is locked but no longer used

Error: 1 module(s) do not match the lock
exit=5
{
  "ok": false,
  "problems": [
    "example.com/x@v0 is locked but no longer used"
  ]
}
exit=5
```

## 6: invalid

Every command that loads the specification exits 6 when it does not load or evaluate. The error is the same for each of them:

```sh run
echo 'x: [' >> infra/main.cue
pravo -C infra plan > /dev/null 2>&1; echo "plan: $?"
pravo -C infra eval > /dev/null 2>&1; echo "eval: $?"
pravo -C infra validate > /dev/null 2>&1; echo "validate: $?"
sed -i.bak '$d' infra/main.cue
```

```text out
plan: 6
eval: 6
validate: 6
```

An error from the state, a provider, a lock or a plan has its own code. `pravo validate` starts no provider and changes nothing.

```cue file=infra/main.cue
package infra

size: 11
size: 3
```

```sh run
pravo -C infra validate; echo "exit=$?"
```

```text out
Error: evaluating ./infra:
size: conflicting values 3 and 11:
    ./infra/main.cue:3:7
    ./infra/main.cue:4:7

exit=6
```

`pravo schema` exits 6 for a type of a provider that is not declared:

```cue file=plain/main.cue
package plain

size: 3
```

```sh run
pravo -C plain schema random_pet; echo "exit=$?"
```

```text out
Error: no provider "random" for type random_pet; declare a provider struct for it, or name its address: NAMESPACE/TYPE/random_pet
exit=6
```

## 7: untrusted

A state in the immutable layout (format 2 and 3, made by [`pravo protect init`](protect.md#protect-init)) stores each record as an object named by the SHA-256 of its bytes, and a small pointer to it. A command that reads a record checks the name and the pointer. An object whose bytes do not hash to its name, a pointer whose address or revision differs from the record it points to, and a sealed value that does not open at the address it was sealed for, are not trusted. The command stops with exit code 7, names the object, and does not use it.

`pravo protect status` checks every record and exits 7 with a line `Untrusted:` for each object that fails, and for each transaction of a sealed state whose [signature](protect.md#signatures) does not verify against the readers file. `pravo state log` marks such a transaction and exits 7 too. A signature from a key that is not in the file, and a transaction without one, are reported and are not this code. A state in the original layout (format 1) has nothing to check, and the command does not exit 7 for it.

Code 7 means that the bytes in the store are not the bytes that pravo wrote. The causes are a write to the bucket by something other than pravo, a damaged copy, and a restore of part of a backup. Do not apply while it lasts. Restore the object from a version of the bucket or a backup that verifies, then run `pravo protect status` again.

## In an agent session

`pravo mcp` returns the exit code of the command a tool ran as `structuredContent.exit_code`, and sets `isError` when it is not 0. A refusal before a process runs has no `exit_code`. See [Operating policies](policies-operations.md#the-mcp-server).

## See also

- [Run pravo in a pipeline](../how-to/ci.md) uses each code in a job.
- [Global flags, environment and exit codes](global-flags.md) lists the flags every command accepts.
- [Error catalogue](errors.md) lists the messages behind each code.
