---
title: After the migration
description: Rewrite the faithful translation in the idiom of CUE, one idea per commit, and let a policy confirm that no commit changes an object.
order: 11
group: Runbook
gate: Each commit leaves the plan empty
minutes: 30
needs: "Step 7 done: [cut over](m7-cut-over.md)."
learn: How do you make a translated specification idiomatic without changing what it manages?
done_when: "Four commits have each left `pravo plan` empty or moving records only."
level: intermediate
normalize:
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|Reading\.\.\.|Read complete|moved to )[^\n]*\n'
    replace: ""
  - pattern: "\\[txn [0-9TZ]+-[0-9a-f]+\\]"
    replace: "[txn <txn>]"
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
  - pattern: "after [0-9]+s"
    replace: "after 0s"
  - pattern: 'server_name: "acme-dev-[a-z]+-[a-z]+"'
    replace: 'server_name: "acme-dev-<pet>"'
---

The translation of [M4](m4-translate-the-specification.md) keeps the shape of the HCL. That is what made it checkable against the old state. It is not yet written the way CUE is written.

Rewrite it after the cut over, when the plan is empty and the pipeline runs pravo. Faithful first, idiomatic second, and never both in one commit. A commit that refactors changes no object, so its plan is empty or moves records and does nothing else.

## The rule for every commit

Let pravo enforce it. This policy refuses any plan that creates, updates, replaces or deletes a managed resource. A plan of no-ops and moves satisfies it.

```cue file=policy/refactor.cue title="policy/refactor.cue"
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

```cue file=cue.mod/module.cue hidden
module: "example.com/estate@v0"
language: version: "v0.14.0"
```

```cue file=providers.cue hidden
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random:  rnd.#Provider & {$version: "~> 3.6"}
$local:   loc.#Provider & {$version: "~> 2.5"}
$archive: loc.#Provider
```

```cue file=variables.cue hidden
package estate

env:     *"dev" | "staging" | "prod" @tag(env)
regions: [...string] | *["eu", "us"]
```

```cue file=locals.cue hidden
package estate

_prefix: "acme-\(env)"
```

```cue file=resources.cue hidden
package estate

banner: $local.#Data.local_file & {filename: "banner.txt"}

server: $random.#Resource.random_pet & {
	prefix: _prefix
	length: 2
}

hello: $local.#Resource.local_file & {
	filename: "out/hello.txt"
	content:  "\(banner.content)Hello, \(server.id)!\n"
}
```

```cue file=hello.cue hidden
package estate

hello: {
	$depends_on: [ports[0], ports[1]]
	$lifecycle: ignore_changes: ["file_permission"]
}
```

```cue file=repeat.cue hidden
package estate

import "list"

ports: [
	for i in list.Range(0, 2, 1) {
		$random.#Resource.random_integer & {min: 8000, max: 8999}
	},
]

region: {
	for r in regions {
		"\(r)": $random.#Resource.random_pet & {prefix: r, length: 2}
	}
}

create_extra: *false | bool @tag(extra, type=bool)

if create_extra {
	extra: $random.#Resource.random_pet & {length: 1}
}
```

```cue file=notes.cue hidden
package estate

import loc "pravo.dev/providers/hashicorp/local"

#Notes: {
	provider!: loc.#Provider
	dir:       string
	names:     [...string]

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

```cue file=instances.cue hidden
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

```cue file=outputs.cue hidden
package estate

import "pravo.dev/pravo"

server_name: pravo.#Output & {value: server.id}
note_paths:  pravo.#Output & {value: notes.paths}

port_numbers: pravo.#Output & {
	value:     [for p in ports {p.result}]
	sensitive: true
}
```

```text file=banner.txt hidden
estate
```


```sh run
pravo init > /dev/null
pravo apply --auto-approve 2>&1 | grep '^Apply complete'
```

```text out
Apply complete! Resources: 8 added, 0 changed, 0 destroyed. [txn <txn>]
```

Commit this state first, so that every later commit has a base to compare with.

```sh
git add -A
git commit -m "Translate the estate from OpenTofu"
```

## Commit 1. Constrain the inputs

Idea: a constraint is a type. A name that does not match is refused when the specification evaluates, before any provider starts. Lesson: [A5 Constraints narrow a value](../learn/a05-constraints-narrow-a-value.md).

The module takes a list of note names. Only lowercase letters make a file name that the estate wants.

```cue file=notes.cue title="notes.cue" hl="8"
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

```sh run
pravo plan --policy policy/refactor.cue
```

```text out
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 1 of 9, 8 unchanged and skipped, 1 refreshed, 8 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-<pet>"
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

The plan is empty and the policy holds. A value outside the constraint is refused:

```cue file=instances.cue title="instances.cue" status=conflict hl="6"
package estate

notes: #Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "Beta"]
}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
notes.names.1: invalid value "Beta" (out of bound =~"^[a-z]+$"):
    ./notes.cue:8:27
    ./instances.cue:6:22
    ./notes.cue:8:18
```

The path is `notes.names.1`, the value is `"Beta"`, and the bound is the pattern. Restore the name and commit.

```cue file=instances.cue title="instances.cue"
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

Idea: a repeated shape is a definition. `server` and every `region` are the same resource with a different prefix. Lessons: [A8 Definitions](../learn/a08-definitions-reject-typos.md) and [A6 Choices and defaults](../learn/a06-choices-and-defaults.md).

`#Pet` is written beside the provider it is built through. The `length` is a default, so a caller can still change it.

```cue file=resources.cue title="resources.cue" hl="5,7"
package estate

banner: $local.#Data.local_file & {filename: "banner.txt"}

#Pet: $random.#Resource.random_pet & {length: *2 | int}

server: #Pet & {prefix: _prefix}

hello: $local.#Resource.local_file & {
	filename: "out/hello.txt"
	content:  "\(banner.content)Hello, \(server.id)!\n"
}
```

```cue file=repeat.cue title="repeat.cue" hl="13,20"
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

```sh run
pravo plan --policy policy/refactor.cue
```

```text out
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 1 of 9, 8 unchanged and skipped, 1 refreshed, 8 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-<pet>"
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git commit -am "Build the pets from one definition"
```

## Commit 3. Keys instead of indexes

Idea: a name outlives a position. `ports[0]` and `ports[1]` are identified by their place in a list, and a reader cannot tell what they are for. A struct keyed by role makes the address say it. Lesson: [A10 Generating fields](../learn/a10-comprehensions.md).

Changing an address is a move. `$from` names the old address, and the record follows. [B8](../learn/b08-renames-moves-imports.md) explains the field.

```cue file=repeat.cue title="repeat.cue" hl="3-13"
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

```cue file=hello.cue title="hello.cue" hl="4"
package estate

hello: {
	$depends_on: [port.http, port.admin]
	$lifecycle: ignore_changes: ["file_permission"]
}
```

```cue file=outputs.cue title="outputs.cue" hl="9"
package estate

import "pravo.dev/pravo"

server_name: pravo.#Output & {value: server.id}
note_paths:  pravo.#Output & {value: notes.paths}

port_numbers: pravo.#Output & {
	value:     [for n in _ports {port[n].result}]
	sensitive: true
}
```

```sh run
pravo plan --policy policy/refactor.cue
```

```text out
pravo will perform the following actions:

  # ports[0] was moved to port.http (record renamed; no change to the object)

  # ports[1] was moved to port.admin (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 9, 8 unchanged and skipped, 1 refreshed, 8 not read, 2 moved.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-<pet>"
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

Both records move, and the objects do not change. The output iterates the list of names, not the struct. The values keep the order you wrote. Apply the plan, so that the moves are final, and plan again:

```sh run
pravo apply --auto-approve 2>&1 | grep -E '^(No changes|Apply complete)'
pravo state list
```

```text out
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

```cue file=repeat.cue title="repeat.cue" hl="5-9"
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

```sh run
pravo plan --policy policy/refactor.cue
```

```text out
No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 1 of 9, 8 unchanged and skipped, 1 refreshed, 8 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-<pet>"
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git commit -am "Remove the moves that have been applied"
```

## Commit 5. A package for the module

Idea: a definition that other roots can use is a package. The root imports it, and its resources keep their addresses. Lesson: [A13 Packages and the standard library](../learn/a13-packages-and-the-standard-library.md).

The module path comes from `cue.mod/module.cue`. The package takes its provider as an input, as before.

```cue file=notes/notes.cue title="notes/notes.cue"
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

```cue file=instances.cue title="instances.cue" hl="3,5"
package estate

import nt "example.com/estate/notes"

notes: nt.#Notes & {
	provider: $archive
	dir:      "out/notes"
	names:    ["alpha", "beta"]
}
```

The alias `nt` differs from the field `notes`. A name in a scope refers to the nearest field of that name.

```sh run
rm notes.cue
pravo validate
pravo plan --policy policy/refactor.cue
```

```text out
Success! 8 resources, 1 data sources, 0 ephemeral, 0 function calls, 3 outputs across 2 providers.

No changes. The specification matches the state. 8 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 1 of 9, 8 unchanged and skipped, 1 refreshed, 8 not read.

Outputs:
  note_paths: ["out/notes/alpha.txt", "out/notes/beta.txt"]
  port_numbers: (sensitive)
  server_name: "acme-dev-<pet>"
Policy policy/refactor.cue (sha256:884a0d7ff8ef) is satisfied.
```

```sh
git add -A
git commit -m "Move the notes module into a package"
```

## What to do next

Each of these is one more commit with the same check.

- Relax the version pins of [M4](m4-translate-the-specification.md#step-1-providers) to ranges, run `pravo init --upgrade`, and read the plan. A provider upgrade is a change of its own, so it gets its own commit and its own review.
- Write a rule for every entry once with a pattern, [A9 Patterns](../learn/a09-patterns.md).
- Replace a repeated expression by a hidden field or a `let`, [A11 References, let and hidden fields](../learn/a11-references-let-hidden.md).
- Replace a flag pair by a condition, [A12 Conditions and strings](../learn/a12-conditions-and-strings.md).
- Put the rules of the platform on the provider definition, so that every resource built through it obeys, [B10 Policy](../learn/b10-policy.md).
- Build the three environments from one definition, [A14 Project](../learn/a14-project-three-environments.md).

## Recap

- Rewrite after the cut over, one idea per commit, and keep refactors apart from changes in behaviour.
- A refactor commit leaves the plan empty or moving records only. A policy turns that rule into a check.
- A new address is a move: `$from` names the old one, and it can go in a later commit.

:::check
Why do the moves of commit 3 not change an object?
::answer
A move renames a record in the state. The plan says so with "record renamed; no change to the object", and the policy accepts the action `move`.

Why is `$from` removed in its own commit?
::answer
The move has been applied, so the field does nothing. A separate commit keeps the refactor and the cleanup apart, and each has an empty plan.
:::

Next: [How-to](../how-to/index.md) has the task-shaped pages for what you do every week, such as remote state, CI and policies.
