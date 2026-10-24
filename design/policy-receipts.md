# Policy receipts

Status: proposal. Nothing here exists.

## The question

For any plan, show cryptographically that every policy that was in the code when the plan was made
was judged against it and passed. Access control, rollback and applies outside pravo are out of scope.

## The idea

A policy is an entity in the specification, so it is checked in with the code. A plan carries a
**receipt** naming each policy that judged it, by digest, and the verdict. The plan file is already
bound to the code by its configuration fingerprint and to itself by its digest. The receipt hangs on
those two links. No new key and no new service.

```cue
$policies: no_delete: {
	// the same CUE a --policy file holds today, judged against the plan document
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
policy in it judges every plan of the root. There is no flag and nothing to omit, so "the policies
that were present" is defined by the code and not by the invocation. A policy that is removed
from the code stops judging, and the change is in git.

## The receipt

`plan` judges the plan document against every entry of `$policies` (the code that `CheckPolicyView`
has now). If any fails, the plan is refused with exit 5 and not saved. If all pass, the plan file
gains:

```json
"receipt": {
  "document": "sha256 of the plan document that was judged",
  "policies": [ { "name": "no_delete", "digest": "sha256 of the policy source", "verdict": "pass" } ],
  "set": "sha256 over the sorted (name, digest) pairs",
  "evaluator": "pravo 0.9.0 / cue 0.14.1"
}
```

The plan file is hashed (`PlanDigest`), recorded by the apply transaction, and signed by the
identity that stores it in a sealed state (`plansign.go`). The receipt is inside that hash, so the
signature covers it with no new signing code.

## The chain

```
signature of the stored plan  (who made it; existing)
  └ plan digest                       covers the receipt
      ├ receipt.policies[].digest  = sha256 of each policy source
      ├ receipt.set                = sha256 of the whole set
      └ config_fingerprint         = digest of every CUE file of the root
            └ git commit            = the code, including $policies
```

Two facts are enough:

1. The receipt's set equals the set of policies in the code the fingerprint names. Nothing present was
   left out, and nothing absent is claimed.
2. Re-running each policy over the document gives `pass`.

## Verify

`pravo plan verify PLAN [--at COMMIT]`, and the same check inside `apply`:

1. Take the code: the checkout, or `COMMIT`. Its fingerprint must equal the plan's. The plan already
   stores a per-file digest (`ConfigFiles`); store it in full so a mismatch names the file.
2. Compute the digest of every entry of `$policies` and the set. They must equal the receipt's.
3. Check that the document in the plan hashes to `receipt.document`.
4. Evaluate each policy over that document. Every one must pass, and the verdicts must equal the
   receipt's.
5. Check the plan signature, if the state is sealed and a signature is there.

Steps 1 to 4 need no key and no trust in the maker of the plan. A forged receipt fails step 4, because
the verifier computes the verdict itself. The signature is for a third party that does not run the
evaluator and does not want to: it attests that the identity stored a plan whose receipt says
`pass`.

`apply` already refuses a plan whose fingerprint differs from the code (`ErrStalePlan`). So a plan
that applies was made from the code it is applied under, which holds exactly the policies it was
judged by. A policy added after the plan makes the plan stale, and the replan is judged by it.

## Changes

| Where | Change |
|---|---|
| Specification loader | Accept `$policies`, compile each entry in the root's CUE context. Today a policy in the root is refused and `$settings.policy` is an error. This reverses both on purpose. |
| `engine.CheckPolicyView` | Judge `$policies` and return a result for each policy; drop `--policy`, or keep it only to add a rule for a rehearsal. |
| `PlanFile` | Add `receipt`; store full `ConfigFiles` digests; store the judged document. |
| Apply transaction | Add `receipt.set` to the signed start (a new domain version, so old transactions verify). The log then shows which policies an apply ran under. |
| Commands | `pravo plan verify`; `plan show` and `apply` print the receipt. |

## The one real wrinkle

Today the judged document is not a function of the plan file. It holds entries for the nodes the
plan left alone, built from the state, and `blast_radius` and `dependents`, read from the state at that
moment. Step 4 above needs the same document. Write the state-derived parts into the plan file when it
is made and judge that. The document is then a pure function of the file, and anyone can reproduce
the verdict from the file and the code.

## Limits

- The proof is only as good as the evaluator. A verifier that runs another version of CUE may judge
  differently, so the receipt records the version and the verifier says when it differs.
- A policy that is wrong, or misspelt and forbids nothing, passes. The receipt proves what was
  judged, not that the rule was good.
- A plan whose receipt is true can still be the wrong plan. The proof covers the plan file and not
  what happens after it.
