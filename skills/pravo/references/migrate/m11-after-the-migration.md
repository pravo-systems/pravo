<!-- Generated from docs/migrate/m11-after-the-migration.md by tools/skillgen. Do not edit. -->

# After the migration

Rewrite the faithful translation in the idiom of CUE, one idea per commit, and let a policy confirm that no commit changes an object.

The translation of [M4](m4-translate-the-specification.md) keeps the shape of the HCL. That is what made it checkable against the old state. It is not yet written the way CUE is written.

Rewrite it after the cut over, when the plan is empty and the pipeline runs pravo. Faithful first, idiomatic second, and never both in one commit. A commit that refactors changes no object, so its plan is empty or moves records and does nothing else.

## The rule for every commit

Let pravo enforce it. This policy refuses any plan that creates, updates, replaces or deletes a managed resource. A plan of no-ops and moves satisfies it.

File `policy/refactor.cue`:

```cue
// A refactor changes no object. It may move a record.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && action != "no-op" && action != "move" {
		error("\(addr): \(action) is not part of a refactor")
	}
}]
```

The workspace holds the translation of M4. Apply it, so that the estate exists and the commits have objects to leave alone.

```sh
pravo init > /dev/null
pravo apply --auto-approve 2>&1 | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 8 added, 0 changed, 0 destroyed. [txn <txn>]
```

Commit this state first, so that every later commit has a base to compare with.

```sh
git add -A
git commit -m "Translate the estate from OpenTofu"
```

## Commit 1. Constrain the inputs

Idea: a constraint is a type. A name that does not match is refused when the specification evaluates, before any provider starts. Lesson: [A5 Constraints narrow a value](https://pravo.systems/learn/a05-constraints-narrow-a-value.html).

The module takes a list of note names. Only lowercase letters make a file name that the estate wants.

File `notes.cue`:

```cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

#Notes: {
	provider!: loc.#Provider
	dir:       string
	names:     [...(string & =~"^[a-z]+$")]

	files: {
		for n in names {
			"\(n)": provider.#Resource.local_file & {
				filename: "\(dir)/\(n).txt"
				content:  "\(n)\n"
			}
		}
	}
	paths: [for n in names {files[n].filename}]
}
```

```sh
pravo plan --policy policy/refactor.cue
```

Output:

```text
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

The plan is empty and the policy holds. A value outside the constraint is refused:

File `instances.cue`:

```cue
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "Beta"]
}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
notes.names.1: invalid value "Beta" (out of bound =~"^[a-z]+$"):
    ./notes.cue:8:27
    ./instances.cue:6:22
    ./notes.cue:8:18
```

The path is `notes.names.1`, the value is `"Beta"`, and the bound is the pattern. Restore the name and commit.

File `instances.cue`:

```cue
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

```sh
git commit -am "Constrain the names of notes to lowercase letters"
```

## Commit 2. One definition for the pets

Idea: a repeated shape is a definition. `server` and every `region` are the same resource with a different prefix. Lessons: [A8 Definitions](https://pravo.systems/learn/a08-definitions-reject-typos.html) and [A6 Choices and defaults](https://pravo.systems/learn/a06-choices-and-defaults.html).

`#Pet` is written beside the provider it is built through. The `length` is a default, so a caller can still change it.

File `resources.cue`:

```cue
package estate

banner: $local.#Data.local_file & {filename: "banner.txt"}

#Pet: $random.#Resource.random_pet & {length: *2 | int}

server: #Pet & {prefix: _prefix}

hello: $local.#Resource.local_file & {
	filename: "out/hello.txt"
	content:  "\(banner.content)Hello, \(server.id)!\n"
}
```

File `repeat.cue`:

```cue
package estate

import "list"

ports: [
	for i in list.Range(0, 2, 1) {
		$random.#Resource.random_integer & {min: 8000, max: 8999}
	},
]

region: {
	for r in regions {
		"\(r)": #Pet & {prefix: r}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: #Pet & {length: 1}
}
```

```sh
pravo plan --policy policy/refactor.cue
```

Output:

```text
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git commit -am "Build the pets from one definition"
```

## Commit 3. Keys instead of indexes

Idea: a name outlives a position. `ports[0]` and `ports[1]` are identified by their place in a list, and a reader cannot tell what they are for. A struct keyed by role makes the address say it. Lesson: [A10 Generating fields](https://pravo.systems/learn/a10-comprehensions.html).

Changing an address is a move. `$from` names the old address, and the record follows. [B8](../learn/b08-renames-moves-imports.md) explains the field.

File `repeat.cue`:

```cue
package estate

_ports: ["http", "admin"]

port: {
	for i, n in _ports {
		"\(n)": $random.#Resource.random_integer & {
			$from: "ports[\(i)]"
			min:   8000
			max:   8999
		}
	}
}

region: {
	for r in regions {
		"\(r)": #Pet & {prefix: r}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: #Pet & {length: 1}
}
```

Three references to the old list follow the new names.

File `hello.cue`:

```cue
package estate

hello: {
	$depends_on: [port.http, port.admin]
	$lifecycle: ignore_changes: ["file_permission"]
}
```

File `outputs.cue`:

```cue
package estate

import "pravo.dev/pravo"

server_name: pravo.#Output & {value: server.id}
note_paths:  pravo.#Output & {value: notes.paths}

port_numbers: pravo.#Output & {
	value:     [for n in _ports {port[n].result}]
	sensitive: true
}
```

```sh
pravo plan --policy policy/refactor.cue
```

Output:

```text
pravo will perform the following actions:

  # ports[0] will be moved to port.http (record renamed; no change to the object)

  # ports[1] will be moved to port.admin (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 2 to move.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8 · to move 2

Outputs: 3 outputs unchanged.
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

Both records will move, and the objects do not change. The plan wrote nothing: the records keep their old addresses until you apply. The output iterates the list of names, not the struct. The values keep the order you wrote. Apply the plan, so that the moves are final, and plan again:

```sh
pravo apply --auto-approve 2>&1 | grep '^Apply complete'
pravo plan 2>&1 | grep '^No changes'
pravo state list
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
banner
hello
notes.files.alpha
notes.files.beta
port.admin
port.http
region.eu
region.us
server
```

```sh
git commit -am "Key the ports by role"
```

## Commit 4. Remove the spent moves

The `$from` fields did their work. A later reader asks what `ports[0]` was. Remove them in a commit of their own.

File `repeat.cue`:

```cue
package estate

_ports: ["http", "admin"]

port: {
	for n in _ports {
		"\(n)": $random.#Resource.random_integer & {min: 8000, max: 8999}
	}
}

region: {
	for r in regions {
		"\(r)": #Pet & {prefix: r}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: #Pet & {length: 1}
}
```

```sh
pravo plan --policy policy/refactor.cue
```

Output:

```text
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git commit -am "Remove the moves that have been applied"
```

## Commit 5. A package for the module

Idea: a definition that other roots can use is a package. The root imports it, and its resources keep their addresses. Lesson: [A13 Packages and the standard library](https://pravo.systems/learn/a13-packages-and-the-standard-library.html).

The module path comes from `cue.mod/module.cue`. The package takes its provider as an input, as before.

File `notes/notes.cue`:

```cue
package notes

import loc "pravo.dev/providers/hashicorp/local"

#Notes: {
	provider!: loc.#Provider
	dir:       string
	names:     [...(string & =~"^[a-z]+$")]

	files: {
		for n in names {
			"\(n)": provider.#Resource.local_file & {
				filename: "\(dir)/\(n).txt"
				content:  "\(n)\n"
			}
		}
	}
	paths: [for n in names {files[n].filename}]
}
```

File `instances.cue`:

```cue
package estate

import nt "example.com/estate/notes"

notes: nt.#Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

The alias `nt` differs from the field `notes`. A name in a scope refers to the nearest field of that name.

```sh
rm notes.cue
pravo validate
pravo plan --policy policy/refactor.cue
```

Output:

```text
Success! 8 resources, 1 data sources, 0 ephemeral, 0 function calls, 3 outputs across 2 providers.

No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 8 · refreshed 1 · not read 8

Outputs: 3 outputs unchanged.
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git add -A
git commit -m "Move the notes module into a package"
```

## What to do next

Each of these is one more commit with the same check.

- Relax the version pins of [M4](m4-translate-the-specification.md#step-1-providers) to ranges, run `pravo init --upgrade`, and read the plan. A provider upgrade is a change of its own, so it gets its own commit and its own review.
- Write a rule for every entry once with a pattern, [A9 Patterns](https://pravo.systems/learn/a09-patterns.html).
- Replace a repeated expression by a hidden field or a `let`, [A11 References, let and hidden fields](https://pravo.systems/learn/a11-references-let-hidden.html).
- Replace a flag pair by a condition, [A12 Conditions and strings](https://pravo.systems/learn/a12-conditions-and-strings.html).
- Put the rules of the platform on the provider definition, so that every resource built through it obeys, [B10 Policy](../learn/b10-policy.md).
- Build the three environments from one definition, [A14 Project](https://pravo.systems/learn/a14-project-three-environments.html).

## Recap

- Rewrite after the cut over, one idea per commit, and keep refactors apart from changes in behaviour.
- A refactor commit leaves the plan empty or moving records only. A policy turns that rule into a check.
- A new address is a move: `$from` names the old one, and it can go in a later commit.

Next: [How-to](https://pravo.systems/how-to/index.html) has the task-shaped pages for what you do every week, such as remote state, CI and policies.
