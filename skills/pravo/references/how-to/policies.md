<!-- Generated from docs/how-to/policies.md by tools/skillgen. Do not edit. -->

# Write and roll out plan policies

Write a plan policy, test it on fixtures with the matching cue version, roll a new rule out as advice, exempt by type and name the policy in the apply job.

A plan policy is one CUE file that a plan must satisfy. Whoever runs pravo names it with `--policy`. This page writes a policy, tests it, adds a rule without blocking anyone and names the policy in the jobs that matter.

Work in an empty directory. The root is `root/`. The policy lives in `policy/`, outside the root, because pravo refuses a policy among the `.cue` files of the root. Every pravo command uses `-C root`.

File `root/main.cue`:

```cue
package guarded

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}
```

```sh
pravo -C root init > /dev/null
pravo -C root apply -auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## 1. Write the policy

The file is evaluated against the plan document, the data that `pravo plan --json` prints. This one forbids a managed resource to be deleted or replaced and limits the size of a plan.

File `policy/plan.cue`:

```cue
// Rules for every plan that names this file. The file is evaluated against the
// plan document that "pravo plan --json" prints.

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

// One plan reaches at most five nodes, counting what depends on them.
summary: blast_radius: <=5
```

The template declares every field that a guard reads, because CUE resolves names lexically. [B10](../learn/b10-policy.md) walks through the parts, and [Writing policies](../reference/policies-writing.md) lists the shapes that work and the ones that check nothing.

## 2. Test it on fixtures

A fixture is a plan document that the policy must accept or refuse. Make it from a throwaway copy of the root, so the real state stays as it is. The copy carries the state, so the plan is the one a real change produces.

```sh
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

Output:

```text
accept-create.json
refuse-replace.json
```

`cue vet -c` judges a fixture the way pravo judges a plan: it requires the result to be concrete.

```sh
cue vet -c policy/plan.cue policy/fixtures/accept-create.json
echo "exit=$?"
cue vet -c policy/plan.cue policy/fixtures/refuse-replace.json
echo "exit=$?"
```

Output:

```text
exit=0
changes.0: server: replace is not allowed:
    ./policy/plan.cue:12:3
exit=1
```

The message is the one pravo prints. The path `changes.0` is a position in the `changes` list. Then comes the text of `error()`, and then the line of the policy that wrote it.

The CUE inside the `pravo` binary evaluates a policy, so test it with the same version. `pravo version` prints that version on its second line.

```sh
pravo version
cue version | head -n 1
```

Output:

```text
pravo 0.1.0-dev
cue v0.17.1
cue version v0.17.1
```

With a Go toolchain, `go install cuelang.org/go/cmd/cue@v0.17.1` installs it. Use the version that `pravo version` prints. Put the checks in one script, so that CI runs what you run:

File `policy/test.sh`:

```sh
#!/bin/sh
# Every rule has a plan it must accept and a plan it must refuse.
want=$(pravo version | awk 'NR==2 {print $2}')
have=$(cue version | awk 'NR==1 {print $3}')
[ "$want" = "$have" ] || { echo "cue $have does not match the cue $want inside pravo"; exit 1; }

for f in policy/fixtures/accept-*.json; do
	cue vet -c policy/plan.cue "$f" || { echo "refused, should be accepted: $f"; exit 1; }
done
for f in policy/fixtures/refuse-*.json; do
	if cue vet -c policy/plan.cue "$f" > /dev/null 2>&1; then
		echo "accepted, should be refused: $f"
		exit 1
	fi
done
echo "policy tests pass"
```

```sh
sh policy/test.sh
```

Output:

```text
policy tests pass
```

> [!PITFALL]
> A plan document is not masked. Sensitive attributes appear in it, in a fixture as in a refusal message. Do not write rules over secrets, and do not commit a fixture that has not been scrubbed.

## 3. Test the test

A misspelt literal in a guard forbids nothing, and pravo does not warn. Misspell `replace` and run the script.

File `policy/plan.cue`:

```cue
// Rules for every plan that names this file. The file is evaluated against the
// plan document that "pravo plan --json" prints.

changes: [...{
	addr:   string
	mode:   string
	action: string

	// A managed resource is neither deleted nor replaced. A replacement is an
	// action of its own, so both are named.
	if mode == "managed" && (action == "delete" || action == "relace") {
		error("\(addr): \(action) is not allowed")
	}
}]

// One plan reaches at most five nodes, counting what depends on them.
summary: blast_radius: <=5
```

```sh
sh policy/test.sh
```

Output:

```text
accepted, should be refused: policy/fixtures/refuse-replace.json
```

The fixture that must be refused was accepted, and the script says so. This is what fixtures are for. Other misspellings behave differently. A misspelt field with a concrete value, such as `summary: destory: 0`, is satisfied by every plan. A misspelt bound, type or pattern, such as `summary: blast_radious: <=5`, refuses every plan. Restore the correct file.

File `policy/plan.cue`:

```cue
// Rules for every plan that names this file. The file is evaluated against the
// plan document that "pravo plan --json" prints.

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

// One plan reaches at most five nodes, counting what depends on them.
summary: blast_radius: <=5
```

```sh
sh policy/test.sh
```

Output:

```text
policy tests pass
```

## 4. Roll a new rule out as advice

A new rule starts in a file of its own. Evaluate it outside pravo, on a plan that already exists, in a step that the pipeline allows to fail. The rule: a pet that is created has a prefix.

File `policy/advice.cue`:

```cue
// A rule on trial: a pet that is created has a prefix.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: prefix: string & !=""
	}
}]
```

Add a pet without a prefix to the root and save the plan under the blocking policy. The advisory file judges the saved plan.

File `root/extra.cue`:

```cue
package guarded

extra: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo -C root plan -out change.plan --policy ../policy/plan.cue | grep -E '^(Plan|Policy)'
pravo -C root show change.plan --json | jq .plan | cue vet -c policy/advice.cue json: - 2>&1 | head -n 1
```

Output:

```text
Plan: 1 to add, 0 to change, 0 to destroy.
Policy ../policy/plan.cue (sha256:<digest>) is satisfied.
changes.0.after.prefix: conflicting values null and string (mismatched types null and string):
```

The blocking policy is satisfied, and the advice names a pet that is created without a prefix. The plan job goes on. The rule judges what a plan does, so it does not report the pet that already exists. `pravo state query` evaluates a CUE expression over the state and lists the current violators.

```sh
pravo -C root state query '[for a, r in resources if r.type == "random_pet" if r.attributes.prefix == null {a}]'
```

Output:

```text
["server"]
```

`server` must be replaced to get a prefix, and the blocking policy refuses a replacement. Section 6 covers exceptions. Fix the new pet first.

File `root/extra.cue`:

```cue
package guarded

extra: $random.#Resource.random_pet & {length: 2, prefix: "app"}
```

```sh
pravo -C root plan -out change.plan --policy ../policy/plan.cue > /dev/null
pravo -C root show change.plan --json | jq .plan | cue vet -c policy/advice.cue json: -
echo "exit=$?"
```

Output:

```text
exit=0
```

## 5. Make it blocking

When the advice is quiet, move the rule into the blocking file and add the fixture that proves it. A fixture is a plan, so the refused one comes from the earlier version of the root.

File `policy/plan.cue`:

```cue
// Rules for every plan that names this file. The file is evaluated against the
// plan document that "pravo plan --json" prints.

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

// One plan reaches at most five nodes, counting what depends on them.
summary: blast_radius: <=5
```

```sh
cp -r root scratch
printf 'package guarded\n\nextra: $random.#Resource.random_pet & {length: 2}\n' > scratch/extra.cue
pravo -C scratch plan --json 2>/dev/null > policy/fixtures/refuse-no-prefix.json
rm -r scratch
sh policy/test.sh
```

Output:

```text
policy tests pass
```

The test passes: the plan that creates `extra` with a prefix is accepted, and the plan that creates it without one is refused. Delete `policy/advice.cue`.

## 6. Exempt by type

The specification cannot exempt a resource, and no flag waives a policy. An exemption is an edit to the policy, reviewed like any other edit. Key it to what does not move: the type, the provider or the recorded id. Do not key it to `addr` or `name`, which are labels the author chooses and which change with `$from`.

Allow `random_integer` resources to be replaced.

File `policy/plan.cue`:

```cue
// Rules for every plan that names this file. The file is evaluated against the
// plan document that "pravo plan --json" prints.

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

// One plan reaches at most five nodes, counting what depends on them.
summary: blast_radius: <=5
```

```sh
cp -r root scratch
sed 's/min: 8000/min: 7000/' root/main.cue > scratch/main.cue
pravo -C scratch plan --json 2>/dev/null > policy/fixtures/accept-replace-port.json
pravo -C scratch plan --json 2>/dev/null | jq -c '.changes[] | select(.action != "no-op") | {addr, type, action}'
rm -r scratch
sh policy/test.sh
```

Output:

```text
{"addr":"extra","type":"random_pet","action":"create"}
{"addr":"port","type":"random_integer","action":"replace"}
policy tests pass
```

The replacement of `port` is accepted, and the replacement of a pet is still refused. A privileged job that needs a wider exception names a narrower policy that the platform team owns, or none. The command line in the transaction log says which.

## 7. Name the policy in the jobs

The plan job saves a plan under the policy. The apply job names the policy itself and applies that file.

```sh
pravo -C root plan -out change.plan --actor ci --reason "add extra" --policy ../policy/plan.cue | grep -E '^(Plan|Policy|Saved)'
pravo -C root apply change.plan --actor ci --policy ../policy/plan.cue | grep -E '^(Policy|Apply)'
```

Output:

```text
Plan: 1 to add, 0 to change, 0 to destroy.
Policy ../policy/plan.cue (sha256:<digest>) is satisfied.
Saved the plan to change.plan. To apply exactly these actions, run:
Policy ../policy/plan.cue (sha256:<digest>) is satisfied.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The transaction log records the command line that was run, `--policy` included.

```sh
pravo -C root state log | head -n 3
```

Output:

```text
<txn>  apply    applied   <date> <time> <took>
    pravo -C root apply change.plan --actor ci --policy ../policy/plan.cue
    actor ci
```

The log is evidence and prevents nothing. It is as writable as the state. Under a policy an apply is of a saved plan, and a plain apply is refused:

```sh
pravo -C root apply -auto-approve --policy ../policy/plan.cue
```

Output:

```text
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

A saved plan is judged by the policies that the applier names, whatever policy or none was present when the plan was made. Editing a policy does not make saved plans stale.

## What a policy does not bind

A policy binds the runs that name it. A laptop, a shell that an agent holds and a pipeline step with the flag missing are not checked. [The trust anchor](../concepts/policies.md#the-trust-anchor) names the four rights that decide what the file guarantees, and [What a policy does not guarantee](../concepts/policies.md#what-a-policy-does-not-guarantee) lists the limits.

Keep the policy in a directory that the authors of the roots cannot merge into without review. Define the apply job where they cannot edit it. Hold the credentials that change infrastructure in that job only.

## What next

- [Run pravo in a pipeline](ci.md) puts the plan and apply jobs in a pipeline with the exit codes.
- [Let an agent plan and apply](agents-and-mcp.md) binds the tool server to the same file.
