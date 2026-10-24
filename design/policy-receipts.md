# Policy receipts

Status: proposal. Nothing here exists. The question: show cryptographically that every policy in the code when a plan was
made is satisfied by that plan. Access control, rollback of state and applies outside pravo are out of scope.

## The claim

A plan file, with the code its `config_fingerprint` names, satisfies every policy in that code. Nobody has to say that the
policies ran: the verifier runs them again. The receipt is the plan file, the code it names and the signatures the state
already makes over the file's digest. There is no verdict object, signature, key or signing domain of its own. "Any plan"
means any plan file that still exists (see Limits). The claim is "passes on these bytes", not "ran when the plan was made".

## The idea

A policy is a node of the specification, checked in with the code, so the code's fingerprint is the record of which policies
existed. The verdict is a pure function of the plan file and the code, both already hashed, so anyone can recompute it.

```cue
no_destroy: pravo.#Policy & {rule: summary: destroy: 0} // a replace counts as a destroy

encrypted: pravo.#Policy & {
	rule: changes: [...{
		type:   string
		action: string
		if type == "aws_db_instance" && action =~ "^(create|update|replace)$" {after: storage_encrypted!: true}
	}]
}
```

The `!` makes an absent field fail; a bare `storage_encrypted: true` would supply it. The walk that finds resources finds
`$kind: "policy"` anywhere in the root, in a module instance too, so a company baseline is a module. `rule` is a struct and a
resource inside a policy is refused. A policy the walk would skip (a conflict, an optional or required label, deeper than
48) is a load error, not silence; a hidden `_p` or a definition `#p` is inert. Every policy judges every plan: no flag, no
list, no name to leave out. A pravo without this feature stops at the unknown `$kind`, so an older binary cannot plan or
apply a root that has a policy.

`Judge(file)` is one function. For each policy in name order, its `rule`, read from the root as loaded with the file's tags,
is unified with the document below and must be concrete; the first failure exits 5. It runs in three places:

- `plan` builds the file, marshals and parses it, and judges the parsed bytes, so it judges what apply will judge. On failure
  it writes and stores nothing (exit 5). An unsaved plan is judged the same way. A root without a policy skips all of this.
- `apply FILE|ID` judges the whole file after the staleness checks and before any lock, ignoring progress marks, so `--only`
  and a resumed apply judge what the first apply did. `apply` and `destroy` without a file are refused when the code has a
  policy (today only `apply --policy` is), after the root loads; to destroy, plan with `--destroy` and apply the file.
- `show FILE|ID` judges and prints the line below: exit 5 when a policy fails, exit 3 when the code has a policy and it, or a
  provider, is not the file's. A root without a policy keeps today's stale warning and exit 0.

## The judged document

It is the `plan` member of `show --json FILE` plus a stub for each declared node the file has no row for. It comes from the
file (rows, counts, stats, outputs, providers and the labels `created`, `actor`, `reason`, `git_revision`), its `dependents`
and the specification, never from the state, the clock, a provider or the invocation. Not in it: `trace_id`, `hints`,
`reservation`, `applied`. The labels are the planner's words (at apply they were the applier's).

- A stub has `addr`, `mode`, `type`, `name`, `action: "no-op"` and `skipped: true`. Stubs are made for the managed, data,
  ephemeral and call nodes of the specification, not for `pravo.#Env`, `#SecretEnv` or `#RemoteOutputs`. A declared node
  with no record gets one too.
- The file has no `open` or `call` rows and gets none. A rule over ephemeral resources or functions keys on `mode` and
  `type`, which the stubs carry; one on `action: "open"` never fires.

## What changes in the plan file

One field, written in every plan file:

```json
"dependents": { "vpc": ["db", "subnet"], "subnet": ["db"], "db": [] }
```

Keys are every pending change and every node reachable from them; a value is the direct dependents the state recorded when
the plan was made. `changes[].dependents` is the value of the change's key, and `summary.blast_radius` is the pending
changes plus everything reachable through the map. `Judge` derives and does not check the map. A dependent added after the
plan is not seen. A file written before this change has no map and shows `blast_radius` equal to its pending changes. It is
the only state-derived value a policy sees: without `blast_radius` and `dependents` the file needs no new field.

## Verify

1. Get the file or id, and D, the SHA-256 of its bytes (the 8-digit id is a 32-bit handle: compare the full D). For a stored
   plan in a sealed state `show` prints the signatures over D; for an applied plan `pravo state log --json` gives the
   transaction's `plan_digest`, signed in a sealed state, and it must be D. That links D to identities and proves no policy.
   An unsealed state signs nothing, and D then names the bytes only.
2. Check out a tree whose fingerprint F equals `config_fingerprint`, and run `pravo init` with the pravo and providers that
   planned: F covers the generated packages and the fetched modules, so an upgrade of either makes older files unverifiable
   (exit 3 names the files that differ and no verdict is claimed). `git_revision` says where to look. Whether you trust the
   policies of that tree is your review of its diff.
3. Run `pravo show FILE|ID` with the providers installed (they decode the file) and, for an id, the state (a reader, in a
   sealed state). Compare the count, names and sources with what you expect. A code without a policy prints `no policies in
   this code`; with `-T` the line adds that the result depends on the verifier's values:
   `3 policies satisfied: baseline.tags (acme/baseline@v1.2.0), cap, no_destroy · 2 values unknown until apply · tags env=prod · D …`

## What it proves

Given a file with digest D and a tree whose F equals its `config_fingerprint`:

- The policies are exactly the policy nodes of that tree; adding, removing or editing one changes F. F becomes the full
  SHA-256 (a 32-digit prefix today), and plans in flight go stale once.
- Each is satisfied by the document derived from D and the tree. The verifier recomputes it and trusts neither the maker nor
  the applier. In a sealed state, signatures say which identity stored D and which applied it.
- An unmodified pravo applies at most the rows of the file: each node by address, action and known values (`checkSaved`),
  each written node at the revision the plan saw.

## What it does not prove

- That the policies ran when the plan was made. A maker who skipped them and made a passing plan is indistinguishable, and harmless.
- That the file is an honest plan. The planner's words are `before`, row identity and flags (`mode`, `type`, `provider_key`,
  `name`, `skipped`), the `prior` of a delete (apply destroys what the file says), the labels, `stats`, `outputs`, drift flags,
  data-source answers and `dependents`. Apply checks address, action, known values and revisions, not the rest. An edited file
  that still passes is held off by D and its signature (sealed state) or by custody (an `--out` file), not by the policies.
- That an unknown value is acceptable. It is the struct `{"$unknown": true}`: a constraint, a typed field or `!= false` fails
  on it, but `if after.public == true {error()}` passes. Apply fills it in, nothing judges it again, and `consistent` accepts
  any value where the file says unknown. Provider-computed attributes are never judged. `show --json` shows where.
- That the code is the reviewed code. The verifier picks the tree; a developer who deletes a policy and plans gets a valid
  receipt for the weaker code. The control is review of the diff. Today `--policy` and `pravo mcp --policy` are named by the
  operator, outside the tree, and `$settings` cannot name, weaken or exempt one (`TestSettingsHasNoPolicies`). That
  guarantee is removed on purpose; a floor outside the tree needs a second mechanism, which this is not.
- That a policy is good: one that forbids nothing passes, and one that reads an undeclared name can bind to a top-level field.

## Limits

- `-t` is in the file, and `show` and `apply FILE` load the root with it. A rule that reads a tag, `pravo.#Env` or a `-T`
  value (not recorded) is conditional on it. A file under `@if`, `_x.cue` or tests is not loaded.
- The receipt is the bytes. A stored plan is collected 24 hours after its last change is applied (`AppliedKeep`, default),
  and then only `plan_digest` remains: keep the file with the apply. An `--out` file is marked in place as an apply goes on,
  so its digest moves; the first apply names D, later ones name the file as marked.
- No pravo or CUE version is recorded: the verdict is relative to the verifier's evaluator. A refusal quotes the offending
  value and the document is not masked, so a rule over a secret prints it. Stubs make the work per plan grow with the declared
  nodes; measure 10,000 nodes against 20 policies before shipping.
- Planning is not read-only: refreshes, moves, imports, relabels, reservations and the `planned` transaction come before any
  verdict, and a refused plan has done them. `import`, `state mv` and `rm` change the state with no plan and are not judged.

## Changes in the code

| Where | Change |
|---|---|
| loader: `config/discover.go`, `schema/settings.go`, `modules.go` | `case "policy"` in `register`, `Root.Policies`, `#Policy` beside `#Output`; the load errors above; `eval -c` skips policy nodes; `Fingerprint` is the full digest of the bytes `Load` read, not a second read of the disk. |
| engine: `policy.go`, `view.go`, `engine.go`, `configdiff.go` | Delete `Policy`, `LoadPolicyFiles`, `SetPolicies`, `HasPolicies`, `CheckPolicy`, `CheckPolicyView`; add `Judge`, keeping the Unify and Validate loop and `refused`. `NewView` reads the plan's `dependents`, never `e.State`; `Engine.Plan` sets it on both return paths; stubs come from the non-builtin `Root.Resources`. `ConfigChange` returns a reason when `Fingerprint` fails (it returns "" today, which fails open); `LoadPlanFor` and `show` share the code reasons. `planfile.go`: `PlanFile.Dependents`. |
| CLI, serve, docs | Drop `--policy` from `plan`, `apply`, `destroy` and `mcp`; `Judge` replaces `CheckPolicy`; `show` prints the line and exits 3 or 5. Serve and the viewer read the file's numbers. Rewrite the policy docs: `--policy` is in 40 files of `docs/` (many executable blocks) and in the generated skills (`tools/skillgen`); update the exit-code tables and `ARCHITECTURE.md`. |

Cost: +1 `$kind`, +1 plan-file field, -1 flag. No command, signing domain, key, transaction field, format number or `PlanDigest` change.
