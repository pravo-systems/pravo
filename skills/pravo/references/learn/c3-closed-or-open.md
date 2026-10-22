<!-- Generated from docs/learn/c3-closed-or-open.md by tools/skillgen. Do not edit. -->

# Closed or open?

Which structs refuse a field they do not declare, how definitions combine, and the real errors when they do not.

A [struct](../reference/glossary.md#struct) is [open](../reference/glossary.md#open) when it accepts fields it does not declare. It is [closed](../reference/glossary.md#closed) when it refuses them. A plain struct is open. A [definition](../reference/glossary.md#definition) is closed.

Work in an empty directory. Every case below replaces `estate.cue`.

## Can I add a field here?

| Where the field is added | Accepted? |
|---|---|
| A plain struct, `bucket: {...}` | Yes. |
| A value built from a definition, `#Bucket & {...}` | Only if the definition declares the field. |
| A struct inside a definition | Only if the definition declares the field. |
| A definition that ends in `...` | Yes. |
| A struct with a [pattern](../reference/glossary.md#pattern), `[string]: string` | Yes, if the value matches the pattern. |
| A provider resource | Only attributes that the provider declares. |

The cases below show each row with the error pravo prints.

## A plain struct is open

File `estate.cue`:

```cue
package estate

logs: {name: "logs", versioning: false, vrsioning: true}
```

```sh
pravo eval
```

Output:

```text
logs: {
	name:       "logs"
	versioning: false
	vrsioning:  true
}
```

Nothing is refused. `vrsioning` is a new field. A plain struct has no list of allowed fields to check against.

## A definition is closed

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!:      string
	versioning: *false | bool
}

logs: #Bucket & {name: "logs", vrsioning: true}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.vrsioning: field not allowed:
    ./estate.cue:8:32
```

The path is `logs.vrsioning`. The rule is `field not allowed`: `#Bucket` does not declare it. The location is the line that wrote the field. Correct the spelling, or declare the field in the definition.

## A struct inside a definition is closed

Closedness reaches every struct inside the definition.

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!: string
	tags: {
		owner!: string
	}
}

logs: #Bucket & {name: "logs", tags: {owner: "platform", team: "core"}}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.tags.team: field not allowed:
    ./estate.cue:10:58
```

The path is `logs.tags.team`. The definition declares `tags`, and the struct in `tags` declares only `owner`. To accept other tags, give `tags` a pattern.

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!: string
	tags: [string]: string
}

logs: #Bucket & {name: "logs", tags: {owner: "platform", team: "core"}}
```

```sh
pravo eval
```

Output:

```text
logs: {
	name: "logs"
	tags: {
		owner: "platform"
		team:  "core"
	}
}
```

The pattern accepts any key whose value is a string. A tag with a number is still refused.

## A closed value stays closed

A value built from a definition refuses new fields from every later line and every other file of the package.

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!: string
}

logs: #Bucket & {name: "logs"}
logs: owner: "platform"
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.owner: field not allowed:
    ./estate.cue:8:7
```

The location is the line that added `owner`. Declare the field in `#Bucket` instead.

## Two definitions with &

Two definitions describe two shapes. Combining them with `&` asks for a value that fits both.

File `estate.cue`:

```cue
package estate

#Named: {name!: string}
#Tagged: {owner!: string}

logs: #Named & #Tagged & {name: "logs", owner: "platform"}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.name: field not allowed:
    ./estate.cue:3:10
    ./estate.cue:6:27
logs.owner: field not allowed:
    ./estate.cue:4:11
    ./estate.cue:6:41
```

Every field is refused. `#Named` allows `name` and refuses `owner`. `#Tagged` allows `owner` and refuses `name`. The value must satisfy both, so it can hold neither.

Write a definition by itself on a line inside the braces. This is [embedding](../reference/glossary.md#embedding): the fields of the embedded definition become fields of the struct.

File `estate.cue`:

```cue
package estate

#Named: {name!: string}
#Tagged: {owner!: string}

logs: {#Named, #Tagged, name: "logs", owner: "platform"}
```

```sh
pravo eval
```

Output:

```text
logs: {
	name:  "logs"
	owner: "platform"
}
```

The struct has the fields of both definitions. It is still closed: a field added to `logs` from another line or file is refused.

## Extending a definition with &

The same rule applies when one definition extends another.

File `estate.cue`:

```cue
package estate

#Base: {name!: string}
#Tagged: #Base & {owner!: string}

logs: #Tagged & {name: "logs", owner: "platform"}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.owner: field not allowed:
    ./estate.cue:4:19
    ./estate.cue:6:32
```

The path is `logs.owner`. `#Base` is closed, so the extra field in `#Tagged` is not allowed. The two locations are the declaration in `#Tagged` and the field in `logs`. Embed the base instead.

File `estate.cue`:

```cue
package estate

#Base: {name!: string}

#Tagged: {
	#Base
	owner!: string
}

logs: #Tagged & {name: "logs", owner: "platform"}
```

```sh
pravo eval
```

Output:

```text
logs: {
	name:  "logs"
	owner: "platform"
}
```

`#Tagged` has `name` from `#Base` and `owner` of its own. It is still closed, so a misspelt field is refused as before.

File `estate.cue`:

```cue
package estate

#Base: {name!: string}

#Tagged: {
	#Base
	owner!: string
}

logs: #Tagged & {name: "logs", owner: "platform", ownr: "x"}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.ownr: field not allowed:
    ./estate.cue:10:51
```

## Opening a definition

End a definition with `...` to accept extra fields. The other constraints still apply.

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!: string
	...
}

logs: #Bucket & {name: "logs", extra: 1}
```

```sh
pravo eval
```

Output:

```text
logs: {
	name:  "logs"
	extra: 1
}
```

The place matters. A `...` at the use site does not reopen the definition.

File `estate.cue`:

```cue
package estate

#Bucket: {
	name!: string
}

logs: #Bucket & {name: "logs", extra: 1, ...}
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
logs.extra: field not allowed:
    ./estate.cue:7:32
```

## A resource is closed

pravo builds every resource from a definition that the provider schema generates. Write a valid resource first and generate the schema.

File `estate.cue`:

```cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "hello"
}
```

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

Now misspell an attribute.

File `estate.cue`:

```cue
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

note: $local.#Resource.local_file & {
	filenmae: "note.txt"
	content:  "hello"
}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
note.filenmae: field not allowed:
    ./estate.cue:8:2
```

The path is `note.filenmae`. The rule is the same one: the definition does not declare the field. The check runs on the specification alone, before any provider starts, so it needs no credentials.

## Which one to write

| You want | Write |
|---|---|
| A shape that refuses typos | A definition: `#Bucket: {...}` |
| A shape that extends another | Embed it: `#Tagged: {#Base, owner!: string}` |
| A value that fits two shapes | Embed both: `{#Named, #Tagged}` |
| A struct that accepts extra fields | End the definition with `...`, or add a pattern |

[A8](https://pravo.systems/learn/a08-definitions-reject-typos.html) introduced definitions. [C2](c2-why-is-my-value-incomplete.md) covers values that are unfinished and not refused.
