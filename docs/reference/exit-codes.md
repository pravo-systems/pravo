---
title: Exit codes
description: The exit status of every pravo command, what each code means, and how --json reports an error.
order: 52
group: Data and formats
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'pravo.local/[A-Za-z0-9_.-]+'
    replace: 'pravo.local/ws'
---

| Code | Name | Meaning |
|---|---|---|
| 0 | success | The command did what it was asked. For `plan --detailed-exitcode`: no changes. For `drift`: no drift. For `serve`: a signal stopped the viewer. |
| 1 | error | Any failure that has no code of its own: an unknown flag, a stray argument, a missing output, a provider or state error, an `apply --json` that has changes and no `--auto-approve`, an answer other than `yes`. |
| 2 | changes | `plan --detailed-exitcode` found changes. `drift` found drift. This is a report and not a failure. |
| 3 | stale | A saved plan cannot be applied: the configuration, a provider or a node changed after it was made. An apply that finds a resource different from the plan, saved or printed by the same command, stops with this code too. |
| 4 | rejected | A node is locked by a running command, or reserved by another actor: before the apply changes anything, or, with `lock lost`, in the middle of an apply that another command took a lock from. The lock on the recorded outputs is held by a command that did not release it in time. |
| 5 | refused | A plan policy refused the run, a policy file cannot be used, or a module does not match its lock. |
| 6 | invalid | A command that loads the specification found that it does not load or evaluate: a syntax error, a conflict, a file outside the package, a setting that is not allowed, resources that depend on each other in a circle. `pravo validate` also exits 6 when a provider cannot be installed. `pravo schema` was asked for a type of a provider that is not declared. |

In `--json` mode an error also prints `{"code":N,"kind":"error","message":"..."}` on standard output. The mismatch report of `pravo mod verify --json` is the exception: it prints `{"ok":false,"problems":[...]}` alone.

Work in an empty directory. The root is `infra`, and the policies are in `policy`.

```cue file=infra/main.cue
package infra

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

hello: $local.#Resource.local_file & {filename: "hello.txt", content: "hello\n"}
```

```cue file=policy/no-destroy.cue
// A managed resource is neither deleted nor replaced.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
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

A saved plan is refused when the configuration changed, the state has another lineage, a provider has another version, or a node was written after the plan was made.

```sh run
echo 'extra: 1' >> infra/main.cue
pravo -C infra apply change.plan; echo "exit=$?"
sed -i.bak '$d' infra/main.cue
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made
exit=3
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

A plan that violates a policy is refused after it is printed. A policy file that cannot be used is refused before anything else happens.

```sh run
pravo -C infra apply --auto-approve > /dev/null 2>&1
pravo -C infra plan --destroy --policy ../policy/no-destroy.cue > /dev/null 2>&1; echo "violation: $?"
pravo -C infra plan --policy ../policy/missing.cue 2>&1; echo "unusable policy: $?"
pravo -C infra apply --policy ../policy/no-destroy.cue > /dev/null 2>&1; echo "apply without a plan file: $?"
```

```text out
violation: 5

Error: policy ../policy/missing.cue: open ../policy/missing.cue: no such file or directory
unusable policy: 5
apply without a plan file: 5
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

## In an agent session

`pravo mcp` returns the exit code of the command a tool ran as `structuredContent.exit_code`, and sets `isError` when it is not 0. A refusal before a process runs has no `exit_code`. See [Operating policies](policies-operations.md#the-mcp-server).

## See also

- [Run pravo in a pipeline](../how-to/ci.md) uses each code in a job.
- [Global flags, environment and exit codes](global-flags.md) lists the flags every command accepts.
- [Error catalogue](errors.md) lists the messages behind each code.
