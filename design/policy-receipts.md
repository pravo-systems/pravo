# Policy receipts
Status: proposal. Nothing here exists. The question: for any plan, show at apply time that every policy in force passed. Code claims
come from reading it, CUE ones from scratch runs with cuelang.org/go v0.17.1, design ones by its rules; no tests were run. In scope:
who may change policies, and who the admins are. Out of scope: other roles, ledgers, applies outside pravo, a compromised admin,
rollback of the latest policy or admin-set object by a bucket writer. `--out`, plan files and `apply FILE` stay.
## The claim
The receipt is the signed start of an apply transaction. It names the versions judged, each with the digest of its text, and D, the
SHA-256 of the exact bytes of the plan file that stage judged (`plan_digest`, signed today). A verifier fetches the transaction, the
plan and the versions, checks hashes, signatures and that the list omits no version in force at the start less a margin, and runs the
versions on the plan: they pass. It is a signed claim of which versions judged D, re-checkable only while the plan is kept.
## The idea
A policy is a node, `$kind: "policy"`, named by its path in the spec; its `rule` is a string of standalone CUE (never empty, standard
library imports only), unified with the judged document, which it must make concrete. Every policy judges every plan.
```cue
no_destroy: pravo.#Policy & {rule: "summary: destroy: 0"} // a replace counts as a destroy
encrypted: pravo.#Policy & {rule: #"""
	changes: [...{
		type: string, action: string
		if type == "aws_db_instance" && action =~ "^(create|update|replace)$" {after: storage_encrypted!: true}
	}]
	"""#}
```
`!` makes an absent field fail; a bare `storage_encrypted: true` would supply it. The plan prints `policies: +1 new, ~1 changed, -1
removed`; the apply writes the versions last. A version is a new object at the next sequence number, made with `PutIfAbsent`, never
replaced or deleted; a removal writes a tombstone, a version with empty text. The policies in force are each name's latest version that
is not a tombstone. An object is `{text,at,aseq,signature}`: its name holds the policy name and number, `at` the signing time, `aseq`
the admin set it was signed under. The first object raises `meta.json` to format 4, which an older pravo refuses while it says 4.
```
policy/_admins@00000001.json      genesis    signed by alice   {alice, bob}
policy/no_destroy@00000001.json   put        signed by alice   aseq 1
policy/no_destroy@00000002.json   tombstone  signed by bob     aseq 1 (removed)
policy/encrypted@00000001.json    put        signed by alice   aseq 1; the policies in force are {encrypted@1}
```
## Who may change policies
- The admin set is in the state: versions of the reserved name `_admins`. The text is sorted lines `NAME ssh-ed25519 BASE64`, a
  `readers.txt` name and signing key, unique. Genesis (seq 1) is the first reviewed set, signed by a listed admin. Version n has `aseq`
  n-1 (genesis: 1) and is signed by an admin of n-1 who is also in n. A removed key stays in the earlier sets, so its versions verify.
- `.pravo/access/admins.txt`, beside `readers.txt`, is the desired set: at least one bare name, each a `readers.txt` line whose signing
  key it takes, and a line `genesis SHA256`, the digest of the text of `_admins@1` (the plan prints it); else exit 6. `_admins` is
  reconciled like a policy; plan and prompt print `admins: +1 new, -1 removed` with each name and `SHA256:fingerprint`.
- A version is signed by its admin: `pravo/v1/policy-version` over the lineage, the encoded name, the seq, the SHA-256 of the text,
  `at` and `aseq`. It counts only when `VerifySignature` over the set `aseq` names says verified; `aseq` and `at` never fall along a
  name. An unknown key, a bad signature, a missing set, a gap or a fall is exit 7. While the chain is intact, a bucket writer without
  an admin key cannot append a version, tombstone or set; the lineage stops a transplant.
- A plan with a policy or `admins:` entry is refused (exit 5), before any lock, unless the applier's signing key is in the head of the
  chain, never `admins.txt`; step 2 tests it again on the head it cites. Others apply resource changes. A root that declares a policy
  or a state at format 4 needs a sealed state, as an unsealed one signs nothing (exit 5).
- Repository anchor, as for sealing: with no chain and `admins.txt` or a format-4 state a writing command is refused (exit 5), readers
  warn, only the genesis apply of a listed admin runs; another genesis than the line is exit 7. The chain is read when the repository
  has `admins.txt` or a policy, whatever `meta.json` says; a chain below format 4 is exit 7, a policy with no admins file 5.
- Why a chain, not the `readers.txt` pin or nothing: that pin compares the reader keys of `meta.json`, which is unsigned, with the
  file; any reader can `rewrap`, write the bucket and edit both, so it stops an honest binary only, and nothing leaves the two-step
  attack (weaken a policy locally, apply a violating plan). In the chain, trust is in keys the previous set signed and a reviewed
  digest, so a reader without an admin key who edits `admins.txt`, an admin's signing field or the `genesis` line is refused (5) or
  reads exit 7, and what is appended without one is untrusted (7). It does not stop an admin key or a deletion (Limits).
## The apply
1. The plan: `apply ID` (stored), `apply FILE` (an `--out` file, marked in place) or a plain `apply`. The command hands the engine the
   plan file it parsed and its SHA-256 (`PlanFile.Digest`), for a plain apply the bytes the run would write: that is D. After stage one
   a file's D moves with its marks; it names the bytes that stage read, and judging ignores marks.
2. Under the locks, after the staleness checks (exit 3) and before the start, read the chain and the policies in force. An entry of the
   plan (none for a destroy) is (name, base seq, text digest; empty text for a removal). Its text, built from the applier's checkout
   (`admins.txt` is outside the configuration fingerprint), must have that digest; the name's latest seq is the base or the base plus
   one that verifies with it (a retry: `at` differs, so not bytes), else exit 3. A new policy must compile alone and accept the empty
   plan document (exit 6; at plan too).
3. Then, when the stage writes a node and the plan file has a create, update, replace or delete row, `Judge`: compile each policy in
   force and judge the WHOLE file, applied marks and `--only` ignored, in every stage. A failure is exit 5 and records a rejected
   transaction naming the versions and D, as a failed revision check does. A judgement at plan or show is feedback only.
4. The signed start carries D and the policies in force as sorted lines `NAME@SEQ:SHA-256` (encoded names never hold `@` or `:`): one
   more part, present only when the list is not empty, so old transactions verify as before and no domain is added.
5. The writes, as today. The stage that leaves no resource change unapplied then builds, signs and puts the versions, each citing the
   head read at step 2: genesis first (if its put loses, nothing else is written: exit 3), then puts and edits, tombstones, `_admins`.
   `at` is the signer's clock, read once when the version is built (a resend sends the same bytes), or the base's if later (a slow
   clock blocks no one); not in the plan entry (a plan lives seven days by default; another admin applies it), not read at step 2 (a
   late landing would look omitted). A policy-only apply judges nothing; its plan asks "yes" and exits 2 with `--detailed-exitcode`. A
   put or a signature that fails or a put that loses its race fails the apply with its resource changes applied; the entry returns in
   the next plan (`LeftToApply` asks the state).

A plain apply that will judge (a policy in force at plan time) first `Put`s the bytes it judged (which signs them), names that plan in
the start, applies the plan in hand as today and marks the stored one applied; otherwise it is unchanged, and a policy that appears
later is judged all the same, its receipt unretained. A stored plan that an apply judged gets `kept` in its progress object (a bool, no
clock; up to two Gets per plan listed), which every collection rule respects, as transactions are never collected.

Policy and admin-set changes are never judged, nor is a plan with no resource change: the plan that adds a policy is not judged by it,
one that edits it not by the new version; the resource changes of any plan are judged by the policies in the state at the start of that
apply. Admins are not exempt. Removal is gated by admin rights, not by the policy: a policy cannot make itself permanent, and one that
refuses every plan is retired by a policy-only apply by an admin (no waiver, no break-glass). The control is the reviewed `admins.txt`
and the signed chain. The step 2 check catches a misspelt bound (`summary: destory: <=0`), not a too-strict rule (`summary: add: <=0`).

The judged document is built from the plan file alone, never from the state, the clock, a provider call or the invocation: `created`,
`actor`, `reason`, `git_revision`, `reservation` and `providers` are the file's, applied marks are cleared, `changes` is an empty list,
not null, and `trace_id`, `hints`, `summary.blast_radius` and `changes[].dependents` are absent: a rule that names them fails, at plan
too (`plan --json` still prints them). A rule sees the plan's rows only, not untouched nodes, ephemeral resources or function calls.
## Verify: `pravo state verify TXN [--plan FILE]` (working name)
1. Find TXN in `Store.Transactions()`; check both signatures against the readers file. No list, or unsealed: cannot be checked.
2. Get the plan, by `plan_id` or from `--plan FILE`; its SHA-256 must be D (the id is a handle). A file that saw later stages matches
   only if archived at that stage. If TXN finished, its create, update, replace and delete changes are rows of the plan with that
   action: a tripwire only. The document needs the providers' schemas: a full session.
3. Walk `_admins` from genesis, whose digest is the `genesis` line, each link signed as above (else exit 7). Fetch each listed version:
   its text digest is the listed one (else exit 7, history rewritten) and its signature verifies against the set its `aseq` names.
   Omission, for a TXN with a create, update, replace or delete change (a killed one: whose plan has one): T is the signed `Started`,
   delta 10 minutes (a choice); per name, m (read from the head down) is the highest seq with `at` <= T - delta; if m is a put, the
   list holds the name at seq >= m unless a tombstone above m has `at` <= T + delta; else exit 7. Compile, build the document from the
   plan (not `LoadPlanFor`: no staleness check) and judge. Print the versions, D, the signer, the unknown count, the pravo version.
4. Exit 0 only when the state is sealed, the signatures verify, the plan is present with digest D and the re-run passes. Anything else
   prints why and exits 1; a failing rule, 5; a bad signature, a digest that differs, an untrusted version, chain or list, 7. A
   resource reaches TXN by `record.txn`, which a later write overwrites; a deleted one is only in the signed change list.
## What it proves, and what it does not
For a retained TXN in a sealed state: its signer started an apply of exactly D under the pravo version it names, and named exactly
these versions; each is the text an admin of the set its `aseq` names signed, in a chain from the reviewed genesis, and holds for the
document derived from D; the list holds every policy in force at `Started` less delta that was not tombstoned by `Started` plus delta,
if the clocks told the truth; an unmodified pravo writes at most the plan's rows (`checkSaved`). It does not prove:
- That the applier ran the policies (a binary that signs without judging looks the same when the plan passes), or that the list is
  complete: a backdated `Started` or `at` escapes the omission rule, which catches an honest short list.
- That the plan is honest: `before`, flags, the `prior` of a delete, labels, `stats`, `outputs`, drift, data-source answers and the
  `type`, `name` and `mode` of a row are the planner's words; apply checks address, action, known values and revisions.
- That an unknown value is acceptable: `{"$unknown": true}` passes `if after.public == true {error("public")}` and fails a constraint,
  then apply fills it in unjudged. Write constraints, not conditionals.
- The evaluator (the pravo version pins CUE for release builds; dev: `0.1.0-dev`) or every write (a killed apply lists none).
## Limits
- A removed admin with the key and bucket write appends a version or tombstone that cites a set from before the removal, to a name last
  edited before it or to a new name, and every verifier accepts it: removal revokes nothing. `at` does not narrow this (the backend
  gives no write time and the forger backdates it); `aseq` never falling only limits the reach, and only while `policy/` is write-once
  at the bucket. It also fails the retained honest receipts it is backdated across (exit 7), as an omission would.
- A bucket writer without an admin key can delete the latest version of a name or of `_admins` (an edit rolls back, a removal returns,
  a removed admin comes back); the next plan shows an entry. Deleting all of `policy/` is a refusal (no chain), and a genesis written
  next is not the reviewed one (exit 7 where the checkout holds the `genesis` line), but the writer's own edited checkout applies. A
  checkout with neither `admins.txt` nor a policy, on a lowered format, applies unjudged; its receipt lacks the list (verify: exit 7).
  Enforcement is a bucket policy denying delete and overwrite on `policy/*` (versioning or object lock; not checked here).
- Every fail-closed read (junk at the next seq, a bad signature, an unknown key) stops every apply, a policy-only one included; the
  repair is deleting the object. A branch that lacks a policy in force, or is behind on `admins.txt`, plans `-1 removed`, as does a
  change to an admin's line in `readers.txt`: a non-admin is refused, an admin applies. A handover is two steps (add the new name,
  whose key removes the old). If every admin key is lost: delete `policy/`, write a new genesis and line; old receipts fail.
- Clocks: `at` is the signer's clock and `Started` the applier's; both are signed and unchecked. No false omission when delta >= d +
  the skew of the two clocks, d the time from reading `at` to the put landing (an ssh-agent signs in up to 90 s) or from `Started` to
  the read of step 2; a version dated ahead counts for no receipt before its `at`. As for `gc` (`sealed-state.md`), clocks must agree.
- The readers pin compares reader keys only, so transaction signatures verify against a signing field of the local `readers.txt` that a
  reader can edit (versions use the chain). Verify needs a reader and the signer's key in the current `readers.txt`: a signer who has
  left reads `key_not_in_file`. Policy objects are plain, as the addresses in `txn/` are: no secret in a rule.
- `--out` stays: no evidence of a file plan is kept (archive it for `verify --plan FILE`), two code paths stay, and in a sealed state a
  file holds every value in the clear. Dropping it (`--out`, `apply FILE`, `show FILE`, `serve --plan FILE`, MCP `out`, `plan pull`,
  file marks, `planlock.go`) is a separate cleanup: 42 docs files, 13 to 37 Go test files by pattern, about 233 lines of Go (estimate).
- Planning is not read-only; `import`, `state mv`, `rm`, `push` and `import-tfstate` are not judged. A format-4 state, or a repository
  with `admins.txt` or a policy, pays a List of `policy/` (S3: a request per 1000 keys) and a Get per name and `_admins` version on
  plan, show and apply (in verify, per version walked), and relies on a listing that sees a new key at once (`protect` probes it).
## Changes in the code
| | Where | What |
|---|---|---|
| Added, changed | config, schema, protect, state | `case "policy"` in `register()` (a concrete, non-empty `rule`), `Root.Policies`, `#Policy` in `internal/schema/settings.go`; a policy the walk would skip (optional or required label, deeper than 48) fails the load; `DomainPolicyVersion`, also for `_admins`, over name, seq, text digest, `at` (RFC 3339 nanoseconds UTC, as `Started`) and `aseq`; a parser for the set text over `ParseSSHBlob` (`ParseReaderLine` wants four fields) and the chain walk from genesis, over `[]protect.Reader{Name, Sign}`; an accessor for the signing key of the store's identity (`signingIdentity` is unexported; `Protection().You.Key` is the reader key); `FormatPolicy = 4` and `MaxFormat` with it (today `FormatSealed`), raised by `setFormat` before the first object is written; `Store.PolicySet`, `PutPolicyVersion`, which builds and signs the bytes once (`landed` in `s3.go` compares the bytes one call sent); `Transaction.Policies`, `Txn.SetPolicies`, one start part; key `policy/NAME@SEQ.json`, flat as every prefix is; `Copy` and `holdsNothing` list `policy`, and `Copy` puts it with `PutIfAbsent`: a destination object that differs, or that the source lacks, is `ErrCopyRefused` (`Copy` uses `Put` today, so `state push` would replace the chain; without the prefix it copies an empty set, fail open) |
| Added, changed | engine, cmd | `Plan.Policies`, `PlanFile.Policies` (the entries, `_admins` among them), counted by `NeedsApply`; `Judge` in `applyRun`, after `checkRevisions`; an `Options` field for the desired admin set, read from `admins.txt` beside `loadReadersFile` in `cmd/pravo/access.go` (it reuses `accessDirs`; bare names resolve through `readers.txt`); the document builder, one for plan, show, apply and verify; the sanity check; the omission rule and the `at` guard (`Begin` stamps `Started` before the locks, so the read of step 2 follows it); `PlanProgress.Kept` (set by a callback of `Judge`) and a guard in `gcRules`; `state verify`; the plain apply's `Put`; the refusals, and the admins expectation (chain, `genesis` line, format) beside `checkReadersFile` in `openStateAs`, which the `protect` commands do not go through. `applyPlanFile` and `runApply` hand the plan file and its digest to the engine, not to `CheckPolicy`; `LeftToApply` takes the policy state; `planWorth`, `--detailed-exitcode`, the prompt (a `NeedsApply` plan without `HasChanges` applies unasked today) and `RenderPlan` count the entry (`site/demos/migrate/derive.go` matches "No changes."); `plan --json` gains a `policies` key beside `summary` and `changes`, `show --json` one beside `plan`; `show` judges for feedback; rows 3, 5, 6 and 7 of `docs/reference/exit-codes.md` and the comments of `ExitRefused` and `ExitUntrusted` in `events.go` |
| DELETED | policy, docs, tests | `--policy` on `plan`, `apply`, `destroy`, `mcp`; the type `Policy`; `LoadPolicyFiles`, `SetPolicies`, `HasPolicies`, `CheckPolicy`, `CheckPolicyView`, `ShortDigest`, `label`, `refused`, `loadPolicies`, `addPolicyFlag`, `serverPolicyArgs`; `flagPolicy`, `Engine.policies`, `serve.Project.Policies`; the call of `withSkipped` in judging (`NewView` keeps it); the refusal "a policy is judged against a saved plan"; the invoker's power to name a policy. Migration, by plain grep: 81 `.md` files mention `--policy` (40 skill copies from `tools/skillgen`), also `tools/skillgen/eval/verify.sh`; 4 Go test files use it and `internal/engine/policy_test.go` the API; the policy pages are rewritten (`blast_radius`, the stubs, exit-code tables, `state-format.md`: "The `txn/` directory can be pruned at any time."); rerun `tools/capture -update` and `go run ./tools/skillgen` |

Cost: +1 each of node kind, object prefix, reserved name, `admins.txt` (and its `genesis` line), signing domain, state format,
transaction field, plan-file field, progress field, `gcRules` guard, constant (delta), key accessor, command; two signed fields; no
key, lock or document member. Removed: `--policy` on four commands, eleven functions and a type, the invoker's naming. A net addition.
## Open (each with a recommendation)
1. Later, additive: audit mode (record, do not refuse). Recommended: later.
2. Later, additive: head pinning against deletion of the latest objects, which can also sign the policy heads into the set version that
   removes an admin; a plan entry that re-signs each head citing a set with a removed key, closing the removed-admin case for existing
   names (from the rules; not run; a version signed concurrently under the old set would be refused for good, as policy writes take no
   lock). Recommended: later.
