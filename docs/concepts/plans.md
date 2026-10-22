---
title: Plans
description: "What a plan contains, how pravo decides which resources a plan reaches, what an unknown value is, how refresh age works, how a saved plan is checked and how a plan lives in the state."
order: 4
minutes: 21
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+'
    replace: 'txn <id>'
  - pattern: 'created [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]+'
    replace: 'created <time>'
  - pattern: '\(hash [0-9a-f]+ -> [0-9a-f]+\)'
    replace: '(hash <old> -> <new>)'
  - pattern: '"[0-9]{12,}"'
    replace: '"<id>"'
  - pattern: 'the record is [0-9]+[smhd] old'
    replace: 'the record is <age> old'
  - pattern: '\[id=[0-9a-f]{40}\]'
    replace: '[id=<sha1>]'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
keywords: "-target scoped plan targeting unknown value refresh age stored plan plan id plan list plan gc"
---

A plan is the list of actions that make the real infrastructure match the specification. pravo computes it from the evaluated specification, the records in the state and the answers of the providers.

A plan changes nothing in the infrastructure. It can write to the state: refreshed records, moved records and imported records. Everything else stays as it was.

This page uses seven small roots: `zones`, `deferred`, `ages`, `reads`, `target`, `feed` and `store`. The global flag `-C DIR` runs pravo in one of them.

## What a plan contains

Each part of a printed plan answers one question.

| Part | Meaning |
|---|---|
| `# ADDRESS (TYPE) will be created` | The header of a change: the address of the resource, its provider type and the action in words. |
| `+`, `~`, `-/+`, `-`, `<=` | The action as a glyph. |
| Attribute lines | The planned values. `OLD -> NEW` is a change, `(known after apply)` is an unknown value, `(sensitive)` is a hidden one. |
| `# forces replacement` | The attribute that the provider cannot change in place. |
| `Plan: N to add, N to change, N to destroy.` | The counts. A replacement counts as one add and one destroy. |
| `Hint:` | A create and a delete that look like one object under two addresses. |
| `Graph:` | How much of the graph the plan walked. |
| `Outputs:` | The outputs, with `(known after apply)` for the ones that are not known yet. |

The action of a change is one of these.

| Action | Glyph | When |
|---|---|---|
| create | `+` | The address has no record. |
| update | `~` | The planned state differs from the record, and the provider changes it in place. |
| replace | `-/+` | The provider reports an attribute that cannot change in place, or `-replace` names the resource. |
| delete | `-` | A record has no address in the specification, or the plan is `-destroy`. |
| read | `<=` | A data source whose read waits for the apply. |
| no-op | none | The planned state equals the record. The plan does not print it. |
| move | none | A `$from` renamed a record. The plan prints `# OLD was moved to NEW`. |

Work in an empty directory. The first root has two resources: a number that the provider draws, and a resource that records it.

```cue file=zones/providers.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	nul "pravo.dev/providers/hashicorp/null:null_"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$null:   nul.#Provider
```

```cue file=zones/zones.cue
package estate

zones: ["a"]

ports: {
	for z in zones {
		"\(z)": $random.#Resource.random_integer & {min: 20000, max: 29999, seed: z}
	}
}

deploy: {
	for z in zones {
		"\(z)": $null.#Resource.null_resource & {
			triggers: {
				zone: z
				port: "\(ports[z].result)"
			}
		}
	}
}
```

```sh run
pravo -C zones init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo -C zones plan
```

```text out
pravo will perform the following actions:

  # ports.a (random_integer) will be created
  + ports: a: {
  +     id:     (known after apply)
  +     max:    29999
  +     min:    20000
  +     result: (known after apply)
  +     seed:   "a"
    }

  # deploy.a (null_resource) will be created
  + deploy: a: {
  +     id:       (known after apply)
  +     triggers: {
  +         port: (known after apply)
  +         zone: "a"
        }
    }

Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.
```

`deploy.a` reads `ports.a`, so `port` is unknown until `ports.a` exists. Attribute values print in CUE syntax and are nested by address.

### The plan document

`pravo plan --json` prints the same plan as one JSON document. A policy is judged against this document. It has these top-level fields.

| Field | Content |
|---|---|
| `summary` | `add`, `change`, `destroy`, `read` and `blast_radius`. |
| `stats` | The numbers behind the `Graph:` line. |
| `changes` | One entry for each node the plan ran. `--include-skipped` adds an entry for each node it left alone. |
| `outputs` | The planned outputs. |
| `hints`, `providers` | The hints, and the provider version of each provider. |
| `format`, `created`, `actor`, `reason`, `git_revision`, `trace_id`, `destroy` | The context of the run. |

A change always has `addr`, `mode`, `type`, `name` and `action`. When they apply, it also has `provider`, `reasons`, `requires_replace`, `before`, `after`, `deferred`, `skipped`, `refreshed`, `drifted`, `applied`, `dependencies`, `dependents` and `warnings`. A value that is not known until apply is `{"$unknown": true}`.

The plan ran the nodes it changes, deletes, moves or imports, the ephemeral resources it opens and the functions it calls. A node it left alone has no entry. `pravo plan --json --include-skipped` lists it too, as an entry with `addr`, `mode`, `type`, `name`, the action `no-op` and `skipped`. A policy is judged against a document that has an entry for every node of the specification, in the order of the specification, with these fields for the nodes the plan left alone.

```sh run
pravo -C zones plan --json | jq '.changes[1]'
```

```text out
{
  "addr": "deploy.a",
  "mode": "managed",
  "type": "null_resource",
  "name": "a",
  "action": "create",
  "provider": "null",
  "reasons": [
    "not in the state: a new resource"
  ],
  "after": {
    "id": {
      "$unknown": true
    },
    "triggers": {
      "port": {
        "$unknown": true
      },
      "zone": "a"
    }
  },
  "dependencies": [
    "ports.a"
  ]
}
```

The document is not masked. A sensitive attribute appears in it as it is. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says what that means.

```sh run
pravo -C zones apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## Unknown values

pravo converts the body of a resource into a value that the provider understands before it asks the provider anything. It classifies each attribute of the body.

:::figure The classification of one attribute. The questions are asked in order.
<svg viewBox="0 0 780 352" role="img" aria-label="A decision list for one attribute of a resource body, asked in order. If the field is absent, the provider receives null. If the value is concrete, it receives the value. If it is not concrete and refers to a resource, a data source or a call, it receives unknown. If it is incomplete for any other reason, pravo reports an error. Otherwise the provider receives null and chooses the value." style="min-width:680px">
<rect x="0" y="8" width="392" height="44" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="12" y="35" style="fill:var(--text);font-size:12.5px">is the field absent?</text>
<line x1="392" y1="30.0" x2="464" y2="30.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="464,30.0 458,26.4 458,33.6" style="fill:var(--text-3)"/>
<text x="426" y="22.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">yes</text>
<rect x="468" y="8" width="116" height="44" rx="2" style="fill:var(--surface-2);stroke:var(--text-3);stroke-width:1.5"/>
<text x="526" y="35" text-anchor="middle" style="fill:var(--text-2);font-size:13px;font-weight:600">null</text>
<text x="598" y="27" style="fill:var(--text-2);font-size:11.5px">an optional attribute</text>
<text x="598" y="42" style="fill:var(--text-2);font-size:11.5px">that is not set</text>
<line x1="40" y1="52" x2="40" y2="80" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="40,80 36.4,74 43.6,74" style="fill:var(--text-3)"/>
<text x="52" y="69" style="fill:var(--text-3);font-size:10.5px">no</text>
<rect x="0" y="80" width="392" height="44" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="12" y="107" style="fill:var(--text);font-size:12.5px">is the value concrete after defaults?</text>
<line x1="392" y1="102.0" x2="464" y2="102.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="464,102.0 458,98.4 458,105.6" style="fill:var(--text-3)"/>
<text x="426" y="94.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">yes</text>
<rect x="468" y="80" width="116" height="44" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.5"/>
<text x="526" y="107" text-anchor="middle" style="fill:var(--accent);font-size:13px;font-weight:600">the value</text>
<text x="598" y="99" style="fill:var(--text-2);font-size:11.5px">sent as written</text>
<line x1="40" y1="124" x2="40" y2="152" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="40,152 36.4,146 43.6,146" style="fill:var(--text-3)"/>
<text x="52" y="141" style="fill:var(--text-3);font-size:10.5px">no</text>
<rect x="0" y="152" width="392" height="44" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="12" y="179" style="fill:var(--text);font-size:12.5px">does it refer to a resource, data source or call?</text>
<line x1="392" y1="174.0" x2="464" y2="174.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="464,174.0 458,170.4 458,177.6" style="fill:var(--text-3)"/>
<text x="426" y="166.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">yes</text>
<rect x="468" y="152" width="116" height="44" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.5"/>
<text x="526" y="179" text-anchor="middle" style="fill:var(--change);font-size:13px;font-weight:600">unknown</text>
<text x="598" y="171" style="fill:var(--text-2);font-size:11.5px">shown as</text>
<text x="598" y="186" style="fill:var(--text-2);font-size:11.5px">(known after apply)</text>
<line x1="40" y1="196" x2="40" y2="224" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="40,224 36.4,218 43.6,218" style="fill:var(--text-3)"/>
<text x="52" y="213" style="fill:var(--text-3);font-size:10.5px">no</text>
<rect x="0" y="224" width="392" height="44" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="12" y="251" style="fill:var(--text);font-size:12.5px">is it incomplete for another reason?</text>
<line x1="392" y1="246.0" x2="464" y2="246.0" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="464,246.0 458,242.4 458,249.6" style="fill:var(--text-3)"/>
<text x="426" y="238.0" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">yes</text>
<rect x="468" y="224" width="116" height="44" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="526" y="251" text-anchor="middle" style="fill:var(--conflict);font-size:13px;font-weight:600">error</text>
<text x="598" y="243" style="fill:var(--text-2);font-size:11.5px">for example a tag</text>
<text x="598" y="258" style="fill:var(--text-2);font-size:11.5px">with no value</text>
<text x="52" y="285" style="fill:var(--text-3);font-size:10.5px">no</text>
<line x1="40" y1="268" x2="40" y2="290" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="40,290 36.4,284 43.6,284" style="fill:var(--text-3)"/>
<rect x="0" y="292" width="116" height="44" rx="2" style="fill:var(--surface-2);stroke:var(--text-3);stroke-width:1.2"/>
<text x="58" y="319" text-anchor="middle" style="fill:var(--text-2);font-size:13px;font-weight:600">null</text>
<text x="132" y="314" style="fill:var(--text-2);font-size:11.5px">the provider picks</text>
<text x="132" y="329" style="fill:var(--text-2);font-size:11.5px">the value</text>
</svg>
:::

An attribute that refers to a resource, a data source or a call is unknown, and so is an incomplete expression over one, such as `strings.ToUpper(a.id)`. An attribute that is not concrete and refers to nothing else is null, and the provider picks the value. An attribute that is not concrete for any other reason is an error, because no provider can resolve it. A tag without a value is the usual cause.

A reference is found structurally. It counts inside an interpolation, a function call, an expression and a list. `"\(ports[z].result)"` is a reference to `ports.a`, so `port` was unknown in the plan above.

```cue file=zones/tagged.cue status=rejected
package estate

env: string @tag(env)

tagged: $null.#Resource.null_resource & {
	triggers: stage: "x-\(env)"
}
```

```sh run exit=1
pravo -C zones plan
```

```text out
Error: tagged.triggers["stage"]: value depends on env, which is not concrete (is a -t tag missing?)
```

```sh run
rm zones/tagged.cue
```

When pravo plans or applies a resource, it fills the known values back into the value at the address of the resource. Only where the specification was not concrete does the planned value replace it. Every expression that reads the resource is then evaluated again, so it becomes concrete when all that it reads is known. A value that the provider marks unknown stays non-concrete.

### Data sources

pravo reads a data source during the plan when two things hold. Its configuration is fully known, and none of the resources it depends on has a pending change. Otherwise the read waits for the apply. The plan shows it as a read with an unknown result, and every resource that depends on it shows an unknown value too.

```cue file=deferred/main.cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

report: $local.#Resource.local_file & {
	filename: "report.txt"
	content:  "quarterly\n"
}

copy: $local.#Data.local_file & {filename: report.filename}
```

```sh run
pravo -C deferred init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo -C deferred plan | grep -E '^( <=|  # |Plan)'
```

```text out
  # report (local_file) will be created
  # copy (local_file) will be read (configuration depends on values known after apply)
 <= copy: (known after apply)
Plan: 1 to add, 0 to change, 0 to destroy.
```

`copy` reads `report.filename`, and `report` has a pending create. The read waits.

A data source whose read returned what its record holds is not a change. The apply does not write it, takes no lock for it and leaves its revision as it is. A resource that reads it gets the value of the record. A saved plan that reads the data source stays current while nothing writes it. When the refresh age of the data source is a duration, the plan records the time of the read and keeps the revision, so the next plan within the age does not read it again. With the age `always`, the default, the plan reads the data source every time. With `never` it does not read it until the configuration changes. Neither records the time.

## Scoped plans

A plan does not plan every resource, and it does not read every record. Each node in the state records the hash of the configuration that the provider last saw. It also records the provider version, the schema version and the time of the last refresh. A plan converts the body of each resource, with the values of the resources that ran before it, and compares.

A resource is **skipped** when all of these hold.

- The converted configuration is fully known, and its hash equals the hash of the record.
- The provider version, the provider instance and the schema version are the ones that wrote the record.
- The record has no mark. A read has not found that the object differs from the record or is gone, and the last apply of the resource finished.

A skipped resource is not validated and not planned. Its record stands in for the planned state, and the resources that depend on it see its attributes as known. Every other resource is **reached**. It goes to its provider.

What a plan reads follows what it reaches. A plan looks at the resources it reaches and at the resources they read, which are their **inputs**. It reads the record of each from its provider when the record is older than its refresh age. A resource that is neither reached nor an input is not upgraded, not read, not locked and not written, and no provider process starts for it, whatever the age of its record. [Refresh age](#refresh-age) says when a record that the plan looks at is read, and which records a plan reads whatever their position.

A plan that has nothing to do starts no provider. A provider configuration or a credential that is wrong shows in the first plan or apply that reaches a resource of that provider, not in a plan with no changes. `pravo plan -refresh=all` starts every provider, so it finds such a fault.

The work of a plan follows the change. A change to one resource reaches that resource and the resources whose configuration changed because of it. Two costs follow the size of the estate: pravo evaluates every resource of the specification to find out which ones changed, and it opens every record of the state. [State](state.md#cost-follows-the-change) says what this means for a large estate. Take eight zones of the first root. The resources number sixteen.

```cue file=zones/zones.cue
package estate

zones: ["a", "b", "c", "d", "e", "f", "g", "h"]

ports: {
	for z in zones {
		"\(z)": $random.#Resource.random_integer & {min: 20000, max: *29999 | int, seed: z}
	}
}

deploy: {
	for z in zones {
		"\(z)": $null.#Resource.null_resource & {
			triggers: {
				zone: z
				port: "\(ports[z].result)"
			}
		}
	}
}
```

```sh run
pravo -C zones plan | grep -E '^(Plan|Graph)'
```

```text out
Plan: 14 to add, 0 to change, 0 to destroy.
Graph: planned 14 of 16, 2 unchanged and skipped, 2 within refresh age.
```

Fourteen resources are new, so the plan reaches fourteen. `ports.a` and `deploy.a` are within their refresh age and skipped.

```sh run
pravo -C zones apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 14 added, 0 changed, 0 destroyed. [txn <id>]
```

Now change one number. `max` has a default, and a second file sets it for one zone.

```cue file=zones/override.cue
package estate

ports: c: max: 29998
```

```sh run
pravo -C zones plan
```

```text out
pravo will perform the following actions:

  # ports.c (random_integer) must be replaced
-/+ ports: c: {
  ~     id:      "28969" -> (known after apply)
  ~     max:     29999 -> 29998 # forces replacement
        min:     20000
  ~     result:  28969 -> (known after apply)
        seed:    "c"
    }

  # deploy.c (null_resource) must be replaced
-/+ deploy: c: {
  ~     id:       "<id>" -> (known after apply)
  ~     triggers: {
  ~         port: "28969" -> (known after apply)
            zone: "c"
        } # forces replacement
    }

Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 16, 14 unchanged and skipped, 16 within refresh age.
```

The provider cannot change `max` in place, so `ports.c` is replaced. A replacement makes the computed attributes of the resource unknown again, so `deploy.c`, which reads `ports.c`, is reached and replaced too. Fourteen resources are not.

:::figure The plan above, drawn over the graph. Two nodes are reached. Fourteen are skipped, and their records stand in.
<svg viewBox="0 0 780 318" role="img" aria-label="Sixteen nodes in two columns, ports a to h on the left and deploy a to h on the right, each joined to its partner. Only ports c and deploy c are reached and replaced. The other fourteen are skipped. The plan reports planned 2 of 16, 14 unchanged and skipped." style="min-width:700px">
<text x="215.0" y="16" text-anchor="middle" style="fill:var(--text-3);font-size:11px;letter-spacing:0.04em">ports</text>
<text x="425.0" y="16" text-anchor="middle" style="fill:var(--text-3);font-size:11px;letter-spacing:0.04em">deploy</text>
<line x1="280" y1="40.0" x2="360" y2="40.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="28" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="44.5" style="fill:var(--text-3);font-size:12px">ports.a</text>
<text x="272" y="44.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="28" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="44.5" style="fill:var(--text-3);font-size:12px">deploy.a</text>
<text x="482" y="44.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="74.0" x2="360" y2="74.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="62" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="78.5" style="fill:var(--text-3);font-size:12px">ports.b</text>
<text x="272" y="78.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="62" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="78.5" style="fill:var(--text-3);font-size:12px">deploy.b</text>
<text x="482" y="78.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="108.0" x2="360" y2="108.0" style="stroke:var(--change);stroke-width:1.6"/>
<rect x="150" y="96" width="130" height="24" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.6"/>
<text x="160" y="112.5" style="fill:var(--text);font-size:12px;font-weight:600">ports.c</text>
<text x="272" y="112.5" text-anchor="end" style="fill:var(--change);font-size:11px;font-weight:600">-/+</text>
<rect x="360" y="96" width="130" height="24" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.6"/>
<text x="370" y="112.5" style="fill:var(--text);font-size:12px;font-weight:600">deploy.c</text>
<text x="482" y="112.5" text-anchor="end" style="fill:var(--change);font-size:11px;font-weight:600">-/+</text>
<line x1="280" y1="142.0" x2="360" y2="142.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="130" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="146.5" style="fill:var(--text-3);font-size:12px">ports.d</text>
<text x="272" y="146.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="130" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="146.5" style="fill:var(--text-3);font-size:12px">deploy.d</text>
<text x="482" y="146.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="176.0" x2="360" y2="176.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="164" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="180.5" style="fill:var(--text-3);font-size:12px">ports.e</text>
<text x="272" y="180.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="164" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="180.5" style="fill:var(--text-3);font-size:12px">deploy.e</text>
<text x="482" y="180.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="210.0" x2="360" y2="210.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="198" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="214.5" style="fill:var(--text-3);font-size:12px">ports.f</text>
<text x="272" y="214.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="198" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="214.5" style="fill:var(--text-3);font-size:12px">deploy.f</text>
<text x="482" y="214.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="244.0" x2="360" y2="244.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="232" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="248.5" style="fill:var(--text-3);font-size:12px">ports.g</text>
<text x="272" y="248.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="232" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="248.5" style="fill:var(--text-3);font-size:12px">deploy.g</text>
<text x="482" y="248.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<line x1="280" y1="278.0" x2="360" y2="278.0" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="150" y="266" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="160" y="282.5" style="fill:var(--text-3);font-size:12px">ports.h</text>
<text x="272" y="282.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<rect x="360" y="266" width="130" height="24" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="370" y="282.5" style="fill:var(--text-3);font-size:12px">deploy.h</text>
<text x="482" y="282.5" text-anchor="end" style="fill:var(--text-3);font-size:10px">skip</text>
<text x="0" y="102" style="fill:var(--text-2);font-size:11px">max: 29999 -&gt; 29998</text>
<line x1="0" y1="114" x2="147" y2="114" style="stroke:var(--change);stroke-width:1.5"/>
<polygon points="147,114 141,110.4 141,117.6" style="fill:var(--change)"/>
<text x="540" y="60" style="fill:var(--change);font-size:11px;font-weight:600">reached</text>
<text x="540" y="78" style="fill:var(--text-2);font-size:11.5px">planned with the provider</text>
<text x="540" y="106" style="fill:var(--text-3);font-size:11px;font-weight:600">skipped</text>
<text x="540" y="124" style="fill:var(--text-2);font-size:11.5px">the recorded state stands in</text>
<text x="540" y="142" style="fill:var(--text-2);font-size:11.5px">for the planned state</text>
<line x1="540" y1="168" x2="770" y2="168" style="stroke:var(--line-strong);stroke-width:1"/>
<text x="540" y="192" style="fill:var(--text);font-size:11.5px">Plan: 2 to add, 0 to change,</text>
<text x="540" y="210" style="fill:var(--text);font-size:11.5px">      2 to destroy.</text>
<text x="540" y="236" style="fill:var(--text);font-size:11.5px">Graph: planned 2 of 16,</text>
<text x="540" y="254" style="fill:var(--text);font-size:11.5px">       14 unchanged and skipped</text>
</svg>
:::

The `Graph:` line reports what happened.

| Part | Counts |
|---|---|
| `planned N of M` | The resources and data sources that were reached, of all that the specification declares. |
| `N unchanged and skipped` | The ones that were not reached. |
| `N removed from the specification` | The records of resources that the specification does not declare and that the plan deletes. They are not among the `M`. |
| `N refreshed` | The ones whose real state was read from the provider during the run. |
| `N drifted` | The refreshed ones that differed from the record. |
| `N within refresh age` | The ones that were not read and whose record is within its refresh age. |
| `N not read` | The ones that were not read and whose record is older than its refresh age. |
| `N ephemeral opened`, `N functions called`, `N imported`, `N moved` | Work of other kinds. |

Each resource and data source that has a record is in exactly one of `refreshed`, `within refresh age` and `not read`. The three add up to the number of records, which are those of the declarations and the removed ones, and `drifted` is a part of `refreshed`. A resource that has no record, because the plan creates it, is in none of them, and neither is an ephemeral resource or a call. A part with a count of zero is not printed, except `planned`. With `-refresh=false` the line has neither `within refresh age` nor `not read`, because nothing is read. `pravo plan -full` plans every resource with its provider.

A plan that reads an input of a reached resource counts the input as `refreshed`, so the counts are final when the plan ends and not before.

The sentence under a plan with no change says how much the plan can promise. `No changes. Your infrastructure matches the configuration.` is printed when `not read` is zero and the refresh is not `false`: every record was read, or is within its refresh age. Otherwise the plan prints `No changes. The specification matches the state.` and says how many records it did not read. `pravo drift` compares them with the infrastructure. With `-refresh=false` the plan says that nothing was read. [Refresh age](#refresh-age) shows each sentence.

```sh run
pravo -C zones plan -full | grep -E '^(Plan|Graph)'
```

```text out
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 16 of 16, 16 within refresh age.
```

`pravo why` prints the reason of each change, and of each skip.

```sh run
pravo -C zones why ports.c deploy.c deploy.a
```

```text out
ports.c: replace
  - the specification changed since the last apply (hash <old> -> <new>)
  - refresh skipped: the record is within its refresh age
  - the provider cannot change max in place; replacement required
  reaches: deploy.c
deploy.c: replace
  - inputs depend on values known only after apply: ports.c
  - refresh skipped: the record is within its refresh age
  - the provider cannot change triggers in place; replacement required
deploy.a: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is 1h
```

`summary.blast_radius` in the plan document counts the nodes that a change touches. They are the changed nodes and every node that depends on them, as the state records the edges.

A plan unifies the values of a node it did not reach into the configuration only when something reads them: another resource, the configuration of a provider or an output. A constraint added to a computed attribute of a node that nothing reads is checked when that node is next planned.

### Requested replacements

`pravo plan -replace ADDRESS` destroys a resource and creates it again although nothing in the specification changed. The request is a change in the plan like any other, and it is reviewed like one. It can be repeated, and it cannot be combined with `-destroy`.

```sh run
pravo -C zones plan -replace deploy.a --json 2>/dev/null | jq -c '.changes[] | select(.addr == "deploy.a") | .reasons'
```

```text out
["replacement requested with -replace","refresh skipped: the record is within its refresh age"]
```

## Refresh age

Refreshing means asking the provider for the real state of a resource before planning against it. It is the most expensive part of a plan. Each node records when the provider last reported its state, and a **refresh age** says how old that may be. A record is within its refresh age while its age is no greater than the refresh age, and older than it once its age is greater. A record whose age equals the refresh age is within it.

Reach decides what a plan looks at. The refresh age decides whether a record that the plan looks at is read.

The refresh age is a staleness budget. It does three things:

- It spares a repeated plan the read of a record that it looks at and that was read within the age.
- On a resource, as `$lifecycle.refresh`, it is a standing promise: a plan or a sweep reads the record once it is older than that age, whether or not the plan reaches the resource.
- In a sweep, `pravo drift --due` and `pravo plan -refresh=expired` read the records that are past their age, the oldest first.

The saving of a plan that reads one resource of ten thousand comes from reach, not from the age.

Together they keep the calls to the providers in line with the change. A plan asks about the resources it plans and the resources they read, and does not ask again about a record that it read within its age. A change to one resource costs the reads of that resource and its inputs, whether the state holds ten records or ten thousand. The price is trust: a record that a plan does not read is taken to match its object. [Choosing a refresh age](#choosing-a-refresh-age) says how long to trust it.

The refresh age of a resource is `$lifecycle.refresh`, else `$settings.refresh`, else `"always"`.

| Value | Meaning |
|---|---|
| `"always"` | An age of zero. A plan that looks at the resource reads it. |
| `"never"` | No limit. A plan reads the resource only when asked. |
| `"30m"`, `"6h"`, `"7d"` | A duration. A plan that looks at the resource reads it when its record is older. |

A plan reads a record in these cases, and in no other.

| Record | Read when |
|---|---|
| A resource the plan reaches | Its record is older than its refresh age. |
| A resource that a reached resource reads | Its record is older than its refresh age. |
| A resource that sets its own `$lifecycle.refresh` | Its record is older than that age, reached or not. `$settings.refresh` is not an age of its own. A resource that sets `"always"` is read by every plan. |
| A data source | It is not read again when its configuration is unchanged, nothing it depends on has a pending change and its record is within its age. Otherwise it is read, reached or not. |
| A resource with a mark | It is reached, and read when its record is older than its refresh age. The mark stays until an apply writes the resource. |
| A resource with a stale lock, a record with no time of a read, or whose provider now points somewhere else | It is read whatever its age. See [Records a plan reads whatever their age](#records-a-plan-reads-whatever-their-age). |

Ephemeral resources, function calls and built-in values such as `pravo.#Env` run in every plan.

CUE rejects any other value when it evaluates the specification. The root below has four resources, each with its own age.

```cue file=ages/main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$local:  loc.#Provider

a: $random.#Resource.random_integer & {min: 1, max: 9, seed: "a", $lifecycle: refresh: "15m"}
b: $random.#Resource.random_integer & {min: 1, max: 9, seed: "b", $lifecycle: refresh: "never"}
c: $random.#Resource.random_integer & {min: 1, max: 9, seed: "c"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "see \(a.result)\n"
	$lifecycle: refresh: "always"
}
```

```sh run
pravo -C ages init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo -C ages apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 4 added, 0 changed, 0 destroyed. [txn <id>]
```

Right after the apply, a plan reads only `note`. The other three records are within their ages.

```sh run
pravo -C ages plan
```

```text out
note: Refreshing state... [id=<sha1>]

No changes. Your infrastructure matches the configuration.
Graph: planned 0 of 4, 4 unchanged and skipped, 1 refreshed, 3 within refresh age.
```

`pravo why` says what happened to each resource.

```sh run
pravo -C ages why a b c note
```

```text out
a: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is 15m
b: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is never
c: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is 1h
note: no-op
  - refreshed because the record is older than its refresh age; no drift
  - skipped: nothing the provider saw has changed
```

The flag `-refresh` overrides the ages for one run.

| Flag | Behaviour |
|---|---|
| `-refresh=age` | The default. A plan reads the records it looks at that are older than their refresh age. |
| `-refresh=expired` | A plan reads every record that is older than its refresh age, reached or not. Use it for a scheduled sweep. |
| `-refresh=all` | A plan reads every record, whatever its age. |
| `-refresh=false` | A plan reads no record and trusts them all. A mark is still honoured. The `Graph:` line has neither `within refresh age` nor `not read`. |

```sh run
pravo -C ages plan -refresh=all | tail -2
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 0 of 4, 4 unchanged and skipped, 4 refreshed.
```

```sh run
pravo -C ages plan -refresh=false | tail -2
```

```text out
No changes. The specification matches the state. Nothing was read from the infrastructure.
Graph: planned 0 of 4, 4 unchanged and skipped.
```

```sh run exit=1
pravo -C ages plan -refresh=sometimes
```

```text out
Error: invalid -refresh value "sometimes": want age, expired, false or all
```

A refresh that happens during a plan is written back to the record with a new refresh time, in a transaction. The revision moves only when the attributes differ. The apply that follows a plan does not read the provider again: it uses what the plan read. A refresh does not by itself cause a new plan: a resource whose refresh returns the attributes that are recorded is still skipped. A resource whose attributes changed, or whose object is gone, is drift. The read writes a **mark** on the record, `drifted_at` and, for an object that is gone, `gone`. The resource is planned by this plan and by every later plan, saved plan and apply, until an apply writes it. `pravo drift` ignores the ages, reads every resource and writes the same marks.

### A change reads what it reaches

The root `reads` has twenty files and one more resource that carries a version. It sets no refresh age, so every record is due.

```cue file=reads/main.cue
package estate

import (
	"list"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

version: *"1" | string @tag(version)

files: {for i in list.Range(0, 20, 1) {
	"f\(i)": $local.#Resource.local_file & {filename: "out/f\(i).txt", content: "item \(i)\n"}
}}

release: $local.#Resource.local_file & {filename: "out/release.txt", content: "version \(version)\n"}
```

```sh run
mkdir -p reads/out
pravo -C reads init | tail -1
pravo -C reads apply -auto-approve | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
Apply complete! Resources: 21 added, 0 changed, 0 destroyed. [txn <id>]
```

Change the version. The plan reaches `release` and reads it. No resource that it reaches reads one of the files, so it does not look at them.

```sh run
pravo -C reads plan -t version=2 | grep -E '^(.*Refreshing state|Plan|Graph)'
```

```text out
release: Refreshing state... [id=<sha1>]
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 of 21, 20 unchanged and skipped, 1 refreshed, 20 not read.
```

`-refresh=expired` reads every record that is older than its refresh age. With the age `always`, that is every record. The two counts below are the lines `Refreshing state` of the plan above and of the plan with `-refresh=expired`.

```sh run
pravo -C reads plan -t version=2 | grep -c 'Refreshing state'
pravo -C reads plan -t version=2 -refresh=expired | grep -c 'Refreshing state'
```

```text out
1
21
```

A plan that changes nothing reads nothing. It says that it did not read the records, because it cannot promise that the infrastructure matches.

```sh run
pravo -C reads plan | tail -2
```

```text out
No changes. The specification matches the state. 21 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 21, 21 unchanged and skipped, 21 not read.
```

`pravo plan -detailed-exitcode` exits 0 for this plan: the plan found no change among what it looked at. It does not detect drift in a resource that it did not read. The next section finds that drift. [CI](../how-to/ci.md) schedules it.

A record that a reached resource reads can differ from its object, or the object can be gone. The read writes the mark, and the plan walks again from the records as they are: the resource is reached now, and the records it reads are read in turn. A chain of resources that each read the one before takes one walk for each resource whose record differs, however long the chain is, and no record is read twice. The plan fails with `NODE changed while the plan read it; run pravo plan again` only when a record is found changed in a walk after it was found changed in an earlier one.

### Drift in a resource the plan does not read

Delete one of the files, as someone with a shell does. A plan does not look at it.

```sh run
rm reads/out/f7.txt
pravo -C reads plan -detailed-exitcode > /dev/null
echo "exit $?"
```

```text out
exit 0
```

`pravo drift` reads every record and reports the difference. The read writes a mark on the record.

```sh run exit=2
pravo -C reads drift
```

```text out
Drift. 1 of 21 resources changed outside pravo:
  files.f7: create
    - refresh found the real state differs from the state (drift)
```

```sh run
pravo -C reads state show files.f7 | grep '^// gone'
```

```text out
// gone
```

The next plan acts on the mark. It reaches `files.f7` and reads it again, and the other twenty are still not read. The apply that follows creates the file again, writes the record and ends the mark.

```sh run
pravo -C reads plan | grep -E '^(Plan|Graph)'
pravo -C reads apply -auto-approve | tail -1
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 21, 20 unchanged and skipped, 1 refreshed, 1 drifted, 20 not read.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

A sweep of a large estate that reads every record can take long. `pravo drift --due --limit N` reads only the records that are older than their refresh age, the oldest first and at most `N` of them, and plans nothing. A job that runs it often covers the estate in turn, and each run costs what it reads. It exits 2 when it finds drift.

```sh run
pravo -C reads drift --due --limit 5
```

```text out
No drift. 5 of 21 records read; 16 more are older than their refresh age.
```

### Choosing a refresh age

The refresh age is the time for which a plan trusts a record that it looks at. A longer age means fewer calls to the provider and a plan that waits for fewer answers. It also means that a change made outside pravo stays unseen for longer. Choose it for the object and not for the estate.

- A resource that only pravo changes can have a long age, `"6h"` or `"7d"`. Set it once with `$settings: refresh`. A plan reads such a record only when it reaches the resource or a resource that reads it, and only when the record is older than the age.
- A resource that people or other systems change, and whose current state a plan must see, sets `$lifecycle: refresh` on itself. The age on a resource is standing: every plan reads the record once it is older than that age, whether or not the plan reaches the resource. `"always"` makes every plan read it.
- The default, `"always"`, reads every record that a plan looks at, so a plan sees the current state of everything it plans. It also reads every data source on every plan, so an estate with many data sources sets an age.

Records that no plan looks at are read by a job that you schedule. `pravo drift --due --limit N` reads the records that are older than their age, the oldest first, and `pravo plan -refresh=expired` reads all of them while it plans. With a long age a job reads each record rarely, and with a short one often. [CI](../how-to/ci.md#5-find-drift-on-a-schedule) shows the job.

### Records a plan reads whatever their age

A plan reads three kinds of record that it does not reach, whatever their refresh age. The refresh age says how long a record may be trusted when nothing suggests it is wrong. These suggest it.

**An apply stopped while it held the node.** A command that is killed does not release its locks, and it may have changed the object before it stopped. The lock stays, and once its holder is gone or has not renewed it for ten minutes, the lock is stale. A plan that finds a stale lock on a node reads the node, and the read removes the lock. With `-refresh=false` the plan reads nothing and prints a warning for each such node. The lock below was left by an apply of `alice`; the root `ages` is the one above.

```sh run
mkdir -p ages/pravo.state/locks
cat > ages/pravo.state/locks/c.lock <<'EOF'
{"addr":"c","pid":4242,"host":"ci-1","user":"alice","command":"pravo apply","txn":"T1","nonce":"n1","since":"2026-01-01T00:00:00Z","kind":"lock","renewed_at":"2026-01-01T00:00:00Z"}
EOF
pravo -C ages why c
pravo -C ages state locks
```

```text out
c: no-op
  - an apply (txn T1, alice on host ci-1) stopped while this node was locked; its real state is read again whatever the refresh age
  - skipped: nothing the provider saw has changed
No locks or reservations held.
```

**The provider points somewhere else.** A record carries a digest of the configuration of the provider instance that wrote it. The digest covers the attributes that say where the provider points, such as a region, an endpoint or a profile. It leaves out an attribute that is sensitive, one that is not known yet and one that is built from an ephemeral value, so a credential that rotates changes nothing. A plan reads the records whose digest differs from the digest of their provider now. It plans one only when the read finds drift. A record written without a digest is not read for this; its next write or read adds one, and a provider with no such attribute leaves none. The root `target` configures the `tls` provider with a proxy.

```cue file=target/main.cue
package estate

import (
	"pravo.dev/pravo"
	tls "pravo.dev/providers/hashicorp/tls"
)

$settings: pravo.#Settings & {refresh: "6h"}

$tls: tls.#Provider & {proxy: [{url: "http://proxy-a.example:3128", username: "ci", password: "one"}]}

key: $tls.#Resource.tls_private_key & {algorithm: "ECDSA"}
```

```sh run
pravo -C target init | tail -1
pravo -C target apply -auto-approve | tail -1
jq 'has("provider_config")' target/pravo.state/resources/key.json
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
true
```

A new password for the proxy changes nothing the digest covers, and the record is within its age.

```cue file=target/main.cue
package estate

import (
	"pravo.dev/pravo"
	tls "pravo.dev/providers/hashicorp/tls"
)

$settings: pravo.#Settings & {refresh: "6h"}

$tls: tls.#Provider & {proxy: [{url: "http://proxy-a.example:3128", username: "ci", password: "two"}]}

key: $tls.#Resource.tls_private_key & {algorithm: "ECDSA"}
```

```sh run
pravo -C target plan | tail -2
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 0 of 1, 1 unchanged and skipped, 1 within refresh age.
```

Another proxy address does.

```cue file=target/main.cue
package estate

import (
	"pravo.dev/pravo"
	tls "pravo.dev/providers/hashicorp/tls"
)

$settings: pravo.#Settings & {refresh: "6h"}

$tls: tls.#Provider & {proxy: [{url: "http://proxy-b.example:3128", username: "ci", password: "two"}]}

key: $tls.#Resource.tls_private_key & {algorithm: "ECDSA"}
```

```sh run
pravo -C target why key
pravo -C target plan | tail -1
```

```text out
key: no-op
  - the non-sensitive configuration of its provider changed
  - skipped: nothing the provider saw has changed
Graph: planned 0 of 1, 1 unchanged and skipped, 1 within refresh age.
```

The first command read the record, found no drift and wrote the digest of the new configuration. The plan that follows has nothing to read.

**An apply could not tell what it did to the node.** A provider can fail without returning a state, and an apply can exit without waiting for a provider. [Interrupting an apply](../reference/apply.md#interrupting-an-apply) describes both. The apply writes the record of the node again without `refreshed_at`. The next plan reads the node whatever its refresh age, also when the specification has gone back to what the record says and the plan does not reach the node. The read writes `refreshed_at`. With `-refresh=false` the plan reads nothing and prints a warning for each such node.

## Saved plans

`pravo plan -out FILE` writes the plan to a file, and `pravo apply FILE` applies those actions without a prompt. The reviewed file is what runs.

The file records the change of every node the plan ran, with its configuration, its prior and its planned values, unknown values included. It records the private data of the providers and the refresh and skip decisions. It records the tags of the plan, the provider versions and a fingerprint of every `.cue` file of the root. It records the lineage of the state, the revision of every node that the plan changes and the revision of every record that those changes read. A node that the plan left alone has no entry. Values in it can be secrets. pravo creates the file with owner-only permissions. A plan can also be [stored in the state](#stored-plans).

:::figure What apply does with a saved plan, in order.
<svg viewBox="0 0 780 346" role="img" aria-label="A saved plan is made by plan with out, held in a file with owner-only permissions, and read by apply with the file. Apply checks four things: the configuration fingerprint, the state lineage, the provider versions and the revision of the nodes it writes and reads. A difference is stale and exits 3. Apply then judges the plan by any policy, plans each change again, compares the result with the saved change, and only then changes the resource." style="min-width:700px">
<rect x="0" y="20" width="128" height="54" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="64.0" y="42" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">plan -out</text>
<text x="64.0" y="60" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo plan</text>
<rect x="158" y="20" width="128" height="54" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="222.0" y="42" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">plan file</text>
<text x="222.0" y="60" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">mode 0600</text>
<rect x="316" y="20" width="128" height="54" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="380.0" y="42" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">apply FILE</text>
<text x="380.0" y="60" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">pravo apply</text>
<rect x="474" y="20" width="128" height="54" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3" stroke-dasharray="4 3"/>
<text x="538.0" y="42" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">judge</text>
<text x="538.0" y="60" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">with --policy</text>
<rect x="632" y="20" width="148" height="54" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.3"/>
<text x="706.0" y="42" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">plan again</text>
<text x="706.0" y="60" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">each resource</text>
<line x1="130" y1="47" x2="156" y2="47" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="156,47 150,43.4 150,50.6" style="fill:var(--text-3)"/>
<line x1="288" y1="47" x2="314" y2="47" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="314,47 308,43.4 308,50.6" style="fill:var(--text-3)"/>
<line x1="446" y1="47" x2="472" y2="47" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="472,47 466,43.4 466,50.6" style="fill:var(--text-3)"/>
<line x1="604" y1="47" x2="630" y2="47" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="630,47 624,43.4 624,50.6" style="fill:var(--text-3)"/>
<text x="538" y="12" text-anchor="middle" style="fill:var(--text-3);font-size:10px">exit 5 when refused</text>
<text x="526" y="104" text-anchor="end" style="fill:var(--text-3);font-size:10.5px;letter-spacing:0.02em">checks, in order</text>
<rect x="316" y="112" width="210" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="326" y="127" style="fill:var(--text);font-size:11.5px">configuration fingerprint</text>
<rect x="316" y="138" width="210" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="326" y="153" style="fill:var(--text);font-size:11.5px">state lineage</text>
<rect x="316" y="164" width="210" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="326" y="179" style="fill:var(--text);font-size:11.5px">provider versions</text>
<rect x="316" y="190" width="210" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="326" y="205" style="fill:var(--text);font-size:11.5px">revision of nodes it uses</text>
<line x1="330" y1="74" x2="330" y2="110" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="330,110 326.4,104 333.6,104" style="fill:var(--text-3)"/>
<rect x="576" y="126" width="144" height="50" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="648" y="146" text-anchor="middle" style="fill:var(--conflict);font-size:12.5px;font-weight:600">stale</text>
<text x="648" y="164" text-anchor="middle" style="fill:var(--text-2);font-size:10.5px">exit 3, no change</text>
<line x1="526" y1="151" x2="574" y2="151" style="stroke:var(--conflict);stroke-width:1.5"/>
<polygon points="574,151 568,147.4 568,154.6" style="fill:var(--conflict)"/>
<text x="550" y="143" text-anchor="middle" style="fill:var(--text-3);font-size:9.5px">differs</text>
<text x="576" y="226" style="fill:var(--text-3);font-size:10px">compare with saved change</text>
<rect x="576" y="236" width="204" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="586" y="251" style="fill:var(--text);font-size:11.5px">same action</text>
<rect x="576" y="262" width="204" height="22" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="586" y="277" style="fill:var(--text);font-size:11.5px">known values unchanged</text>
<line x1="756" y1="74" x2="756" y2="232" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="756,232 752.4,226 759.6,226" style="fill:var(--text-3)"/>
<rect x="600" y="306" width="180" height="32" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="690" y="327" text-anchor="middle" style="fill:var(--accent);font-size:11.5px;font-weight:600">provider changes it</text>
<line x1="690" y1="290" x2="690" y2="304" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="690,304 687.0,299 693.0,299" style="fill:var(--text-3)"/>
<rect x="350" y="306" width="210" height="32" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="455" y="327" text-anchor="middle" style="fill:var(--conflict);font-size:11px;font-weight:600">refused before the change</text>
<line x1="598" y1="322" x2="562" y2="322" style="stroke:var(--conflict);stroke-width:1.5"/>
<polygon points="562,322 567,319.0 567,325.0" style="fill:var(--conflict)"/>
</svg>
:::

The next plan is the pending change of `ports.c` and `deploy.c`.

```sh run
pravo -C zones plan -out c.plan | tail -3
```

```text out
Saved the plan to c.plan. To apply exactly these actions, run:
  pravo apply c.plan
```

`pravo show FILE` prints a saved plan. It adds a header with the time, the command and the actor.

```sh run
pravo -C zones show c.plan | head -3
```

```text out
Saved plan created <time> by "pravo -C zones plan --out c.plan"

pravo will perform the following actions:
```

### A plan can be stale

Before anything is applied, pravo checks the file against the root. Four things must match.

- The fingerprint of the `.cue` files. Any edit, a comment included, changes it. The reason names the files that differ: `(main.cue changed)`, `(extra.cue added)`, `(old.cue removed)`. The module file is `cue.mod/module.cue`.
- The lineage of the state.
- The versions of the providers.
- The revision of every node that the plan changes, and of every record that those changes read.

A difference makes the plan stale. The apply refuses with the reasons and exit code 3, and nothing changes.

```sh run
echo '// a comment' >> zones/override.cue
```

```sh run exit=3
pravo -C zones apply c.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (override.cue changed)
```

Restore the file. The plan is current again.

```cue file=zones/override.cue
package estate

ports: c: max: 29998
```

### Applying part of a plan

`pravo apply FILE -only ADDR,ADDR` applies the named changes and marks them in the file. The rest waits for a later `pravo apply FILE`.

Each name must be a change in the file. A node that the plan left alone has no entry in the file, and naming it is an error. The selection must be closed under dependencies. Every pending change that a selected change depends on must be selected too, or already applied. So must a data source whose read returned something other than its record, because the change was planned from what it returned. A selected delete needs every pending delete that depends on it. When the selection is not closed, nothing runs, and the command prints the smallest closed selection.

```sh run exit=1
pravo -C zones apply c.plan -only deploy.c
```

```text out
Error: the selection is not closed under dependencies; it also needs ports.c
  (the smallest closed selection is -only deploy.c,ports.c)
```

`ports.c` alone is closed.

```sh run
pravo -C zones apply c.plan -only ports.c
```

```text out
Applying 1 of 2 remaining changes from c.plan.
ports.c: Destroying... [id=28969]
ports.c: Creating replacement...
ports.c: Replacement complete after 0s [id=21730]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
1 change remains in c.plan; apply with "pravo apply c.plan".
```

The apply wrote `ports.c` and left `deploy.c`. The file records which change is done.

```sh run
pravo -C zones show c.plan | grep -E '^  # '
```

```text out
  # ports.c (random_integer) must be replaced (already applied in txn <id>)
  # deploy.c (null_resource) must be replaced
```

During a staged apply, a resource outside the selection is neither planned nor touched. The selected resources see it through its record, which the closure rule makes enough. An output that reads a resource outside the selection, one that has no record yet, is not recorded by that apply. The apply that finishes the plan records it. [`pravo apply`](../reference/apply.md#outputs-of-a-part) shows it. A saved destroy plan is staged in the same way: a part removes the outputs that read what it destroys, leaves the others, and the part that destroys the last resources removes the rest. See [Outputs of a partial destroy](../reference/apply.md#outputs-of-a-partial-destroy).

```sh run
pravo -C zones apply c.plan | tail -1
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
```

### A plan can be stale through the state

The revision of a node changes whenever a command writes it. Plan the creation of a ninth zone, then let another apply create it first.

```cue file=zones/zones.cue
package estate

zones: ["a", "b", "c", "d", "e", "f", "g", "h", "i"]

ports: {
	for z in zones {
		"\(z)": $random.#Resource.random_integer & {min: 20000, max: *29999 | int, seed: z}
	}
}

deploy: {
	for z in zones {
		"\(z)": $null.#Resource.null_resource & {
			triggers: {
				zone: z
				port: "\(ports[z].result)"
			}
		}
	}
}
```

```sh run
pravo -C zones plan -out i.plan | grep -E '^(Plan|Graph)'
```

```text out
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 18, 16 unchanged and skipped, 16 within refresh age.
```

```sh run
pravo -C zones apply -auto-approve | tail -1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

```sh run exit=3
pravo -C zones apply i.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - ports.i changed in state (revision 1, plan expected 0)
  - deploy.i changed in state (revision 1, plan expected 0)
```

The plan expected no record for `ports.i` and `deploy.i`. The other apply wrote them.

An apply that writes a node which the plan neither changes nor reads leaves the plan current. A record that a change reads is listed in the file with its revision, and so are the records that it reads in turn, because a field of a record can be an expression over them. When one of them was written since, the plan is stale, and the reason ends with `the plan reads it`.

### A saved plan keeps what it read

A saved plan holds the answer that each data source gave when the plan was made. The apply uses it and does not read the source again. The root below copies a file that a data source reads.

```cue file=feed/main.cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

src: $local.#Data.local_file & {filename: "feed.txt"}

copy: $local.#Resource.local_file & {
	filename: "copy.txt"
	content:  src.content
}
```

```sh run
echo one > feed/feed.txt
pravo -C feed init | tail -1
pravo -C feed apply -auto-approve | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

Change the file and save a plan. The plan reads `two`. Change the file again before the plan is applied.

```sh run
echo two > feed/feed.txt
pravo -C feed plan -out feed.plan | grep -E '^(Plan|Graph)'
echo three > feed/feed.txt
```

```text out
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 2 of 2, 2 refreshed.
```

The apply writes what the plan read.

```sh run
pravo -C feed apply feed.plan | tail -1
cat feed/copy.txt
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
two
```

The next plan reads the file again and finds `three`.

```sh run
pravo -C feed plan | grep -E '^(Plan|Graph)'
```

```text out
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 2 of 2, 2 refreshed.
```

### What apply checks while it runs

Once an apply holds the locks of the nodes it writes, it reads the revision of each of them, and of each record it reads that the plan lists, from the state itself and not from what it loaded when it started. A revision that differs from the one in the plan means that another command wrote the node while the plan waited. A record that was removed and created again while the plan waited starts at revision 1 again, so the apply compares the generation of the record as well, and a record in another generation is refused whatever its revision. The apply stops with exit code 3 before it changes anything, whether the plan is a file or the plan that `pravo apply` printed. A saved plan lists the reasons as it does when it is loaded. A plain apply says that the plan changed while it waited and lists them. A record that the apply only reads is not locked, so a write to it after this check is not seen.

An apply plans each change of the plan again with the values that are known by then, as an apply always does. It compares the result with the change in the plan before it touches the resource. The action must be the same, or the resource has nothing left to do. A resource that has nothing left to do must already hold every value that the plan would have given it. Every value that the plan already knew must be unchanged. A provider that resolves an unknown is accepted. A provider that changes an answer it gave stops the apply at that resource, which is not touched. The resources the apply changed before it reached that one keep their changes. `pravo apply` without a file makes the same comparison with the plan it has just printed. A difference is exit code 3. The message of a plain apply says that the plan changed while it waited. The message of an apply of a file names the resource and what the provider planned instead.

A value that an expression builds from a container of resources is the same in the plan and in the apply. A comprehension over a container visits the resources in the order in which the specification declares them, whatever order the providers finish in.

A data source that the plan read is not read again. The apply uses the answer the plan holds, and the resources that read the source are applied with it, whether the source answers the same now or not. A later plan reads the source as its refresh age says.

The comparison runs as the apply reaches each resource, and resources that do not depend on each other are applied at the same time. When one resource differs, the apply stops. The resources it applied by then keep their changes, and the apply records them in the plan: it marks them in the plan file, or writes them into the progress object of a stored plan, and prints the command that applies the rest. The resource that differed is not recorded. The next `pravo apply FILE` leaves the marked changes alone and does what is left. A provider that plans the resource differently again stops it again: make a new plan.

While it runs, the apply renews its locks every two minutes, and before it asks a provider to change an object it renews them if more than five minutes have passed since it last knew that they were its own. A lock that another command took over meanwhile, which can happen after the computer was suspended and the lock went stale, stops the apply with `lock lost` and exit code 4. A renewal that fails for another reason, so that the locks cannot be confirmed, stops it with exit code 1. Either way the apply makes no further change, and the changes it made are recorded.

The apply walks the changes of the plan and the nodes they read, and no others. A node that a change reads is taken from its record, as the plan saw it. A node that no change reads is not planned again, not read and not checked: how far its record can be from the infrastructure is decided when the plan is made, by the reach of the plan and the [refresh age](#refresh-age).

### What a saved plan does not do

A saved plan is not signed. pravo checks that the file still matches the root, the state and what the providers plan. It does not check who made it.

`pravo apply FILE --policy POLICY` judges the changes that remain in the file, all of them, before any provider is asked to change anything. The file records the command that made it. Nothing reads that line as a requirement. A policy file is not part of the fingerprint. [Policies](policies.md) covers the rest.

## Stored plans

A saved plan is a file. A [stored plan](../reference/glossary.md#stored-plan) is the same plan written into the state. `pravo plan --store` writes it, and anyone who can read the state can list it, read it and apply it. [Share a plan before you apply it](../how-to/share-a-plan-before-you-apply-it.md) follows two people through it.

The plan is the object that `plan --out` writes: [the saved plan file](../reference/plan-document.md#the-saved-plan-file). A stored plan is three objects below `plans/` in the state. `plan --store` writes the plan and its summary, and the progress object appears when an apply or a collection first touches the plan.

| Object | Holds | Changes |
|---|---|---|
| `plans/ID.plan.json` | The plan. | Never. |
| `plans/ID.meta.json` | The summary that a listing reads: who made the plan, when it expires, what it changes and which records it expects. | Never. |
| `plans/ID.progress.json` | What was applied from the plan and by which transaction, and who is applying or removing it. | By a conditional replace, and only this one. |

The id is `plan-` and the first eight hexadecimal digits of the SHA-256 digest of the plan object, so the same bytes are always the same plan. Any unique prefix of six digits or more finds a plan. [State format](../reference/state-format.md#plans) lists every field.

The root `store` has two resources that do not depend on each other. Store a plan for it.

```cue file=store/main.cue
package store

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_integer & {min: 1, max: 9, seed: "a"}
b: $random.#Resource.random_integer & {min: 1, max: 9, seed: "b"}
```

```sh run
pravo -C store init > /dev/null
pravo -C store plan --store --actor alice | tail -n 3
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

The plan prints as usual, then pravo says where it went. Nothing is applied, and a plan with no changes is not stored. The listing shows who made it and where it stands.

```sh run
pravo -C store plan list
```

```text out
plan-<id>  pending  alice  just now  +2 ~0 -0  expires in 7 days
```

### Where a stored plan stands

A listing reads the summaries and the progress objects, and compares each plan with the state. It does not read the plan objects, and it does not compare the configuration or the providers. A plan whose summary or progress object cannot be read is not listed. The command that shows or applies the plan does, because they belong to a checkout and the people who share a state have several.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | Some changes were applied, by `apply --only` or by an apply that failed after it finished them. The rest can be applied. |
| `partly-stale` | A record that some changes not applied yet write has another revision or generation, and the other changes can still be applied. The whole plan is refused. `apply --only` with the changes that can be applied is not. |
| `stale` | No part of the plan can be applied: the state has another lineage, a record that the plan only reads moved, or every change not applied yet conflicts. |
| `applied` | Every change was applied. |
| `expired` | The time to live ended before the plan was applied. |

A plan is judged change by change. A change conflicts when the record it writes has another revision or generation than the plan expected. A change that has to be applied with one that conflicts conflicts too: the summary lists, for each change, the changes that an apply of it needs, which are the rules by which `--only` closes a selection. The changes that do not conflict, with the changes they need, are the live part. A change that is applied is not judged. The lineage of the state and a record that the plan only reads are not judged change by change: the plan was worked out from them, and when one moved, the whole plan is stale. So is a plan whose configuration or providers differ from the checkout, which `plan show` and `apply` compare.

A listing of a partly stale plan names the conflicts and the `--only` set that applies the live part. The apply of the whole plan is refused with exit code 3, and so is an `--only` that holds a conflict. [`pravo apply`](../reference/apply.md#a-partly-stale-plan) runs it. A collection neither marks nor removes a partly stale plan: it stays until its time to live ends. When the live part is applied, nothing that can be applied is left, the plan is stale, and a collection marks it and removes it after the grace like any stale plan. The rest is planned again.

:::figure What happens to a stored plan, from the plan that is applied to the plan that nobody applies. A plan that is in use is never removed.
<svg viewBox="0 0 780 236" role="img" aria-label="A timeline with three rows. A plan that is applied is pending, then applied, is kept for 24 hours and is then removed. A plan that the state moved away from is pending, then stale; the first collection that sees it marks it, and a collection that runs an hour later removes it. A plan that nobody applies is pending until its time to live ends, is then expired, and the next collection removes it." style="min-width:680px">
<text x="0" y="62" style="fill:var(--text-2);font-size:12px;font-weight:500">applied</text>
<line x1="120" y1="70" x2="770" y2="70" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="122" style="fill:var(--text-2);font-size:12px;font-weight:500">state moved</text>
<line x1="120" y1="130" x2="770" y2="130" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="182" style="fill:var(--text-2);font-size:12px;font-weight:500">not applied</text>
<line x1="120" y1="190" x2="770" y2="190" style="stroke:var(--line);stroke-width:1"/>
<rect x="135" y="44" width="150" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="62" style="fill:var(--text);font-size:12px">pending</text>
<rect x="285" y="44" width="130" height="26" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="295" y="62" style="fill:var(--text);font-size:12px;font-weight:500">applied</text>
<rect x="415" y="44" width="150" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="425" y="62" style="fill:var(--text-2);font-size:12px">kept for 24 hours</text>
<rect x="565" y="44" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="62" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="285" y="38" style="fill:var(--text-3);font-size:10px">last change applied</text>
<rect x="135" y="104" width="120" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="122" style="fill:var(--text);font-size:12px">pending</text>
<rect x="255" y="104" width="100" height="26" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="265" y="122" style="fill:var(--conflict);font-size:12px;font-weight:600">stale</text>
<rect x="355" y="104" width="210" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="365" y="122" style="fill:var(--text-2);font-size:12px">marked, kept for 1 hour</text>
<rect x="565" y="104" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="122" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="255" y="98" style="fill:var(--text-3);font-size:10px">a record moved</text>
<text x="355" y="98" style="fill:var(--text-3);font-size:10px">a collection sees it</text>
<rect x="135" y="164" width="310" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="145" y="182" style="fill:var(--text);font-size:12px">pending until the time to live ends</text>
<rect x="445" y="164" width="120" height="26" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.4"/>
<text x="455" y="182" style="fill:var(--text);font-size:12px;font-weight:500">expired</text>
<rect x="565" y="164" width="70" height="26" rx="2" style="fill:var(--surface-2);stroke:var(--line-strong);stroke-width:1"/>
<text x="575" y="182" style="fill:var(--text-2);font-size:12px">removed</text>
<text x="445" y="158" style="fill:var(--text-3);font-size:10px">7 days, or --ttl</text>
<text x="565" y="158" style="fill:var(--text-3);font-size:10px">next collection</text>
<line x1="120" y1="212" x2="770" y2="212" style="stroke:var(--text-3);stroke-width:1.2"/>
<polygon points="770,212 764,208.4 764,215.6" style="fill:var(--text-3)"/>
<text x="770" y="230" text-anchor="end" style="fill:var(--text-3);font-size:10.5px">time</text>
</svg>
:::

### Two plans for one resource

Stored plans do not exclude each other. Two people can plan the same resource. The first plan applied changes the record, and the other conflicts there: it is stale, or partly stale when it has changes that do not touch that record. A plan that finds an open plan changing one of its resources prints a warning that names the other plan, its author and the resources, each once. A change that the other plan has applied already does not count. The warning informs and blocks nothing. A reservation, made with `plan --reserve`, is what keeps another actor from planning or applying a node.

### Applying a stored plan

`pravo apply ID` reads the plan from the state and then does what `pravo apply FILE` does, with the same checks. [`pravo apply`](../reference/apply.md#apply-a-stored-plan) lists them. Before it reads the plan, apply writes a claim into the progress object, so that no collection removes the plan while the apply runs. The claim is that apply's own, so applies of separate parts of one plan run at the same time, and each ends only its own. An apply fails with exit code 3 when a collection has claimed the plan to remove it, and when the plan was not applied and has expired and no collection has removed it yet. Any other failure to claim the plan is exit code 1, and so is a plan that a collection removed. A plan that was applied completely has nothing left to apply, also after it expired.

An apply records what it did in the progress object: the changes applied and the transaction. The plan object stays as it was written. An apply that fails or is interrupted records the changes it finished, as an apply from a plan file marks them in the file. A change that failed, one whose result is not known and one that was never started are not recorded. The apply prints how many changes it recorded and the command that applies the rest, the plan lists as `partial`, and the next apply does what is left. A change that is recorded is not judged against the state again, so the records that the apply wrote for it do not make the plan stale. The plan is `applied` when every change that was pending has been applied, whichever apply finishes last. Apply part of the plan and the listing shows it. [`pravo apply`](../reference/apply.md#apply-a-stored-plan) runs a failure.

```sh run
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id --only a | tail -n 2
pravo -C store plan list
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
1 change remains in plan-<id>; apply with "pravo apply plan-<id>".
plan-<id>  partial  alice  just now  +2 ~0 -0  1 of 2 changes applied; expires in 7 days
```

The rest of the plan can still be applied. When the last change is applied the plan is `applied`.

```sh run
id=$(pravo -C store plan list --json | jq -r '.plans[0].id')
pravo -C store apply $id | tail -n 1
pravo -C store plan list --all
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
plan-<id>  applied  alice  just now  +2 ~0 -0  applied just now
```

### Collection

`pravo plan gc` removes the plans that nobody needs, and `pravo plan --store` removes a few on its way. The rules are in [`pravo plan`](../reference/plan.md#collect-stored-plans). A collection claims a plan in its progress object before it deletes anything. The deletes are conditional: pravo removes an object only if it is what its name says it is: a plan whose digest gives its id, or a summary that names the id. Two collections and an apply that run at once cannot damage each other, on a directory or in a bucket. Only the objects below `plans/` that have the names of plan objects are ever removed. A plan whose summary cannot be read is left alone, and a progress object that cannot be parsed is made empty once the plan is older than an hour. The small collection of `plan --store` never removes the plan that it has just stored.

### What a stored plan does not do

The author of a stored plan is the `--actor` label, or the operating system user when there is none. pravo does not verify it.

A stored plan holds the same values as a plan file, sensitive values included. The objects are as readable as the rest of the state: whoever can read the state can read its plans, exactly as they can read the records. [The viewer](the-viewer.md#stored-plans) lists stored plans and withholds their sensitive values as it does for a plan file. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says what the state holds.

A stored plan applies to the files it was made from. It records the `-t` and `-T` of the command that made it. `plan show` and `apply` need the same, because the tags select the state, and refuse others with the command to run. `plan list` reads the state that its own `-t` and `-T` select.

## Recap

- A plan is a list of actions with reasons. It is printed for people and available as JSON for programs.
- A value that depends on a resource that does not exist yet is unknown. The plan shows it as `(known after apply)`.
- A plan reaches the resources that changed and the resources that read them. The rest is skipped, and the `Graph:` line counts both.
- A saved plan is checked against the files, the state and the providers, and then each resource is planned again.
- A stored plan is a saved plan in the state, with a summary and a progress object. A listing judges it by the state, and a collection removes it when nobody needs it.

Next: [State](state.md) describes the records that a plan compares with.
