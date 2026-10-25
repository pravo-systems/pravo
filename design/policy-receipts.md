# Policy receipts

Status: proposal. Nothing here exists. The question: for any plan, show at apply time that every policy in force passed.
Out of scope: access control, rollback of stored state, applies outside pravo, roles, ledgers, admin keys. Statements about
the code come from reading it, those about CUE from scratch runs with cuelang.org/go v0.17.1; no test suite was run. Two
changes, in this order: A, one way to save a plan (the state); B, the policies. B assumes A.

## The claim

A verifier can run the policies listed in a transaction again, on the plan whose digest D its signed start names, and they
pass. The applier asserts one thing no verifier can check: that the list is the policies in force. In a sealed state the start
is signed, so a list that fails the re-run or omits a declared policy names its signer. Where the plan is gone, or the state is
unsealed (nothing signs), the transaction is an attestation: readable, no proof. The claim covers the changes in the plan:
"passes on these bytes at that start", not "the applier's pravo ran them".

A policy is a name (its path in the spec) and a text of CUE. The declared set is the policies of the code the plan was made
from; the recorded set R, those in the state, written by the last apply that finished its plan; the judged set, their union.

## The idea

A policy is a node of the specification: `$kind: "policy"` anywhere in the root, in a module instance too, so a company
baseline is a module. Its `rule` is a string of standalone CUE, as a `--policy` file is today: unified with the judged
document, it must be concrete. It must compile alone and import only the standard library, or the load fails; so does a policy
the walk would skip (an optional or required label, deeper than 48). The text judged, hashed, recorded and shown is the
author's bytes. Every policy judges every plan.

```cue
no_destroy: pravo.#Policy & {rule: "summary: destroy: 0"} // a replace counts as a destroy
encrypted: pravo.#Policy & {rule: #"""
	changes: [...{
		type: string, action: string
		if type == "aws_db_instance" && action =~ "^(create|update|replace)$" {after: storage_encrypted!: true}
	}]
	"""#}
permanent: pravo.#Policy & {rule: #"policies: permanent: "new" | "kept""#} // refuses its own removal and edit
```

`!` makes an absent field fail; a bare `storage_encrypted: true` would supply it. A root that declares a policy stops an older
binary at the unknown `$kind`. The judged set is taken by name and text:

- Only recorded: the policy is being removed, and the plan that removes it must pass it. Delete `no_destroy` and plan: the
  recorded copy judges the plan and fails it if it destroys anything. Only declared: it is being added; the plan must pass it.
  Changed: judged in both versions. The first plan that installs policies has no recorded set. Code that omits a policy
  removes it once the plan passes it, so R is a one-step guard, not a floor: `plan`, `show` and the prompt print `policies:
  +1 new, -1 removed, ~0 changed`.
- The document gives each rule its policy's standing: `policies: NAME` is `"new"`, `"kept"`, `"changed"` or `"removed"`, so a
  rule that allows only `"new" | "kept"` fails every plan that removes or edits its policy.

## The apply

1. The plan is a stored plan: `apply ID`, or plain `apply` and `destroy`, which plan, ask, store and apply from the stored copy.
   Its bytes never change, so every stage (one apply of it: `--only`, a resume) names one D; N stages are N transactions.
2. Staleness as today, and a fingerprint that also covers the declared policies as loaded, so a `-T` value cannot change the
   set unseen. A failure to compute it is stale, not fine. Exit 3.
3. Lock the nodes the plan writes, plus `$policies` when this stage will write R or writes no node (a start with no lock that
   carries its id reads as interrupted). Under the locks, before the start, one hook, `Judge`: read R (absent only on not found;
   any other failure refuses); an R whose digest is not the plan's `policy_set` is stale (exit 3, re-plan); judge the whole file
   against the judged set, applied marks and `--only` ignored (a failure is exit 5: only locks and the apply's claim on the
   stored plan were written, and no transaction is recorded); mark the plan kept; put the list (name, text, origin: recorded,
   declared or both) on the transaction. A root with no policy and no R skips all of it.
4. The signed start, then the writes, as today. No record, output or R is written before the judgement.
5. A stage that leaves a change of the plan unapplied ends here. The last stage writes R as the declared set when that differs
   (an empty set removes the object), then the transaction finishes. If that write fails, the apply fails with its changes
   applied: the old R stays in force and the next apply that writes records the set.

A plan with nothing to write stores nothing and makes no claim; one that writes only outputs or records is stored and judged
like any other; a change of the declared set alone is no plan. `plan` and `show ID` run `Judge` for feedback; only the apply's
judgement is evidence, and `applyRun` refuses a root that declares a policy when no hook is set.

## The judged document

The `plan` member of `show --json ID`, built from the plan file alone, never from the state, the clock, a provider or the
invocation: `created`, `actor`, `reason`, `git_revision`, `reservation` and `providers` are the file's (today they describe the
apply), `trace_id` and `hints` are empty, applied marks are cleared, and `summary.blast_radius` and `changes[].dependents` are
absent (a bound or a `!` on one fails; a bare value would supply it). Plus `policies`. A rule sees the plan's rows, not the
nodes it leaves alone, ephemeral resources or functions: installing a policy audits nothing already there. The plan file gains
one field, `policy_set`: the digest of R as the planner read it, absent when there was none.

## What is stored

| Object | Written when | Kept until |
|---|---|---|
| stored plan, progress | `plan --store`; plain apply after "yes"; `kept` in progress, just before the start | today's rules; if an apply judged policies, `KeptFor` (90 days) after it completes or expires |
| transaction `txn/ID.json` | start (with the list), rewritten at finish | never collected |
| `policies.json` | last write of an apply that finishes its plan | replaced by the next; an empty set removes it |

## Verify: `pravo state verify TXN` (working name)

1. Read TXN and check both signatures against the readers file. No list: not attested.
2. Fetch the stored plan by `plan_id`, in a full session (the providers that planned decode it). The SHA-256 of its opened
   bytes must be D (the 8-digit id is a 32-bit handle).
3. If TXN finished, each change in its list with action create, update, replace or delete is a row of the plan with that
   action (refresh, drift, partial, forget, unsettled, move and import are not compared). A tripwire: the list is signed at finish.
4. Compile the listed texts, derive the document from the plan and the origins, judge (first failure: exit 5). Print the
   policies, D, the signer, the count of unknown values, and V beside the running version.
5. With a checkout whose fingerprint is the plan's `config_fingerprint`: the declared entries must equal the tree's policies,
   text by text. One missing means the applier left it out; one extra, that it invented it.

Exit 0 only when the state is sealed, the signatures verify (a killed apply has the start only: "writes unknown"), the plan is
present, its digest is D and the re-run passes. Anything else prints `unattested` first and exits 1; a bad signature or digest, 7.

## What it proves, and what it does not

For a retained TXN in a sealed state, with step 5: the identity that signed it started an apply of exactly D, under V, and
named exactly the listed policies as judged; each holds for the document derived from D; the declared ones are the code's; an
unmodified pravo writes at most the rows of the plan (`checkSaved`). A killed apply keeps its start, which vouches for the
plan and the set. It does not prove:

- That the applier ran the policies. A binary that signs a start without judging looks the same when the plan passes.
- The recorded half of the list: it is what the applier read from `policies.json`, a plain object that a writer of the bucket
  can edit or delete undetected. Only the declared half checks against a tree.
- That the plan is honest: `before`, row identity and flags, the `prior` of a delete, labels, `stats`, `outputs`, drift flags
  and data-source answers are the planner's words; apply checks address, action, known values and revisions.
- That an unknown value is acceptable: `{"$unknown": true}` fails a constraint or typed field but passes `if after.public ==
  true {error()}`; apply fills it in and nothing judges it again. Write constraints, not conditionals.
- The evaluator (V pins CUE for release builds; every dev build says `0.1.0-dev`) or every write: a modified binary can write
  more than it lists, and a killed apply lists none.
- That the code is the reviewed code, or that a policy exists: a policy never recorded can be dropped unjudged, a policy that
  forbids nothing passes, and a transaction without a list looks like a root without policies.

## Limits

- A policy takes force when an apply that finishes its plan records it. Until then it judges only plans from code that declares
  it; a staged or abandoned plan records nothing, and a branch without the policy is not judged by it.
- `policies.json` is plain, unlike `outputs.json`, which a sealed state seals; sealing it costs a kind and sites in about eight files, and
  stops forgery, not deletion. Deleting it is the escape from a recorded rule that fails for good (a CUE upgrade, a rename of a
  permanent policy). Each plan, apply and show pays one more read; `Copy`, `holdsNothing` and the viewer watcher must list it.
- No lock spans an apply: a policy recorded by an overlapping apply did not judge this one. Two writers of R meet at `$policies`
  (exit 4). Planning is not read-only (refreshes, moves, imports, reservations); `import`, `state mv` and `rm` are not judged.
- Older binaries are not refused. One ignores `policies.json` and `policy_set`, reports a transaction with a list as invalid
  (exit 7 in `state log`), drops the list when it rewraps a failed one, and drops `kept` on a progress write, as does
  `repairProgress` of an unreadable one.
- The texts (a few KB) sit in every transaction, readable in a sealed state like addresses, and every listing reads them. Kept
  plans add to every plan listing, overlap check and `apply ID`: `KeptFor` bounds it.

## Changes in the code

| | Where | What |
|---|---|---|
| A, deleted | plan files | `--out`; `SavePlan`, `ReadPlanFile`, file marking, `planlock.go` (68 lines), the digest that moved with each mark; `apply FILE`, `show FILE`, `serve --plan FILE`, MCP `out`; `plan pull` (no consumer left; `plan push` was never built). `filelock` stays; the plan file format stays, as the payload of a stored plan. |
| A, changed | cmd | `runApply`: plan, render, prompt, store, apply from the stored copy, on a fresh session (`engine.New` rebinds `ConfigConvert`); `planWorth` becomes `NeedsApply`; JSON key `plan`; `serve --plan` and MCP `planfile` take only an id. |
| Migration | docs, tests | By grep, 42 docs files use `--out` and 46 `--policy` (62 together, 56 skill copies), 28 Go test files match (a few only through `id new --ci --out`); also `README.md`, `ARCHITECTURE.md` 2.3, `site/`, `tools/serveshots`. A run block is its own shell: pass ids through `plan list --json`. The policy pages are rewritten (`blast_radius` in 12 files, the stubs text, exit-code tables, `SKILL.md`, `state-format.md`: "`txn/` can be pruned at any time"). Rerun `tools/capture -update` and `tools/skillgen`. |
| B, added | loader, state, engine, cmd | `case "policy"` (reads and compiles `rule`), `Root.Policies`, `#Policy`; declared texts in `Fingerprint`; `policies.json`; `Transaction.Policies`, whose digest is one more start part when present (the message carries its part count: no new domain); `Store.Transaction(id)`; `Judge`; `PlanFile.PolicySet`; `PlanProgress.Kept`; a block in `gcRules`; `state verify`. |
| B, changed | engine | `plan` builds the file once and judges, renders and stores those bytes (`StorePlan` rebuilds it today); `Judge` builds the document from the file, not `NewView` on the live state; `ConfigChange` fails closed; `applyPlanFile` judges the whole file, not `Remaining()`. |
| B, deleted | policy | `--policy` on `plan`, `apply`, `destroy`, `mcp`; `Policy`, `LoadPolicyFiles`, `SetPolicies`, `HasPolicies`, `CheckPolicy`, `CheckPolicyView`; the stubs (`withSkipped`) in judging; the refusal "a policy is judged against a saved plan". |

Cost, B: +1 each of `$kind`, state object, transaction field, lock address, progress field, plan-file field, document member,
command, `gcRules` block and constant; no key, flag, signing domain or format number. Removed: `--policy`, six functions, the
stubs. Cost, A: nothing added; `--out`, `plan pull`, three file arguments and the file lock removed; plain apply writes ~4 objects.
