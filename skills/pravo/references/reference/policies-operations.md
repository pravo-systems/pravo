<!-- Generated from docs/reference/policies-operations.md by tools/skillgen. Do not edit. -->

# Operating policies

Where each command judges a policy, what a stale plan does, how to retire a policy, how to roll a rule out and test it, and what a policy does and does not guarantee.

Every command that applies is judged by the same versions: the policies in force in the state. There is no flag to name a policy or to leave one out.

| Command | The policies are judged | A violation |
|---|---|---|
| `plan` | After the plan is printed, as feedback. | Exit 5. |
| `plan --out FILE`, `plan --store` | After the plan is printed, before it is saved. | Exit 5. Nothing is saved. |
| `apply FILE`, `apply ID`, `apply` | Under the locks, after the staleness checks and before the transaction starts, against the whole plan file. | Exit 5. Nothing is applied. A rejected transaction is recorded. |
| `destroy` | As `apply`, after the plan of deletions is printed. | Exit 5. Nothing is destroyed. |
| `show FILE` | After the plan is printed, as a warning. | A warning. Exit 0. |
| `mcp` | By the `plan` and `apply` tools, as above. | Exit 5 from the tool call. |

A plan with no create, update, replace or delete is not judged. A change to a policy is never judged. How rules are written is on [Writing policies](policies-writing.md), and what a policy is is on [Policies](policies-overview.md).

Work in an empty directory. The root is `infra`, the rules are in `policy`, and the state is local and sealed to ada. `set-rule.sh NAME FILE` writes a rule into a policy node of the root. It applies nothing.

File `infra/main.cue`:

```cue
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

File `set-rule.sh`:

```sh
#!/bin/sh
# set-rule.sh NAME FILE: make the contents of FILE the rule of the policy NAME of the root.
{
	printf 'package infra\n\nimport "pravo.dev/pravo"\n\n%s: pravo.#Policy & {rule: #"""\n' "$1"
	sed 's/^\(.\)/\t\1/' "$2"
	printf '\t"""#}\n'
} > "infra/policy-$1.cue"
```

File `policy/no-destroy.cue`:

```cue
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

File `policy/owner.cue`:

```cue
// Every random_pet that is created carries an owner.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: keepers: owner: string & !=""
	}
}]
```

## Plan and apply

A plan that adds a policy is not judged by it. The plan prints the policy beside the resources, and the apply writes the policy after the resources:

```sh
./set-rule.sh no_destroy policy/no-destroy.cue
pravo -C infra plan --out first.plan 2>&1 | sed -n '/^Plan:/,/^Graph/p'
pravo -C infra apply first.plan | grep '^Apply complete'
ls infra/pravo.state/policy
```

Output:

```text
Plan: 3 to add, 0 to change, 0 to destroy.
Policies: +1 new, ~0 changed, -0 removed.
  + no_destroy (sha256:20bbfeca6bff)
Graph: planned 3
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
no_destroy@00000001.json
```

A plan with a policy entry and no resource change is not judged, so a second policy is added without a verdict:

```sh
./set-rule.sh owner policy/owner.cue
pravo -C infra plan --out second.plan 2>&1 | sed -n '/^Policies/,/^Graph/p'
pravo -C infra apply second.plan | grep '^Apply complete'
```

Output:

```text
Policies: +1 new, ~0 changed, -0 removed.
  + owner (sha256:4578c4749e3b)
Graph: planned 0 · skipped 3 · not read 3
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
```

From then on every plan that changes a resource is judged. A plan prints one line for each policy in force, and the transaction of its apply lists the versions that judged it:

```sh
cat >> infra/main.cue <<'CUE'

extra: $random.#Resource.random_pet & {length: 2, keepers: owner: "ops"}
CUE
pravo -C infra plan --out third.plan 2>&1 | grep '^Policy'
pravo -C infra apply third.plan | grep '^Apply complete'
pravo -C infra state log --json | jq -c '.[0] | {kind, status, policies}'
```

Output:

```text
Policy no_destroy@1:20bbfeca6bfffdca459eb1853d15500bead5e387401ebf065f237194c2ac3af6 is satisfied.
Policy owner@1:4578c4749e3b453c66a554d4583f947134be34d4b8eae59c17ae179badf17671 is satisfied.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
{"kind":"apply","status":"applied","policies":["no_destroy@1:20bbfeca6bfffdca459eb1853d15500bead5e387401ebf065f237194c2ac3af6","owner@1:4578c4749e3b453c66a554d4583f947134be34d4b8eae59c17ae179badf17671"]}
```

The list is part of what the signed start of the transaction covers. [What a policy guarantees](#what-a-policy-guarantees) says what that proves.

## A violation applies nothing

Change the root so that the plan holds a replacement of `seed`, `pet` and `ticket`. The plan is printed and refused, and `--out` writes no file:

```sh
cp infra/main.cue base.cue
sed -i 's/length: 2, keepers/length: 3, keepers/; s/^\tlength: 2$/\tlength: 3/; s/byte_length: 4/byte_length: 5/' infra/main.cue
pravo -C infra plan --out change.plan 2>&1 | sed -n '/^Error/,$p'
test -e change.plan || echo "change.plan does not exist"
```

Output:

```text
Error: the plan violates the policy no_destroy@1 (sha256:20bbfeca6bff):
changes.0: seed: replace is not allowed:
    no_destroy:7:3
changes.1: pet: replace is not allowed:
    no_destroy:7:3
changes.2: ticket: replace is not allowed:
    no_destroy:7:3
changes.3: extra: replace is not allowed:
    no_destroy:7:3

change.plan does not exist
```

`apply` judges again under the locks. A plan that a policy refuses applies nothing, and the transaction is recorded as rejected, with the versions and the plan that were judged. The revision of the node `seed` is still 1:

```sh
pravo -C infra apply --auto-approve 2>&1 | sed -n '/^Error/,$p'
pravo -C infra state log --json | jq -c '.[0] | {kind, status, policies}'
pravo -C infra state query 'resources.seed.revision'
```

Output:

```text
Error: the plan violates the policy no_destroy@1 (sha256:20bbfeca6bff):
changes.0: seed: replace is not allowed:
    no_destroy:7:3
changes.1: pet: replace is not allowed:
    no_destroy:7:3
changes.2: ticket: replace is not allowed:
    no_destroy:7:3
changes.3: extra: replace is not allowed:
    no_destroy:7:3

{"kind":"apply","status":"rejected","policies":["no_destroy@1:20bbfeca6bfffdca459eb1853d15500bead5e387401ebf065f237194c2ac3af6","owner@1:4578c4749e3b453c66a554d4583f947134be34d4b8eae59c17ae179badf17671"]}
1
```

The whole plan file is judged, whatever an earlier stage applied and whatever `--only` selects. A limit on the size of a plan cannot be passed by applying the plan in stages.

## Retire a policy

There is no waiver and no break-glass flag. A policy that refuses every plan is retired by removing it from the specification. A change to a policy is not judged, so the apply that removes it passes whatever the policy says, and it writes a signed removal. Add a policy that refuses every change, and see what it does to a plan:

File `policy/freeze.cue`:

```cue
// Nothing changes.
changes: []
```

```sh
./set-rule.sh freeze policy/freeze.cue
pravo -C infra apply --auto-approve | grep -E '^(Policies|Apply complete)'
sed -i 's/length: 2, keepers/length: 3, keepers/; s/^\tlength: 2$/\tlength: 3/' infra/main.cue
pravo -C infra plan 2>&1 | sed -n '/^Error/,$p'
```

Output:

```text
Policies: +1 new, ~0 changed, -0 removed.
Error: the plan violates the policy no_destroy@1 (sha256:20bbfeca6bff):
changes.0: seed: replace is not allowed:
    no_destroy:7:3
changes.1: pet: replace is not allowed:
    no_destroy:7:3
changes.2: ticket: replace is not allowed:
    no_destroy:7:3
changes.3: extra: replace is not allowed:
    no_destroy:7:3
```

Remove the file of the policy. The plan prints the removal, the apply passes, and the resource change that follows is judged by the policies that remain:

```sh
rm infra/policy-freeze.cue
cp base.cue infra/main.cue
pravo -C infra plan 2>&1 | sed -n '/^Policies/,/^Graph/p'
pravo -C infra apply --auto-approve | grep '^Apply complete'
ls infra/pravo.state/policy
```

Output:

```text
no_destroy@00000001.json
owner@00000001.json
```

The removal is a version with empty text, signed like any other. Every apply is signed, so the log names who retired the policy and when. Control of a policy is review of the files that declare it.

## Destroy

`destroy` is a plan of deletions. It is printed and confirmed, and then judged by the policies in force before it is executed.

```sh
pravo -C infra destroy --auto-approve 2>&1 | sed -n '/^Plan:/,$p'
```

Output:

```text
Plan: 0 to add, 0 to change, 4 to destroy.
Graph: 4 resources in state · refreshed 4

Error: the plan violates the policy no_destroy@1 (sha256:20bbfeca6bff):
changes.0: extra: delete is not allowed:
    no_destroy:7:3
changes.1: seed: delete is not allowed:
    no_destroy:7:3
changes.2: ticket: delete is not allowed:
    no_destroy:7:3
changes.3: pet: delete is not allowed:
    no_destroy:7:3
```

Exit code 5, and `pravo state list` still shows all four nodes:

```sh
pravo -C infra state list
```

Output:

```text
extra
pet
seed
ticket
```

## The MCP server

`pravo mcp` takes no policy. Its `plan` and `apply` tools run the same commands, so they are judged by the same versions, and a tool argument cannot remove a policy.

- The server applies only from saved plan files, and refuses the id of a plan stored in the state: `pravo plan pull ID FILE` writes one to a file. It refuses a tool argument that is empty or begins with `-` for the values that reach the command line as a word of their own: `planfile`, `addr`, `addrs`, `types` and `expression`. The refusal is a tool result with `isError` true, returned before any pravo process runs.

A session against a root with a pending replacement. The client sends `initialize` and three tool calls: `pravo_plan` with an output file, `pravo_apply_plan` with `-auto-approve` as the file name, and `pravo_state_log`:

```sh
sed -i 's/length: 2, keepers/length: 3, keepers/; s/^\tlength: 2$/\tlength: 3/' infra/main.cue
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pravo_plan","arguments":{"out":"mcp.plan"}}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pravo_apply_plan","arguments":{"planfile":"-auto-approve"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pravo_state_log","arguments":{}}}' \
  | pravo -C infra mcp 2>/dev/null \
  | jq -c '{id, isError: .result.isError, exit_code: .result.structuredContent.exit_code}'
```

Output:

```text
{"id":1,"isError":null,"exit_code":null}
{"id":2,"isError":true,"exit_code":5}
{"id":3,"isError":true,"exit_code":null}
{"id":4,"isError":false,"exit_code":0}
```

The plan call is refused with exit code 5. The apply call is refused before a process runs, so it has no `exit_code`. This binds the tool, not the process around it. An agent with a shell, or with its own credentials, is not bound by the policies of the state.

## Before the verdict

A plan is not read-only. By the time a policy gives its verdict, pravo has:

- installed the providers and updated the lock file;
- refreshed the records it looked at whose refresh age had run out, writing the refreshed state, and marked a record whose object differs or is gone;
- read the objects that `$import` names, and staged the `$from` moves and the imports in memory, without writing a record: the apply writes them;
- read data sources and opened ephemeral resources, which runs provider code with the invoker's credentials; and
- taken the reservation, if `--reserve` was given. A refusal does not release it. It expires.

```sh
pravo -C infra plan --reserve 15m --actor ci 2>&1 | grep -E '^(Reserved|Error)'
pravo -C infra state locks
```

Output:

```text
Reserved 3 nodes until <time>.
Error: the plan violates the policy no_destroy@1 (sha256:20bbfeca6bff):
extra  reserved by ci until <time> pravo -C infra plan --reserve 15m --actor <sealed>
pet  reserved by ci until <time> pravo -C infra plan --reserve 15m --actor <sealed>
ticket  reserved by ci until <time> pravo -C infra plan --reserve 15m --actor <sealed>
```

The reservation of the refused plan stays until it expires, and it keeps other actors out of those nodes. Release it by hand:

```sh
for a in pet ticket extra; do pravo -C infra state unlock $a; done
```

Output:

```text
Unlocked pet
Unlocked ticket
Unlocked extra
```

The guarantee concerns changes to infrastructure. `plan --out` writes no refused plan, and `apply` and `destroy` ask no provider to create, update or delete anything unless every policy in force is satisfied.

## Rolling out and testing

A rule that blocks the wrong plans stops work. A rule that blocks nothing protects nothing. Try a rule where it cannot block, then let it block.

### Current violators

A plan judges only what it changes. List what already breaks a rule from the state, and fix it before the rule blocks anyone. A rule that every pet sets a `prefix` blocks no plan today, and the pets that exist do not set one. The query lists them:

```sh
cp base.cue infra/main.cue
pravo -C infra state query '[for a, r in resources if r.type == "random_pet" if r.attributes.prefix == null {a}]'
```

Output:

```text
["extra", "pet"]
```

When the list is empty, add the rule. The two `if` clauses are chained so that the comparison is read only for pets. Joined with `&&`, the query fails with `undefined field` on the records of other types.

### A scratch root

Rehearse a rule in a copy of the root with its own state, where an apply costs nothing. Seal the scratch state to your own identity, apply the rule there, and read the verdict of `plan` on the changes you expect it to refuse. [Writing policies](policies-writing.md) does this with `set-rule.sh`. Nothing the scratch state records reaches the real one.

### An advisory file

Start a new rule in an advisory file, evaluated outside pravo on a plan that was already produced, in a pipeline step that is allowed to fail. `pravo eval -e NAME.rule` prints the text of a rule as a CUE string, and `cue export cue: - --out text` makes it a file. `cue vet` reads a plan document with the same CUE version as pravo:

```text
pravo eval -e no_destroy.rule | cue export cue: - --out text > rule.cue
pravo plan --json > fixture.json
cue vet -c rule.cue fixture.json
```

`pravo version` prints the CUE version of the binary. With a Go toolchain, `go install cuelang.org/go/cmd/cue@v0.17.1` installs that release; use the version that your `pravo version` prints. `-c` asks for a concrete result, which is what pravo asks. The messages carry the position in the fixture in place of `plan:1:N`. The document that `plan --json` prints has fields the judged document lacks, such as `summary.blast_radius`: see [The plan document in a policy](policies-plan-document.md#what-the-judged-document-leaves-out). A fixture is a plan document, and the document is not masked: scrub it, or write it by hand, before it goes into a repository.

## Exemptions

A rule that must have exceptions says which objects are excepted. Key an exemption to the type, the provider and the recorded id, which belong to the object. Do not key it to `addr` or `name`: they are the author's labels and move with `$from`. The id is in `before`, which is absent for a create, so test its presence. The `ticket` has a seed, so its id is the same on every machine:

```sh
pravo -C infra state query 'resources.ticket.attributes.id'
```

Output:

```text
"57"
```

File `policy/no-destroy-except.cue`:

```cue
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

An edit to this list is an edit to the policy, which is reviewed like the rest of the root. Replace `no_destroy` with this rule, and change the pet, the seed and the ticket so that all three are replaced. The ticket may be replaced and the others may not:

```sh
./set-rule.sh no_destroy policy/no-destroy-except.cue
pravo -C infra apply --auto-approve | grep -E '^(Policies|Apply complete)'
sed -i 's/length: 2, keepers/length: 3, keepers/; s/^\tlength: 2$/\tlength: 3/; s/byte_length: 4/byte_length: 5/' infra/main.cue
pravo -C infra plan 2>&1 | sed -n '/^Error/,$p'
```

Output:

```text
Policies: +0 new, ~1 changed, -0 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
Error: the plan violates the policy no_destroy@2 (sha256:8668bf62afac):
changes.0: seed: replace is not allowed:
    no_destroy:21:3
changes.1: pet: replace is not allowed:
    no_destroy:21:3
changes.3: extra: replace is not allowed:
    no_destroy:21:3
```

## Verify a receipt

The signed start of an apply lists the versions that judged it and the digest of the plan. `pravo state verify` checks both against the state. Store a plan, apply it by its id, and verify the transaction:

```sh
cp base.cue infra/main.cue
cat >> infra/main.cue <<'CUE'

second: $random.#Resource.random_pet & {length: 2, keepers: owner: "ops"}
CUE
pravo -C infra plan --store 2>&1 | grep '^Stored'
ID=$(pravo -C infra plan list | awk 'NR==1 {print $1}')
pravo -C infra apply "$ID" | grep '^Apply complete'
TXN=$(pravo -C infra state log --json | jq -r '.[0].id')
pravo -C infra state verify "$TXN"
```

Output:

```text
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
Transaction <txn>
  signed by ada, pravo 0.1.0-dev
  plan      sha256:<digest>
  judged by 2 policy versions:
    no_destroy@2:8668bf62afac32fd114f34a1af6ff2fcdae2b85ce1e83bc6804c54774a62d77a
    owner@1:4578c4749e3b453c66a554d4583f947134be34d4b8eae59c17ae179badf17671
Verified.
```

The command finds the plan by the id that the transaction names, or takes it from `--plan FILE`. It exits with 0 when the signatures verify, the plan has the digest that the transaction signed, and every version listed passes on the plan. A bad signature, a plan with another digest or a version whose text is not the listed one is exit code 7. A rule that fails on the plan is exit code 5. Anything else that stops the check, such as a plan that is not kept, is exit code 1 with the reason.

A plan that an apply judged is marked kept: `pravo plan gc` does not remove it, so the receipt can be checked later. A plain `apply` without a plan names the digest of the plan it built, and keeps no plan, so its receipt lists the versions and cannot be run again.

## What is recorded

The signed start of every apply that judged a plan carries the sorted list `NAME@SEQ:SHA-256`, and the transaction log shows it. A version is signed by the identity that applied it. `pravo state log` shows the transaction, and `pravo state verify` shows the versions. The record is as writable as the state it is in, and signatures make a change visible: they do not prevent it.

Telemetry counts a refusal by a policy in `pravo.rejections` with the reason `policy`. See [Telemetry](telemetry.md).

## What a policy guarantees

What a policy guarantees depends on four rights: write access to the policy source, to the job definition and to the state store, and the cloud credentials. [The trust anchor](../concepts/policies.md#the-trust-anchor) states what each right allows.

- `plan --out` and `plan --store` save no plan that violates a policy in force.
- `apply` and `destroy` ask no provider to create, update or delete anything unless every policy in force is satisfied by the whole plan.
- A policy version is signed by its writer, and the state refuses a version whose signature does not verify.
- The transaction of an apply names the versions that judged it, and `pravo state verify` runs them again on the plan.
- `pravo mcp` runs the same commands, and a tool argument cannot remove a policy.

## What a policy does not guarantee

A policy binds the invocations of pravo that read the state, and it judges the plan that the specification, the state and the providers produce. [Policies](../concepts/policies.md#what-a-policy-does-not-guarantee) lists each limit. Typos that forbid nothing are in [Writing policies](policies-writing.md#mistakes-that-forbid-nothing). Operations on the state and calls to the cloud API are outside pravo. A reader who can apply can write a new version of a policy, and the signed version names who did.

## Errors

| Message | Exit code | When |
|---|---|---|
| `the plan violates the policy NAME@SEQ (sha256:<12 hex>):` followed by details | 5 | A plan does not satisfy a version in force. |
| `the policy NAME changed since the plan was made (the plan is from version N, the state has M)` | 3 | A plan with a policy entry meets another version. |
| `the root declares policies, and a policy version is signed: the state must be sealed (pravo protect init --encrypt)` | 5 | A root with a policy on a state that is not sealed. |
| The messages under [The rule](policies-overview.md#the-rule) | 6 | A rule that cannot be used. |
| A policy version whose signature does not verify, or a gap in the numbers | 7 | The state holds a version that no key signed. |

Exit code 5 also means a module that does not match its lock: `pravo mod verify` exits with 5. All the codes are in [Exit codes](exit-codes.md).

## See also

- [Run pravo in a pipeline](../how-to/ci.md) shows the plan job and the apply job.
- [Let an agent plan and apply](../how-to/agents-and-mcp.md) binds the tool server to the policies of the state.
- [`pravo mcp`](mcp.md) lists the tools.
