# Policies as approvers

Status: proposal. Nothing here exists. The current behaviour is in
[Policies](../docs/concepts/policies.md), [What sealing protects](../docs/concepts/sealed-state.md)
and [Signatures](../docs/reference/protect.md#signatures).

## The problem

A plan policy is a file that the invoker names with `--policy`. Three things follow from that.

1. **Omission.** A run that leaves the flag out is unchecked. The job definition, the laptop and the
   agent shell all decide for themselves.
2. **No identity.** A policy has a digest and nothing else. Nothing says who put it in force, who
   changed it, or which version judged which plan.
3. **No proof.** `Policy X is satisfied` is a line on a terminal. An apply carries no evidence that
   a policy judged its plan, and a reviewer of the log cannot tell.

The state already has the pieces for a better answer: identities with ed25519 signing keys, a readers
file in the repository that a bucket writer cannot change, signed transactions, signed stored plans
(`plans/<id>.sig-<key>.json`), domain-separated signatures bound to the lineage, create-only writes,
and a format bump that makes older binaries refuse the state. What is missing is the thing the docs
call roles and a ledger.

## The idea

A policy is an **object in the state**, installed and removed by an authorised key. While it is
installed it is **required**: `apply` and `destroy` refuse a plan unless it carries a valid
**approval** from that policy. An approval is a signed statement:

> policy `no-delete` (digest `D`, installed at ledger entry `N`) judged plan `P` of lineage `L` and
> found it satisfied. Attested by key `K`, evaluator `pravo X / cue Y`, at time `T`.

Removing the policy appends a signed removal to the state. From then on it is not required. Nothing
about the invocation decides whether a policy applies; the state does.

Your sentence "the policy signs" has two readings, and the choice is the main decision in this note.

| | A. Evaluator signs for the policy (recommended) | B. The policy owns a key |
|---|---|---|
| Who signs | A key from the policy's approver set: a CI gate, a person, or whoever applies. | A key generated at install time, locked to the evaluators with HPKE as data keys are. |
| Verifier trusts | The ledger entry that lists the approver keys. | The policy public key in the ledger entry. |
| Attribution | Says who evaluated. | Says only that someone in the set did. |
| Revocation | Remove one key from the set. | Replace the policy key and re-lock it for the survivors. |
| Self mode (applier evaluates) | Honest: the applier signs for itself. | Meaningless: every applier holds the key. |

A reads as "the policy approved it, and here is who ran it". B is closer to the wording but gives up
attribution and adds key distribution. The rest of this note assumes A.

## What is installed, and where the trust comes from

**Ledger.** An append-only chain of signed entries under `policy/ledger/<seq>.json`, created with
`PutIfAbsent`, so two racing writers cannot both take a sequence number.

```
{ v, lineage, seq, prev,            # prev = sha256 of the previous entry
  op: install | replace | remove | set-approvers,
  policy: { id, digest },           # digest of the CUE source
  approvers: { keys: [fingerprint…], quorum: k },   # or { self: true }
  expires?: duration,               # how long an approval of this policy stays valid
  at, signature }                   # domain pravo/v1/policy-entry
```

The source of a policy is stored content-addressed as `policy/src/<sha256>`, sealed to the readers like
any other value. The ledger carries only the digest.

**Head.** The head is the highest `seq` whose chain verifies from the genesis entry. The **required
set** is the policies that are installed at the head and not removed.

**Trust root.** The repository, as with the readers file: `.pravo/access/policy-admins.txt` lists
the names (from `readers.txt`) whose keys may sign ledger entries. A bucket writer cannot add
themselves. A change to the file is a reviewed commit. The ledger is not trusted for who may change it,
only for what was changed.

**Expectation.** If the repository has a `policy-admins.txt` and the state has no ledger, a command
that writes stops with exit code 5, as it does today for a repository that lists readers and a state
that is not sealed. Deleting the ledger does not switch policies off.

**Requires a sealed state.** Signatures need an identity, and the repository anchor comes with
sealing. A policy ledger on an unsealed state is not offered. Turning it on bumps the format, so a
binary that does not know about policies refuses the state.

## Approvals

An approval is an object `approvals/<plan-id>/<policy-id>.<key>.json`, created and never replaced, in
the way of the plan signatures. It holds the signature and a few clear fields, and no digest of the
plan: a reader recomputes the digest from the plan, so the object cannot confirm a guess.

Domain `pravo/v1/approval`, signed over: lineage, plan digest, policy id, policy digest, ledger seq
of the entry that installed that digest, evaluator (`pravo` and CUE versions), time, expiry.

An `apply` verifies, for **each policy in the required set at the head**:

1. an approval exists whose policy digest equals the digest at the head, so replacing a policy
   invalidates the approvals of the old text and installing one demands new approvals;
2. its key is in the approver set at the head, and the quorum is met;
3. the signature verifies over the plan this apply will run, and the approval has not expired;
4. the ledger chain from genesis to the head verifies and is signed by names in
   `policy-admins.txt`.

Any miss is exit 5 and names the policy and what is missing. A broken chain or a bad signature is
exit 7, as an untrusted record is today.

Nothing is new cryptographically. The signing, the framing, the domain tags, the lineage binding and
the ssh-agent path already exist in `internal/protect/signing.go`. The additions are three domains,
two object kinds and a verifier.

### Who evaluates

A CUE policy is deterministic over the plan document, so its approval can be **re-checked by anyone
who holds the plan**: re-run the evaluator and compare. The signature adds who vouches, not new
truth. That gives three kinds of approver under one verifier:

- **Self.** `approvers: self`. The applier evaluates and signs. The policy is mandatory and the
  transaction proves which version judged the plan. It is not separation of duties: the applier
  holds the key. This is the first step and the default.
- **Gate.** `approvers: keys`. A CI job or a service with its own identity runs `pravo approve
  PLAN`, evaluates the policies it is listed for and writes approvals. The apply job holds cloud
  credentials and no approver key. A gate has to be a reader, because it opens a sealed plan.
- **Quorum.** `quorum: 2` of a named set, for a policy no machine can judge (a change board). The
  same object, signed by people, each with a touch on the ssh-agent if they use `--unlock agent`.

A later step lets a CUE policy answer with a requirement ("deletes need two humans") instead of pass
or fail, so a machine policy can escalate to a quorum.

### Binding the plan

`PlanDigest` is the SHA-256 of the plan file, which already pins the lineage, the configuration
fingerprint, provider versions and the revision of every record the plan reads or changes. A policy
is judged on the plan document, which also holds `blast_radius` and `dependents` read from the state
"at that moment". For an approval to be reproducible from the plan file alone, those values should
be written into the plan file when it is made. This is a small change to the plan format and I
recommend doing it with the first step.

### The apply transaction

The signed start of an apply gains the ledger head and the digest of the approvals it relied on, under
a new domain version (`pravo/v2/txn-start`) so old transactions still verify. The log then says: this
apply ran under head `H`, with these approvals.

## What each right becomes

The four rights in [the trust anchor](../docs/concepts/policies.md#the-trust-anchor):

| Right | Today | With the ledger |
|---|---|---|
| 1. Write the policy source | Whoever can edit the file decides. | Only a key in `policy-admins.txt` can add, replace or remove, and every change is a signed, ordered entry that `pravo policy log` shows. The file in git stays the place to review; merging it does not install it. |
| 2. Write the job that spells `--policy` | Drop the flag and nothing is checked. | There is no flag to drop. The required set comes from the state. `--policy` stays for rehearsal and can only add a rule, never remove one. |
| 3. Write the state store | Rewrite anything. | Cannot forge an entry or an approval, because there is no key. Can still delete objects or restore an older prefix of the ledger. Deletion is caught by the repository expectation. Rollback is caught only with a floor: see below. |
| 4. Hold the cloud credentials | Change infrastructure without pravo. | Unchanged. pravo verifies the approvals, and a holder of the credentials runs another tool or a patched binary. |

Only the last row is a limit of the design and not a gap in it. Signed approvals make a policy
**mandatory, separable and provable**. They do not make it **unbypassable**, because the party that
checks is the party that holds the credentials. Closing right 4 means the approval has to gate the
credentials themselves: a gate that mints short-lived cloud credentials only for a plan whose
approvals verify, so the apply job holds nothing that works without them. That is outside pravo and
is the natural last step. It is the one place where "cryptographic" turns into "enforced".

### Rollback

The existing docs say it plainly: an old signed record is as valid as a new one, and finding a
rollback takes a ledger. This is that ledger for policies, and it detects a rollback only if the
verifier knows a sequence number it has seen. Three options, cheapest first:

1. **A floor in the repository.** `.pravo/access/policy-floor` holds a `seq` that a writer must not
   see the head below. It moves when someone commits it. Cheap, needs a commit per policy change if
   you want it tight.
2. **A local high-water mark** in the identity directory of each runner. Free and weak for a fresh
   CI runner.
3. **Freshness.** A gate re-signs `ledger head is H as of T` every few hours, and `apply` refuses a
   head older than a configured age. This is the timestamp role of TUF.

Start with the floor, optional. State the residual risk in the docs.

## Edits with no plan

`state rm`, `state mv` and `import` change what pravo believes without a plan, so no policy sees them.
With policies as state objects this can close: such a command builds a synthetic change document
(`kind: state-edit`), and a policy may declare `applies_to: [plan, state-edit]`. A bucket writer is
still outside it. Later step.

## Interface sketch

```
pravo policy init                 # writes the genesis entry; format bump; names the admins
pravo policy install FILE --id no-delete --approvers self
pravo policy install FILE --id cost-limit --approvers gate-ci --expires 24h
pravo policy remove no-delete
pravo policy list                 # id, digest, approvers, since seq, signed by
pravo policy log                  # the ledger, verified, like state log
pravo policy test FILE --plan ID  # rehearse without installing

pravo plan --store                # judges the self policies, writes their approvals
pravo approve plan-2f9c1a4e       # a gate or a person signs
pravo apply  plan-2f9c1a4e        # refuses: needs cost-limit (gate-ci)
pravo protect status              # Policies  2 required · ledger seq 14 verified
```

`pravo plan show` lists approvals beside signatures. `pravo serve` shows the ledger and the
approvals read-only, as it does for readers.

## Steps

1. **Required set, self mode.** `policy init/install/remove/list/log`, the ledger, the repository
   expectation, enforcement in `apply` and `destroy`, the ledger head and the approvals in the
   transaction. Fixes omission and gives policy changes an author. No new role beyond the admins.
2. **Gate mode.** `pravo approve`, approval objects, display in `plan show`, the plan file carries what
   the policy judged.
3. **Quorum, expiry, floor.** Human approvers, the optional floor, `state-edit` policies.
4. **Credentials.** An external gate that issues credentials against verified approvals.

## Open questions

- A or B: does the signature name the person or gate that evaluated, or only the policy?
- Is a self-signed approval acceptable as the default, knowing it is accountable and not separate?
- Is `policy-admins.txt` in the repository the right root, or should admins be a quorum recorded
  only in the state? The repository is the only anchor a bucket writer cannot move.
- Should a policy source be sealed? It is code and is not secret, but nothing in the bucket
  today is readable by a non-reader except addresses.
- A plan file made with `--out` has no id. Do approvals need a random `plan_id` in the file
  (format 2), or do required policies force stored plans?
