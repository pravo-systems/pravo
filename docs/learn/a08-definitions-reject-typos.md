---
title: Definitions reject typos
description: Write a shape once, build values from it, and let the shape refuse every field it does not declare.
order: 8
group: Track A · CUE from zero
minutes: 10
needs: A7
learn: How do you write a schema in CUE? What happens to a field that the schema does not declare?
done_when: "`pravo eval` refuses a misspelt field, and `pravo eval -c` passes once three buckets carry both tags."
level: beginner
keywords: "field not allowed closed struct typo definition"
---

HCL checks the arguments of a resource against the provider schema. A convention of your own has no schema. Every bucket must carry an `owner` and a `cost_center`. The rule becomes a `validation` block on each variable that must follow it.

```hcl title="variables.tf"
variable "tags" {
  type = map(string)
  validation {
    condition     = alltrue([for k in ["owner", "cost_center"] : contains(keys(var.tags), k)])
    error_message = "tags must contain owner and cost_center."
  }
}
```

The block checks that two keys are present. It does not reject a key it does not list.

CUE has the same gap until you write a shape. Here is a bucket with a misspelt field. Work in an empty directory and write `estate.cue`.

```cue file=estate.cue status=unintended hl="3"
package estate

logs: {name: "logs", versioning: false, vrsioning: true}
```

:::predict Does `pravo eval` complain about `vrsioning`?
Read the file once more. The intended field is `versioning`.

::reveal
No. A plain [struct](../reference/glossary.md#struct) accepts any field, so `vrsioning` is a new field.

```sh run
pravo eval
```

```text out
logs: {
	name:       "logs"
	versioning: false
	vrsioning:  true
}
```

:::

The file is valid and wrong. Versioning stays off, and nothing says so.

A name that starts with `#` is a [definition](../reference/glossary.md#definition). A definition is a shape. You build a value from it with `&`. A definition is [closed](../reference/glossary.md#closed): a value built from it may contain only the fields that the definition declares.

## Warm-up

:::check From earlier lessons
A field is `name!: string` and nothing sets it. What does `pravo eval -c` report?
::answer
The required field: `field is required but not present` ([A7](a07-required-optional-regular.md)).

A field is `versioning: *false | bool`, and a second line sets it to `"yes"`. Is the value accepted?
::answer
No. The text `"yes"` is a string, and neither choice is a string ([A6](a06-choices-and-defaults.md)).
:::

## Build values from a shape

Replace the contents of `estate.cue`. The shape declares three fields.

```cue file=estate.cue status=valid hl="3-7"
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

logs:  #Bucket & {name: "logs"}
media: #Bucket & {name: "media", versioning: true}
```

```sh run
pravo eval
```

```text out
logs: {
	name:       "logs"
	class:      "standard"
	versioning: false
}
media: {
	name:       "media"
	class:      "standard"
	versioning: true
}
```

The definition `#Bucket` is not printed. It describes a shape and holds no data. The two buckets take the defaults of the shape.

## Break it

Misspell the field again. This time the bucket is built from `#Bucket`.

```cue file=estate.cue status=rejected hl="9"
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

logs:  #Bucket & {name: "logs", vrsioning: true}
media: #Bucket & {name: "media", versioning: true}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
logs.vrsioning: field not allowed:
    ./estate.cue:9:33
```

Read the error from the top.

- The path is `logs.vrsioning`.
- The rule is `field not allowed`. `#Bucket` is closed and does not declare `vrsioning`.
- One location is given, line 9. Only that line wrote the field.

pravo prints no buckets at all. A specification with an error has no result, so the typo cannot reach a plan.

When a definition refuses a field you meant to add, read [C3](c3-closed-or-open.md). It shows how to open a definition with `...` and how to extend one by embedding.

## Exercise

:::exercise Tag every bucket
Replace `estate.cue` with this file. It has the typo and no tags.

```cue file=estate.cue status=rejected
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

logs:    #Bucket & {name: "logs", vrsioning: true}
media:   #Bucket & {name: "media"}
backups: #Bucket & {name: "backups", class: "glacier"}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
logs.vrsioning: field not allowed:
    ./estate.cue:9:35
```

Do three things.

1. Fix the typo.
2. Define `#Tagged` with two required fields, `owner` and `cost_center`.
3. Give `#Bucket` a field `tags` of that shape. Set both tags on all three buckets.

Then `pravo eval -c` passes. A bucket without tags fails `pravo eval -c`. A misspelt tag is refused by `pravo eval`.

The check adds four more buckets to a copy of the file, one at a time. One is complete, one lacks a tag, one has a misspelt tag and one has no tags.

```sh file=check.sh
pravo eval -c > /dev/null 2>&1 || { echo "pravo eval -c should pass"; exit 1; }
tmp=$(mktemp -d)
fixture() {
  mkdir "$tmp/$1"; cp estate.cue "$tmp/$1/"
  printf 'package estate\n\nextra: #Bucket & {name: "extra"%s}\n' "$2" > "$tmp/$1/fixture.cue"
  pravo -C "$tmp/$1" eval -c 2>&1
}
fixture ok ', tags: {owner: "o", cost_center: "c"}' > /dev/null || { echo "a complete bucket should pass"; exit 1; }
fixture one ', tags: {owner: "o"}' | grep -q 'field is required but not present' || { echo "a missing tag should fail"; exit 1; }
fixture typo ', tags: {owner: "o", cost_center: "c", ownr: "x"}' | grep -q 'field not allowed' || { echo "a misspelt tag should be refused"; exit 1; }
fixture none '' | grep -q 'field is required but not present' || { echo "a bucket without tags should fail"; exit 1; }
rm -rf "$tmp"
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
pravo eval -c should pass
```

::hint
A field can hold a definition as its value: `tags: #Tagged`. The two tag fields need the mark `!`.

::hint
See [definition](../reference/glossary.md#definition) and [closed](../reference/glossary.md#closed). A shape inside a shape is closed as well.

::solution
```cue file=estate.cue status=valid
package estate

#Tagged: {
	owner!:       string
	cost_center!: string
}

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
	tags:       #Tagged
}

logs: #Bucket & {
	name:       "logs"
	versioning: true
	tags: {owner: "platform", cost_center: "c100"}
}

media: #Bucket & {
	name: "media"
	tags: {owner: "web", cost_center: "c200"}
}

backups: #Bucket & {
	name:  "backups"
	class: "glacier"
	tags: {owner: "platform", cost_center: "c100"}
}
```

```sh run check
sh check.sh
```

```sh run
pravo eval -c -e backups
```

```text out
name:       "backups"
class:      "glacier"
versioning: false
tags: {
	owner:       "platform"
	cost_center: "c100"
}
```

:::

## Recap

- A name that starts with `#` is a definition: a shape, not data.
- A value built with `#Bucket & {...}` may contain only declared fields.
- A misspelt field is refused during evaluation and names its path.

:::check
Why does a plain struct accept `vrsioning` while `#Bucket & {...}` refuses it?
::answer
A plain struct is open and takes any new field. A definition is closed and allows only the fields it declares.

Does `pravo eval` print `#Bucket`?
::answer
No. A definition is a shape and is not part of the output. `pravo eval -e '#Bucket'` prints it.
:::

You have already used a definition. In Start, `$local.#Resource.local_file` is one that pravo generates from the provider's schema, and `rules.cue` narrowed it. Every resource is built from such a definition, so a misspelt attribute fails in `pravo validate` before any provider starts. [C3](c3-closed-or-open.md) shows that error on a real resource.
