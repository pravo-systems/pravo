<!-- Generated from docs/reference/glossary.md by tools/skillgen. Do not edit. -->

# Glossary

The terms of CUE and of pravo, each with a definition of one or two sentences and a link to the lesson that teaches it.

A term links to the lesson that teaches it, or to the page that describes it. A link to an entry from another page shows the definition when the pointer rests on it.

| Group | Terms |
|---|---|
| CUE | [attribute](#attribute), [bottom](#bottom), [bound](#bound), [closed](#closed), [comprehension](#comprehension), [concrete](#concrete), [conflict](#conflict), [constraint](#constraint), [default](#default), [definition](#definition), [disjunction](#disjunction), [embedding](#embedding), [evaluation](#evaluation), [field](#field), [hidden field](#hidden-field), [import](#import), [incomplete](#incomplete), [interpolation](#interpolation), [let](#let), [list](#list), [open](#open), [optional field](#optional-field), [package](#package), [pattern](#pattern), [reference](#reference), [regular field](#regular-field), [required field](#required-field), [standard library](#standard-library), [struct](#struct), [tag](#tag), [type](#type), [unification](#unification) |
| pravo | [acknowledgement](#acknowledgement), [actor](#actor), [address](#address), [adoption](#adoption), [approval](#approval), [blast radius](#blast-radius), [call](#call), [collection](#collection), [data source](#data-source), [dependency](#dependency), [discovery](#discovery), [done when](#done-when), [drift](#drift), [ephemeral resource](#ephemeral-resource), [ephemeral value](#ephemeral-value), [generated package](#generated-package), [group](#group), [identity](#identity), [input](#input), [lineage](#lineage), [lock](#lock), [lock file](#lock-file), [mark](#mark), [meta field](#meta-field), [module](#module), [move](#move), [node](#node), [output](#output), [partly stale](#partly-stale), [plan](#plan), [plan document](#plan-document), [plan id](#plan-id), [policy](#policy), [provider](#provider), [reach](#reach), [reader](#reader), [readers file](#readers-file), [refresh age](#refresh-age), [replacement](#replacement), [reservation](#reservation), [resource](#resource), [role](#role), [root](#root), [saved plan](#saved-plan), [scoped plan](#scoped-plan), [sealed state](#sealed-state), [sensitive value](#sensitive-value), [specification](#specification), [state](#state), [stored plan](#stored-plan), [transaction](#transaction), [trust anchor](#trust-anchor), [unknown value](#unknown-value), [write-only attribute](#write-only-attribute) |

## Terms of CUE

### attribute

Metadata attached to a field with `@name(...)`. pravo reads `@tag(name)` to fill a field from `-t name=value`. The generated packages carry `@pravo(...)` marks on sensitive, deprecated and write-only attributes.

Taught in [A6](https://pravo.systems/learn/a06-choices-and-defaults.html). See [Tags and variables](spec-tags-and-variables.md).

### bottom

The result of unifying two values that disagree, written `_|_`. It is an error. CUE reports it as `conflicting values` and lists the position of each declaration.

Taught in [A3](https://pravo.systems/learn/a03-writing-a-field-twice.html). See [C1](../learn/c1-reading-a-cue-error.md).

### bound

A constraint that limits a number or a string by comparison, such as `>=1`, `<65536` or `!=""`. The value `>=1 & <=10` allows the integers 1 to 10 and nothing else.

Taught in [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html).

### closed

A struct that rejects a field it does not declare. Every definition is closed. The attributes of a resource are closed, so a misspelt attribute is `field not allowed`.

Taught in [A8](https://pravo.systems/learn/a08-definitions-reject-typos.html). See [C3](../learn/c3-closed-or-open.md).

### comprehension

A `for` or `if` clause that generates fields or list elements from data. `for r in regions {"\(r)": ...}` makes one field for each region, and pravo finds the resources it produces.

Taught in [A10](https://pravo.systems/learn/a10-comprehensions.html). See [Resources](spec-resources.md#several-resources-from-one-rule).

### concrete

A value that is one definite value, such as `3` or `"dev"`, and not a type or a constraint such as `int` or `>=1`. `pravo eval -c` fails unless every value is concrete.

Taught in [A2](https://pravo.systems/learn/a02-a-value-can-be-a-type.html). See [C2](../learn/c2-why-is-my-value-incomplete.md).

### conflict

Two values that cannot be unified, such as `3` and `11` on one field. The error is `conflicting values 11 and 3`, followed by the positions of both declarations.

Taught in [A3](https://pravo.systems/learn/a03-writing-a-field-twice.html). See [Error catalogue](errors.md#evaluating-cue).

### constraint

A value that allows some values and rejects the rest, such as `string`, `>=1` or `=~"^eu-"`. Unifying a constraint with a value keeps the value when it fits, and is an error when it does not.

Taught in [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html).

### default

The alternative of a disjunction that is marked with `*`, as in `*"dev" | "prod"`. It is chosen when nothing else decides the field.

Taught in [A6](https://pravo.systems/learn/a06-choices-and-defaults.html).

### definition

A struct whose name starts with `#`, such as `#Service`. A definition is closed, and it declares nothing until a regular field instantiates it. A provider package is built from definitions.

Taught in [A8](https://pravo.systems/learn/a08-definitions-reject-typos.html). See [Generated packages](spec-generated-packages.md).

### disjunction

A choice between values written with `|`, such as `"dev" | "staging" | "prod"`. A value must match at least one alternative.

Taught in [A6](https://pravo.systems/learn/a06-choices-and-defaults.html).

### embedding

Writing a struct or a definition as a line of its own inside another struct, so that its fields become fields of the outer one. `pravo.#Movable` is embedded in a definition so that its instances accept `$from`.

Taught in [A8](https://pravo.systems/learn/a08-definitions-reject-typos.html). See [Moves and imports](spec-moves-and-imports.md#from-on-a-group).

### evaluation

Resolving references, expanding comprehensions, unifying values and selecting defaults until one value is left. `pravo eval` prints the result, and `pravo validate` checks it.

See [C4](../learn/c4-eval-and-validate.md). See [Evaluation](../concepts/evaluation.md).

### field

A label and a value, written `name: value`. A field written twice is one field, and its value is the unification of both.

Taught in [A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html).

### hidden field

A field whose name starts with `_`. It is not exported: `pravo eval` does not print it, pravo does not search it for resources, and it never reaches a provider. Files of one package see each other's hidden fields.

Taught in [A11](https://pravo.systems/learn/a11-references-let-hidden.html).

### import

A line that makes the names of another package available, such as `import "strings"`. An import that nothing uses is an error. The generated provider packages are imported the same way.

Taught in [A13](https://pravo.systems/learn/a13-packages-and-the-standard-library.html).

### incomplete

A value that is not concrete yet and that a command needs to be: a type with no value, or a field that no tag has filled. `pravo eval -c` reports `incomplete value string`.

See [C2](../learn/c2-why-is-my-value-incomplete.md).

### interpolation

A string that embeds expressions, written `"\(expression)"`, as in `"Hello, \(pet.id)!"`. The result is a string once every embedded value is concrete.

Taught in [A12](https://pravo.systems/learn/a12-conditions-and-strings.html).

### let

A name for a value that exists only inside one struct, written `let prefix = "acme"`. A `let` is not a field, so it is not exported.

Taught in [A11](https://pravo.systems/learn/a11-references-let-hidden.html).

### list

An ordered sequence of values in brackets, such as `["a", "b"]`. A comprehension can build one, and a list of resources has the addresses `servers[0]`, `servers[1]`.

Taught in [A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html).

### open

A struct that accepts fields it does not declare. The struct literals you write are open. A struct is closed only when it is a definition or when `close()` makes it so.

See [C3](../learn/c3-closed-or-open.md).

### optional field

A field that may stay absent, written `name?: type`. `pravo eval` hides it until it has a value, and `-O` prints it. An absent optional attribute of a resource is sent to the provider as null.

Taught in [A7](https://pravo.systems/learn/a07-required-optional-regular.html).

### package

The files of one directory that begin with the same `package` clause, unified into one value. A root is one package. A file with no package clause, or with another one, is an error.

Taught in [A13](https://pravo.systems/learn/a13-packages-and-the-standard-library.html). See [The specification](spec-overview.md#what-makes-a-root).

### pattern

A constraint that applies to every field whose label matches, written `[string]: ...` or `[=~"^web"]: ...`. A pattern declares no field of its own.

Taught in [A9](https://pravo.systems/learn/a09-patterns.html).

### reference

A name that stands for the value of another field, such as `pet.id`. A reference from one resource to another is a dependency.

Taught in [A11](https://pravo.systems/learn/a11-references-let-hidden.html). See [Resources](spec-resources.md#references).

### regular field

A field with no mark: `name: value`. It is exported, and it may be referenced before its value is known.

Taught in [A7](https://pravo.systems/learn/a07-required-optional-regular.html).

### required field

A field written `name!: type`. Someone must supply a concrete value, and `pravo eval -c` names the field when nobody does.

Taught in [A7](https://pravo.systems/learn/a07-required-optional-regular.html).

### standard library

The packages that ship with CUE, such as `strings`, `list`, `math`, `net` and `encoding/json`. A specification imports them by name, and a policy may import them and nothing else.

Taught in [A13](https://pravo.systems/learn/a13-packages-and-the-standard-library.html).

### struct

A set of fields in braces, `{name: "api", port: 8080}`. The whole specification is one struct, and so is every resource.

Taught in [A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html).

### tag

A field filled from the command line. `env: *"dev" | "prod" @tag(env)` takes its value from `-t env=prod`. A tag value is written into saved plans, so it is not a place for a secret.

Taught in [A6](https://pravo.systems/learn/a06-choices-and-defaults.html) and [B4](../learn/b04-inputs-tags-environments.md). See [Tags and variables](spec-tags-and-variables.md).

### type

In CUE a type is a value: `int` is the set of all integers, and `30` is one of them. Unifying a type with a value gives the value when it fits.

Taught in [A2](https://pravo.systems/learn/a02-a-value-can-be-a-type.html).

### unification

Combining two values into one that satisfies both. CUE unifies every field that is written more than once, and `&` unifies two values explicitly. Disagreeing values give an error.

Taught in [A3](https://pravo.systems/learn/a03-writing-a-field-twice.html).

## Terms of pravo

### acknowledgement

An entry of the `$ack` list of a [discovery](#discovery), in the specification, that says that the objects that match its patterns are known and are meant to stay unmanaged. They are still listed, and they stop asking to be adopted. The viewer writes one for an object.

See [Discovery](../concepts/discovery.md#acknowledging).

### actor

A label for who runs a command: a person, a job or an agent. It is set with `--actor` or `PRAVO_ACTOR` and recorded on transactions, locks, reservations and plans. pravo does not verify it.

See [Global flags](global-flags.md).

### address

The CUE path at which a resource sits in the specification, such as `pet`, `api.config` or `servers[0]`. `pravo state list` prints addresses, and `--replace`, `why` and `state show` take them.

Taught in [B1](../learn/b01-providers-and-resources.md). See [Resources](spec-resources.md#addresses).

### adoption

Bringing an object that already exists under management. `$import: "<id>"` or `$import: {...identity}` on a resource does it in the specification, and `pravo import` does it once from the command line.

Taught in [B8](../learn/b08-renames-moves-imports.md). See [Moves and imports](spec-moves-and-imports.md#import).

### approval

A signature of a [stored plan](#stored-plan), made with `pravo plan approve` by a [reader](#reader) whose [role](#role) suffices. A [policy](#policy) says how many approvals a plan needs before it is applied and who may give them. The key that stored the plan does not count unless the policy allows it, and an admin applies without them.

Taught in [D6](../learn/d06-ask-for-approvals.md). See [Stored plans and approvals](../concepts/stored-plans-and-approvals.md).

### blast radius

The number of distinct nodes that have a pending change, together with every node that depends on one of them. It is `summary.blast_radius` in the output of `plan --json`. A policy does not see it, because the judged document is built from the plan file alone.

See [The plan document](plan-document.md#summary-and-stats).

### call

A provider-defined function declared as a struct built from `#Function.<name>`. Its `args` are the arguments and its `result` is filled in when the call has run. A call leaves no record in state.

See [Resources](spec-resources.md#function-calls).

### collection

The removal of the stored plans that nobody needs. `pravo plan gc` runs one, and `pravo plan --store` runs a small one on its way. It removes a plan that expired, a plan that was applied completely a day ago and a plan that a collection saw stale more than an hour ago. It leaves a [partly stale](#partly-stale) plan to its time to live. It never removes a plan that an apply is running from.

See [`pravo plan`](plan.md#collect-stored-plans).

### data source

An object that pravo reads and does not manage, built from `#Data.<type>`. It is read during the plan when its configuration is known, and during the apply otherwise.

See [Resources](spec-resources.md#data-sources).

### dependency

An edge of the state graph. A resource depends on another when it refers to it, or lists it in `$depends_on`. The dependencies decide the order of an apply and the blast radius of a change.

See [Meta fields](spec-meta-fields.md#depends-on).

### discovery

A struct built from `#Discovery.<type>` of a provider that can list: a list resource type with the scope to list it in. `pravo discover` runs it, and the objects it finds that no record accounts for are shown for adoption. It manages nothing and is not a node of the graph.

See [Discovery](../concepts/discovery.md) and [`pravo discover`](discover.md).

### done when

The line of a lesson header that says what you see when the lesson is complete. It names an observable result, such as an output or an exit code.

See [Learn](https://pravo.systems/learn/index.html).

### drift

A difference between a record in the state and the real object, caused by a change made outside pravo. `pravo drift` reads every resource and reports it, with exit code 2, and a read that finds it leaves a [mark](#mark) on the record.

Taught in [B9](../learn/b09-drift.md). See [Exit codes](exit-codes.md).

### ephemeral resource

A provider object that is opened for one run and never written to state: a short-lived credential, a token, a tunnel. It is built from `#Ephemeral.<type>`.

See [Resources](spec-resources.md#ephemeral-resources).

### ephemeral value

A value of an ephemeral resource or of `pravo.#SecretEnv`. It may be used in provider configuration, in write-only attributes and in other ephemeral resources, and nowhere that is recorded.

Taught in [B6](../learn/b06-modules-providers-credentials.md). See [Environment](spec-env.md#secret-env).

### generated package

A CUE package that `pravo init` writes under `cue.mod/gen/`: one for each provider, and `pravo.dev/pravo`. A specification imports them to build resources.

See [Generated packages](spec-generated-packages.md).

### group

In the viewer, a struct of the specification that holds resources: an instance of a definition, an instance of a package or a plain struct. Every proper prefix of a node's address that holds a resource below it is a group. It is not a [module](#module), which needs a definition or a package that takes providers as inputs.

See [The viewer](../concepts/the-viewer.md#groups).

### identity

A name, a signing key and a reader key, kept in a file that `pravo id new` writes. The reader key opens a [sealed state](#sealed-state), and the signing key signs the [transactions](protect.md#signatures) the identity runs. The reader key is sealed under a passphrase, or under a key that the ssh-agent derives (`pravo id new --unlock agent`). An identity for a CI job has no passphrase and is read from `PRAVO_IDENTITY`.

See [pravo id and pravo protect](protect.md#the-identity).

### input

A resource or data source that another resource reads. A plan looks at the inputs of the resources it reaches and reads their records when they are older than their refresh age. An apply takes an input from its record.

See [Plans](../concepts/plans.md#scoped-plans).

### lineage

An identifier chosen when a state store is created. A saved plan records it, and the plan is refused against a store with another lineage.

See [State format](state-format.md#metajson).

### lock

A file that a running command holds on a node while it changes it. Another command that needs the node is rejected with exit code 4. A lock that is not renewed for 10 minutes is reclaimed.

Taught in [Lab 2](https://pravo.systems/learn/lab2-two-applies-at-once.html). See [State format](state-format.md#locks-and-reservations).

### lock file

`pravo.lock.cue`, written by `pravo init`. It holds the version of each provider and the hash of each archive, and the modules a root loads. Commit it.

See [Compatibility](compatibility.md#providers-and-registries).

### mark

A field of a record that says a read found the object different from the record, `drifted_at`, or gone, `gone`. Every plan, saved plan and apply plans a resource with a mark, until an apply writes the resource.

See [Plans](../concepts/plans.md#refresh-age) and [State format](state-format.md#a-node).

### meta field

A field that starts with `$` and is not part of a provider schema: `$depends_on`, `$lifecycle`, `$from` and `$import`. It cannot collide with an attribute.

See [Meta fields](spec-meta-fields.md).

### module

In CUE, the tree under a `cue.mod/module.cue` file, whose path begins every import path. In pravo, a definition or a package that contains resources and takes its providers as inputs.

Taught in [B6](../learn/b06-modules-providers-credentials.md). See [Modules](spec-modules.md).

### move

Keeping the record of a resource when its address changes, by writing the old address in `$from`. The plan lists the rename, the apply makes it, and the object is left alone.

Taught in [B8](../learn/b08-renames-moves-imports.md). See [Moves and imports](spec-moves-and-imports.md).

### node

One record of the state graph: a resource or a data source with its attributes, dependencies, revision and times. It is one file, written on its own.

See [State format](state-format.md#a-node).

### output

A value that the root publishes, built from `pravo.#Output`. It is recorded in state when a plan is applied and printed by `pravo output`.

Taught in [B3](../learn/b03-apply-outputs-state.md). See [Outputs](spec-outputs.md).

### partly stale

The status of a stored plan when the state moved on under some of its changes that are not applied yet, and the other changes can still be applied. The changes that conflict are listed with their reasons, and `pravo apply ID --only` with the set that the listing prints applies the others. The plan as a whole is refused with exit code 3.

See [`pravo apply`](apply.md#a-partly-stale-plan) and [`pravo plan`](plan.md#list-stored-plans).

### plan

The list of actions that an apply takes, printed before anything changes: create, update, replace, delete, read. It is a diff with a legend.

Taught in [B2](../learn/b02-reading-a-plan.md). See [Plans](../concepts/plans.md).

### plan document

The JSON that `pravo plan --json` prints, and that a plan policy is judged against. It lists every change with its action, its before and after values and its reasons.

See [The plan document](plan-document.md).

### plan id

`plan-` and the first eight hexadecimal digits of the SHA-256 digest of a stored plan. The first six digits are enough when no other plan starts with them. `pravo plan --store` prints it, and `pravo apply` and `pravo show` take it.

See [State format](state-format.md#plans).

### policy

A `pravo.#Policy` node of the specification whose rule is CUE text that a plan must satisfy. pravo keeps each version in the state, signed, and the signed start of an apply lists the versions that judged it. A specification cannot exempt a resource from it. A refusal is exit code 5.

Taught in [B10](../learn/b10-policy.md). See [Policies](policies-overview.md).

### provider

A struct built from the `#Provider` definition of a generated package. It tells pravo which OpenTofu provider to run, at which version, and with which configuration. Resources are built through it.

Taught in [B1](../learn/b01-providers-and-resources.md). See [Providers](spec-providers.md).

### reach

What a change touches: the resource that changed and the resources whose configuration changed because of it. A plan plans what it reaches, and `pravo why` lists what a change reaches.

See [Plans](../concepts/plans.md#scoped-plans).

### reader

A person or a job that can open the values of a [sealed state](#sealed-state). A reader is a line of the [readers file](#readers-file) and has a lock for each data key of the state.

See [Signatures and roles](../concepts/signatures-and-roles.md).

### readers file

`.pravo/access/readers.txt` in the repository: one line for each [reader](#reader), as `pravo id new` prints it. Writers take the list of readers from it and never from the state.

See [pravo id and pravo protect](protect.md#the-readers-file).

### refresh age

How old a record may be before a plan that looks at the resource reads it from its provider again. A record is within its refresh age while its age is no greater than the refresh age, and older than it once its age is greater. `always` is an age of zero, `never` is no limit, and `"30m"` is a duration. It is set per root and per resource. [Reach](#reach) decides what a plan looks at; the refresh age decides whether a record that it looks at is read.

See [Settings](spec-settings.md#refresh) and [Meta fields](spec-meta-fields.md#refresh).

### replacement

A change that destroys an object and creates it again, because the provider cannot change an attribute in place. The plan marks it `-/+` and `# forces replacement`, and the action is `replace`.

Taught in [B2](../learn/b02-reading-a-plan.md).

### reservation

A claim on the nodes of a plan, taken with `plan --reserve`. Other actors cannot plan or apply those nodes until it expires. It needs no running process.

See [State format](state-format.md#locks-and-reservations).

### resource

A struct built through a provider from `#Resource.<type>`. pravo creates, updates and destroys the object it describes, and records it in state. Its address is its path.

Taught in [B1](../learn/b01-providers-and-resources.md). See [Resources](spec-resources.md).

### role

What a key on the [readers file](#readers-file) may do with a [sealed state](#sealed-state). A reader opens the values. A writer applies and stores plans as well. An admin changes the readers file, pins the roles and removes a policy that asks for approvals as well. The roles are pinned in the state, signed by an admin, so a checkout cannot promote its own key by editing its readers file.

Taught in [D4](../learn/d04-readers-writers-admins.md). See [Roles](roles.md).

### root

A directory that holds one CUE package, and the unit that pravo plans, applies and keeps a state for. Its location is where you run pravo, or what `-C` names.

See [The specification](spec-overview.md).

### saved plan

A plan written with `plan --out` and applied later with `apply <file>`. It is applied as it was made or not at all, and it is refused when the configuration, a provider or a node has changed since.

Taught in [B7](../learn/b07-changing-live-infrastructure.md). See [The plan document](plan-document.md#the-saved-plan-file).

### scoped plan

A plan that plans only what a change [reaches](#reach), and reads only the records of those resources and of their [inputs](#input). A resource whose configuration hash and provider versions are unchanged is reused from the state without being validated, planned or read. The `Graph:` line counts them.

See [Plans](../concepts/plans.md).

### sealed state

A state whose attributes, outputs, stored plans and the values in command lines are encrypted to a list of [readers](#reader). Addresses, types, names, dependencies and revisions stay readable. It is format 3.

See [Signatures and roles](../concepts/signatures-and-roles.md).

### sensitive value

A value that a plan hides as `(sensitive)`. It is still recorded in state as it is, and the plan document shows it, unless the state is [sealed](#sealed-state). Protect the state as you protect any other secret store.

See [Outputs](spec-outputs.md#sensitive-outputs).

### specification

The `.cue` files of a root, unified into one value. It declares providers, resources and outputs, and the inputs they take. Everything pravo does starts from it.

See [The specification](spec-overview.md).

### state

The record of what pravo manages: a graph of nodes in a directory or a bucket, with outputs and a log of transactions. There is no state file and no global lock.

Taught in [B3](../learn/b03-apply-outputs-state.md). See [State format](state-format.md).

### stored plan

A plan that `pravo plan --store` wrote into the state, as objects below `plans/`. Anyone who can read the state can list it, show it and apply it by its [plan id](#plan-id). A listing judges it by the state alone, change by change, and a [collection](#collection) removes it when nobody needs it. An apply that fails records the changes it finished, and the plan is `partial`.

Taught in [D2](../learn/d02-store-a-plan.md). See [Store a plan and apply it later](../how-to/store-a-plan-and-apply-it-later.md) and [Stored plans and approvals](../concepts/stored-plans-and-approvals.md).

### transaction

The record of one plan that touched a record, or of one apply, destroy, import or state edit. It holds the command line, the actor, the outcome and the revision of each node it touched.

See [State format](state-format.md#transactions).

### trust anchor

The rights that decide what a policy guarantees: write access to the policy source, to the job definition and environment, and to the state store, and the cloud credentials. pravo cannot see who holds them.

See [Policies](../concepts/policies.md#the-trust-anchor).

### unknown value

A value that is not known until the apply, such as the `id` of a resource that does not exist yet. The plan prints `(known after apply)`, and the plan document holds `{"$unknown": true}`.

Taught in [B2](../learn/b02-reading-a-plan.md). See [The plan document](policies-plan-document.md#absent-null-and-unknown).

### viewer

The read-only web page that `pravo serve` runs on the loopback interface. It shows the specification, the state and a saved plan as a graph of [groups](#group) and resources. A node's presence says whether it is declared, recorded or both.

See [The viewer](../concepts/the-viewer.md) and [`pravo serve`](serve.md).

### write-only attribute

An attribute that a provider accepts and never stores, marked `@pravo(write_only)` in the generated definitions. It is the one kind of attribute that may take an ephemeral value.

See [Generated packages](spec-generated-packages.md#schema-mapping).
