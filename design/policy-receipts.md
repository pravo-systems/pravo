# Policy receipts
Status: proposal. Nothing here exists. The question: for any plan, show at apply time that every policy in force passed. Code claims
come from reading it, CUE ones from scratch runs with cuelang.org/go v0.17.1, design ones by its rules; no tests were run. Out of
scope: roles, ledgers, applies outside pravo, and a writer of the bucket who deletes or rolls back the latest policy objects. Any
reader who can apply can change a policy; review of the policy files is the control. `--out`, plan files and `apply FILE` stay.
## The claim
The receipt is the signed start of an apply transaction. It names the versions judged, each with the digest of its text, and D, the
SHA-256 of the exact bytes of the plan file that stage judged (`plan_digest`, signed today). A verifier fetches the transaction, the
plan and the versions, checks hashes, signatures and that the list omits no version in force at the start less a margin, and runs the
versions on the plan: they pass. It is a signed claim of which versions judged D, re-checkable only while the plan is kept.
## The idea
A policy is a node, `$kind: "policy"`, named by its path in the spec; its `rule` is a CUE struct, standalone (never empty, standard
library imports only), unified with the judged document, which it must make concrete. Every policy judges every plan.
```cue
no_destroy: pravo.#Policy & {rule: summary: destroy: 0} // a replace counts as a destroy
encrypted: pravo.#Policy & {rule: {
	changes: [...{
		type: string, action: string
		if type == "aws_db_instance" && action =~ "^(create|update|replace)$" {after: storage_encrypted!: true}
	}]
}}
```
`!` makes an absent field fail; a bare `storage_encrypted: true` would supply it. The plan prints `policies: +1 new, ~1 changed, -1
removed`; the apply writes the versions last. A version is a new object at the next sequence number, made with `PutIfAbsent`, never
replaced or deleted; a removal writes a tombstone, a version with empty text. The policies in force are each name's latest version that
is not a tombstone. An object is `{text,at,signature}`: its name holds the policy name and number, `at` the signing time. The first
object raises `meta.json` to format 4, which an older pravo refuses while it says 4.
```
policy/no_destroy@00000001.json   put        signed by alice
policy/no_destroy@00000002.json   tombstone  signed by bob     (removed)
policy/encrypted@00000001.json    put        signed by alice   the policies in force are {encrypted@1}
```
## Who may change policies
Any reader who can apply: a plan with a policy entry needs no more than a plan with a resource change. A policy change is reviewed as
code, like the rest of the root: branch protection and code owners on the policy files are the control, and the signed version names
who wrote it. A policy cannot make itself permanent.
- A version is signed by its writer: `pravo/v1/policy-version` over the lineage, the encoded name, the seq, the SHA-256 of the text and
  `at`, with `Store.SignMessage`. A verifier reports it as a transaction signature is reported: `verified`; `key_not_in_file` (a reader
  who left), accepted and shown; a key that is listed and does not verify, no signature, or a gap in the seqs, exit 7. The lineage stops
  a transplant from another state. A root that declares a policy, or a state at format 4, needs a sealed state (exit 5): an unsealed
  one signs nothing.
## The apply
1. The plan: `apply ID` (stored), `apply FILE` (an `--out` file, marked in place) or a plain `apply`. The command hands the engine the
   plan file it parsed and its SHA-256 (`PlanFile.Digest`), for a plain apply the bytes the run would write: that is D. After stage one
   a file's D moves with its marks; it names the bytes that stage read, and judging ignores marks.
2. Under the locks, after the staleness checks (exit 3) and before the start, read the policies in force. An entry of the plan (none
   for a destroy) is (name, base seq, text digest; empty text for a removal); the rule text is in the root's `.cue` files, so the
   configuration fingerprint ties it to the applier's checkout. The name's latest seq is the base, or the base plus one with that
   digest (a retry: `at` differs, so not bytes), else exit 3. A new policy must compile alone and accept the empty plan document
   (exit 6; at plan too).
3. Then, when the stage writes a node and the plan file has a create, update, replace or delete row, `Judge`: compile each policy in
   force and judge the WHOLE file, applied marks and `--only` ignored, in every stage. A failure is exit 5 and records a rejected
   transaction naming the versions and D, as a failed revision check does. A judgement at plan or show is feedback only.
4. The signed start carries D and the policies in force as sorted lines `NAME@SEQ:SHA-256` (encoded names never hold `@` or `:`): one
   more part, present only when the list is not empty, so old transactions verify as before and no domain is added.
5. The writes, as today. The stage that leaves no resource change unapplied then builds, signs and puts the versions, puts and edits
   before tombstones. `at` is the signer's clock, read once when the version is built (a resend sends the same bytes), or the base's
   if later (a slow clock blocks no one); not in the plan entry (a plan lives seven days by default; another reader applies it), not
   read at step 2 (a late landing would look omitted). A policy-only apply judges nothing; its plan asks "yes" and exits 2 with `--detailed-exitcode`. A
   put or a signature that fails or a put that loses its race fails the apply with its resource changes applied; the entry returns in
   the next plan (`LeftToApply` asks the state).

A plain apply that will judge (a policy in force at plan time) first `Put`s the bytes it judged (which signs them), names that plan in
the start, applies the plan in hand as today and marks the stored one applied; otherwise it is unchanged, and a policy that appears
later is judged all the same, its receipt unretained. A stored plan that an apply judged gets `kept` in its progress object (a bool, no
clock; up to two Gets per plan listed), which every collection rule respects, as transactions are never collected.

Policy changes are never judged, nor is a plan with no resource change: the plan that adds a policy is not judged by it, one that
edits it not by the new version; the resource changes of any plan are judged by the policies in the state at the start of that apply.
So a policy that refuses every plan is retired by a policy-only apply (no waiver, no break-glass), and removing a policy is not
gated by the policy: the control is review, and the signed tombstone. The step 2 check catches a misspelt bound
(`summary: destory: <=0`), not a too-strict rule (`summary: add: <=0`).

The judged document is built from the plan file alone, never from the state, the clock, a provider call or the invocation: `created`,
`actor`, `reason`, `git_revision`, `reservation` and `providers` are the file's, applied marks are cleared, `changes` is an empty list,
not null, and `trace_id`, `hints`, `summary.blast_radius` and `changes[].dependents` are absent: a rule that names them fails, at plan
too (`plan --json` still prints them). A rule sees the plan's rows only, not untouched nodes, ephemeral resources or function calls.
## Verify: `pravo state verify TXN [--plan FILE]` (working name)
1. Find TXN in `Store.Transactions()`; check both signatures against the readers file. No list, or unsealed: cannot be checked.
2. Get the plan, by `plan_id` or from `--plan FILE`; its SHA-256 must be D (the id is a handle). A file that saw later stages matches
   only if archived at that stage. If TXN finished, its create, update, replace and delete changes are rows of the plan with that
   action: a tripwire only. The document needs the providers' schemas: a full session.
3. Fetch each listed version: its text digest is the listed one (else exit 7, history rewritten) and its signature is `verified` or
   `key_not_in_file` (shown), as above. Omission, for a TXN with a create, update, replace or delete change (a killed one: whose plan has one): T is the signed `Started`,
   delta 10 minutes (a choice); per name, m (read from the head down) is the highest seq with `at` <= T - delta; if m is a put, the
   list holds the name at seq >= m unless a tombstone above m has `at` <= T + delta; else exit 7. Compile, build the document from the
   plan (not `LoadPlanFor`: no staleness check) and judge. Print the versions, D, the signer, the unknown count, the pravo version.
4. Exit 0 only when the state is sealed, the signatures verify, the plan is present with digest D and the re-run passes. Anything else
   prints why and exits 1; a failing rule, 5; a bad signature, a digest that differs, an untrusted version or list, 7. A
   resource reaches TXN by `record.txn`, which a later write overwrites; a deleted one is only in the signed change list.
## What it proves, and what it does not
For a retained TXN in a sealed state: its signer started an apply of exactly D under the pravo version it names, and named exactly
these versions; each is the text a key signed (a reader's, unless it left), and holds for the document derived from D; the list holds every policy in force at `Started` less delta that was not tombstoned by `Started` plus delta,
if the clocks told the truth; an unmodified pravo writes at most the plan's rows (`checkSaved`). It does not prove:
- That the applier ran the policies (a binary that signs without judging looks the same when the plan passes), or that the list is
  complete: a backdated `Started` or `at` escapes the omission rule, which catches an honest short list.
- That the plan is honest: `before`, flags, the `prior` of a delete, labels, `stats`, `outputs`, drift, data-source answers and the
  `type`, `name` and `mode` of a row are the planner's words; apply checks address, action, known values and revisions.
- That an unknown value is acceptable: `{"$unknown": true}` passes `if after.public == true {error("public")}` and fails a constraint,
  then apply fills it in unjudged. Write constraints, not conditionals.
- The evaluator (the pravo version pins CUE for release builds; dev: `0.1.0-dev`) or every write (a killed apply lists none).
## Limits
- Any reader who can apply can remove a policy, then apply a violating plan in the next apply. Nothing in pravo prevents it; the
  signed tombstone and the receipts show who and when. The control is review of the policy files.
- A bucket writer can delete the latest version of a name (an edit rolls back, a removal returns) or all of `policy/`; the next plan
  shows `+N new` and a policy-only apply restores what the code declares. They can also append a version signed by a key that is not
  in `readers.txt`: it reads `key_not_in_file` and is accepted, as a departed reader's is. A checkout with no policy, on a lowered
  format, applies unjudged; its receipt lacks the list (verify: exit 7). Enforcement is a bucket policy denying delete and overwrite
  on `policy/*` (versioning or object lock; not checked here).
- Every fail-closed read (junk at the next seq, a listed key that does not verify) stops every apply, a policy-only one included; the
  repair is deleting the object. A branch that lacks a policy in force plans `-1 removed`.
- Clocks: `at` is the signer's clock and `Started` the applier's; both are signed and unchecked. No false omission when delta >= d +
  the skew of the two clocks, d the time from reading `at` to the put landing (an ssh-agent signs in up to 90 s) or from `Started` to
  the read of step 2; a version dated ahead counts for no receipt before its `at`. As for `gc` (`sealed-state.md`), clocks must agree.
- The readers pin compares reader keys only, so signatures verify against a signing field of the local `readers.txt` that a reader can
  edit. Verify needs a reader. Policy objects are plain, as the addresses in `txn/` are: no secret in a rule.
- `--out` stays: no evidence of a file plan is kept (archive it for `verify --plan FILE`), two code paths stay, and in a sealed state a
  file holds every value in the clear. Dropping it (`--out`, `apply FILE`, `show FILE`, `serve --plan FILE`, MCP `out`, `plan pull`,
  file marks, `planlock.go`) is a separate cleanup: 42 docs files, 13 to 37 Go test files by pattern, about 233 lines of Go (estimate).
- Planning is not read-only; `import`, `state mv`, `rm`, `push` and `import-tfstate` are not judged. A format-4 state, or a repository
  with a policy, pays a List of `policy/` (S3: a request per 1000 keys) and a Get per name on plan, show and apply (in verify, per
  version listed), and relies on a listing that sees a new key at once (`protect` probes it).
## Changes in the code
| | Where | What |
|---|---|---|
| Added, changed | config, schema, protect, state | `case "policy"` in `register()` (a concrete, non-empty `rule`), `Root.Policies`, `#Policy` in `internal/schema/settings.go`; a policy the walk would skip (optional or required label, deeper than 48) fails the load; `DomainPolicyVersion` over name, seq, text digest and `at` (RFC 3339 nanoseconds UTC, as `Started`), signed with `Store.SignMessage`; `FormatPolicy = 4` and `MaxFormat` with it (today `FormatSealed`), raised by `setFormat` before the first object is written; `Store.PolicySet`, `PutPolicyVersion`, which builds and signs the bytes once (`landed` in `s3.go` compares the bytes one call sent); `Transaction.Policies`, `Txn.SetPolicies`, one start part; key `policy/NAME@SEQ.json`, flat as every prefix is; `Copy` and `holdsNothing` list `policy`, and `Copy` puts it with `PutIfAbsent`: a destination object that differs, or that the source lacks, is `ErrCopyRefused` (`Copy` uses `Put` today, so `state push` would replace the chain; without the prefix it copies an empty set, fail open) |
| Added, changed | engine, cmd | `Plan.Policies`, `PlanFile.Policies` (the entries), counted by `NeedsApply`; `Judge` in `applyRun`, after `checkRevisions`; the document builder, one for plan, show, apply and verify; the sanity check; the omission rule and the `at` guard (`Begin` stamps `Started` before the locks, so the read of step 2 follows it); `PlanProgress.Kept` (set by a callback of `Judge`) and a guard in `gcRules`; `state verify`; the plain apply's `Put`; the refusals. `applyPlanFile` and `runApply` hand the plan file and its digest to the engine, not to `CheckPolicy`; `LeftToApply` takes the policy state; `planWorth`, `--detailed-exitcode`, the prompt (a `NeedsApply` plan without `HasChanges` applies unasked today) and `RenderPlan` count the entry (`site/demos/migrate/derive.go` matches "No changes."); `plan --json` gains a `policies` key beside `summary` and `changes`, `show --json` one beside `plan`; `show` judges for feedback; rows 3, 5, 6 and 7 of `docs/reference/exit-codes.md` and the comments of `ExitRefused` and `ExitUntrusted` in `events.go` |
| DELETED | policy, docs, tests | `--policy` on `plan`, `apply`, `destroy`, `mcp`; the type `Policy`; `LoadPolicyFiles`, `SetPolicies`, `HasPolicies`, `CheckPolicy`, `CheckPolicyView`, `ShortDigest`, `label`, `refused`, `loadPolicies`, `addPolicyFlag`, `serverPolicyArgs`; `flagPolicy`, `Engine.policies`, `serve.Project.Policies`; the call of `withSkipped` in judging (`NewView` keeps it); the refusal "a policy is judged against a saved plan"; the invoker's power to name a policy. Migration, by plain grep: 81 `.md` files mention `--policy` (40 skill copies from `tools/skillgen`), also `tools/skillgen/eval/verify.sh`; 4 Go test files use it and `internal/engine/policy_test.go` the API; the policy pages are rewritten (`blast_radius`, the stubs, exit-code tables, `state-format.md`: "The `txn/` directory can be pruned at any time."); rerun `tools/capture -update` and `go run ./tools/skillgen` |

Cost: +1 each of node kind, object prefix, signing domain, state format, transaction field, plan-file field, progress field, `gcRules`
guard, constant (delta), command; one signed time; no key, lock, file or document member. Removed: `--policy` on four commands, eleven
functions and a type, the invoker's naming. A net addition, smaller than with admins.
## Open (each with a recommendation)
1. Later, additive: audit mode (record, do not refuse). Recommended: later.
2. Later, additive: head pinning against deletion of the latest objects. Recommended: later.
3. Later, if review proves too weak: admins, a signed chain of who may write policies. It needs a new signed field in the version, so
   it is a new version format and not part of this design. Recommended: only on evidence.
