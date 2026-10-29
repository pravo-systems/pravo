<!-- Generated from docs/reference/promote.md by tools/skillgen. Do not edit. -->

# pravo promote

Vouch that a state runs a specification, and list the attestations a state holds. Subcommands, flags, what an attestation names, what the policy of the next state checks, and the exit codes.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo promote attest [--note TEXT] [--json]`, `pravo promote list [--all] [--json]` |
| Changes infrastructure | No. |
| Reads | The state, and the specification, which `attest` plans against it. |
| Writes | `attest` writes one object, `promotions/FINGERPRINT.KEY.json`, in the state. `list` writes nothing. |
| Prints | The fingerprint that was vouched for, and the lines that the policy of the next state pins. |
| Taught in | [Promote a specification between environments](../how-to/promote-between-environments.md). |

A specification passes through several states in order, and each state takes it only after the one before ran it. The state before holds an attestation: a signed object that names the specification by its fingerprint, and the policy of the state after reads it. `pravo promote` writes and lists attestations. The check is a rule with [`promote`](policies-operations.md#ask-for-a-promotion), and runs at every `pravo apply` and `pravo plan` of the later state.

Both subcommands take `-t`, `-T` and `--access-dir` as [`pravo secret`](secret.md) does. The tags select the state, and they are part of what is vouched for, so the command has the tags that the apply had.

## What an attestation holds

| Field | Meaning |
|---|---|
| `kind` | What is vouched for. `config-fingerprint/v1`: the fingerprint of the root, which hashes the names and the bytes of its files and `cue.mod/module.cue`, and not the tags. |
| `fingerprint` | The fingerprint of the specification. |
| `tags` | A digest of each tag that the specification was applied with. A digest, not the value, so a tag that holds a secret is not copied into the state. |
| `txn`, `applied` | The newest transaction of the state that had finished when the key signed, and when. They place the attestation in the history of the state. |
| `at` | When the key signed. `min_age` and `max_age` count from it. |
| `note` | A line of text from `--note`. |
| `signer_key`, `signature` | The public key, and the signature in the domain `pravo/v1/promotion` with the lineage of the state it lies in, so that it verifies nowhere else. |

The transactions of a state record the fingerprint and the tag digests of each apply, signed with the rest of the transaction. An attestation is made only for a specification that the newest of them ran.

## pravo promote attest

`pravo promote attest [--note TEXT]` plans the specification against the state, as `pravo plan --refresh` does, and signs an attestation when the plan changes nothing. A plan that adds, changes or destroys anything is refused with exit code 5: apply first, with the same tags, and then vouch. The state must be sealed, because an attestation is a signature, and the key must be a writer's, where the readers file gives roles. An attestation by the same key of the same specification is replaced, which moves the time that `min_age` counts from.

The command prints the fingerprint, and the two lines that the policy of the next state takes: `signers`, the fingerprint of the key, and `lineage`, the lineage of this state.

| Flag | Default | Meaning |
|---|---|---|
| `--note TEXT` | none | Kept in the attestation, and shown by `--json`. |
| `--json` | off | Prints the attestation as a document. |

## pravo promote list

`pravo promote list` prints the attestations of the specification, newest first, with who signed, when, the transaction, and whether the attestation is the one that matches the specification now (`current`). `--all` lists every attestation in the state. A row says `verified`, `key not in the file` when the key is not in the readers file, or `invalid` when the signature does not verify. `--json` prints the rows with the fields `fingerprint`, `txn`, `applied`, `at`, `signer`, `key`, `status`, `current` and `note`.

## What the next state checks

The rule `promote` of a [policy](policies-operations.md#ask-for-a-promotion) opens the state in `from`, reads the attestations of the specification of the plan, and counts one when all of these hold:

1. The signature verifies against the lineage of that state, and the key is among `signers`. A key that the state lists but the policy does not pin does not count, because the state that holds an attestation is not the one that decides.
2. When the rule names a `lineage`, the state in `from` has it.
3. Every tag that is not in `vary` has the digest that the attestation holds.
4. The attestation is at least `min_age` old and at most `max_age`.

An unmet rule refuses the apply with exit code 5 and says which of the four failed. `pravo plan` prints the same as a `Promotion:` line without refusing. An admin is not exempt.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Attested, or listed. |
| 5 | `attest`: the state does not run the specification yet. `apply`: the policy asks for an attestation that is not there. |
| 7 | The state is sealed and the key is not one of its readers, or a record fails its signature. |

[Exit codes](exit-codes.md) has the rest.
