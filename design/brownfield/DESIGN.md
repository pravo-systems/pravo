# pravo brownfield: discovery and onboarding

Status: design, not code. Written 2026-10-22. This file is the single source of truth for the brownfield site.

How to read the marks. **verified** means someone read a file, fetched a page or ran a binary in this work and the source is named in section 15. **unverified** means nobody did, and the design either does not depend on it or has a fallback. **assumption** means a number or behaviour chosen by the designer, to be tuned on real data. R1 to R4 are the four research notes (protocol, ecosystem, landscape, pravo internals).

---

## 1. Summary

The chosen design in ten lines.

1. pravo gains a **shadow estate**: objects that exist in the cloud and that no record in this root's state owns. It is tracked, never touched.
2. Tracking is a **ledger**: sharded JSON pages under a new `inventory/` prefix in the state backend, written only by an explicit `pravo sweep`. It holds facts (identity, display name, times, completeness). It holds no attribute values, no decisions, and no state name. The ledger may live on its own backend or prefix with its own credential, so a read-only sweep job needs no write access to `resources/` (sections 5 and 10).
3. **Decisions** (what to sweep, what to ignore and why) are CUE in the specification, reviewed in git. "Managed" is never stored; it is computed by matching ledger against `resources/`.
4. A shadow object is **never a record** in `resources/`. A plan, apply or destroy cannot see it, so none can plan it for deletion.
5. There is one door that discovers and two ways to feed adoption. **Discovery** is the provider's `ListResource` stream, and it works for a minority of types today. The **adoption inputs** are a file of ids or identities and one typed id: they find nothing, they let you adopt what you already know. For a type that cannot list, discovery is out of scope. The design ships deterministic recipes (shell, versioned data, no model) that produce the file for the thirty most common aws types, and says plainly that for everything else the user, another tool or an agent produces the list.
6. `pravo adopt` turns a shadow object into ordinary CUE with `$import`. It hydrates in memory (import then read), generates CUE with one deterministic generator, repairs provider noise by pinning, and checks the result with an **empty plan in a scratch overlay** before it writes a file. That gate is advisory and says how much of the CUE is specified and how much is inherited; the saved plan that follows is the authority.
7. Adoption is a normal change after that: saved plan, policies, apply. Adoption ends with an apply, because only an apply writes the dependency edges.
8. Three new verbs (`sweep`, `shadow`, `adopt`), one new subcommand (`state identify`), the existing `state rm` to forget a record, and three extended ones (`schema`, `why`, `mcp`). Every step works by hand with no model once a list of ids exists. Producing that list for a type that cannot list is the one step that needs outside tools.
9. An agent may read the ledger, run bounded sweeps, print redacted proposals, edit CUE and JSON-lines files, and explain diffs. It may not write the ledger, state or cloud, apply anything but a saved plan, or choose a secret, and it sees attribute values only if the operator allows it.
10. Coverage is part of the product: per type, `listed`, `no-list`, `needs-parent`, `truncated`, `failed`, `not-asked`. Absence from the ledger means "unknown", never "does not exist".

The three decisions that matter most.

- **A. Facts and decisions live apart, and neither lives in the managed state.** This is what makes "pravo watches the shadow estate and cannot destroy it" a structural property and not a promise. We rejected the one design that mixes them (a shadow record mode in `resources/`) because a probe showed the next plan destroys such a record, and ten code paths walk all records.
- **B. Nothing is written until an empty plan has been shown, and the report says what that proves.** The gate is a test pravo can run because it owns the plan. An empty plan proves only that the CUE does not contradict the cloud. To make it prove more, the generator writes the optional-and-computed attributes it can read (`--spec full`, the default) and the gate prints `specified N, inherited M` per object. A `--spec minimal` result is labelled "tracked, not specified". The generator is pravo's own schema-driven code, not the provider's `GenerateResourceConfig`, so the same input gives the same output for every provider.
- **C. The by-id path comes first, and it is as good as the list path for adoption, not for discovery.** Only 14.6% of aws types can be listed, none of kubernetes, and no list stream has been completed in this research. For every other type nothing in pravo discovers anything: the user brings the ids (a recipe, another tool, an agent). List is an accelerator, not the foundation. Phase 1 therefore ships adoption by id on today's plugin layer.

Three changes to pravo's core come first (section 13, D1, D2 and D13). They are not small, and each touches an invariant pravo documents today, so "phase 1 needs nothing outside pravo" is true and misleading: import must stop writing state at plan time, an adopted record must be protected against the orphan delete that follows from removing its text, and two records must never own one object. Section 13 says, for each, which documented property changes.

---

## 2. The problem and the success test

### The problem

Most real projects start from something that already exists. Some of it is in Terraform or OpenTofu state, some was made in a console, some is made by another team's pipeline. Today an engineer moving such an estate into pravo has two tools:

- `pravo import ADDR ID` and `$import`, which adopt one object per call. The CUE must be written first and the import id must be known (R4 a).
- `pravo state import-tfstate`, which handles only what an existing `.tfstate` already knows (R4 f).

Nothing finds the objects nobody declared, nothing writes the CUE, and nothing remembers what was found.

Brownfield imports go wrong in ten recurring ways (R3 section 4). The design answers each:

| Failure in the field | What this design does |
|---|---|
| Generated config is a snapshot of literals, not a design | Generated once, flat, then owned by humans. Restructuring (modules, loops) is a separate step under the same gate, helped by an agent |
| Generated config is invalid (conflicting arguments, dropped nested ids) | Own generator, then `ValidateResourceConfig` and a provider-level plan. Failures are listed, never written |
| First plan after import is not clean (defaults, null vs empty, `tags_all`) | Omit computed-only attributes, write the optional-and-computed ones that have a value (`--spec full`), pin what the plan shows differs, at most three rounds. Leftovers reported with `specified N, inherited M` |
| Secrets cannot be read back or leak into git | Sensitive and write-only values are never emitted. A required one is a visible hole a human fills |
| Wrong granularity (inline block vs separate resource) | Reported per type in the review output; the choice stays human. Not solved (section 12) |
| Adopting things that belong to someone else | Ignore rules with a mandatory reason, `elsewhere` from known states, a controller-marker check when an object is hydrated for adopt. Ownership detection in general is not solved |
| Silent coverage holes | Coverage table. "Unknown" is a state |
| Tools rot | We call the provider protocol directly, not `tofu query` or `terraform query` |
| Import and config do not land together; review does not scale | Gate before write; batches of about 50; review by rule and batch |
| Identity and naming (ids per type, `res-0` names) | Identity keys, deterministic names derived from the display name, `$from` to rename |

### The success test

**An empty plan after onboarding.**

Precisely, for a batch of N objects:

1. `pravo plan` on the batch prints, for every object, that it was imported and matches the specification, and ends with `0 to add, 0 to change, 0 to destroy`.
2. After `pravo apply` of that plan (an apply with zero changes still writes the dependency edges and config hash, R4 section 0 item 7), a second `pravo plan` reaches zero resources.
3. Anything else (an add, a replace, a destroy, a change) means the generated CUE is wrong. Stop. This is the same stop rule as the migrate runbook (R4 f).

What the test proves, and no more: **the CUE does not contradict what the provider reads.** An attribute the CUE leaves null is inherited from the prior value (R4 d, `ProposedNew`), so an empty plan says nothing about it. If the generator wrote almost nothing, the plan would be empty for the wrong reason, and later drift on the omitted attributes would never be detected or reverted. Two things keep the test honest. First, the default profile `--spec full` writes every optional-and-computed attribute that has a value; in the installed aws provider that includes an S3 bucket's `bucket` and `region`, which `pravo schema hashicorp/aws/aws_s3_bucket` prints without `?` (checked in the critique of this design). Second, the gate prints `specified N, inherited M` per object and warns when M exceeds N. The protocol cannot tell a provider default from an owner's choice, so a written default is frozen as if chosen; the report lists every pinned and every frozen attribute. What the test does not prove: that the frozen values are what the owner intends (R3 section 6). A `--spec minimal` result is labelled "tracked, not specified": a way to put an object under the engine, not a description of it.

The test is run twice, on purpose. The first run is the **gate**, inside a scratch overlay, before any file is written. The second is the **proof**, on the real state, after apply.

---

## 3. Vocabulary

Ten words. The site uses these and no others.

| Word | Meaning |
|---|---|
| **estate** | Everything that really exists in the cloud account. |
| **managed** | Has a record in this root's state. pravo plans changes to it. |
| **shadow** | Exists in the estate, no record in this root owns it. pravo may list it and may read it when asked to adopt it. Nothing else. |
| **sweep** | One run of list calls for chosen types, read-only. |
| **ledger** | The stored facts from sweeps, under `inventory/` in the state backend. Disposable. |
| **shard** | One page of the ledger: one type, one provider instance (account and region), one sweep. The unit of cost, age and absence. |
| **identity** | The provider's own key for an object (for an S3 bucket: the bucket name required, account and region optional). The ledger key is built from the required attributes plus the scope (section 5). |
| **coverage** | Per type: can pravo list it, and how did the last try end. |
| **ignore rule** | CUE that says "not ours", with a reason. Reviewed in git. |
| **adopt** | Turn a shadow object into CUE that plans empty, then into a record. |

Words used but defined where they appear: gate, proof, pin, hole, batch.

---

## 4. The lifecycle

### States

States are a view, computed on read from three sources: the ledger (facts), the specification (decisions) and `resources/` (records). Nothing stores a state name.

| State | How pravo knows | Meaning |
|---|---|---|
| **seen** | In the latest ledger shard, no record, no `$import`, no ignore rule, not in a known state | Shadow, undecided |
| **ignored** | An ignore rule matches | Shadow, deliberately not ours. Reason is mandatory |
| **elsewhere** | Present in a state named in `$discover.known` (a `.tfstate`, or another pravo state, local or on a remote backend, read-only) | Managed by another root or by OpenTofu or Terraform, as of the age of that state. Each `known` entry has an `as_of` date and `shadow` warns when it is stale. Adopting it is refused |
| **declared** | An `$import` in the specification names it, and no record exists | Mid-adoption: text exists, record does not |
| **managed** | A record in `resources/` matches the identity | Owned. Plans can change or destroy it |
| **unlisted** | Listed before, absent from a later **complete** sweep of its shard | Hint only. For a managed object, the existing drift marks apply instead. (Not named `gone`: that word is already a record mark in `docs/reference/state-format.md`, meaning the read did not find the object.) |

### Transitions and who moves an object

| Transition | Moved by |
|---|---|
| (cloud) to **seen** | `pravo sweep`, or `--from FILE` |
| seen to **ignored**, and back | A human or agent edits an ignore rule in CUE; a human merges it. Expiry returns the object to seen |
| seen to **elsewhere** | `$discover.known` or `--known-state`, evaluated by `shadow` |
| seen to **declared** | `pravo adopt` writes `$import` CUE |
| declared to **managed** | `pravo apply` of a saved plan |
| managed to **seen** | `pravo state rm ADDR` (forget the record, touch nothing in the cloud; the command exists today), and removing the text |
| seen, ignored or elsewhere to **unlisted** | A later complete sweep of the same shard does not list it |
| unlisted to seen | A later complete sweep lists it again |

Two cases the states do not cover, stated so nobody is surprised. An object deleted and recreated out of band shows as one managed record with a drift mark plus one new `seen` object; `shadow` prints a hint ("managed X is missing, seen Y has the same type and name"). A resource shared across accounts (for example aws RAM-shared) can be listed in an account that does not own it; this is not handled, and an ignore rule can express it.

`unlisted` is pruned from the ledger after 30 days (assumption). The shard header keeps a count of pruned entries and the date, so the evidence that something disappeared does not vanish with the entry. Only a **complete** shard can produce `unlisted`. Complete means all three: the stream ended with gRPC OK, **no diagnostic of error severity appeared anywhere in the stream**, and the event count is below the limit. (A provider that fails midway through its API pagination may report an error diagnostic and still end the stream with OK. That is inferred from how providers report errors; no stream was observed, so it is unverified.) A broken stream, a cancelled run, an error diagnostic or a result cut by `limit` keeps the old entries and marks the attempt incomplete.

### The rule for what pravo does to an object it does not manage

**Nothing, ever, unless declared.**

In full:

- For a shadow object pravo may do exactly two things: list it (a read, in a sweep of a type the specification named) and read it (import then read, in memory, when someone runs `adopt` on it).
- pravo never writes to a shadow object, never tags it, never plans a change for it, never deletes it, and holds no record for it.
- "Claimed" means one thing: an `$import` that went through a saved plan, review and an apply. Before that moment the object is shadow. After it the object is managed and the ordinary rules apply, including that removing its declaration plans its deletion, unless the record is protected, in which case the plan is an error (section 13, D2).
- Sweeps do not create claims. Ignore rules do not touch the cloud. A human saying "adopt" is the only path to a claim.
- pravo itself writes nothing to the cloud. That the list and import calls only read is a provider convention, not something the protocol enforces, and nobody read a provider's list or import code in this work (only the aws list documentation was fetched). The real enforcement is the read-only role used for sweeps (section 10).
- The ledger is a map, not the territory. Nothing acts on a ledger entry without re-reading the real object first. This is why eventual consistency in list APIs does limited harm: a stale entry costs one failed adopt, not a wrong change.

---

## 5. What is stored and where

### Three kinds of thing, three homes

| Thing | Home | Format | Why there |
|---|---|---|---|
| Facts: what the last sweeps saw | State backend, `inventory/` prefix | JSON, sharded | Needs to be shared and durable in CI, but is rebuildable |
| Decisions: scope, ignore rules, known states | CUE in the specification (`$discover`) | CUE | Reviewed in git like a policy |
| Managed objects | `resources/` records | unchanged | The existing graph store |

### Layout

```
pravo.state/
  meta.json
  resources/            managed records, unchanged
  txn/  locks/          unchanged
  outputs.json
  inventory/
    <provider_source>/<account>/<region>/<type>.json    one shard per file
    <provider_source>/<account>/global/<type>.json      global types: by account only, never by scope
```

Shards are keyed by provider source, account and region, not by the name of a provider instance, so renaming an instance or reordering scopes orphans nothing.

`inventory/` is a sibling of `resources/`. `Store.load` lists `resources/` and never touches `inventory/` (R4 b). Local and S3 use the same keys. The key segments use the existing address percent-encoding.

There is no index file. Reports list the prefix: one LIST plus one GET per shard. Shards number in the tens to hundreds (types times provider instances), not one per object. An S3 write per discovered object would be the wrong shape (R4 b).

### A shard

```json
{
  "header": {
    "provider": "hashicorp/aws", "provider_version": "6.69.1",
    "type": "aws_s3_bucket",
    "scope": {"account": "123456789012", "region": "eu-west-1", "sweep_instance": "aws_eu_read"},
    "identity_version": 0,
    "list_config_digest": "sha256:9c1e...",
    "source": "list",
    "swept_at": "2026-10-22T09:12:00Z",
    "complete": true, "truncated": false, "error": null,
    "last_attempt": {"at": "2026-10-22T09:12:00Z", "outcome": "complete"},
    "pruned": {"count": 0, "last": null},
    "actor": "ci-nightly"
  },
  "entries": [
    {"key": {"bucket": "tmp-export-0912"},
     "identity": {"account_id": "123456789012", "bucket": "tmp-export-0912", "region": "eu-west-1"},
     "name": "tmp-export-0912",
     "first_seen": "2026-10-15", "last_seen": "2026-10-22", "gone_since": null}
  ]
}
```

Rules:

- **Key** of an entry: the scope's account label, the type, and the **required** identity attributes only (keys sorted), plus the identity schema version. Optional identity attributes (`account_id`, `region`) are filled from the scope before comparing and are never key parts, so the key is the same whether a list event or a `ReadResource` returned them. The aws 6.69.1 dump shows why: `aws_s3_bucket` has `bucket` required and `account_id` and `region` optional; `aws_iam_role` has `name` required and `account_id` optional, and a list config with no attributes; 208 of the 253 list identities carry an optional `account_id` and 192 an optional `region` (aws-all-list.tsv, binary run). Without an account label, two accounts that each have a role named `admin` would produce equal keys. The account label comes from the identity when it carries one, else from the `$discover` scope declaration (below). Nobody has seen what a real list event fills in (R1: no stream completed), so the key is a design under test in phase 0. Entries of differing versions are unequal until `UpgradeResourceIdentity` has run (R1 section 2.3).
- **Cross-input merge.** An entry from a file (keyed by import id) and an entry from a list (keyed by identity) can name one object. When adopt hydrates a file entry and learns its identity, the two collapse into the listed entry, and the identity lock (section 10, D13) refuses a second adoption of it either way.
- **Fallback key** for types with no identity schema: account label, type, import id string, `source: "file"` or `"manual"`. In aws 6.69.1 that is 1,184 of 1,735 managed types (1,735 minus 551, arithmetic on R1).
- Entries sorted by canonical key, so a re-sweep of an unchanged account writes an identical file.
- **No attribute values.** Listed objects can hold secrets (R4 b). The ledger holds identity and display name only. Attributes are fetched at adopt time and never persisted.
- **Display names are untrusted.** They come from whoever can create cloud objects. Control characters (escapes, tabs, newlines) are replaced by a visible escape sequence when the shard is written, so a name cannot reach a terminal as an escape sequence or corrupt a file. Every machine-readable file in this design is JSON lines, never TSV. Section 9 says how they are handled.
- Shards over 5,000 entries (assumption, about 200 bytes an entry) are written as numbered parts plus a **manifest** object written last, naming the parts and their digests. A reader ignores parts the manifest does not name, so a crash between parts leaves the old snapshot readable and never a mixed one. The default `limit` is 10,000 per type, so a split is expected for the largest types.
- File-sourced and manual entries always have `complete: false`, so they can never produce `unlisted`.

### Writes and concurrency

A sweep writes one shard with `PutIfAbsent` the first time and `ReplaceIf` (ETag) afterwards, both already in the `Backend` interface (R4 b). On an ETag conflict the later writer re-reads and merges by this rule: **a complete snapshot beats an incomplete one whatever the times**; between two of the same kind the newer `swept_at` wins; `first_seen` is the earliest of the two, per entry, so two writers cannot lose it. The entries of a shard are always those of the last **complete** sweep. A failed or truncated sweep does not replace them; it updates only `last_attempt` (time and outcome) in the header. A newer failed sweep therefore never overwrites an older complete shard, and the coverage word `failed` comes from `last_attempt`. No per-shard lock. We dropped the `~`-prefixed lock proposal because it was an untested hypothesis and the merge rule makes the failure it prevents harmless.

### Who can write the ledger

By default the ledger shares a backend with the state. On a local store, or an S3 bucket without prefix-scoped IAM, anything that can write `inventory/` can also write `resources/`, so a nightly "read-only" sweep job would hold power over the managed state whatever its cloud role. `$discover.ledger` may therefore name a separate backend or prefix with its own credential. The sweep job runs with that ledger credential and the read-only sweep instance, and needs no write access to `resources/`. `shadow`, which matches ledger against records, needs read on both. On S3 the separation is a prefix condition in IAM; the policy text is not written here because nobody tested it (unverified). On a local store the separation does not exist, and the design says so: use a separate backend if the job must not be trusted with state. This is a security measure, not a sharing one; D4 is a separate question.

### Format version

No bump. The ledger is a **rebuildable cache**. A current binary ignores an unknown `inventory/` directory (probe, R4 item 6), so an older binary keeps working. What an older binary can lose: `state pull` and `state push` copy only `resources`, `txn`, `meta.json` and `outputs.json`, so `inventory/` is dropped, which costs one re-sweep and the `first_seen` history. The new `state.Copy` carries `inventory/`. `ARCHITECTURE.md` line 167 says format "2, or 3" and the code says `Format = 1`; reconcile the text before any change (R4 b). This paragraph is about the ledger only. A new field on **records** (protection, `identity`) is a different question, because an older binary that rewrites a record may drop an unknown field without a warning; section 13 D5 splits the two.

### How the shadow stays apart so that no record can be planned for deletion

A shadow object is not a record, by construction.

1. The ledger is a different prefix. Everything that deletes walks `resources/`: `planOrphans`, `destroy`, `relabelProviders`, `DriftDue`, `warnSuspects`, `Snapshot`, `state list`, the viewer. None of them reads `inventory/` (R4 b lists ten such sites).
2. There is no third record mode. We considered it and rejected it (section 12).
3. A record appears only through `adopt` plus an apply of a saved plan.
4. `newSession` resolves a provider for every record's provider. Ledger entries create no records, so a ledger entry never causes pravo to install or start a provider. A sweep starts only the providers its `$discover` scope names.
5. **Acceptance test for the build:** with 10,000 ledger entries and no matching records, `plan`, `apply` and `destroy` perform zero reads of `inventory/` and propose zero changes for them.

### Matching ledger to records

"Managed" is computed, never stored. Records today carry no identity (R4 item 1). Two rules:

- Primary: an additive `identity` field on records, written by import, apply and refresh (`ReadResource` returns `new_identity`, R1 section 2.4).
- Fallback until it exists: provider key and type match, and every identity attribute equals the same-named attribute in the record's state (assumption; for aws the identity attributes are named like state attributes, which is unverified for the whole type list).
- Backfill: a one-time `pravo state identify` reads each record through the provider (`ReadResource` returns `new_identity`, or the identity is derived from the identity schema and the record's attributes) and writes the `identity` field. It is a state write (one additive field per record, under the per-resource lock) and no cloud write. It is a command of its own and not a side effect of `drift`, whose contract is to find differences and leave marks.

Whether an older binary that rewrites a record keeps an unknown `identity` field is **unverified** (nobody ran it). It is a phase 0 check, and it also decides D2(b) and D5. **A type with any unmatched record is never listed as `seen`.** `shadow` shows it as "unreliable until `state identify` has run", and `adopt --from-ledger` refuses it, because a false `seen` for a managed object is the bad failure: the object could be adopted twice (section 10). Records applied long ago have no identity until each is refreshed or identified, so on an old project most types will show as unreliable at first. The design accepts that rather than guess.

### Decisions: the `$discover` block

A new top-level field in the specification (placement is a build-time decision, section 13 D3). Sketch, illustrative:

```cue
$discover: {
	eu: {
		account:  "123456789012"              // the scope's account label: a property of the scope, not of an instance
		sweep:    "aws_eu_read"               // provider instance used only to list: read-only credentials
		manage:   "aws_eu"                    // provider instance recorded on adopted records: the credentials every later plan uses
		types:    ["aws_vpc", "aws_subnet", "aws_s3_bucket", "aws_iam_role"]
		config: aws_vpc: filter: [{name: "tag:Env", values: ["prod"]}]   // the list type's own config
		refresh: "7d"                         // sweep age for --due
	}
	known: tfstate: [{path: "../legacy/terraform.tfstate", as_of: "2026-10-01"}]   // owned elsewhere: subtract; a stale as_of is warned about
	ledger: {backend: "s3://acme-inventory/prod/"}   // optional: its own credential, so a sweep job needs no write access to resources/
	ignore: [{
		name: "service-linked roles"
		match: {type: "aws_iam_role", name: "AWSServiceRole*", glob: true, expect: 57}   // exact identity is the default; a pattern needs glob and the count seen at review
		why:   "created and owned by AWS"
		expires: "2027-04-01"                 // optional
	}]
}
```

- `why` is required. After `expires`, matching objects return to `seen`.
- **Exact identity is the default match.** A pattern needs `glob: true` and `expect: N`, the number of objects it hides at review time. When it later matches more than N, the extras show as `seen (rule overflow)`. The reason: display names are set by whoever can create objects, an attacker included, and the ledger holds no attributes (no path, no tags, no creator) to check them against. A bare glob such as `AWSServiceRole*` would hide every present and future object named to match, and the shadow estate is where a security team looks for unknown objects. (Whether aws refuses customer-made roles with that prefix was not verified; no claim either way.)
- The review output of a rule lists the objects it currently hides. An agent may draft a rule, but a rule does not merge without that listing in the pull request.
- Rules on tags need the full object (an N+1 read, R2 d) and are deferred.
- `account` is a property of the scope. The sweep checks it against `account_id` when the identity carries one and warns on a mismatch.
- `sweep` is used only for listing. `manage` is the instance recorded on adopted records and used by import, read and every later plan. They must not be mixed: an object adopted under a read-only instance would be recorded with credentials that cannot update it. `adopt` hydrates through `manage`, so import and read behave as later plans will. With one credential set the two names may point at the same instance.
- `shadow` reports a rule that matches nothing (unused) and a claim whose object has vanished (stale).
- The engine's plan ignores `$discover` except to validate its shape.

---

## 6. Discovery

### One door that discovers, two adoption inputs

| # | Source | Exact capability used | Works for | Cost to the user |
|---|---|---|---|---|
| 1 | Provider list | `GetMetadata` (`list_resources`), `GetProviderSchema` (`list_resource_schemas`), `ValidateListResourceConfig`, `ListResource`, `GetResourceIdentitySchemas` | Types a provider can list. aws 6.69.1: 253 of 1,735 | Name the types in `$discover` |
| 2 | Adoption input: a file (not discovery) | `pravo sweep --from FILE`, or `pravo adopt --ids FILE`: JSON lines of type, id or identity, optional address | Any type that imports, once someone has produced the list. The bridge for `aws` CLI output, `kubectl`, Steampipe, Cloud Control, Azure Resource Graph, an agent's research | Produce the file and know each type's import id (the registry page of the type, or a recipe) |
| 3 | Adoption input: one typed id (not discovery) | `pravo adopt TYPE ID` (today's `ImportResourceState` by id) | Any type that imports | Know the id |

For a type that cannot list, **discovery is out of scope**: the protocol has nothing to offer and pravo will not pretend. What the design adds is a **recipes folder**, shipped with pravo, versioned, data and not code: for thirty aws types chosen from those that cannot list (the thirty are not picked; none is written), a shell recipe that prints the JSON lines the file door takes (`aws ... | jq`), with that type's import-id format written beside it. It makes the manual path real for the common types, with no model. Limits, stated plainly. Recipes need the vendor CLI and credentials. They cannot be tested in pravo's documentation build, which has no cloud account; they are tested against a sandbox account (phase 0 onward). Nobody has written one yet (unverified). For the other ~1,450 aws managed types (1,735 minus 253 listable, minus the thirty) and for every other provider, the user, another tool or an agent produces the list, and at a thousand objects across thirty such types that step is not manageable by hand. `pravo schema --list TYPE` prints the identity schema and, when the identity has one required attribute, the hint "import id: the value of `bucket`" (a heuristic, unverified for composite or ARN ids). **Kubernetes users are on the file door only**: the cached provider lists nothing.

Candidates deliberately not built (section 12): native cloud-inventory adapters, plural data sources as lists, reading `terraform query` output.

### Detect features, never versions

On first use per provider instance, cached with the schema:

1. `GetMetadata` (may answer Unimplemented). Gives `list_resources[]`.
2. `GetProviderSchema` (`GetSchema` in protocol 5). `list_resource_schemas` is the **config** of each list type, which is the set of filters and scopes a caller may pass.
3. `GetResourceIdentitySchemas` (may answer Unimplemented).
4. `ServerCapabilities.generate_resource_config`.

There is no list capability bit, and the label "6.10" names three different file contents across Terraform tags (R1 section 6.1), so a version gate would be wrong. Every call must tolerate gRPC `Unimplemented`: scottwinkler/shell does and aws 5.100.0 does for identity (R1 section 4.1). The list-capable provider on the test machine, aws 6.69.1, speaks protocol 5 (R1 section 4.1), so list support must exist in **both** protocol adapters or the first real provider finds nothing. The schema cache needs a new `CacheVersion` because the stored form changes (R4 item 2).

### What the list call actually gives

`ListResource(type_name, config, include_resource_object, limit)` returns a stream of events: `identity`, `display_name`, optionally `resource_object`, `diagnostic`. The protocol has no pagination token, no total, no changed-since, no tags, no parent field, no ordering guarantee, no cost hint (R1 section 2.2, verified from the proto). Consequences, all designed in:

- Every sweep is a **full** scan of its shard. There is no incremental refresh.
- A sweep passes `include_resource_object = false`. Full objects can mean N+1 provider calls (R2 d), and what a list returns is not guaranteed to equal what Read returns (unverified; one third party says it differs). Hydration is always import then read.
- A sweep passes an explicit high `limit` (default 10,000 per shard, assumption). The Terraform default of 100 cuts silently (R2 d). If events equal the limit, the shard is `truncated`. What `limit = 0` means is unverified.
- A dead stream is `failed`, never "no results". In the one test, `ListResource` on an unconfigured aws provider killed the process and the stream returned `Unavailable` (R1 section 4.4, cause unverified).
- **Configure first.** `ConfigureProvider` must succeed before any list call. The runner supervises the provider process and restarts it for the next shard after a crash.
- Per-event diagnostics and per-type failures do not stop the sweep, but an error-severity diagnostic anywhere in a stream makes that attempt incomplete: a provider may report an error and still end the stream with OK (inferred, not observed), and every object after the failure would otherwise be marked `unlisted`. A failing type marks its shard `failed`, the sweep continues.

### Scoping by account and region

- **A scope is an account and a region, reached through two provider instances.** In pravo a provider instance is one configuration, hence one account and one region (R4 c). A scope in `$discover` names a `sweep` instance (read-only credentials, used only to list) and a `manage` instance (recorded on adopted records), section 5. An account-wide aws sweep needs one declared pair per region the user cares about.
- Why not the `region` attribute in the list config? 233 of 253 aws list types have one and it defaults to the provider region (R1 section 4.2, R2 c). But an adopted resource in another region needs its `region` attribute pinned in CUE or its read goes to the wrong region, and nobody ran a multi-region sweep from one instance. Phase 5.
- `account_id` is in 208 of 551 aws identity schemas (R1 section 4.2) and in 208 of the 253 list identities, always optional (aws-all-list.tsv, binary run). Keys use the scope's account label and the required attributes only (section 5), so two instances that reach the same account dedupe and two accounts do not merge.
- **Global types** (list config without a `region` attribute) are keyed by account only, never by scope. In aws 6.69.1 there are 19, not 10: 10 with an empty list config (for example `aws_iam_role`, `aws_cloudfront_distribution`) and 9 more with another config but no `region` (for example `aws_iam_group`, `aws_route53_zone`) (aws-all-list.tsv, binary run, counted again in this revision). They are swept once per account, by the first scope in declaration order that names them, and stamped `region: null`. Removing that scope does not orphan the shard: the next scope that names the type takes it over on its next sweep.
- **Types that need a parent** (76 aws list types have a required attribute such as `rest_api_id`, `log_group_name`, `bucket`) are reported `needs-parent` and not swept in v1. Mapping `rest_api_id` to a parent type is a naming heuristic (R1 section 7.5). Phase 5 lets `$discover` name the edge explicitly, for example `rest_api_id: from aws_api_gateway_rest_api`. Meanwhile the by-id doors work.
- **Filters** are per type and read from the list schema: aws EC2-style `filter` blocks on 18 types, an OData string for azurerm, `organization_name` for tfe (R2 c). pravo offers no uniform tag filter. `pravo schema --list TYPE` prints the knobs.

### Coverage, so absence never reads as "does not exist"

Each type gets one coverage word.

| Word | Meaning |
|---|---|
| `listed` | The provider lists it and the last shard completed |
| `truncated` | The last stream hit the limit |
| `failed` | The last stream broke or the type returned an error |
| `needs-parent` | The list config requires a parent attribute and no edge is declared |
| `no-list` | The provider cannot list this type. Adoption is by id or file only |
| `not-asked` | Listable, but the scope does not name it |

Even for `listed`, the provider may omit objects by design. In aws the list excludes non-manageable objects (default RDS parameter groups), special cases (the default VPC and its parts have their own list types), instances owned by an Auto Scaling group and instances in `terminated` or `shutting-down` state (R2 c, from the aws provider's own docs). `shadow` prints this once per provider as a standing note. A "percent managed" figure is not offered: the denominator is unknowable.

### Schedule and bounds

- `pravo sweep --due [--limit N]` takes the N oldest shards past their age, never-swept first, then oldest, ties by name. It copies `pravo drift --due --limit N` (R4 c). Age comes from `$discover.refresh` (default 7 days, assumption).
- `--plan` prints the number of list calls and touches nothing. For aws the arithmetic is 253 list types per provider instance; across 17 regions up to 4,301 streams (R1 section 4.3, arithmetic, never measured).
- Bounds: `--parallel N` (default 4, assumption), `--max-calls`, `--time-budget`, and `--instances N` (default 2, assumption), which caps how many provider processes run at once: 17 regions mean 17 provider processes, and that is a cost of its own.
- Scheduling is the user's: cron or CI. pravo has no daemon. A nightly job is `sweep --due`, then `shadow --new --fail`.
- Latency and rate limits are unmeasured: no `ListResource` stream was ever completed in this work (R1 section 4.4).

### How this respects "cost follows the change"

It does not, and the design says so. A plan reaches what a change touches. A sweep reaches the whole of its shard, every time. The replacement rule is:

**Cost follows the scope you declare and the age you accept.**

- `plan`, `apply`, `drift` and `destroy` never sweep and never read the ledger. `Store.load` skips `inventory/`.
- Sweeps never run inside another command.
- An optional `plan --show-shadow` line reads only shard headers. It is off by default.
- The migrate promise "before an apply, pravo calls only the read side of a provider" still holds **by convention**: `ListResource` and `ImportResourceState` are read RPCs by convention, the protocol does not forbid a provider from doing more, and nobody read a provider's list or import code in this work. pravo itself writes nothing to the cloud; the read-only role (section 10) is the enforcement. The sentence in `docs/migrate/index.md` must add "list" (R4 c).

---

## 7. Onboarding: from a discovered object to CUE that plans empty

`pravo adopt` is one deterministic pipeline. The same code runs for one object and for fifty. The gate decides.

### Step 0. Select

Three ways, same pipeline:

- `pravo adopt TYPE ID`
- `pravo adopt --ids FILE`: JSON lines, `{"type": ..., "id": "..." or "identity": {...}, "address": ..., "provider": ...}`, address and provider optional. JSON lines and not TSV, because names and ids are untrusted text and a tab or newline in one would corrupt a TSV
- `pravo adopt --from-ledger --type T [--match GLOB] [--limit N] [--order] [--parallel N] [--resume FILE] [--spec full|minimal] [--allow-owned]`

Idempotent: anything already declared by `$import` or already recorded is skipped. A hydrated object that matches an existing record of that type is refused (`already managed as ADDR`). `elsewhere` and `unlisted` objects are refused, and so is any object of a type that is unreliable until `state identify` has run (section 5). **Two declarations of one object are an error at plan time:** two `$import` entries in one specification that resolve to the same canonical identity, or the same provider, type and import id, fail with the invalid exit code (6) before anything is written. The commit-time guard that closes the rest is the identity lock (section 10, D13). When the root declares exactly one provider instance for the type's provider it is used; otherwise `--provider KEY` is required.

### Step 1. Hydrate, in memory

`ImportResourceState` then `ReadResource`, the two calls `importResource` makes today, but into memory and never into the store (R4 d).

- **By identity first** when the provider has an identity schema for the type and the type is listed. Whether aws honours an identity-only import is **unverified** (R1 section 4.4). On failure or `Unimplemented`, fall back to the id string.
- **By id** for everything else. The id format is per type and is not in the protocol. For an identity with a single required attribute, that attribute's value is the candidate id (heuristic, unverified for composite or ARN identities). If the candidate fails, the object is reported `needs id` and the user supplies it in the JSON-lines file or on the command line.
- Import can return several resources. Keep the one with the declared type and **report** the others; today they are dropped silently (R4 a).
- **Controller markers.** The hydrated object is in memory anyway, so adopt checks it for signs that another controller owns it (objects managed by CloudFormation, autoscaling, EKS and Elastic Beanstalk carry marker tags). The exact tag names were not verified in this work and must be taken from the aws documentation before they are coded. A hit warns and blocks the object unless `--allow-owned` is given. The marker list is data, editable CUE, per provider. This is detection by marker, not proof that no owner exists. Tag-based triage at **sweep** time stays out, because a sweep holds no attributes.
- A null read is `no object with id ... exists`.

### Step 2. Place and name

- Address = a CUE path. Default: `adopted.<type minus provider prefix>.<name>`, for example `adopted.s3_bucket."legacy-logs-2019"`. No pluralisation table. `--into PATH` changes the group. An address in the JSON-lines file wins.
- The name is the display name from the ledger; if missing, the identity's required name-like attribute (`name`, `bucket`, `id`); if missing, the id. Quoted as a label when it is not an identifier (the quoting code exists in `FieldLabel`/`quoteCUE`, R4 d).
- Collision: append `-` and six hex characters of the identity hash.
- The label is a human handle. **Matching is by identity or id, never by name**, so renaming the label before the first apply is free and renaming after is a `$from` move (R4 d).
- Flat files: `adopted_<type>.cue` in the root package. Whether a subdirectory is a separate CUE package is unchecked, so v1 writes into the root directory.
- **No module inference.** Moving resources into modules, folding twins into `for_each`: later, by a human or an agent, with `$from`, proven by the same empty-plan gate (R4 d). This is the architecture-level work that the OpenTofu maintainers say object-by-object generation cannot see (R1 section 6.3, R2 b).

### Step 3. Generate

One generator, pravo's own, driven by `schema.Attribute` flags (R4 d):

| Attribute | Rule |
|---|---|
| Required | emit the value |
| Optional, not computed | emit when not null |
| Optional and computed | `--spec full` (the default): emit when the observed value is not null, except a short per-type deny list kept as data (for aws, `tags_all`). `--spec minimal`: omit; a null configuration inherits the prior value (R4 d, `ProposedNew`). In the installed aws provider an S3 bucket's `bucket` and `region` are of this kind, and omitting them would make the plan empty for the wrong reason (section 2) |
| Computed only, or null | omit |
| Sensitive, optional | omit, add the name to `$lifecycle.ignore_changes`, comment why. Reported as "not managed" |
| Sensitive or write-only, required | **hole** (Step 6) |
| Not flagged, but looks secret | An attribute name matching `secret`, `token`, `password`, `key` or `credential`, a PEM block, a known key prefix, or a high-entropy string: treated as a **hole** and not emitted. Best-effort; the detector cannot be complete |
| Deprecated | omit when null, warn when set |
| Dynamic type | render the JSON as `jsonToCUE` does |
| Sets | sorted; maps keyed in order; a second run is byte-identical |

With `full`, the generator freezes into CUE every optional-and-computed value the provider reads, including values the provider would have defaulted. The protocol cannot tell a default from a choice, so the report lists them as "frozen by profile". A later provider upgrade that changes a default then shows up as a diff against the CUE, which is the intended signal. If a frozen value makes `ValidateResourceConfig` fail (conflicting arguments), the pin step drops the offending attribute, which becomes inherited, and says so. A `--spec minimal` result is labelled "tracked, not specified" in the report and in `shadow`.

Rendering is built with `cue/ast` and `format.Node`, never by string concatenation. `convert.RenderCUE` is for plan display and emits `(sensitive)` and `(known after apply)`, which are not CUE (R4 item 8). Names, tags and descriptions enter the file only as labels and string literals produced by the AST builder, never as comments and never spliced into text (section 9).

Why not the provider's `GenerateResourceConfig`: one of seven cached providers has it, it was seen only on a synthetic state, it still returned `tags_all`, and a second generation path has produced wrong output before (Terraform issue 35178) (R1 sections 2.5 and 4.1, R3 section 2.1). One path we own is cheaper to test and gives identical behaviour across providers. When the capability bit is set it may later serve as an optional second opinion (phase 5).

### Step 4. Validate and pin: the provider-level gate

Per object, in memory, no state and no engine:

1. `ValidateResourceConfig` on the generated value.
2. `PlanResourceChange` of the generated config against the hydrated state. Outcome: `clean`, `differs` (diff shown), `invalid`.
3. **Pin.** If the plan shows a diff on an attribute that was omitted or differs, set it to the observed value and re-plan. At most **three** rounds (assumption). If `ValidateResourceConfig` rejects an optional argument, drop it (the SDKv2 zero-value problem one third party reports, unverified) (R1 section 6.3).
4. Still `differs`, or any plan that would replace: the object is listed with its diff and **not written**. There is no `--keep-going` that writes broken CUE.
5. Count `specified N` (attributes the CUE sets) and `inherited M` (optional-and-computed attributes left null; computed-only ones are not counted). The gate warns when M exceeds N: the plan is empty because the CUE says little, not because it says enough.

This is deliberately not plan-as-oracle minimisation (dropping attributes while the plan stays empty). That multiplies provider calls (R3 section 7.3). Starting small by omitting computed attributes gets most of the benefit, and pinning only adds.

### Step 5. References and dependencies

The protocol says nothing about which attribute points at which type (R4 d). pravo recovers references heuristically, visibly:

- Build an index: `id`, `arn` and identity key values of every managed record and every member of the batch, to an address.
- Replace a string with a reference only when **all** hold: the value equals an index key exactly; the key is unique in the index; the attribute name looks like a reference (`*_id`, `*_ids`, `*_arn`, `arn`); it is not the object's own id.
- Never by name. Names collide.
- A cycle (security groups referencing each other) keeps one direction as a literal; `checkCycles` rejects cycles otherwise (R4 d).
- Every substitution is printed in a `links` table. A false link is cheap (the plan still compares values). A **missed** edge is not: the record gets no `dependencies`, so no ordering and no blast radius (R4 item 7). Without links the CUE is still correct, only edgeless.
- If a batch member that others reference is trimmed, the gate fails with the reason.
- **Order and relinking.** Reference recovery sees only records and the current batch, so a subnet adopted in batch 3 before its VPC in batch 7 keeps a literal VPC id, and that is the costly missed edge. Two answers. `adopt --order` forms batches in dependency order by type (network, then identity and access, then compute) from a built-in type-order table, which is data and incomplete by nature. `pravo adopt --relink` re-runs reference recovery over files already adopted, proposes literal-to-reference changes, and puts them under the same empty-plan gate. Neither is a guarantee; the `links` table and the relink pass are the audit.

### Step 6. Sensitive values and holes

- A required sensitive or write-only attribute cannot be recovered; the API never returns it (R3 section 4 item 4).
- Example of a required sensitive attribute: `aws_docdbelastic_cluster.admin_user_password` (19 aws types have one in the cached 6.69.1 schema, checked by script). `aws_db_instance.password` is optional and sensitive, so it is omitted and shielded by `ignore_changes`, not a hole.
- **An object with a hole is held back:** the gate does not run on it until the hole is filled, so a hole is never reported as `clean`. Open case, not solved: the cloud never returns the secret, so after a human fills it the imported state holds null while the configuration holds a value, and the plan shows a change. The success test passes only if the attribute is shielded by `ignore_changes` once filled. The generator adds that shield when it is filled, and the report says "not managed". Phase 0 checks this.
- The generator writes the field as its schema type (for example `master_password: string`), so the specification is non-concrete and `pravo validate` fails until a human supplies a source. The exact failure message for a non-concrete required field is **unverified**; phase 0 checks it.
- The human writes `pravo.#Env` (value recorded in state) or a literal, or `pravo.#SecretEnv` where the attribute is write-only or ephemeral. `#SecretEnv` is not allowed in an ordinary sensitive attribute (R4 item 8, `spec-env.md`).
- The agent may propose **where** a secret comes from. It never chooses or sees the value.
- Provider `sensitive` flags are not complete: a password inside `user_data` or a tag is not marked. The report says "scan generated files with your secret scanner before merge". pravo cannot guarantee this.

### Step 7. The overlay gate: the empty plan

Run the real engine over the generated files in a **scratch overlay**: reads fall through to the real state read-only, writes go to a temporary directory (mode 0700, deleted after). Whether an overlay `Backend` is cheap is an **assumption**: the interface has nine small methods (`Get, Put, PutIfAbsent, DeleteIf, ReplaceIf, Delete, List, Name, Local`, R4 b), which makes it plausible. If it is not, the fallback is a temporary local backend seeded with the records the batch references.

Pass condition: every object prints `matches the specification`, and the plan reads `0 to add, 0 to change, 0 to destroy`. Anything else: nothing is written, the offending object is reported, stop.

The gate runs with `ignore_changes` as generated. The report states how many attributes are shielded by `ignore_changes`, because a blanket `"*"` would pass trivially. Whether `ignore_changes: "*"` covers nested blocks is unverified, so the generator never emits `"*"`.

### Step 8. Write

Only gate-clean objects are written: `adopted_<type>.cue`, plus an optional `--report FILE` (markdown: links, pins, shielded attributes, holes, leftovers). Each resource carries `$lifecycle: prevent_destroy: true` (opt out with `--no-protect`). `prevent_destroy` makes a replacing plan an error and makes `pravo destroy` refuse; it does **not** stop the orphan delete when the declaration is removed (R4 b table). That gap is decision D2.

### Step 9. Land

Ordinary change: `pravo plan --out adopt.plan` with the example policy `adoption-only` (it refuses any plan that adds, changes, replaces or destroys), then `pravo apply adopt.plan`, then `pravo plan` again, which must reach zero. **The gate is advisory and the saved plan is authoritative.** Between the overlay gate and the plan the cloud object may have changed (it is shadow; nobody in pravo controls it), so a plan that differs from the gate's summary is possible and correct. The policy turns that into a refusal (exit 5), and the answer is to re-run `adopt`, which is idempotent, not to apply. The report records the gate's summary (addresses, counts, pins) so a reviewer can compare it with the plan. Adoption **ends with an apply, even when the plan changes nothing**: an adopted record has no `dependencies` and no `config_hash` until an apply, so until then every later plan re-reads it and `pravo state dependents` prints nothing (probe, R4 item 7). Settling without an apply is a riskier core change (R4 g change 9) and is not in the design.

### Import identity in CUE

`$import` today takes an id string. When identity import is honoured (phase 0 decides), adopt writes the identity form instead, for example `$import: identity: {bucket: "...", region: "..."}`. This is a language change and is flagged as such. Until phase 0 proves it for aws, adopt writes the id string.

### Batches

A batch is whatever one `adopt` run writes: default cap of 50 objects (assumption), one pull request. 1,000 objects are 20 batches. Review scales per rule and per batch, not per object: a human reads the shadow table, the ignore rules, the batch summary, the links and pins, and samples the CUE. The gate makes the unread part safe, not well named (R3 section 4 item 9).

Calendar cost, said plainly. Batch k+1 can link to batch k only once k is applied, so 20 batches are 20 pull requests, each with plan, apply and a second plan, one after another. That is days of calendar time, and the design does not make it faster. Running one gate over the union of several pending batches might (unverified; a phase 0 question). Nothing is persisted between steps except what the user asks for: `--resume FILE` writes identities and statuses only, never attribute values (hydrated values can hold secrets), so a run that dies at object 37 of 50 re-hydrates the rest instead of starting over. `adopt --parallel N` hydrates objects concurrently (default 1 until timing is measured; assumption).

### Rollback

- Before apply: delete the generated file. With D1 (import at apply time) nothing was written to the state. Without it, the stopgap `pending_adoption` mark must be cleared (section 13, D1). With D1(a), the apply-time import compares the freshly read state with the state in the saved plan and stops with the stale exit code (3) on any difference.
- After apply: the plan changed nothing in the cloud, so there is nothing to undo there. `pravo state rm ADDR...` (an existing command, "Forget a resource without destroying it") removes the records and leaves the objects. Remove the text in the same change. With a protected record (D2) the next plan is an error rather than a delete; `state rm` should also warn when the declaration is still in the specification (a small change to an existing command; whether it already warns is unverified).
- A partly applied batch (the apply stops midway) is rolled forward: re-run the same saved plan or re-run `adopt`, which skips what is recorded.

---

## 8. The commands

Three new verbs, one new subcommand, three extended. All have `--json`. Commands print fragments and write files only when asked or when their purpose is writing (`adopt`). Output below is illustrative; nothing was run.

| Command | What it does | Cloud calls |
|---|---|---|
| `pravo sweep [--type T]... [--scope NAME] [--due [--limit N]] [--from FILE] [--all-types] [--plan] [--parallel N] [--instances N] [--max-calls N] [--time-budget D]` | Lists chosen types, writes ledger shards | Read only (list) |
| `pravo shadow [--type T] [--new] [--state S] [--known-state FILE] [--coverage] [--json] [--fail]` | Prints the shadow estate and coverage from the ledger | None |
| `pravo adopt (TYPE ID \| --ids FILE \| --from-ledger [--type T] [--match GLOB]) [--limit N] [--into PATH] [--provider KEY] [--order] [--parallel N] [--resume FILE] [--spec full\|minimal] [--allow-owned] [--print [--redact]] [--report FILE] [--no-protect]` | Select, hydrate, generate, gate, write | Read only (import, read) |
| `pravo adopt --relink [FILE...]` | Re-run reference recovery over adopted files, propose literal-to-reference changes under the same gate | Read only (import, read) |
| `pravo state identify [ADDR...]` | One time: read each record through the provider and write its `identity` field. A state write (additive field, per-resource lock), no cloud write | Read only (read) |

To forget a record without touching the cloud use the existing `pravo state rm`; there is no `release` command.
| `pravo schema --list TYPE` | Prints the list config (filters, scope) of a type | None |
| `pravo why TYPE:ID` | Why an object is seen, ignored, elsewhere, declared or managed | None |
| `pravo mcp` | Adds read-only tools (section 9) | as above |

Exit codes: the current table in `docs/reference/exit-codes.md` ends at 6 (file read, verified), so 7 and 8 are free. `shadow --fail` exits 7 when new shadow objects exist. `sweep` exits 8 when any shard failed or was truncated. Neither reuses drift's 2 (R4 c). The numbers are a proposal.

### One resource, by hand, no sweep, no agent

```
$ pravo adopt aws_s3_bucket legacy-logs-2019
adopt  aws_s3_bucket "legacy-logs-2019"   provider aws_eu (123456789012, eu-west-1)
  import     ok   by id
  read       ok
  generate   specified 5, inherited 2 (--spec full), 1 pinned (force_destroy = false), 2 frozen by profile (region, object_lock_enabled)
  validate   ok
  gate       clean   0 to add, 0 to change, 0 to destroy   (overlay)
  protect    prevent_destroy
wrote adopted_s3_bucket.cue
  adopted.s3_bucket."legacy-logs-2019"
next
  pravo plan --out adopt.plan
  pravo apply adopt.plan       # adoption ends with an apply, even at zero changes
  pravo plan                   # must reach 0
```

The file it wrote, illustrative:

```cue
adopted: s3_bucket: "legacy-logs-2019": $aws.#Resource.aws_s3_bucket & {
	$import:    "legacy-logs-2019"
	$lifecycle: prevent_destroy: true
	bucket:        "legacy-logs-2019"
	region:        "eu-west-1"      // frozen by profile (optional and computed)
	object_lock_enabled: false      // frozen by profile
	force_destroy: false            // pinned: provider default, shown by the plan
	tags: {retention: "2019", team: "data"}
}
```

```
$ pravo plan --out adopt.plan
# adopted.s3_bucket."legacy-logs-2019" was imported from id "legacy-logs-2019" and matches the specification
Plan: 0 to add, 0 to change, 0 to destroy. 1 imported.
$ pravo apply adopt.plan
$ pravo plan
No changes. reached 0
```

### A thousand objects

```
$ pravo sweep --plan
scope eu   sweep aws_eu_read, manage aws_eu   (123456789012, eu-west-1)   38 types   38 list calls   time: unknown (no stream has been timed)

$ pravo sweep --due --limit 38
sweep  scope eu   actor ci-nightly
  aws_s3_bucket       listed      212   complete
  aws_iam_role        listed      458   complete
  aws_vpc             listed        3   complete
  aws_instance        failed            stream ended: unavailable. old entries (2026-10-15) kept
  aws_api_gateway_method   needs-parent
  ...
37 shards, 1 failed. exit 8

$ pravo shadow
scope eu   last sweep 2026-10-22 02:14   (provider omits defaults, ASG instances, terminated instances)
type                 seen  ignored  elsewhere  managed  unlisted   coverage
aws_s3_bucket         207        3          0        2     0   listed, complete
aws_iam_role            -        -          -        -     -   listed, complete; unreliable until state identify has run
aws_vpc                 2        0          0        1     0   listed, complete
aws_instance            -        -          -        -     -   failed (old data 2026-10-15)
aws_wafv2_web_acl       -        -          -        -     -   no-list: use --ids
aws_api_gateway_method  -        -          -        -     -   needs-parent
9 aws_iam_role records have no identity: run pravo state identify. A type with an unmatched record is never listed as seen.

$ pravo shadow --type aws_s3_bucket --state seen       # one row per object; --json for scripts
state  name                      identity                  first seen   last seen
seen   lantern-photos-2019       lantern-photos-2019       2026-10-22   2026-10-22
seen   lantern-tmp-export-0912   lantern-tmp-export-0912   2026-10-22   2026-10-22

$ $EDITOR infra.cue          # add ignore rule: glob true, expect 3, why "scratch buckets"

$ pravo adopt --from-ledger --type aws_s3_bucket --limit 50 --report batch1.md
50 selected: 50 clean, 0 differs, 0 invalid, 0 holes, 0 owned elsewhere, 6 links, 11 pinned attributes, specified 412 / inherited 61
wrote adopted_s3_bucket.cue (50 resources). report batch1.md

$ pravo plan --out a1.plan --policy adoption-only.cue && pravo apply a1.plan     # one pull request per batch
```

The manual path with no model, for a type that lists, is: `sweep`, `shadow`, edit the ignore rules, `adopt --limit 50`, read the report, plan, apply. For a type that cannot list, add one step at the front: produce the JSON-lines file (a recipe for the thirty common aws types, or your own pipeline). At a thousand objects across thirty such types that step is the hard work, it is where an agent is the realistic helper, and it is not manageable by hand beyond the recipes. The design does not hide that.

---

## 9. The agent layer

Everything in sections 5 to 8 works with no model. An agent is a contributor that proposes through files and read-only commands.

### The manual and agent versions of each step

| Step | By hand | With an agent |
|---|---|---|
| Decide what to sweep | Write `$discover.types` | Read `pravo schema --list`, propose scope |
| Triage the shadow list | Read `shadow`, write ignore rules | Draft ignore rules with reasons as a CUE diff |
| Produce the id list for a type that cannot list | A recipe from the recipes folder, or your own CLI pipeline | Research the type's import-id format and write the recipe or the JSON-lines file; the realistic tool at scale, deterministic once written |
| Select objects | `--match GLOB`, or a JSON-lines file | Build the file, set addresses |
| Generate and gate | `adopt` | Same command, `--print` |
| Fill holes | Write `pravo.#Env` | Propose which source; never the value |
| Resolve a `differs` | Pin by hand | Explain the diff, propose the pin |
| Reshape (twins to `for_each`, modules) | Edit CUE, `$from` | Do it, proven by the empty-plan gate |
| Review text | Write the PR | Write the PR description from the report |

### MCP tools

`pravo mcp` has 13 tools and none that edits state or applies without a saved plan (R4 e). New tools keep that:

- `pravo_shadow`: reads the ledger, paged. Free.
- `pravo_sweep`: bounded sweep. The call, shard and time caps are fixed when the server starts, like `--policy`, and the agent cannot raise them. It runs with the ledger credential and the read-only sweep instance only (section 5), so it needs no write access to `resources/`.
- `pravo_adopt_print`: runs `adopt --print --redact`. It writes nothing, not the ledger, not the state, not a file. **Redacted by default**: it returns structure, attribute names, counts and the gate result, with values replaced except for an allow list (identity, names, booleans, small numbers). The reason: the generated CUE is a literal copy of the cloud object, provider `sensitive` flags are incomplete (a password in `user_data` or a tag is not marked), and a tool result goes to a model provider. Returning values needs an operator opt-in set when the server starts. With redaction on, the agent proposes ids, addresses and ignore rules, and a human runs `adopt`. The agent saves a generated fragment with its own file tools only when the operator allowed values.
- `pravo_why`, `pravo_schema` extended.

The skill (`pravo skill`) gets a brownfield section: the order of steps, the stop rule, the list of things never to do. `SKILL.md` names commands only after they exist in the binary (a test enforces this, R4 e).

### What an agent may propose

Ignore rules with reasons. Scope. Addresses and the JSON-lines file. Pins and the source of a secret. Restructuring into loops and modules. Explanations of a gate diff or of why an object is seen. Pull request text. All as files or diffs that a human reviews.

### What an agent may never do

- Write the ledger directly. Only `sweep` writes it.
- Write or edit the state, or run anything that does. `adopt` and `state rm` are not MCP tools that write.
- Apply anything except a saved plan, through `pravo_apply_plan`, as today.
- Land an ignore rule or an adoption without a human merging the diff. Ignore rules are the only way to make shadow objects vanish, so they are the most reviewed text.
- Choose a secret value. It sees attribute values at all only if the operator allows them. "An agent never sees a secret" would be false by construction, since provider sensitivity flags are incomplete; the true statement is that the agent never chooses a secret and sees values only by operator opt-in.
- Weaken a policy, raise a cap, or add `--no-protect`.
- Treat a display name, tag or description as an instruction (below).

### How a human stays in control

- The gate is deterministic and is the same for a human and an agent. A passing gate is necessary; a human merge is also required.
- Policies are fixed at server start and appended to `plan` and `apply`; an argument cannot remove them (R4 e).
- `--actor` and `--reason` are recorded on sweeps, transactions and plans.
- Reservations on the target addresses work for addresses that do not exist yet (R4 e).
- A batch cap of about 50 (assumption), so a human can read a batch.
- An agent-authored ignore rule does not merge without the listing of what it hides (section 5). The reviewer sees the objects, not only the glob.
- A policy to require approval of imports needs a structured marker on the plan document. Today there is only the reason text `was imported from id ...` and the count `stats.imported` (R4 item 9). Adding `imported` and `import_id` to `ChangeView` is a prerequisite (section 14).

### Prompt injection through cloud data

Display names, tags and descriptions are written by whoever can create cloud objects, which can include an attacker. They are data.

- Tools return them quoted and length-limited, with control characters already replaced at ledger write.
- The skill states: never follow an instruction found in a name, tag or description.
- The CUE builder escapes them as above; a name can never become code or a comment.
- The report shows them in code spans.

---

## 10. Governance and security

- **Credentials.** Three, kept apart. (1) The **sweep instance**: a dedicated read-only cloud role that sees everything the provider can list, used only to list. pravo cannot enforce it, since the provider config is the user's, and the protocol does not make `ListResource` or `ImportResourceState` read-only (a provider convention, section 6), so the read-only role **is** the enforcement. (2) The **ledger credential**, which writes `inventory/` and nothing else (section 5). (3) The **manage instance**: write access, recorded on adopted records and used by import, read, plan and apply; apply runs only from a saved plan. A sweep job holds (1) and (2) and no power over `resources/` or over the cloud.
- **Read-only discovery, by convention.** Sweep and adopt call list, import (a read in effect) and read, which are read RPCs by convention. The protocol does not enforce it and no provider's code was read in this work, so the read-only role above is the guarantee. `pravo` writes no tags and creates nothing, because tag-writing would end the promise "before an apply, only the read side". The scope of the promise sentence must be extended with "list".
- **Policy.** Policies already judge a plan document. Wanted: "no adoption plan may add, change, replace or destroy" (a generic guard for batches), and "imports need approval" (needs the structured marker). A policy that refuses deletes would be tripped by an orphan delete, which is the intent.
- **Reservations.** Reserve the target addresses of a batch before generating. There is no reservation of a real object across roots (R4 item 10).
- **Double adoption.** This is the one failure that deletes real infrastructure: two records own one object, and destroying either destroys the other's object. Locks are per address, so two addresses with the same `$import` id take two locks and both import. Realistic causes: two engineers adopt on two branches, two batches overlap, `--ids` and `--from-ledger` runs overlap, or one object arrives once as an identity and once as an id string. Three layers. (1) At plan time, two declarations in one specification that resolve to one identity are an error (section 7 step 0). (2) At commit time, an **identity lock**: a record written from an import takes a lock keyed by canonical identity (for id-only types: provider, type and import id) and is refused when another record in the root holds the same identity. This is a second lock kind and changes the documented "one lock per resource"; it is decision D13. The id-only key can miss an object reached once by identity and once by a differently formatted id; the cross-input merge (section 5) reduces that and nothing removes it. (3) Mitigations that are not guarantees: `known` states, the refuse on `elsewhere`, the controller-marker check, review. **Across roots nothing detects it.** A cross-root ownership registry is an open decision (D4), not built.
- **Audit.** Ledger shards carry `actor` and time. Records written by adopt are in `import`-kind transactions. `--actor` and `--reason` are recorded.
- **Secrets.** The ledger holds no attribute values. Hydrated values exist in memory, and in the overlay directory (0700, deleted after) during the gate. The report and PR text carry no values. Generated files hold no sensitive-marked value and no value the built-in detector flags (section 7 step 3), which is best-effort and cannot be complete. `--resume` files hold identities and statuses only. MCP output is redacted by default (section 9). State holds secrets as today ("treat `pravo.state` as sensitive", R4 b). Provider sensitivity flags are incomplete, so a secret scanner on the PR is advised.
- **The ledger is sensitive too.** It holds object names, which can be confidential. Protect it like the state.
- **Generated text is escaped.** AST-built CUE only (section 7 step 3).

---

## 11. Scale and cost

| Quantity | Figure | Basis |
|---|---|---|
| List calls per sweep of one aws provider instance | up to 253, fewer if scoped | R1: 253 list types; arithmetic |
| List calls across 17 regions | up to 4,301 | R1 section 4.3; arithmetic, not measured |
| Ledger objects written | N shards, not N objects | design |
| Ledger size | about 200 bytes per entry | assumption |
| Cost of opening the ledger | 1 LIST plus one GET per shard | design |
| Provider calls to adopt an object | import and read in the provider-level gate, again in the overlay gate, again in the real plan, again in the proof plan, plus validate and pin rounds: about 8 to 12 | arithmetic on the pipeline; **not measured** |
| Provider calls to adopt 1,000 objects | about 8,000 to 12,000, plus retries | arithmetic; **not measured** |
| Records added | N more records read by every command | R4 item 5 |

Rules:

- A sweep is O(estate) and full, every time. The protocol cannot say "changed since" (R1 section 2.2).
- Default scope is **only what the specification names**. `--all-types` prints the number of list calls and asks for `--yes`.
- Plan and apply pay nothing for the ledger.
- Adoption is not free: after adopting N objects every command reads N more records (8 at a time locally, 64 at a time on S3, R4 item 5). **Split roots by account or region before adopting thousands.**
- Hydration is lazy and per object, only at adopt. `adopt --parallel N` and `sweep --instances N` bound concurrency; defaults are unmeasured.
- Time per list stream, rate limits, and API billing are **unmeasured**. A spike on a sandbox account measures them (section 14, phase 0).
- Nightly cost is bounded by `--due --limit` and `--time-budget`.

---

## 12. Where this gets messy or impossible

Honest pushback, with reasons.

### Things we will not do

1. **Shadow records in `resources/`, or a third record mode.** A probe showed that a record without a declaration is planned for deletion (`dice will be destroyed`, action `delete`, R4 item 3), `destroy` destroys every record, and ten more sites walk all records. One missed site destroys a real object. This is the most dangerous option and the reason for decision A.
2. **A promise of the complete estate.** See below.
3. **A "percent managed" figure.** The denominator is unknowable. Vendor figures of "97.5% via IaC" are marketing (R3 section 2.13).
4. **Auto-adoption, or an agent or cron job that merges.** A batch needs a human.
5. **Native cloud-inventory adapters** (Cloud Control, Resource Explorer, Cloud Asset Inventory, Resource Graph). Each needs a cloud-id-to-provider-type-and-identity table that nobody publishes except `aztft` for Azure (R3 section 7.2). The file door lets others feed them.
6. **A dependency on `tofu query`, `terraform query` or `.tfquery.hcl`.** No `tofu query` was found in OpenTofu v1.13.2 (three Go files read, the rest of the repository not searched), and its issue (#3787) was open and labelled `pending-decision` on 2026-10-20, so as of that date OpenTofu has not shipped list or query. That is not a claim about anywhere else, and a reader deciding whether to wait for OpenTofu should check again. Terraform shipped it in 1.14.0 (R2 a, b). pravo talks to providers directly, so the same RPCs serve either.
7. **Observe-only records and a rights ladder in v1.** Crossplane's observe, update, delete ladder and Pulumi's protect-by-default are good ideas (R3 sections 2.8 and 2.10). They need new record semantics. `prevent_destroy` plus the section 13 D2 mark gives most of the safety. A watch-only recipe (`ignore_changes` plus `prevent_destroy` plus a policy) can be documented, but `ignore_changes: "*"` over nested blocks is unverified.
8. **Incremental sweeps and a watch daemon.** The protocol has no change feed; cron is enough.
9. **Tag-based triage at sweep time, writing tags, cleanup of the shadow estate.** A sweep holds no attributes, and tag writing breaks read-only discovery. (A marker check at **adopt** time is in the design, section 7 step 1, because the object is in memory anyway.) Deleting what nobody owns is a different product.
10. **Automatic modules or `for_each`.** No tool does it (R3 section 6). Intent is human or agent work, then the same gate.
11. **Plan-as-oracle minimisation, stored attributes, SQL over the ledger.** Cost and secrets.
12. **Discovery of types that cannot list.** The file and id inputs are not discovery. For about 1,450 of aws's 1,735 managed types (after the thirty recipes) and for every Kubernetes type, the user brings the list. This is the largest honest gap in the "works without an agent" promise: the deterministic path is complete from the id onward, and thin before it.
13. **A promise that an empty plan means "described".** It means "not contradicted" (section 2). Attributes left to the provider follow the cloud and are never reverted.

### Things that depend on providers

- **Coverage is partial and uneven.** Verified on this machine: aws 6.69.1 lists 253 of 1,735 managed types (14.6%) and has 551 identity schemas (31.8%). kubernetes 3.4.0 has 41 identity schemas and no list types. local, null, random, tls and shell have neither. aws 5.100.0 has neither and does not implement `GetResourceIdentitySchemas` (R1 section 4.1). By documentation page count (a proxy): azurerm about 11%, google about 13%, awscc about 75%; none found for github, datadog, cloudflare, vault, helm (R2 c).
- **Pure SDKv2 providers cannot list.** terraform-plugin-sdk 2.38.0 answers the RPC with an error (R1 section 6.2, R2 c).
- **Kubernetes** has no list at all in the cached provider. Most of a cluster is already YAML. Discovery there is a file door (`kubectl get -o json` to JSON lines), and `kubernetes_manifest` has an identity. Kubernetes users are on that door only. We do not claim more.
- **Coverage moves.** The oldest aws changelog mention of list is 6.14.0 (2025-09-18); 253 types by 6.69.1 (2026-10-15) (R1 section 4.2). The coverage table is computed from the installed provider version, never hard-coded.
- **List is best-effort.** Providers exclude defaults, special cases, ASG-owned and terminated instances (R2 c). Complete means the union of list, file and by-id, not a single call.
- **Identity import** is unverified for aws, and import ids per type are not in the protocol. The id path always stays open.
- **What a list returns is not what Read returns.** One third party reports SDKv2 zero values that break `ConflictsWith`/`RequiredWith`, per-type failures, and no references between listed objects (R1 section 6.3, R2 c; single source, unverified). The design assumes all three and handles each: validate, per-shard failure, heuristic links.
- **Provider crashes.** An unconfigured list call killed the aws process (R1 section 4.4). Supervision is required.
- **Eventual consistency** of cloud list APIs is common but no cloud statement was verified in this work. The ledger being a hint, re-read at adopt, is the defence.

### Things that cannot be made safe

- **Adoption makes an object destroyable, by design.** Removing a declaration plans the deletion of the real object. `prevent_destroy` does not cover that case (R4 b). With D2(b) the plan for a protected, undeclared record is an error instead; protection (D2), policy and review reduce the risk, and no tool removes it. If a protection field can be dropped by an older binary (unverified), D2(b) needs the format bump (D5).
- **Two records for one object** are prevented inside a root only by the identity lock (D13), and only when the identity is known. The by-id key can miss. Across roots there is no guard at all.
- **Identity keys are canonical only as far as a real list event fills them.** Nobody has seen one (section 5).
- **Serial batches.** A thousand objects are twenty sequential pull requests (section 7). That is days of calendar time.
- **Ownership cannot be detected in general.** CloudFormation stacks, operators, service-linked roles and other roots are recognised at best by naming. Vanta did it with a hand-built ignore file (R3 section 2.9). Ignore rules and `known` states are the honest answer.
- **Who created an object is not recoverable** from the list protocol. env0 claims it; the method is unpublished (R3 section 6).
- **Secrets the API never returns.** Every tool says "add them by hand"; so do we.
- **Equivalence.** An empty plan means the CUE reproduces what the provider reads, defaults included. Whether those defaults are intended is a human question.
- **Granularity.** Whether a subnet is an inline block or a separate resource, association objects, side-effect objects such as an OS disk: the provider offers the choice and a tool cannot know the owner's preference (R3 section 4 item 5).
- **Two roots adopting one object** is not detected across roots.

### Things that are messy in pravo today

- A leftover `differs` after three pin rounds is not written. By hand the person uses `--print`, edits the file and lets the ordinary plan be the check. A command that re-runs the overlay gate over a hand-edited file is not designed. This is a gap in the manual path.
- Plan-time `$import` writes state before review (R4 item 4). Prerequisite D1, which changes the documented property "a plan is not read-only".
- Locks are per address (R4); the identity lock (D13) is a second kind.
- `pravo state rm` exists but its warning when a declaration remains is unverified.
- `pravo import` and the engine path differ: the command takes the first imported resource without type matching, does not stamp `ProviderConfig`, and calls `State.Set` without `Lock` or `Verify` (R4 a). Fix before reuse.
- Records have no identity (R4 item 1) and no structured import marker in the plan document (R4 item 9).
- No `ListResource` stream was ever completed. Everything about event contents, `display_name` quality, ordering and latency is untested. Docs and tests need a list-capable provider that needs no cloud account; none of the cached ones qualifies (R4 f). A fake provider and recorded replay through `Recorder` are the options; a recorded stream is a new kind of record (R1 section 3).

---

## 13. Open decisions for the user

The blockers are D1, D2, D7 and D13 (D13 sits at the end of the list, after D12). D1, D2 and D13 each change a property pravo documents today, and each entry says which.

**D1. When does an import write the record? (blocker for releasing `adopt`)**
Today `pravo plan` writes the record before anyone reviews the plan. With agents planning and a thousand objects, an abandoned adoption leaves an orphan, and the next plan proposes to delete the real object (R4 item 4).
- (a) Carry the import in the saved plan, so an unreviewed plan writes nothing and an import takes the address lock. Largest change; changes the documented property "a plan is not read-only" (R4 g change 10). Safest result.
- (b) Stopgap: a `pending_adoption` mark on the record that `planOrphans` never deletes and `pravo state` can list and clear.
- (c) Document the hazard.
Recommendation: (a), and (b) only as a bridge for the first release if (a) slips. Trade-off: (a) touches the plan/apply contract; (b) adds a mark every site must honour; (c) ships a known trap.
Under (a), the invariant "saved plans are applied exactly as reviewed" is kept by one rule: at apply, the import reads the object again and compares it with the state recorded in the saved plan, and any difference stops the apply with the stale exit code (3). Without that rule (a) breaks the invariant it claims to protect. Under (b), the invariant "an undeclared record is planned for deletion" holds except for the marked record, and the mark is a new record field (see D5).

**D2. What protects an adopted record from the orphan delete? (blocker)**
`prevent_destroy` guards only declared resources (R4 b).
- (a) `prevent_destroy` only.
- (b) Copy the protection into the record at write time. The rule, stated exactly: **a protected record that is not declared makes the plan an error** (exit code 6, invalid, is the nearest existing code; a new one may be chosen), instead of a delete and instead of being silently skipped. `destroy` refuses a protected record the same way. Unprotecting is a reviewed change of its own: declare the resource with `prevent_destroy: false`, apply that plan (the record's protection is rewritten), and only then remove the text; or run `pravo state rm`. The "allow delete" decision therefore lives in the saved plan document and never in an apply flag, so "applied exactly as reviewed" holds and no operator or agent can delete an adopted object outside the reviewed plan. This changes the documented property "an undeclared record is planned for deletion" for protected records only.
- (c) An observe-only record mode.
Recommendation: (b), with two preconditions that phase 0 settles before it is chosen: (1) whether an older binary that rewrites a record keeps an unknown field (unverified); (2) the format. If an older binary may drop the field, protection can vanish with no warning, so the safe option is a **format bump to 2** that older binaries refuse (`state-format.md`: a store with a higher format is refused). That makes D5 the price of D2(b), and the user decides. `ARCHITECTURE.md` says the format becomes 3 once a record has a mark while the code says `Format = 1`; reconcile that first. Trade-off: (b) adds a record field, a format question and a new plan error; (c) is more powerful and much more invasive.

**D3. Where does `$discover` live?**
- (a) Top-level field in the specification (recommended).
- (b) A meta field on the provider struct, so scope falls out of the instance. Unverified how provider structs treat `$` fields.
- (c) A sibling file named on the command line, like policies.
Trade-off: (a) is one place for scope, rules and known states; (b) is neat for scope but splits rules from scope; (c) keeps the specification untouched but makes decisions easy to miss.

**D4. One ledger per root, or one per account?**
The shadow estate belongs to the account, but "managed" is relative to a root (R4 item 10).
- (a) Per root in v1, keys that merge later (recommended).
- (b) A shared ledger on a named backend plus a per-root list of identities (also gives cross-root double-adoption detection).
Trade-off: (a) repeats sweeps across roots and cannot see other roots; (b) is a bigger design.

**D5. Format bump or none? Two questions, not one.**
- For the ledger, (a): none; the ledger is a rebuildable cache (recommended).
- For the ledger, (b): raise `Format` when `inventory/` first exists, so older binaries refuse the store.
- For records: a new record field (D2(b) protection, the `identity` field, the D1(b) `pending_adoption` mark) is not a cache. Unless phase 0 shows that an older binary keeps unknown fields, the field needs a bump to 2 that older binaries refuse, or the feature is unsafe. Recommendation: bump for protection.
Trade-off: (a) lets an old `pull` drop the ledger (one re-sweep, history lost); (b) locks older pravo out of the whole store.

**D6. Settle adopted records without an apply?**
- (a) Accept "adoption ends with an apply" (recommended).
- (b) Make a plan write `config_hash` and `dependencies` when an imported record matches (R4 g change 9).
Trade-off: (b) touches the core "unchanged" decision and can mark a record settled that is not.

**D7. First provider, and a sandbox account. (blocker for phase 0)**
Recommendation: aws, because it is the only list-capable provider on the machine (R1 section 4.1). Needs a read-only aws account, roughly a day or two. Without one, list behaviour stays unverified. azurerm and google are the next candidates by documentation count; awscc lists about 75% but through Cloud Control with different CUE types.

**D8. Regions.**
- (a) One declared provider instance per region (recommended for v1).
- (b) The `region` list attribute on one instance. Unverified, and it needs the region attribute pinned on adopted resources.

**D9. Observe-only rights in v1?** Recommendation: no (section 12 item 7). Option: document the watch-only recipe.

**D10. Use the provider's `GenerateResourceConfig`?** Recommendation: not in v1 (section 7 step 3). Revisit when real reads exist.

**D11. Defaults to tune on real data.** Batch cap 50, pin rounds 3, `limit` 10,000 per shard, sweep age 7 days, `unlisted` pruned after 30 days, sweep parallel 4, sweep instances 2, adopt parallel 1, exit codes 7 and 8. Recommendation: ship them as flags with these defaults and tune after phase 0.

**D12. Test provider for docs and tests.** Docs are verified by running every command (`tools/capture`), so each page needs a provider that works with no cloud. Options: a small in-repo fake list-capable provider; recorded replay through `Recorder`. Recommendation: both, the fake first.

**D13. Identity lock. (blocker for releasing `adopt` in bulk)**
Two addresses with one `$import` id take two locks and both import, so two records can own one object and destroying either destroys the other's object (section 10).
- (a) At commit time, a record written from an import takes a lock keyed by canonical identity (or provider, type and import id), and is refused when another record holds the same identity. A second lock kind; it changes the documented "one lock per resource".
- (b) A plan-time check only: two `$import` entries in one specification that resolve to one identity are an error. Cheap, and does not cover two branches, two runs or two roots.
- (c) Review only.
Recommendation: (a) and (b). Limits stated: the id-only key can miss a differently formatted id for the same object; across roots nothing is detected (D4 stays open).

**D14. Controller-marker list.** The adopt-time check for objects owned by another controller (section 7 step 1) needs the actual marker tags per provider, taken from the provider's documentation. None was verified here. Recommendation: ship the list as editable CUE data, aws first, after phase 0 reads the aws documentation.

---

## 14. Phases

The phases are an ordering, not a schedule. Any duration would be a guess (assumption), because phase 0 needs a read-only aws sandbox account that does not exist yet (D7) and measures about twenty unverified behaviours, and phase 1 is a generator, two gates, reference recovery, holes, a report, three core changes, a marker on `ChangeView`, a `pravo import` rewrite and docs.

### Phase 0. Spike

Needs a read-only sandbox aws account (D7). It measures the unverified, and also checks seven questions about pravo itself.

On aws: a full `ListResource` stream; event contents; `display_name`; ordering; behaviour of `limit` and `limit = 0`; the cause of the crash on unconfigured list; identity-only `ImportResourceState`; both `identity` and `id` given; `ReadResource` with identity; `UpgradeResourceIdentity`; `GenerateResourceConfig` on a real read; multi-region from one instance; timing and rate limits for one stream; whether list output equals Read output; what a real list event puts in `account_id` and `region`, and whether `ReadResource` `new_identity` gives the same key (canonical keys); whether a provider that fails midway ends the stream with OK and an error diagnostic; which tags aws puts on CloudFormation, autoscaling, EKS and Elastic Beanstalk objects (from its documentation).

On pravo: does an older binary keep an unknown record field (this decides D2(b) and the format bump in D5); does `$discover` fit as a top-level field; does `pravo validate` reject a non-concrete required field and with what message; can pravo's policy language say "refuse any add, change, replace or destroy" (the `adoption-only` policy is not written); can a subdirectory be a separate CUE package; is `pravo state rm` or equivalent able to forget a record without destroying; is an overlay `Backend` cheap; which exit code and message a protected, undeclared record should produce; can two `$import` entries with one identity be caught at plan time; does `pravo state rm` warn when a declaration remains; does a gate over the union of pending batches work.

### Phase 1. Core changes first, then adoption by id (no provider support needed)

Needs no provider support and works with today's plugin layer and every provider that imports, but it is **not small**: it changes pravo's core. Order: core changes first (D1, D2 with its format decision in D5, D13, the `ChangeView` marker, the `pravo import` rewrite, `state identify`, the `state rm` warning), then the generator and the gates.
- The generator (`--spec full|minimal`, secret detector, controller-marker check), the provider-level gate, the overlay gate, references, `--order`, `--relink`, `--resume`, holes, report.
- `pravo adopt TYPE ID` and `--ids FILE` (JSON lines); `pravo state identify`; the recipes folder for the thirty most common aws types.
- Prerequisites: D1, D2 and D13; structured `imported` marker on `ChangeView`; make `pravo import` share the engine's path (R4 g changes 2 and 3); fix `ARCHITECTURE.md:167` and `construct-matrix.md:48`.
- Docs and skill text; a runnable example with `random` or `local`.

### Phase 2. Plugin layer: list and identity

- Hand-written client stubs for protocol 5 and 6: `GetMetadata`, `GetResourceIdentitySchemas`, `UpgradeResourceIdentity`, `GenerateResourceConfig`, `ListResource` (streaming; the client has unary calls only), `ValidateListResourceConfig`; `ImportResourceState` and `ReadResource` with identity.
- The `Provider` interface, `v5.go`, `v6.go`, `Traced`, `Recorder` (stream records), test fakes.
- `schema.ProviderSchema` gets list and identity schemas; `CacheVersion` moves.
- Capability detection; `pravo shadow --coverage` and `pravo schema --list` work without a sweep. Tolerate `Unimplemented` everywhere.
- Identity-form `$import` if phase 0 supports it.

### Phase 3. Ledger and sweep

- `inventory/` store, sharded pages with a manifest for multi-part shards, an optional separate ledger backend, `Copy`, `Clear` decision, `Store.load` skips it.
- `pravo sweep` (list and `--from FILE`), `pravo shadow`, `$discover`, ignore rules, `known`, `--due`, `--plan`, exit codes.
- `adopt --from-ledger`, `pravo why TYPE:ID`.
- Record `identity` field and matching.

### Phase 4. Agent layer

- MCP tools, skill section, docs, example policies.

### Phase 5. Later, or risky

- Parent edges in `$discover`; region fan-out on one instance; provider `GenerateResourceConfig` as second opinion; shared ledger and cross-root registry; observe-only rights; twins report; settle without apply; tag rules.

### What is risky

- D1 (changes the plan/apply contract), D2(b) (new plan error, possible format bump) and D13 (a second lock kind).
- Canonical identity keys, which rest on list events nobody has seen.
- Serial batches: days of calendar time at a thousand objects.
- The overlay backend (assumed cheap).
- Reference recovery (heuristic by nature).
- Streaming list in both protocol adapters plus recorder.
- Every aws behaviour in the phase 0 list.
- Honest expectations: a first release lists a minority of types. It still onboards any type by id.

---

## 15. Evidence

Marks: V = verified in this work from the named source. U = unverified. A = assumption or inference. "Binary run" means a throwaway probe against the cached provider binary; "file read" means a file in the repository; "fetched" means a URL or GitHub API response.

### Protocol and providers

| # | Fact | Source | Mark |
|---|---|---|---|
| 1 | `ListResource` is a server-streaming RPC in the vendored protocol 5.11 and 6.11; request `type_name`, `config`, `include_resource_object`, `limit`; event `identity`, `display_name`, optional `resource_object`, diagnostics | R1 section 2.2; file read `internal/tfplugin/proto/tfplugin{5,6}.proto` | V |
| 2 | Protocol has no pagination, total, changed-since, tags, parent field or ordering guarantee | R1 section 2.2; proto read field by field | V |
| 3 | `ValidateListResourceConfig` on aws `aws_s3_bucket` with null config returned no diagnostics | R1 section 2.2; binary run | V |
| 4 | aws 6.69.1: 1,735 managed types, 253 list types, 551 identity schemas; all list types have an identity; list name equals managed type name | R1 sections 0, 2.1, 4.1; binary run | V |
| 5 | aws 6.69.1 speaks protocol 5 | R1 section 4.1; binary run | V |
| 6 | kubernetes 3.4.0: 81 types, 41 identity schemas, 0 list. local, null, random, tls, shell: none. aws 5.100.0: none, identity RPC Unimplemented. shell: `GetMetadata` Unimplemented | R1 section 4.1; binary run | V |
| 7 | aws list config: 233 types have `region`, 18 a `filter` block, 76 a required parent attribute; 19 have no `region` (10 with an empty config, 9 with another config) | R1 section 4.2; aws schema; the 19 recounted from aws-all-list.tsv in this revision | V |
| 8 | aws identity shapes, for example `aws_s3_bucket` v0 = `account_id` (optional), `bucket` (required), `region` (optional); `aws_iam_role` v0 = `account_id` (optional), `name` (required), empty list config; 208 of 253 list identities carry an optional `account_id`, 192 an optional `region` | R1 sections 2.3, 4.2; aws-all-list.tsv, counted again in this revision; binary run | V |
| 9 | `GenerateResourceConfig` on unconfigured aws with a synthetic bucket state dropped computed-only attributes and kept `tags_all` | R1 section 2.5; binary run, synthetic input | V (limited) |
| 10 | `ListResource` on an unconfigured aws provider ended with `Unavailable ... EOF` and the process exited; cause not known | R1 section 4.4; binary run | V (cause U) |
| 11 | No successful `ListResource` stream was observed anywhere; event contents, ordering, `display_name` quality, `limit` behaviour, latency | R1 sections 4.4, 8 | U |
| 12 | Identity-only `ImportResourceState`, `ReadResource` with identity, `UpgradeResourceIdentity` on aws | R1 section 4.4 | U |
| 13 | `ReadResource` needs a state-shaped object; identity is an optional extra | R1 section 2.4; proto read | V |
| 14 | Import id formats per type are not in the protocol; the Terraform docs show the S3 identity | R1 section 4.4 (docs fetched); the rest is inference | A |
| 15 | Protocol label "6.10" covers three different file contents; identity first at 5.9/6.9 (Terraform 1.12), list first present in the file at Terraform v1.13.0 | R1 section 6.1; fetched protos at tags | V |
| 16 | Terraform 1.14.0 (2025-11-19) shipped `list` blocks, `terraform query`, `GenerateResourceConfiguration`; 1.12.0 shipped `identity` in `import` blocks | R2 section (a); fetched changelogs | V |
| 17 | OpenTofu v1.13.2 vendors proto 6.10 with `ListResource`; its three Go files read implement identity and contain no list or generate-config code; rest of the repo not searched | R1 section 6.3; fetched | V (limited scope) |
| 18 | OpenTofu issue #3787 is open, labelled `pending-decision`; maintainers asked whether LLM agents were tried for import because object-by-object generation cannot see the architecture | R1 section 6.3; R2 section (b); fetched | V |
| 19 | OpenTofu first: identity in `import` blocks (v1.12 line). No `tofu query` found in v1.13.2, and issue #3787 open on 2026-10-20; not a claim about anywhere else | R2 section (b); fetched | V (limited scope) |
| 20 | terraform-plugin-sdk 2.38.0 answers ListResource with an error; SDKv2-only providers cannot list | R1 section 6.2; R2 section (c); fetched release notes | V |
| 21 | aws list: default result is identity plus display name; excludes non-manageable, special-case types, ASG instances and terminated instances; regional; filters per type | R2 section (c); aws provider docs fetched | V |
| 22 | Doc-page counts: aws 259/1,736, azurerm 122/1,106, google 159/1,179, awscc 1,270/1,698; none found for kubernetes, github, datadog, cloudflare, vault, helm | R2 section (c); GitHub tree API | V as proxy |
| 23 | Terraform `list` default `limit` 100; reaching it stops results; `-generate-config-out` forces full data | R2 section (a); docs fetched (summaries) | V |
| 24 | Third-party scanner (Unclick) reports SDKv2 zero values, per-type failures, no references in list results | R1 section 6.3; R2 section (c); one issue comment | U (one source) |
| 25 | Terraform config generation is "experimental", can fail on conflicting arguments; issue 35178 shows generation dropping a required nested id | R3 section 2.1; fetched | V |

### Other tools and clouds

| # | Fact | Source | Mark |
|---|---|---|---|
| 26 | terraformer archived 2026-03-16; driftctl in maintenance mode; Vanta's `.driftignore` content | R3 sections 2.3, 2.9; fetched | V |
| 27 | aztfexport: mapping file, `aztft` type resolution, listed limitations; Pulumi: protect by default, retain on delete, write-only values omitted; Crossplane: Observe policy | R3 sections 2.4, 2.8, 2.10; fetched | V |
| 28 | Cloud Control `ListResources` needs extra input for about 40 types; Resource Explorer, Asset Inventory and Resource Graph are eventually consistent or throttled | R3 section 2.12; fetched | V |
| 29 | env0, Firefly, ControlMonkey claims | R3 section 2.13; vendor pages | U (marketing) |
| 30 | Eventual consistency of provider list results | R2 section (d) | U |

### pravo internals

| # | Fact | Source | Mark |
|---|---|---|---|
| 31 | pravo's gRPC client has 17 unary RPCs; no list, identity, metadata, generate-config, streaming; `Provider` interface and `schema.ProviderSchema` have none; `GetSchema` drops `list_resource_schemas` | R1 section 3; R4 section 0 item 2; code read | V |
| 32 | `ImportResourceState` is called with type and id only; identity unused; `Deferred` ignored | R4 section (a); code read | V |
| 33 | A record absent from the specification is planned `delete` (`dice will be destroyed`); `destroy` deletes every record | R4 section 0 item 3; probe | V |
| 34 | Ten code sites walk all records (planOrphans, relabelProviders, DriftDue, newSession, viewer, policy view, and others) | R4 section (b) table; code read | V |
| 35 | Plan-time `$import` writes the record before review | R4 section 0 item 4; code and docs | V |
| 36 | `Store.load` reads every record at every open (8 wide local, 64 wide S3) | R4 section 0 item 5; code read | V |
| 37 | An unknown `inventory/` directory is ignored by `plan` and `state list`; `state.Copy` copies only four names, so `pull` drops it | R4 section 0 item 6; probe | V |
| 38 | An adopted record has no `dependencies` or `config_hash` until an apply | R4 section 0 item 7; probe | V |
| 39 | No CUE-from-record renderer fit for specifications; `RenderCUE` emits non-CUE markers; `jsonToCUE` does no schema filtering; `#SecretEnv` is not allowed in ordinary sensitive attributes | R4 section 0 item 8; code and docs read | V |
| 40 | `ChangeView` has no structured import marker | R4 section 0 item 9; code read | V |
| 41 | `drift --due --limit N` pattern; exit code 2 for drift | R4 section (c); code read | V |
| 42 | `pravo mcp` has 13 tools, none edits state or applies without a saved plan | R4 section (e); docs and code read | V |
| 43 | `Format = 1` in code; `ARCHITECTURE.md:167` says 2 or 3 | R4 section (b); code and doc read | V (docs inconsistent) |
| 44 | `pravo import` differs from the engine path (no type match, no `ProviderConfig` stamp, no lock) | R4 section (a); code read | V |
| 45 | Omitted optional-and-computed attributes inherit the prior value (`ProposedNew`) | R4 section (d); code and ARCHITECTURE read | V |
| 46 | `pravo.state` records carry no identity; an additive `identity` field survives older binaries | R4 item 1; survival | first V, second U (a phase 0 check that decides D2(b) and D5) |
| 47 | `$import` takes the provider's id as a string; `$lifecycle` has `prevent_destroy` (replace is an error, `destroy` refuses while in state) and `ignore_changes` (names, or `["*"]`); exit codes 0 to 6 are in use | file read: `docs/reference/spec-moves-and-imports.md`, `spec-meta-fields.md`, `exit-codes.md` (checked by the lead) | V |
| 48 | An overlay `Backend` is cheap; `$discover` placement; subdirectory CUE packages; non-concrete required fields rejected by `validate`; `ignore_changes: "*"` over nested blocks; `state rm` warning on a remaining declaration | proposals; not run | U |
| 49 | `pravo state rm` exists: "Forget a resource without destroying it" | `pravo state --help`, run in this revision | V |
| 50 | `docs/reference/state-format.md` defines a record mark `gone` ("the read that left the mark did not find the object"); a store with a higher format is refused | file read, lines 118 to 131 and 186, in this revision | V |
| 51 | Exit codes 3 (stale) and 5 (refused: a plan policy) exist; 6 is invalid; 7 and 8 are free | `docs/reference/exit-codes.md`, read in this revision | V |
| 52 | The installed aws provider prints `bucket`, `region`, `object_lock_enabled` and `acl` of `aws_s3_bucket` without `?` (optional and computed) | `pravo schema hashicorp/aws/aws_s3_bucket`, run by the critic of this design; not re-run here | V (second hand) |
| 53 | A provider may end a stream with OK after an error diagnostic; aws refuses customer roles named `AWSServiceRole*`; controller marker tag names; an IAM prefix condition for the ledger; recipes for thirty aws types exist or work; the older binary drops or keeps record fields | nobody checked | U |

---

## 16. What the review changed

An adversarial review of the first version found 3 blockers, 11 majors and 6 minors (numbered 1 to 20 in the review). Every blocker and major is accepted in substance and changed in the design or stated as a limit in sections 12 and 13. Where the fix differs from what the review proposed, or a limit remains, it is said here.

### Accepted

| # | Finding | What changed | Where |
|---|---|---|---|
| 1 | Generator omitted optional-and-computed attributes, so the empty plan could pass for nothing | `--spec full` is the default and writes them; `--spec minimal` is labelled "tracked, not specified"; the gate prints `specified N, inherited M` and warns when M exceeds N; the success test now says an empty plan proves "not contradicted", not "described"; the example is rewritten | 1, 2, 7, 8, 12 |
| 2 | D1 and D2 break pravo's own invariants; protection could be lost to an older binary | D2(b) is stated as a plan error for a protected undeclared record, with the allow-delete decision in the saved plan only; D1(a) re-reads at apply and fails with the stale code (3); the older-binary question and the format bump are tied together in D2 and D5; phase 1 is relabelled "core changes first" | 1, 13, 14 |
| 3 | Two addresses or roots could adopt one object | A plan-time check across `$import` entries and a commit-time identity lock (new D13); cross-root detection stays missing and is said so | 7, 10, 12, 13 |
| 4 | Read-only sweep credentials conflict with the record's provider instance | A scope names a `sweep` and a `manage` instance; `account` is a property of the scope; adopt hydrates through `manage` | 5, 6, 10 |
| 5 | The ledger needs state-write power | `$discover.ledger` names a separate backend or prefix; the sweep job needs only the ledger credential; the IAM text is left unwritten as untested | 5, 9, 10 |
| 6 | Doors 2 and 3 are not discovery | Renamed: one discovery door, two adoption inputs; discovery for no-list types is out of scope; a recipes folder for the top thirty aws types; identity-schema and import-id hints; Kubernetes is the file door only | 1, 6, 8, 9, 12 |
| 7 | Identity keys not canonical | Key is account label, type and required identity attributes only; cross-input merge; account label in the scope; to be tested on a real stream in phase 0 | 5, 14 |
| 8 | Matching rests on an unverified fallback, with no backfill | `pravo state identify`; a type with any unmatched record is never listed as `seen` | 5, 8 |
| 9 | Objects owned by another controller are not detected | A controller-marker check at adopt time, blocking without `--allow-owned`; `known` entries carry `as_of` and may read a remote backend; the marker names are unverified (D14) | 4, 5, 7, 12 |
| 10 | Ignore globs can hide attacker-made objects | Exact identity is the default; a glob needs `glob: true` and `expect: N`; overflow shows as `seen (rule overflow)`; the rule review lists what it hides | 5, 9 |
| 11 | The agent sees secrets; control characters reach terminals and TSV | `--redact` is the MCP default and values need an operator opt-in; a secret detector in the generator; control characters escaped at ledger write; JSON lines replace TSV; section 9 now says what is true | 5, 7, 9, 10 |
| 12 | A thousand objects: ordering, relinking, resume, call counts | `adopt --order`, `--relink`, `--parallel`, `--resume`; calls restated as about 8 to 12 per object, unmeasured; serial batches stated as days of calendar time | 7, 8, 11, 12 |
| 13 | "Complete shard" ignores error diagnostics; merge can overwrite a complete shard | Complete needs OK, no error diagnostic and a count below the limit; complete beats incomplete; entries are those of the last complete sweep; multi-part shards use a manifest | 4, 5, 6 |
| 14 | Gate versus saved plan | The gate is advisory and the saved plan authoritative; an `adoption-only` policy turns any difference into a refusal | 7 |
| 15 | `gone` collides with an existing record mark; `release` duplicates `state rm` | Ledger state renamed `unlisted`; `release` removed, `state rm` used | 3, 4, 7, 8 |
| 16 | "Read-only" stronger than the protocol | Stated as a provider convention; the read-only role is the enforcement | 4, 6, 10 |
| 17 | Global type count and instance counts | 19 global types (10 plus 9, recounted from the tsv); global shards keyed by account; `--instances N` | 5, 6, 15 |
| 18 | Smaller data-model gaps | Replaced-object hint; cross-account shared resources named as unhandled; pruned-entry count kept in the header; shards keyed by account and region, not instance name | 4, 5 |
| 19 | OpenTofu and Terraform claims too broad | Narrowed to "no `tofu query` found in v1.13.2, issue #3787 open on 2026-10-20" | 12, 15 |
| 20 | Phase estimates are guesses | Durations removed; phases are an ordering | 14 |

### Changed or limited, not taken as proposed

- **Finding 2, fix 3 (bump to 2).** Offered as the safe option, not decided: it locks older binaries out of the whole store, so it stays the user's call in D5, after the phase 0 check.
- **Finding 3, fix 1 (identity lock).** Taken, but it does not close the case of one object reached once by identity and once by a differently formatted id string, and it does nothing across roots. Both limits are written down.
- **Finding 5 (IAM condition).** The separate ledger credential is taken. The IAM policy text is not written, because nobody tested it, and the local store cannot be separated at all.
- **Finding 6, fix 2 (recipes "tested in docs").** Taken as shipped data, but pravo's documentation build has no cloud account, so recipes can be tested only against a sandbox account. Nobody has written one; the recipes are unverified until phase 0.
- **Finding 8 ("make `drift` write identity").** Rejected: drift's contract is to find differences and leave marks. A separate `state identify` does the backfill. It is a state write, so it carries the same older-binary question.
- **Finding 12 (serial batches).** Described, not fixed. A gate over the union of pending batches might shorten it; unverified, a phase 0 question.
- **Finding 12 (`adopt --parallel`).** Taken with default 1 until timing is measured.
- **Finding 14 (print a plan fingerprint).** Replaced by the `adoption-only` policy, which needs no new fingerprint format.
- **Finding 15 (`release` as an alias).** Rejected in favour of removing `release`: two commands for one act invite drift. `state rm` should gain a warning when the declaration remains; whether it already has one is unverified.
- **Finding 1, fix 2 (deny list).** Taken as per-type data. `arn` and `id` are computed-only and omitted anyway, so the list holds only optional-and-computed attributes such as aws `tags_all`.

### Still not solved

Discovery for types that cannot list (section 12 item 12), cross-root double adoption, canonical keys before a real list event is seen, the calendar cost of serial batches, and every behaviour in the phase 0 list. The design does not claim otherwise.
