---
title: Operating policies
description: Where each command judges a policy, what a plan does before the verdict, how to roll a policy out and test it, and what a policy does and does not guarantee.
order: 43
group: Policies
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[id=[a-z]+-[a-z]+\]'
    replace: '[id=<pet>]'
  - pattern: '\[id=[A-Za-z0-9_-]{6}\]'
    replace: '[id=<seed>]'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'plan:1:[0-9]+'
    replace: 'plan:1:N'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: 'until [0-9T:Z+-]+'
    replace: 'until <time>'
  - pattern: '\{"code":5,"kind":"error","message":"the plan violates'
    replace: '{"code":5,"kind":"error","message":"the plan violates'
---

| Command | The policy is judged | A violation |
|---|---|---|
| `plan` | After the plan is printed. | Exit 5. |
| `plan --out FILE` | After the plan is printed, before the file is saved. | Exit 5. No file is written. |
| `apply PLANFILE` | Before anything is applied, against the whole remaining plan. A stale plan is refused first, with exit 3. | Exit 5. Nothing is applied. The file is unchanged. |
| `apply` without a plan file | Not judged. The command is refused when a policy is named. | Exit 5. |
| `destroy` | After the plan of deletions is printed, before the confirmation. | Exit 5. Nothing is destroyed. |
| `mcp` | The `plan` and `apply` tools carry the policy. | Exit 5 from the tool call. |

`show`, `drift`, `import` and the `state` commands take no `--policy`. How the files are named and written is on [Policies](policies-overview.md) and [Writing policies](policies-writing.md).

Work in an empty directory. The root is `infra`, and the policies are in `policy`.

```cue file=infra/main.cue
package infra

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider & {$version: "~> 3.6"}

seed: $random.#Resource.random_id & {byte_length: 4}

pet: $random.#Resource.random_pet & {
	length: 2
	keepers: owner: "platform"
}

ticket: $random.#Resource.random_integer & {
	seed: "fixed"
	min:  1
	max:  99
	keepers: source: pet.id
}

name: pravo.#Output & {value: pet.id}
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

```cue file=policy/owner.cue
// Every random_pet that is created carries an owner.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: keepers: owner: string & !=""
	}
}]
```

```cue file=policy/limit.cue
// A plan touches at most two nodes.
summary: blast_radius: <=2
```

```cue file=policy/limit-five.cue
// A plan touches at most five nodes.
summary: blast_radius: <=5
```

## Plan and apply

`plan` prints the plan, judges it, and then saves it. The plan that was refused is on the screen, and under `--out` no file is written for it. When every policy is satisfied, their lines come first and the apply follows. This is the first plan of the root, saved under two policies, and its apply under the same two:

```sh run
pravo -C infra init > /dev/null
pravo -C infra plan --policy ../policy/no-destroy.cue --policy ../policy/owner.cue --out first.plan 2>&1 | sed -n '/^Policy/,$p'
pravo -C infra apply --policy ../policy/no-destroy.cue --policy ../policy/owner.cue first.plan 2>&1 | sed -n '1,2p;/^Apply/p'
```

```text out
Policy ../policy/no-destroy.cue (sha256:0c23a31e78bd) is satisfied.
Policy ../policy/owner.cue (sha256:bf44cd1fce1f) is satisfied.

Saved the plan to first.plan. To apply exactly these actions, run:
  pravo apply first.plan
Policy ../policy/no-destroy.cue (sha256:0c23a31e78bd) is satisfied.
Policy ../policy/owner.cue (sha256:bf44cd1fce1f) is satisfied.
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
```

### A violation applies nothing

Change the root so that the plan holds a replacement of `seed`, `pet` and `ticket`, and save the plan without a policy:

```sh run
sed -i.bak -e 's/length: 2/length: 3/' -e 's/byte_length: 4/byte_length: 5/' infra/main.cue
pravo -C infra plan --out change.plan > /dev/null 2>&1
```

With `no-destroy.cue` named, the plan file is refused and the state is as it was. The revision of the node `seed` is still 1:

```sh run exit=5
pravo -C infra apply --policy ../policy/no-destroy.cue change.plan
```

```text out
Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
changes.0: seed: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.1: pet: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.2: ticket: replace is not allowed:
    ../policy/no-destroy.cue:7:3
```

```sh run
pravo -C infra state query 'resources.seed.revision'
```

```text out
1
```

A saved plan is judged by the policies the applier names, whatever policy was named when it was made, or none. Editing a policy does not make a saved plan stale: the plan file records the root, the providers and the state, and a policy is outside them. The plan file is not signed. pravo checks it against the root and the state, not against whoever made it.

### The whole plan is judged

`apply PLANFILE --policy FILE` judges every change that is still pending in the file, and not the `--only` selection. A limit on the size of a plan cannot be passed by applying the plan in stages. A plan larger than the limit can never be applied. Split it in the specification.

The plan touches three nodes. `limit.cue` allows two, so applying only `seed`, which touches one, is refused:

```sh run exit=5
pravo -C infra apply --policy ../policy/limit.cue change.plan --only seed
```

```text out
Error: the plan violates the policy ../policy/limit.cue (sha256:b8d1f7b6cd67):
summary.blast_radius: invalid value 3 (out of bound <=2):
    ../policy/limit.cue:2:24
    plan:1:N
```

Under `limit-five.cue` the same command is satisfied, and the stage is applied. The file keeps the rest of the changes for a later `pravo apply change.plan`:

```sh run
pravo -C infra apply --policy ../policy/limit-five.cue change.plan --only seed 2>&1 | grep -E '^(Policy|Apply)'
```

```text out
Policy ../policy/limit-five.cue (sha256:4cc07921d576) is satisfied.
Applying 1 of 3 remaining changes from change.plan.
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

A saved plan with nothing left to apply is not judged, because there is nothing to apply. The policy files are still read and compiled first.

### A stale plan is refused first

A stale saved plan is refused before any policy is consulted, with exit code 3 and not 5. After `main.cue` is edited following `plan --out`:

```sh run exit=3
sed -i.bak 's/length: 3/length: 4/' infra/main.cue
pravo -C infra apply change.plan --policy ../policy/no-destroy.cue
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
```

A job that treats exit code 5 as the only refusal misses this one.

### Apply without a plan file

An apply with a policy named and no plan file is refused. A plain apply plans again while it executes, and the second pass is not the one that was judged. Under a policy an apply is of a saved plan: the plan that was made, shown and judged is the plan that runs.

```sh run exit=5
pravo -C infra apply --policy ../policy/no-destroy.cue
pravo -C infra apply --policy ../policy/no-destroy.cue --auto-approve
```

```text out
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"

Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

## Destroy

`destroy` is a plan of deletions. It is printed, judged, and only then confirmed and executed.

```sh run
pravo -C infra destroy --policy ../policy/no-destroy.cue 2>&1 | sed -n '/^Plan:/,$p'
```

```text out
Plan: 0 to add, 0 to change, 3 to destroy.
Graph: 3 resources in state · refreshed 3

Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
changes.0: seed: delete is not allowed:
    ../policy/no-destroy.cue:7:3
changes.1: ticket: delete is not allowed:
    ../policy/no-destroy.cue:7:3
changes.2: pet: delete is not allowed:
    ../policy/no-destroy.cue:7:3
```

Exit code 5, and `pravo state list` still shows all three nodes:

```sh run
pravo -C infra state list
```

```text out
pet
seed
ticket
```

With a policy that is satisfied, the verdict is printed before the question. The answer here is "no":

```sh run
echo no | pravo -C infra destroy --policy ../policy/limit-five.cue 2>&1 | sed -n '/^Policy/,$p'
```

```text out
Policy ../policy/limit-five.cue (sha256:4cc07921d576) is satisfied.

Do you really want to destroy all resources?
  Only 'yes' will be accepted to approve.

  Enter a value:

Error: apply cancelled
```

## The MCP server

`pravo mcp --policy FILE` binds the tool server. The option is repeatable.

- The files are read and compiled when the server starts. A relative path, an empty value, a missing file or a file that cannot be used fails the start with exit code 5.
- The server appends `--policy` to its `plan` and `apply` tool calls, and to no other command, because none of the others takes the flag.
- A tool argument cannot remove a policy that the server names.
- The server applies only from saved plan files, and refuses the id of a plan stored in the state: `pravo plan pull ID FILE` writes one to a file. It refuses a tool argument that is empty or begins with `-` for the values that reach the command line as a word of their own: `planfile`, `addr`, `addrs`, `types` and `expression`. The refusal is a tool result with `isError` true, returned before any pravo process runs.
- Without `--policy` the server judges nothing. It still applies only a saved plan.

```sh run exit=5
pravo -C infra mcp --policy ../policy/no-destroy.cue < /dev/null
pravo -C infra mcp --policy /nope/missing.cue < /dev/null
```

```text out
Error: --policy "../policy/no-destroy.cue": the server needs an absolute path

Error: policy /nope/missing.cue: open /nope/missing.cue: no such file or directory
```

A session against a root with a pending replacement, with the server started as `pravo mcp --policy` and an absolute path. The client sends `initialize` and three tool calls: `pravo_plan` with an output file, `pravo_apply_plan` with `-auto-approve` as the file name, and `pravo_state_log`:

```sh run
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pravo_plan","arguments":{"out":"mcp.plan"}}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pravo_apply_plan","arguments":{"planfile":"-auto-approve"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pravo_state_log","arguments":{}}}' \
  | pravo -C infra mcp --policy "$PWD/policy/no-destroy.cue" 2>/dev/null \
  | jq -c '{id, isError: .result.isError, exit_code: .result.structuredContent.exit_code}'
```

```text out
{"id":1,"isError":null,"exit_code":null}
{"id":2,"isError":true,"exit_code":5}
{"id":3,"isError":true,"exit_code":null}
{"id":4,"isError":false,"exit_code":0}
```

The plan call is refused with exit code 5. The apply call is refused before a process runs, so it has no `exit_code`. The command line that the first call ran, from the transaction log, shows what the server added:

```sh run
pravo -C infra state log --json | jq -r '.[0].command'
```

```text out
pravo --json --no-color plan --out mcp.plan --policy ./policy/no-destroy.cue
```

This binds the tool, not the process around it. An agent with a shell, or with its own credentials, is not bound by the policy of the server.

## Before the verdict

A plan is not read-only. By the time a policy gives its verdict, pravo has:

- installed the providers and updated the lock file;
- refreshed the records it looked at whose refresh age had run out, writing the refreshed state, and marked a record whose object differs or is gone;
- read the objects that `$import` names, and staged the `$from` moves and the imports in memory, without writing a record: the apply writes them;
- read data sources and opened ephemeral resources, which runs provider code with the invoker's credentials; and
- taken the reservation, if `--reserve` was given. A refusal does not release it. It expires.

```sh run
pravo -C infra plan --reserve 15m --actor ci --policy ../policy/no-destroy.cue 2>&1 | grep -E '^(Reserved|Error)'
pravo -C infra state locks
```

```text out
Reserved 2 nodes until <time>.
Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
pet  reserved by ci until <time> pravo -C infra plan --reserve 15m --actor ci --policy ../policy/no-destroy.cue
ticket  reserved by ci until <time> pravo -C infra plan --reserve 15m --actor ci --policy ../policy/no-destroy.cue
```

The reservation of the refused plan stays until it expires, and it keeps other actors out of those nodes. Release it by hand:

```sh run
for a in pet ticket; do pravo -C infra state unlock $a; done
```

```text out
Unlocked pet
Unlocked ticket
```

The guarantee concerns changes to infrastructure. With `--policy`, `plan --out` writes no refused plan, and `apply PLANFILE` and `destroy` ask no provider to create, update or delete anything unless every named policy is satisfied.

## Rolling out and testing

A policy that blocks the wrong plans stops work. A policy that blocks nothing protects nothing. Try a rule where it cannot block, then let it block.

### Current violators

A plan judges only what it changes. List what already breaks a rule from the state, and fix it before the rule blocks anyone. Add a pet without an owner, apply it without a policy, as a resource that predates the owner rule:

```cue file=infra/stray.cue
package infra

stray: $random.#Resource.random_pet & {length: 2}
```

```sh run
sed -i.bak 's/length: 4/length: 3/' infra/main.cue
pravo -C infra apply --auto-approve > /dev/null 2>&1
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | tail -n 1
pravo -C infra state query '[for a, r in resources if r.type == "random_pet" if r.attributes.keepers == null {a}]'
```

```text out
Policy ../policy/owner.cue (sha256:bf44cd1fce1f) is satisfied.
["stray"]
```

The plan is satisfied: no change creates a pet, and the stray one is not touched. The query finds it. When the list is empty, move the rule to the file that `--policy` names. The two `if` clauses are chained so that the comparison is read only for pets. Joined with `&&`, the query fails with `undefined field` on the records of other types.

### An advisory file

Start a new rule in an advisory file, evaluated outside pravo on a plan that was already produced, in a pipeline step that is allowed to fail. `pravo show PLANFILE --json` wraps the document, and `jq .plan` takes it out. `cue vet` reads it with the same CUE version as pravo:

```text
pravo show change.plan --json | jq .plan | cue vet -c advice.cue json: -
```

The step reports what the rule refuses and blocks nothing. The document of a saved plan can carry `reservation`, and its `created`, `actor`, `reason`, `git_revision` and `trace_id` are those of the plan file. Keep advisory rules off those fields.

### Fixtures and the matching cue

Policies are evaluated by the CUE inside the `pravo` binary. Test a policy with a `cue` binary of the same version. `pravo version` prints both:

```sh run
pravo version
```

```text out
pravo 0.1.0-dev
cue v0.17.1
```

With a Go toolchain, `go install cuelang.org/go/cmd/cue@v0.17.1` installs that release; use the version that your `pravo version` prints. Save a plan document and check a policy against it:

```text
pravo plan --json > fixture.json
cue vet -c ../policy/no-destroy.cue fixture.json
```

`-c` asks for a concrete result, which is what pravo asks. The messages carry the position in the fixture in place of `plan:1:N`. A fixture is a plan document, and the document is not masked: scrub it, or write it by hand, before it goes into a repository.

### A candidate on a recorded plan

`pravo plan --policy candidate.cue` shows the candidate's verdict on a real plan, and a plan does what [Before the verdict](#before-the-verdict) lists. To try a candidate on a plan without running providers, replay a recorded run: `PRAVO_RECORD=DIR` records the provider responses of a run, and `PRAVO_REPLAY=DIR` replays them with no provider, registry or credentials. A recording holds the values the providers returned, so handle it like state; [Record and replay](global-flags.md#record-and-replay) says what it holds.

Change the length of the pet, the byte length of the seed and the length of the stray pet so that the plan holds replacements:

```sh run
sed -i.bak -e 's/length: 3/length: 5/' -e 's/byte_length: 5/byte_length: 6/' infra/main.cue
PRAVO_RECORD=recordings pravo -C infra plan > /dev/null 2>&1
PRAVO_REPLAY=recordings pravo -C infra plan --policy ../policy/no-destroy.cue 2>&1 | sed -n '/^Error/,$p'
```

```text out
Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
changes.0: seed: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.1: pet: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.3: ticket: replace is not allowed:
    ../policy/no-destroy.cue:7:3
```

## Exemptions and break-glass

A rule that must have exceptions says which objects are excepted. Key an exemption to the type, the provider and the recorded id, which belong to the object. Do not key it to `addr` or `name`: they are the author's labels and move with `$from`. The id is in `before`, which is absent for a create, so test its presence. The `ticket` has a seed, so its id is the same on every machine:

```sh run
pravo -C infra state query 'resources.ticket.attributes.id'
```

```text out
"57"
```

```cue file=policy/no-destroy-except.cue
// A managed resource is neither deleted nor replaced, except the objects
// listed here by type and by the id the provider recorded.
_exempt: [
	{type: "random_integer", id: "57"},
]

changes: [...{
	addr:    string
	mode:    string
	type:    string
	action:  string
	before?: {id: string}

	_listed: *false | bool
	if before != _|_ {
		for e in _exempt if e.type == type && e.id == before.id {
			_listed: true
		}
	}
	if mode == "managed" && (action == "delete" || action == "replace") && !_listed {
		error("\(addr): \(action) is not allowed")
	}
}]
```

An edit to this list is an edit to the policy. Against a saved plan that replaces the pet, the seed and the ticket, the ticket may be replaced and the others may not:

```sh run exit=5
pravo -C infra plan --out exempt.plan > /dev/null 2>&1
pravo -C infra apply --policy ../policy/no-destroy-except.cue exempt.plan
```

```text out
Error: the plan violates the policy ../policy/no-destroy-except.cue (sha256:0818b96b2801):
changes.0: seed: replace is not allowed:
    ../policy/no-destroy-except.cue:21:3
changes.1: pet: replace is not allowed:
    ../policy/no-destroy-except.cue:21:3
```

**Break-glass.** There is no flag that lifts a policy. A privileged job names a narrower policy that the platform team owns, or none. The command line of its transaction says which. Apply the plan under the narrower policy, and then make and apply a second plan with no policy:

```sh run
pravo -C infra apply exempt.plan --policy ../policy/limit-five.cue > /dev/null 2>&1
sed -i.bak 's/length: 5/length: 6/' infra/main.cue
pravo -C infra plan --out second.plan > /dev/null 2>&1
pravo -C infra apply second.plan > /dev/null 2>&1
pravo -C infra state log --json | jq -r '.[] | select(.kind == "apply") | .command'
```

```text out
pravo -C infra apply second.plan
pravo -C infra apply exempt.plan --policy ../policy/limit-five.cue
pravo -C infra apply --auto-approve
pravo -C infra apply --policy ../policy/limit-five.cue change.plan --only seed
pravo -C infra apply --policy ../policy/no-destroy.cue --policy ../policy/owner.cue first.plan
```

The log is evidence of what was named. It prevents nothing.

**Review of a policy.** A policy is source, and it is reviewed as source: a pull request against the repository that holds it, with code owners. pravo records no approval.

## What is recorded

An apply of a saved plan records its command line, including every `--policy`, on its transaction, and `pravo state log` shows it. So the log shows whether `--policy` was given and which file, which is the evidence of a break-glass run. The record is as writable as the state it is in, and it prevents nothing.

Telemetry counts a refusal by a policy in `pravo.rejections` with the reason `policy`. A run that omitted `--policy` is not counted. See [Telemetry](telemetry.md).

## What a policy guarantees

What a policy guarantees depends on four rights: write access to the policy source, to the job definition and to the state store, and the cloud credentials. [The trust anchor](../concepts/policies.md#the-trust-anchor) states what each right allows.

- With `--policy`, `plan --out` writes no plan file for a plan that violates a named policy.
- With `--policy`, `apply PLANFILE` and `destroy` ask no provider to create, update or delete anything unless every named policy is satisfied.
- The specification cannot name, weaken or exempt a policy.
- A refusal names the policy file and its digest, quotes CUE's details, and exits with code 5.
- `pravo mcp --policy` adds the policy to its `plan` and `apply` tool calls, and a tool argument cannot remove it.
- The command line of an apply, with every `--policy`, is recorded on its transaction.

## What a policy does not guarantee

A policy binds the invocations that name it, and it judges the plan that the specification, the state and the providers produce. [Policies](../concepts/policies.md#what-a-policy-does-not-guarantee) lists each limit. Typos that forbid nothing are in [Writing policies](policies-writing.md#mistakes-that-forbid-nothing). Operations on the state and calls to the cloud API are outside pravo.

## Errors

All of these are exit code 5. In `--json` mode they are also printed as `{"code":5,"kind":"error","message":...}` on standard output.

| Message | When |
|---|---|
| `the plan violates the policy <file> (sha256:<12 hex>):` followed by details | A plan does not satisfy a policy. |
| `a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"` | `apply` without a plan file while a policy is named. |
| The messages under [Load refusals](policies-overview.md#load-refusals) | A policy file that cannot be used. |
| `--policy "<path>": the server needs an absolute path` | `pravo mcp --policy` with a relative path. |

Exit code 5 also means a module that does not match its lock: `pravo mod verify` exits with 5. All the codes are in [Exit codes](exit-codes.md).

## See also

- [Run pravo in a pipeline](../how-to/ci.md) names the policy in the plan job and the apply job.
- [Let an agent plan and apply](../how-to/agents-and-mcp.md) binds the tool server to a policy.
- [`pravo mcp`](mcp.md) lists the tools.
