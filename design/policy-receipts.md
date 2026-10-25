# Policy receipts

Status: proposal. Nothing here exists. The question: for any plan, show at apply time that every policy in force passed. Code
statements come from reading it, CUE ones from scratch runs with cuelang.org/go v0.17.1; no test suite was run. In scope: who
may change policies (one role, admin). Out of scope: other roles, ledgers, applies outside pravo, deletion or rollback of the
latest policy objects by a writer of the bucket. `--out`, plan files and `apply FILE` stay.

## The claim
The receipt is the signed start of an apply transaction. It names the policy versions judged, each with the digest of its text,
and D, the SHA-256 of the exact bytes of the plan file that stage judged (`plan_digest`, signed today). A verifier fetches the
transaction, the plan and the versions, checks hashes and signatures, and runs the versions on the plan: they pass. The receipt
is a signed claim of which versions judged D, and can be re-checked only while the plan is retained.

## The idea
A policy is a node, `$kind: "policy"`, named by its path in the spec; its `rule` is a string of standalone CUE (never empty,
standard library imports only), unified with the judged document, which it must make concrete. Every policy judges every plan.
```cue
no_destroy: pravo.#Policy & {rule: "summary: destroy: 0"} // a replace counts as a destroy
encrypted: pravo.#Policy & {rule: #"""
	changes: [...{
		type: string, action: string
		if type == "aws_db_instance" && action =~ "^(create|update|replace)$" {after: storage_encrypted!: true}
	}]
	"""#}
```
`!` makes an absent field fail; a bare `storage_encrypted: true` would supply it. An admin edits the code and applies; the plan
prints `policies: +1 new, ~1 changed, -1 removed` and the apply writes the versions last. As in a versioned bucket, a version is
a new object at the next sequence number, made with `PutIfAbsent` and never replaced or deleted; a removal writes a tombstone,
a version with an empty text. The set in force is the latest version of each name that is not a tombstone. An object is
`{text, signature}`: its name holds the policy name and the number, it holds no time, so a resend compares equal. The first
object raises the state to format 4, which an older pravo refuses: it cannot apply unjudged.
```
policy/no_destroy@00000001.json   put        signed by alice
policy/no_destroy@00000002.json   tombstone  signed by bob     (removed)
policy/encrypted@00000001.json    put        signed by alice   the set in force is {encrypted@1}
```
## Who may change policies
- `.pravo/access/admins.txt`, found beside `readers.txt` and parsed with its parser. A bare name is an active admin, whose key is
  that name's line in `readers.txt`. A full readers line is a retired admin: the key is kept, so leaving `readers.txt` (which
  rewraps the state) does not void the versions signed. Active admins write; active and retired verify. A line that does not
  resolve to one key is exit 6; a rotated key stays as a retired line under a new name.
- Each version is signed by its admin: `pravo/v1/policy-version` over the lineage, the encoded name, the sequence number and the
  SHA-256 of the text. The latest version of a name counts only when `VerifySignature` against the admin lines says verified;
  an unknown key or a bad signature is exit 7, as for a record in the clear. A writer of the bucket who holds no admin key
  cannot forge a policy or a tombstone; the lineage stops a transplant from another state.
- A plan with a policy entry is refused (exit 5), before any lock, unless the applying identity is an active admin
  (`Protection().You.Key` against the admin lines); other users keep applying resource changes. A root that declares a policy or a
  state at format 4 needs a sealed state, as an unsealed one signs nothing (exit 5). No admins file is 5, a malformed one 6.

## The apply
1. The plan: `apply ID` (stored), `apply FILE` (an `--out` file, marked in place) or a plain `apply`. The command hands the
   engine the plan file it parsed and its SHA-256 (`PlanFile.Digest`), for a plain apply the bytes the run would write: that is
   D. After stage one a file's D moves with its marks; it names the bytes that stage read, and judging ignores marks.
2. Under the locks, after the staleness checks (exit 3) and before the start, read the set in force (a List of `policy/`, the
   latest version per name). An entry of the plan (none for a destroy) is (name, base seq, text digest; empty text for a
   removal): the name's latest seq must be the base, or the base plus one with that digest (a retry), else exit 3. Each new
   version is built and signed here, and must compile alone and accept the empty plan document, or it is not written (exit 6;
   at plan too). The put is last.
3. Then, when the stage writes a node and the plan file has a create, update, replace or delete row, `Judge`: compile each text
   of the set and judge the WHOLE file, applied marks and `--only` ignored, in every stage. A failure is exit 5 and records a
   rejected transaction naming the versions and D, as a failed revision check does. A judgement at plan or show is feedback only.
4. The signed start carries D and the versions as sorted lines `NAME@SEQ:SHA-256` (encoded names never hold `@` or `:`): one
   more part, present only when the list is not empty, so old transactions verify as before and no domain is added.
5. The writes, as today. The stage that leaves no resource change unapplied then puts the versions, puts and edits before
   tombstones. A policy-only apply judges nothing and finishes with `record=true`. A put that fails or loses its race fails the
   apply with its resource changes applied; the entry returns in the next plan (`LeftToApply` asks the state).

A plain apply that will judge (a policy in force at plan time) first `Put`s the bytes it judged (which signs them), names that
plan in the start, applies the plan in hand as today and marks the stored one applied; otherwise it is unchanged, and a policy
that appears later is judged all the same, its receipt unretained. A stored plan that an apply judged gets `kept` in its
progress object, which every collection rule respects: kept while a transaction names it, and transactions are never collected.

Policy changes are never judged, nor is a plan with no resource change: the plan that adds a policy is not judged by it, one
that edits it is not judged by the new version; the resource changes of any plan are judged by the set in the state at the start
of that apply. Admins are not exempt from judging their resource changes. Removal is gated by admin rights, not by the policy:
a policy cannot make itself permanent, and one that refuses every plan is retired by a policy-only apply by an admin (no waiver,
no break-glass). The control is the admins file, reviewed like `readers.txt`, and the signed tombstone that names who removed
it. The step 2 check catches a misspelt bound (`summary: destory: <=0` is incomplete), not a too-strict rule (`summary: add:
<=0`). A mixed plan that a broken policy blocks is split in two commits, the policy first.

The judged document is built from the plan file alone, never from the state, the clock, a provider call or the invocation:
`created`, `actor`, `reason`, `git_revision`, `reservation` and `providers` are the file's, applied marks are cleared,
`changes` is an empty list, not null, and `trace_id`, `hints`, `summary.blast_radius` and `changes[].dependents` are absent: a
rule that names them fails, at plan already (`plan --json` still prints them). A rule sees the plan's rows only, not the nodes
the plan leaves alone, nor ephemeral resources and function calls, which a plan file does not hold (judged at plan today,
nowhere after). One builder serves plan and show (feedback), apply and verify.

## Verify: `pravo state verify TXN [--plan FILE]` (working name)
1. Find TXN in `Store.Transactions()`; check both signatures against the readers file. No list, or unsealed: cannot be checked.
2. Get the plan, by `plan_id` or from `--plan FILE`; its SHA-256 must be D (the id is a handle). A file that saw later stages
   matches only if archived at that stage. If TXN finished, its create, update, replace and delete changes are rows of the plan
   with that action: a tripwire only. The document needs the providers' schemas: a full session.
3. Fetch each listed version: its text digest must be the listed one (else exit 7, history rewritten) and its signature must
   verify against the admin lines, active or retired. Compile, build the document from the plan (not `LoadPlanFor`: no
   staleness check) and judge. Print the versions, D, the signer, the count of unknown values and the pravo version.
4. Exit 0 only when the state is sealed, the signatures verify (a killed apply has the start only: writes unknown), the plan is
   present with digest D and the re-run passes. Anything else prints why and exits 1; a failing rule, 5; a bad signature, a
   digest that differs or an untrusted version, 7. A resource reaches TXN by `record.txn`, which a later write overwrites, so
   scan finished applies for its address; a deleted one is only in the signed change list (resource tombstones: later, additive).

## What it proves, and what it does not
For a retained TXN in a sealed state: its signer started an apply of exactly D under the pravo version it names, and named
exactly these versions; each is the text an admin of the verifier's admins file signed and holds for the document derived from
D; an unmodified pravo writes at most the plan's rows (`checkSaved`). It does not prove:
- That the applier ran the policies (a binary that signs without judging looks the same when the plan passes), or that the list
  is every policy in force: a version has no signed time, so no verifier can say which were latest at that start.
- That the plan is honest: `before`, flags, the `prior` of a delete, labels, `stats`, `outputs`, drift, data-source answers and
  the `type`, `name` and `mode` of a row are the planner's words; apply checks address, action, known values and revisions.
- That an unknown value is acceptable: `{"$unknown": true}` passes `if after.public == true {error("public")}` and fails a
  constraint, then apply fills it in unjudged. Write constraints, not conditionals.
- The evaluator (the pravo version pins CUE for release builds; dev builds say `0.1.0-dev`) or every write (a killed apply lists none).
## Limits
- Retirement stops an honest pravo, not a key: `VerifySignature` knows no time, so a retired key held by someone who can write
  the bucket signs a version or tombstone at the next seq that every reader accepts. `admins.txt` is not pinned to the state,
  as `readers.txt` is: a reader who lists himself locally weakens a policy in one apply and applies a violating plan in the
  next (from the rules; not run). The reviewed checkout reads the new head as untrusted (exit 7): detection by lockout.
- Every fail-closed read (junk at the next seq, a bad signature, an unknown key) stops every apply, a policy-only one included;
  the repair is deleting the object in the bucket. A writer there can also delete the latest version of a name (an edit rolls
  back, a removal returns) or all of `policy/`; nothing in the objects shows it, and the difference between code and set shows
  only as an entry in the next plan. A branch that lacks a policy in force plans `-1 removed`: a non-admin is refused, an admin
  removes it; CI as admin controls policies, so its policy applies should be a reviewed `apply ID`.
- Verify needs a reader (stored plans are sealed) and the signer's key in the current `readers.txt`: a signer who has left reads
  `key_not_in_file`. Policy objects are plain, as the addresses in `txn/` are: rules must hold no secret or account id.
- `--out` stays: pravo retains no evidence of a file plan (archive it; `verify --plan FILE` takes it), two code paths stay, and
  in a sealed state a file holds every value in the clear. Dropping it (`--out`, `apply FILE`, `show FILE`, `serve --plan FILE`,
  MCP `out`, `plan pull`, file marks, `planlock.go`) is an independent cleanup: 42 docs files, 13 to 37 Go test files by
  pattern, about 233 lines of Go (an estimate). Do it separately.
- Planning is not read-only; `import`, `state mv`, `rm`, `push` and `import-tfstate` are not judged. A format-4 state or a root
  with a policy pays a List of `policy/` (S3: a request per 1000 keys, every version) and a Get per name on plan, show, apply
  and verify, and relies on a listing that sees a new key at once (`protect` probes it).

## Changes in the code
| | Where | What |
|---|---|---|
| Added | config, schema, protect, state | `case "policy"` in `register()` (a concrete, non-empty `rule`), `Root.Policies`, `#Policy` in `internal/schema/settings.go`; a policy the walk would skip (optional or required label, deeper than 48) fails the load; `DomainPolicyVersion`; the admins parser beside `ParseReaders` (`accessDirs` finds the file); `FormatPolicy = 4`, raised by `setFormat` before the first object is written, so an older pravo refuses the state ("written by a newer pravo"; no way back); `Store.PolicySet`, `PutPolicyVersion`; `Transaction.Policies`, `Txn.SetPolicies`, one start part; key `policy/NAME@SEQ.json`, flat as every prefix is; `Copy` and `holdsNothing` list `policy`, or `state push` copies an empty set (fail open) |
| Added, changed | engine, cmd | `Plan.Policies`, `PlanFile.Policies` (the entry), counted by `NeedsApply`; `Judge` in `applyRun`, after `checkRevisions`; an `Options` hook for the admin lines; the document builder; the sanity check; `PlanProgress.Kept` (set by a callback of `Judge`) and a guard in `gcRules`; `state verify`; the plain apply's `Put`; the refusals. `applyPlanFile` and `runApply` hand the plan file and its digest to the engine, not to `CheckPolicy`; `LeftToApply` takes the policy state; `planWorth`, `--detailed-exitcode`, the prompt (a `NeedsApply` plan without `HasChanges` applies unasked today) and `RenderPlan` count the entry (`site/demos/migrate/derive.go` matches "No changes."); `plan --json` and `show --json` gain a `policies` key beside `plan`; `show` judges for feedback |
| DELETED | policy, docs, tests | `--policy` on `plan`, `apply`, `destroy`, `mcp`; the type `Policy`; `LoadPolicyFiles`, `SetPolicies`, `HasPolicies`, `CheckPolicy`, `CheckPolicyView`, `ShortDigest`, `label`, `refused`, `loadPolicies`, `addPolicyFlag`; the call of `withSkipped` in judging (`NewView` keeps it); the refusal "a policy is judged against a saved plan"; the invoker's power to name a policy. Migration, by plain grep: 81 `.md` files mention `--policy` (40 skill copies from `tools/skillgen`), also `tools/skillgen/eval/verify.sh`, and 4 Go test files use it; the policy pages are rewritten (`blast_radius`, the stubs, exit-code tables, `state-format.md`: "`txn/` can be pruned at any time"); rerun `tools/capture -update` and `go run ./tools/skillgen` |

Cost: +1 each of node kind, object prefix, `admins.txt` (a file of its own: `readers.txt` is pinned to the state), signing
domain, state format, transaction field, plan-file field, progress field, `gcRules` guard, command; no key, lock or document
member. Removed: `--policy` on four commands, five functions and a type, the stubs, the invoker's naming. A net addition.

## Open (each with a recommendation)
1. Retention: `kept` as a bool, no clock; it costs up to two Gets per plan in every plan listing. Else a 90 day constant (receipts
   lapse), or no mark and no implicit `Put`: `plan pull` before the apply (that its bytes equal D is not run). Recommended: the bool.
2. Recommended as written: a sealed state is required; the first admin is named by the first reviewed commit of `admins.txt`;
   a policy-only plan asks "yes" and exits 2 with `--detailed-exitcode`. Later, additive: `sha256(admins.txt)` pinned in
   `meta.json` (stops the reader bypass for a field and a check), audit mode (record, do not refuse), head pinning.