<!-- Generated from docs/reference/state.md by tools/skillgen. Do not edit. -->

# pravo state

Inspect and edit the state graph: list, show, move or forget a node, read the log and the locks, copy the state, query it, convert an OpenTofu state file.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo state SUBCOMMAND [ARGS]` |
| Reads | The state graph in the location the specification names: a directory, `pravo.state` by default, or an S3-compatible bucket. |
| Tags | Every subcommand takes `-t` and `-T` and evaluates the specification with them. When `$settings.state.path` depends on a tag, the tags select the state. Without a tag, a subcommand reads the state that the defaults select. |
| Taught in | [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md), [B8, Renames, moves, imports](../learn/b08-renames-moves-imports.md) and [Lab 2, Two applies at once](https://pravo.systems/learn/lab2-two-applies-at-once.html). |

An address is the CUE path of a resource: `pet`, `api.config`, `servers[0]`. Quote an address that contains brackets or quotes for the shell. See [Resources and addresses](../concepts/resources-and-addresses.md).

### Subcommands

| Subcommand | Prints or does | Writes the state | `--json` |
|---|---|---|---|
| `list` | Every address, sorted, one per line. | no | no |
| `show ADDR` | One node: bookkeeping as comments, then the attributes as CUE. | no | yes |
| `rm ADDR` | Forgets the node. The real object stays. | yes | no |
| `mv FROM TO` | Renames a node and updates the edges that point at it. | yes | no |
| `log` | The recorded transactions, newest first. | no | yes |
| `verify TXN [--plan FILE]` | Checks the receipt of an apply: its signatures, its plan and the policy versions that judged it. | no | yes |
| `locks` | The locks and reservations held. | no | yes |
| `probe` | Checks that the S3 store or http server honours the conditional requests that locks need. One line for each check. See [S3-compatible stores](s3-stores.md#test-a-store) and [HTTP state servers](http-stores.md). | no, it writes and deletes scratch objects | yes |
| `unlock ADDR`, `unlock --stale` | Releases a lock or a reservation, or every one that may be reclaimed. | yes | no |
| `pull DIR` | Copies the configured state, stored plans included, into the local directory `DIR`. | no | no |
| `push DIR` | Copies the local directory `DIR`, stored plans included, into the configured backend. | yes | no |
| `graph [--dot]` | The dependency edges, as `from -> to` or as Graphviz. | no | no |
| `dependents ADDR` | Every node a change to `ADDR` can reach. | no | no |
| `query EXPR` | The value of a CUE expression over the state. | no | yes |
| `import-tfstate FILE [--force]` | Converts a state file of OpenTofu or legacy Terraform. | yes | no |

`list`, `show`, `log`, `verify`, `locks`, `graph`, `dependents` and `query` read an empty state when none exists, and create nothing.

### Flags

| Flag | Subcommand | Meaning |
|---|---|---|
| `-t`, `--tag key=value` | all | Inject a value for an `@tag()` field. Repeatable. |
| `-T`, `--inject-vars` | all | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |
| `--dot` | `graph` | Print Graphviz DOT. |
| `--force` | `import-tfstate` | Replace a state that is not empty. |
| `--plan FILE` | `verify` | The plan file the transaction applied, when the state does not hold it. |

The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Done. |
| 1 | The address is not in the state or already exists, or the expression is invalid. |
| 4 | Another process holds a lock on a node that `rm` or `mv` needs. |
| 5 | `verify`: a policy listed in the receipt does not hold for the plan. |
| 6 | The specification does not load or evaluate. |
| 7 | `verify`: a signature does not verify, the plan has another digest than the transaction signed, a listed version is not the text that was signed, or the list omits a version that was in force. |

## Examples

Work in an empty directory named `demo`. The state needs something in it.

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

port: $random.#Resource.random_integer & {
	min:  20000 + base.result
	max:  20100
	seed: "port"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
```

```sh
pravo init > /dev/null
pravo apply -auto-approve > /dev/null
pravo state list
```

Output:

```text
base
port
```

### show

```sh
pravo state show port
```

Output:

```text
// port (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 1)
// refreshed <age> ago, applied <age> ago, config hash <hash>
// depends on base
{
	id:      "20093"
	keepers: null
	max:     20100
	min:     20009
	result:  20093
	seed:    "port"
}
```

The first line names the provider instance after `as`, the schema version and the revision of the node. `refreshed` and `applied` say when a provider last reported the object and when pravo last wrote it. `config hash` identifies the configuration that the provider last saw. `depends on` and `needed by` list the edges. A record that a read left a mark on has the lines `// drifted AGE ago` and, for an object that is gone, `// gone`. The attributes include sensitive values in the clear.

### graph, dependents and query

An edge `port -> base` means that `port` depends on `base`.

```sh
pravo state graph
pravo state dependents base
pravo state query 'len(resources)'
pravo state query '{for a, r in resources {"\(a)": r.attributes.result}}'
```

Output:

```text
port -> base
port
2
{
	base: 9
	port: 20093
}
```

The scope of `query` has three fields:

| Field | Content |
|---|---|
| `resources` | A struct keyed by address. Each node has `mode`, `type`, `name`, `provider`, `attributes`, `dependencies`, `revision` and the times. |
| `edges` | A list of `{from, to}`. |
| `outputs` | The recorded outputs by name. |

With `--json`, `query` prints JSON. `pravo state graph --dot | dot -Tsvg` draws the graph when Graphviz is installed.

### log

Every apply, destroy, import and state edit is a transaction. A plan that refreshes or moves records is one too, and so is a plan that finds drift: the entry says `drift`. A plan that reads nothing and writes nothing leaves none.

```sh
pravo state log
```

Output:

```text
<txn>  apply    applied   <time>
    pravo apply --auto-approve
    2 added, 0 changed, 0 destroyed
    create   base  r0 -> r1
    create   port  r0 -> r1
```

Each entry has the identifier, the kind, the outcome, the start time and the duration. Below come the command line as it was run, the actor if any, a summary, and one line per node with the revision before and after. The log is evidence. It prevents nothing, and it is as writable as the state.

In a sealed state each entry also says whether its signature verifies against the readers file: `signed by NAME (verified)`, `start signed by NAME (verified)` for a run that has not finished, `signed by NAME, whose key is not in the readers file`, `unsigned` or `the signature does not verify: the transaction was changed`. A signature that does not verify makes the command exit with 7, after the listing. With `--json` each entry has a `signature` object: `status` (`verified`, `key_not_in_file`, `unsigned` or `invalid`) and `signer` for a signature that verified, or `claimed` for the name a signature gives itself; the exit code is the same. See [Signatures](protect.md#signatures).

An apply is `running` from the moment it holds its locks. When its command is killed, the record in the state keeps that status, and the log lists the apply as `interrupted` once no lock that is still live carries its transaction.

### verify

`pravo state verify TXN` checks the receipt of an apply in a sealed state. The signed start of the transaction names the digest of the plan it judged and the policy versions that judged it. The command finds the transaction, checks both signatures against the readers file, reads the plan by the id the transaction names (or from `--plan FILE`), and requires the digest of the plan to be the signed one. It then reads each listed version, requires its text to match the listed digest and its signature to verify, requires that the list omits no version that was in force ten minutes before the transaction started, and judges the plan again with the listed versions. A short prefix of the id is enough when it names one transaction.

It prints `Verified.` and exits 0 when all of it holds. A bad signature, a plan with another digest, a version that is not the listed text and a list that omits a version are exit code 7. A rule that fails on the plan is exit code 5. Anything that stops the check, such as a plan that is gone or a state that is not sealed, is exit code 1 with the reason. A stored plan that an apply judged is kept, so `pravo plan gc` leaves it. [Operating policies](policies-operations.md#verify-a-receipt) runs the command.

A receipt proves that the signer started an apply of exactly that plan and named exactly those versions, that each version is text that a key signed, and that the plan satisfies them. It does not prove that the signer ran the policies, that the plan is honest, or that a value which is unknown at plan time is acceptable.

### mv and rm

`mv` renames a record. It does not check the new address against the specification, so a plan after a typo destroys the record and creates the resource again.

```sh
pravo state mv port gateway
pravo state list
pravo plan | grep -E '^(Plan|  #)'
```

Output:

```text
Moved port to gateway
base
gateway
  # port (random_integer) will be created
  # gateway (random_integer) will be destroyed
Plan: 1 to add, 0 to change, 1 to destroy.
```

Move it back. `rm` forgets a record without touching the object, and the next plan creates the resource again.

```sh
pravo state mv gateway port
pravo state rm port
pravo plan | grep -E '^(Plan|  #)'
```

Output:

```text
Moved gateway to port
Removed port
  # port (random_integer) will be created
Plan: 1 to add, 0 to change, 0 to destroy.
```

The declarative forms are `$from` for a move and `$import` for an adoption. See [Moves and imports](spec-moves-and-imports.md).

### locks and reservations

An apply locks the nodes it writes until it finishes. A plan with `--reserve` claims nodes for an actor. `locks` lists both, and `unlock` releases either.

```sh
pravo plan --reserve=10m --actor alice | tail -n 1
pravo state locks
pravo state unlock port
pravo state locks
```

Output:

```text
Reserved 1 node until <time>.
port  reserved by alice until <time>: pravo plan --reserve=10m --actor alice
Unlocked port
No locks or reservations held.
```

A running command renews its locks every 2 minutes. The next command reclaims a lock whose process is gone on the same host, or that was not renewed for 10 minutes, and `locks` marks such a lock with `[stale]`. `unlock ADDRESS` releases a lock by hand. `unlock --stale` releases every lock and reservation that may be reclaimed, and no other.

### Another environment

A root can keep one state per environment. Here `$settings.state.path` depends on the tag `env`:

File `env/main.cue`:

```cue
package env

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

env: *"dev" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}

$random: rnd.#Provider

n: $random.#Resource.random_integer & {min: 1, max: 9, seed: "n"}
```

```sh
pravo -C env init > /dev/null
pravo -C env apply -auto-approve -t env=prod > /dev/null
pravo -C env state list
pravo -C env state list -t env=prod
ls env/state
```

Output:

```text
n
prod
```

Without `-t` the subcommands read `state/dev`, which does not exist and is not created. With `-t env=prod` they read the state that the apply wrote.

### pull and push

```sh
pravo state pull backup
ls backup
```

Output:

```text
Copied 10 objects from ./pravo.state to backup
locks
meta.json
outputs.json
resources
txn
```

`pull` and `push` copy the stored plans below `plans/` with the records, the claims on them included. A stored plan that a collection removes during the copy is left out of it. `push` copies a local directory into the configured backend. To move a root to a bucket, add the `state` block to `$settings`, run `pravo state push pravo.state`, and archive the local directory. See [Remote state](../how-to/remote-state.md).

### import-tfstate

`import-tfstate` converts the state file of OpenTofu or legacy Terraform. It accepts format version 4 and does not change the file. Work in a second root.

OpenTofu 1.13.1 wrote the state file below for this HCL. The page does not run OpenTofu and does not print the file.

```hcl
data "terraform_remote_state" "net" {
  backend = "local"

  config = {
    path = "../net/terraform.tfstate"
  }
}

module "net" {
  source = "./modules/net"
}

resource "random_pet" "server" {
  length = 2
}

resource "random_pet" "node" {
  count  = 2
  length = 2
}

output "name" {
  value = random_pet.server.id
}

output "token" {
  value     = "t-${random_pet.server.id}"
  sensitive = true
}
```

File `migrate/main.cue`:

```cue
package migrate
```

```sh
pravo -C migrate init > /dev/null
pravo -C migrate state import-tfstate terraform.tfstate
```

Output:

```text
Imported 4 resources and 2 outputs from terraform.tfstate into ./migrate/pravo.state
Warning: data.terraform_remote_state.net: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)

Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):
  module.net.random_pet.main -> random_pet.net_main
  random_pet.node[0] -> random_pet.node_0
  random_pet.node[1] -> random_pet.node_1
```

Every instance of a managed resource or data source becomes one node and keeps its OpenTofu address. An instance made by `count` or `for_each` is named `<name>_<key>`. A resource in a module gets the module path as a prefix: `module.net.random_pet.main` becomes `random_pet.net_main`. A data source keeps `data.<type>.<name>`. The command lists each renamed instance. Point each node at its place in the specification with `$from` or `pravo state mv`.

The name joins the module path, each instance key and the resource name with `_`. Any character other than an ASCII letter, an ASCII digit, `_` or `-` becomes `_`, so `module.site["eu.west"].random_pet.p` becomes `random_pet.site_eu_west_p`. Two instances with one name stop the command, and nothing is imported: `random_pet.region["eu"]` and `random_pet.region_eu` both become `random_pet.region_eu`.

The dependencies between converted records are kept. OpenTofu writes a dependency on a resource in a module without the key of the module instance. The conversion points it at the record in the same instance of that module, or at every instance when the dependent sits outside the module. A state in another format than 4 stops the command with `unsupported state format version N`.

Outputs are copied with their `sensitive` mark. A sensitive output is recorded as `{"$sensitive": value}`, listed as `(sensitive)` and left out of `pravo.#RemoteOutputs`.

```sh
pravo -C migrate output
```

Output:

```text
name: "free-akita"
token: (sensitive)
```

Deposed objects are skipped with a warning, and a tainted instance is recorded as healthy. A record of the built-in provider, `terraform.io/builtin/terraform`, is skipped with a warning that names its replacement: `pravo.#RemoteOutputs` for `terraform_remote_state`. Without `--force`, the command refuses to replace a state that is not empty.

[Convert the state](../migrate/m5-convert-the-state.md) shows the whole procedure.

## See also

- [State format](state-format.md) describes the files of the state directory.
- [State](../concepts/state.md) explains nodes, transactions and locks.
- [`pravo drift`](why-and-drift.md) reads the records and marks the ones that differ from their objects.
