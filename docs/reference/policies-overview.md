---
title: "Policies overview"
description: A plan policy is one CUE file that a plan must satisfy. It is named with --policy by whoever runs pravo and never by the specification.
order: 40
group: Policies
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[id=[a-z]+-[a-z]+\]'
    replace: '[id=<pet>]'
---

A plan policy is one CUE file that a plan must satisfy. Whoever runs pravo names it with `--policy <file>`. The specification cannot name a policy, weaken it or exempt a resource from it. A plan that does not satisfy a policy is refused with exit code 5.

| Item | Fact |
|---|---|
| Flag | `--policy FILE`, repeatable. |
| Commands that take it | `plan`, `apply`, `destroy`, `mcp`. |
| Judged against | The plan document that `pravo plan --json` prints. See [The plan document](policies-plan-document.md). |
| Satisfied when | The policy unified with the plan document is concrete and has no error. |
| Verdict | One line per policy: `Policy FILE (sha256:<12 hex digits>) is satisfied.` |
| Refusal | Exit code 5. The plan is printed. Nothing is applied, and `plan --out` writes no file. |
| The file | One CUE file. It may import the CUE standard library and nothing else. It may not sit in the root directory. |
| Where it is named | The command line of the job. There is no setting, no environment variable and no default location. |

How a policy is written is on [Writing policies](policies-writing.md). Where each command judges it is on [Operating policies](policies-operations.md). The idea is taught in [B10](../learn/b10-policy.md), and a rollout is in [Write and roll out plan policies](../how-to/policies.md).

## A policy and a plan

Work in an empty directory named for a repository: the root is `infra`, and the policies are in `policy`, outside the root.

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
	min: 1
	max: 99
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

```sh run
pravo -C infra init > /dev/null
pravo -C infra plan --policy ../policy/no-destroy.cue 2>&1 | sed -n '/^Plan:/,$p'
```

```text out
Plan: 3 to add, 0 to change, 0 to destroy.
Graph: planned 3

Outputs:
  name: (known after apply)
Policy ../policy/no-destroy.cue (sha256:0c23a31e78bd) is satisfied.
```

The plan is printed first. The verdict follows. It names the file as it was written on the command line, the first twelve hexadecimal digits of the SHA-256 of its bytes, and the word *satisfied*. The digest identifies the bytes that were used:

```sh run
shasum -a 256 policy/no-destroy.cue
```

```text out
0c23a31e78bda4155de6c4983b382b0041de2fe21627240eca920dfc8a2972dc  policy/no-destroy.cue
```

Four properties follow.

- The CUE inside the `pravo` binary evaluates the policy against data. A policy is not a program, and it calls no provider.
- A policy is one file. It may import only the CUE standard library.
- Several policies may be named. Each is judged on its own, and all of them must hold.
- A policy judges the plan, which lists what a run does. A change with the action `no-op` carries the identity of a resource and no attributes, so a rule over attributes cannot judge a resource that the plan leaves alone.

## Naming a policy

```text
pravo plan    --policy ../policy/no-destroy.cue
pravo plan    --policy ../policy/no-destroy.cue --policy ../policy/owner.cue --out change.plan
pravo apply   --policy ../policy/no-destroy.cue change.plan
pravo destroy --policy ../policy/no-destroy.cue
pravo mcp     --policy /srv/policy/no-destroy.cue
```

No other command takes `--policy`:

```sh run exit=1
pravo -C infra plan --out first.plan > /dev/null
pravo -C infra show first.plan --policy ../policy/no-destroy.cue
```

```text out
Error: unknown flag: --policy
```

A relative path is resolved in the directory pravo runs in, which is the `-C` directory when that flag is given. From the directory that holds `infra` and `policy`, `pravo -C infra plan --policy ../policy/x.cue` reads `policy/x.cue`:

```sh run exit=5
pravo -C infra plan --policy policy/no-destroy.cue
```

```text out
Error: policy policy/no-destroy.cue: open policy/no-destroy.cue: no such file or directory
```

The specification has no say. `$settings` takes `state` and `refresh`, and a `policy` key is an error: see [Settings](spec-settings.md#unknown-keys). A file in a repository is a policy only if a command line names it.

### Where the file may sit

A policy may sit anywhere except directly in the root directory, whose `.cue` files are the specification. A file there is part of the specification. A file in a subdirectory of the root is not refused, and it is as writable as the root. Keep policies in a directory with its own review, outside the root.

### What the file may contain

A policy is a single file and may import only the CUE standard library, such as `strings`. A definition (`#Limit: 5`) or a hidden field (`_exempt: [...]`) can serve as a helper: the document has no such fields, and neither has to be concrete.

```cue file=policy/no-iam.cue
// IAM resources are managed elsewhere.
import "strings"

changes: [...{
	addr: string
	type: string
	if strings.HasPrefix(type, "aws_iam_") {
		error("\(addr): IAM resources are managed elsewhere")
	}
}]
```

```sh run
pravo -C infra plan --policy ../policy/no-iam.cue 2>&1 | tail -n 1
```

```text out
Policy ../policy/no-iam.cue (sha256:2520599268a9) is satisfied.
```

## Load refusals

pravo reads and compiles every named file before anything else happens: before a provider is installed, before the lock file is written and before the state is opened. A file that cannot be used refuses the run with exit code 5 and names the policy as the fault.

| Cause | Message |
|---|---|
| An empty value | `--policy needs a file; the value is empty` |
| A file that is missing or cannot be read | `policy <file>: <the operating system's error>` |
| A file directly in the root directory | `policy <file> sits in the root directory, whose .cue files are the specification; keep policies outside it` |
| An import outside the standard library | `policy <file> imports "<path>"; a policy is a single file and may import only the CUE standard library` |
| A file that does not parse | `policy <file>: <CUE's error>` |
| A file that parses and does not compile | `policy <file> does not compile: <CUE's error>` |

```cue file=infra/in-root.cue
package infra
```

```cue file=policy/foreign.cue
import "pravo.dev/pravo"

changes: [...{addr: string}]
```

```cue file=policy/syntax.cue
changes: [...{
```

```cue file=policy/scope.cue
changes: [...{
	addr: string
	if mode == "managed" {
		error(addr)
	}
}]
```

```sh run exit=5
pravo -C infra plan --policy nope.cue
pravo -C infra plan --policy in-root.cue
pravo -C infra plan --policy ../policy/foreign.cue
pravo -C infra plan --policy ""
pravo -C infra plan --policy ../policy/syntax.cue
pravo -C infra plan --policy ../policy/scope.cue
pravo -C infra plan --policy ../policy
```

```text out
Error: policy nope.cue: open nope.cue: no such file or directory

Error: policy in-root.cue sits in the root directory, whose .cue files are the specification; keep policies outside it

Error: policy ../policy/foreign.cue imports "pravo.dev/pravo"; a policy is a single file and may import only the CUE standard library

Error: --policy needs a file; the value is empty

Error: policy ../policy/syntax.cue: expected '}', found 'EOF':
    ../policy/syntax.cue:1:16


Error: policy ../policy/scope.cue does not compile: changes: reference "mode" not found:
    ../policy/scope.cue:3:5


Error: policy ../policy: read ../policy: is a directory
```

`foreign.cue` imports `pravo.dev/pravo`. `syntax.cue` ends in the middle of a struct. `scope.cue` reads a field it did not declare: CUE resolves a name by lexical scope, and the template must declare `mode` before a guard can read it. All seven refusals are exit code 5.

## How a plan is judged

For each named policy, in the order given, pravo unifies the policy with the plan document and validates the result as concrete. Each policy is judged on its own. One that is satisfied does not excuse another. The first policy that is not satisfied ends the judging: the policies after it are not reported, and the satisfied lines of the policies before it are not printed.

When every policy is satisfied, there is one line for each, in the order given. Under `plan --json` these lines go to standard error, and standard output holds only the document.

### Reading a refusal

A refusal is exit code 5. It names the policy file and its digest, and gives CUE's details:

```text
Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
changes.0: seed: replace is not allowed:
    ../policy/no-destroy.cue:7:3
```

- The path at the start of a line indexes the plan document: `changes.0` is the first element of `changes`, and `changes.1.after.keepers.owner` is a value inside the second.
- A message from `error()` is the text the policy wrote, with the address of the change if the policy included it.
- The lines below a message are positions. They point into the policy file. For a violation of a value they also point into the document, as `plan:1:782`: the line and column in the document as pravo built it. That document was held in memory when it was judged, so the position is not in a file. Use the path to find the value in `pravo plan --json`.
- The text of a plan leaves out the unchanged resources, and the document does not. The index in `changes.N` is a position in the list of the document, not in the text.
- A refusal quotes the value that failed. The document is not masked: see [The plan document](policies-plan-document.md#the-document-is-not-masked).

Under `--json` the error is also printed as an object on standard output, after the document: `{"code":5,"kind":"error","message":"..."}`.

## Two moments

A specification policy is a constraint on the `#Resource` of a `#Provider` definition. It is checked when the specification evaluates, and the specification names it. A plan policy is a CUE file named with `--policy`. It is checked when the plan is judged, and whoever runs pravo names it, so the specification cannot weaken it.

A specification policy rejects a value that is set and wrong. It does not require that an attribute is set, which is a rule for the plan policy. See [Providers](spec-providers.md#provider-scoped-constraints). [Policies](../concepts/policies.md#two-moments) compares the two.

## See also

- [Write and roll out plan policies](../how-to/policies.md) takes a policy from a first rule to an apply job.
- [Policies](../concepts/policies.md) explains the two moments and what a policy binds.
- [Writing policies](policies-writing.md) lists the shapes that work.
