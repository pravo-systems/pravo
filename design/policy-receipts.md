# Policy receipts

Status: proposal. Nothing here exists.

## The question

For any plan, show cryptographically that every policy that was in the code when the plan was made
was judged against it and passed. Access control, rollback and applies outside pravo are out of scope.

## The idea

A policy is an entity in the specification, so it is checked in with the code. A policy judges the plan
file and nothing else, so the verdict is a pure function of two things that are already hashed: the
plan file and the code. The plan file records which policies judged it. No new object, no new key.

```cue
$policies: no_delete: {
	changes: [...{
		addr:   string
		mode:   string
		action: string
		if mode == "managed" && action == "delete" {
			error("\(addr): delete is not allowed")
		}
	}]
}
```

`$policies` is a meta block like `$settings`. Its members are not unified with the resources. Every
policy in it judges every plan of the root. There is no flag to omit. A policy removed from the code
stops judging, and the change is in git.

## What the plan file gains

```json
"policies": [ { "name": "no_delete", "digest": "sha256 of the policy source" } ],
"blast_radius": 4,
"dependents": { "vpc": ["subnet", "db"] }
```

A saved plan has passed every policy, so a verdict field adds nothing. `plan` refuses a plan that fails
(exit 5) and saves none. `blast_radius` and `dependents` are read from the state once, when the plan is
made, and written into the file. They are the only state-derived values a policy needs; the rest of the
document is the plan's changes.

## The document is a function of the file

The policy sees the changes of the plan, the two values above, the counts and the provider versions.
It does not see:

- stubs for the nodes the plan left alone. They carry only address, type and name, and a rule over what
  exists is an audit and not a judgment of a plan (`pravo state query`);
- `created`, `actor`, `reason`, `git_revision` and `trace_id`. They are the caller's words, and the
  policy docs already say that a rule over them binds nothing.

This is a deliberate narrowing. A policy judges what a plan does.

## The chain

```
plan digest (SHA-256 of the file; signed by the storing identity in a sealed state)
  ├ policies[].digest        covered by the digest
  ├ blast_radius, dependents covered by the digest
  └ config_fingerprint       digest of every CUE file of the root, $policies included
        └ git commit         the code
```

## Verify

`pravo plan verify PLAN [--at COMMIT]`, and the same check inside `apply`:

1. The plan's fingerprint equals the fingerprint of the code (the checkout, or `COMMIT`). `apply`
   already refuses a plan when it differs.
2. The digest of every policy in the code, and the set, equal `policies` in the file. Nothing present
   was left out.
3. Build the document from the file, run every policy, and require that all pass.

Steps 1 and 2 are hash comparisons. Step 3 is a re-run, so the verifier does not trust the maker of
the file. The signature is for a third party that will not re-run it: it says the identity stored this
file with this list of policies.

## Changes

| Where | Change |
|---|---|
| Specification loader | Accept `$policies`. A policy in the root is refused today and `$settings.policy` is an error; this reverses both on purpose. |
| `NewView` | A mode that builds the document from the plan only. Judge that. |
| `PlanFile` | Add `policies`, `blast_radius`, `dependents`. Store full `ConfigFiles` digests. |
| Apply transaction | Add the policy set to the signed start, under a new domain version so old transactions verify. |
| Commands | `pravo plan verify`; `plan show` and `apply` print the list. |

Cost: about 100 bytes per policy, plus the dependents of the changed nodes.

## Limits

- The proof is as good as the evaluator. The file records its version, and the verifier says when it
  differs.
- A policy that is wrong, or misspelt and forbids nothing, passes. The file proves what judged the
  plan, not that the rule was good.
- A rule about what exists, such as a count of resources, is not a plan policy any more.
