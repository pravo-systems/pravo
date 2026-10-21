---
title: One rule for every entry
description: Write a rule once for every entry of a collection with a pattern. Each entry states only what differs.
order: 9
group: Track A · CUE from zero
minutes: 8
needs: A8
learn: How do you apply one definition to every entry of a collection? What does such a rule not do?
done_when: "`pravo eval` prints three buckets from one rule, and the output equals the long-hand version."
level: intermediate
---

In HCL, the shape of an entry sits in a variable type. The loop sits in the resource. The key reaches the resource as `each.key`.

```hcl title="main.tf"
variable "buckets" {
  type = map(object({ class = optional(string, "standard") }))
}

resource "aws_s3_bucket" "this" {
  for_each = var.buckets
  bucket   = each.key
}
```

Three places must agree: the type, the loop and the use of the key. In CUE you already have the shape from [A8](a08-definitions-reject-typos.md). The question is how to apply it to every entry.

The text before the colon is the key of a field. A [pattern](../reference/glossary.md#pattern) is a rule for fields. It names a kind of key and a value. Every field whose key matches must satisfy that value. The pattern `[Name=string]: #Bucket` says that every field of a struct is a `#Bucket`. The `Name=` gives the key a name, so the rule can use it.

## Warm-up

:::check From earlier lessons
A bucket is `#Bucket & {name: "logs", extra: 1}`, and `#Bucket` declares no field `extra`. Is it accepted?
::answer
No. `#Bucket` is closed, and the error is `field not allowed` ([A8](a08-definitions-reject-typos.md)).

A field is `env: *"dev" | "prod" @tag(env)`. What does `pravo eval -t env=qa` do?
::answer
It refuses `qa`. The tag value must match one of the choices ([A6](a06-choices-and-defaults.md)).
:::

## Long-hand first

Work in an empty directory. Write `estate.cue`. Three buckets are written one by one.

```cue file=estate.cue hl="10-12"
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

buckets: {
	logs:    #Bucket & {name: "logs"}
	media:   #Bucket & {name: "media", versioning: true}
	backups: #Bucket & {name: "backups", class: "glacier"}
}
```

Save the result. `tee` prints it and keeps a copy in `long.txt`.

```sh run
pravo eval | tee long.txt
```

```text out
buckets: {
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
	backups: {
		name:       "backups"
		class:      "glacier"
		versioning: false
	}
}
```

Every entry repeats `#Bucket &`. Every entry writes its own key a second time, as `name`. Three entries show the repetition. Thirty entries repeat it thirty times.

## One rule

State the rule once on `buckets`. Each entry then lists only what differs from the defaults.

```cue file=estate.cue status=valid hl="9,12-14"
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

buckets: [Name=string]: #Bucket & {name: Name}

buckets: {
	logs:    {}
	media:   {versioning: true}
	backups: {class: "glacier"}
}
```

Compare the new result with the saved one.

```sh run
pravo eval | diff long.txt -
```

`diff` prints nothing, so the two files give the same result. Read the rule in three parts.

- `buckets:` is the struct that holds the entries.
- `[Name=string]:` matches the key of every entry. `Name` stands for that key.
- `#Bucket & {name: Name}` is what every entry must be. Its `name` is its key.

An empty struct such as `logs: {}` is a complete entry. The rule supplies the shape, the defaults and the name.

## Break it

Misspell a field in one entry.

```cue file=estate.cue status=rejected hl="13"
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

buckets: [Name=string]: #Bucket & {name: Name}

buckets: {
	logs:    {}
	media:   {vrsioning: true}
	backups: {class: "glacier"}
}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.media.vrsioning: field not allowed:
    ./estate.cue:13:12
```

The path is `buckets.media.vrsioning`. The rule reached the entry `media`, built it from `#Bucket`, and the closed shape refused the field. The single location is the line that wrote it. The other two entries are fine, and no result is printed.

## A rule does not create entries

Remove the entries and keep the shape and the rule.

```cue file=estate.cue status=unintended
package estate

#Bucket: {
	name!:      string
	class:      *"standard" | "ia" | "glacier"
	versioning: *false | bool
}

buckets: [Name=string]: #Bucket & {name: Name}
```

:::predict What does `pravo eval` print?
Decide before you open the answer.

::reveal
An empty struct. The rule checks the entries that exist. There are none.

```sh run
pravo eval
```

```text out
buckets: {}
```

:::

> [!PITFALL]
> A pattern constrains entries that exist. It never adds one. To create entries from data, use a comprehension ([A10](a10-comprehensions.md)).

## Exercise

:::exercise Collapse the rules
Replace `estate.cue` with this file. Three security rules are written long-hand.

```cue file=estate.cue
package estate

#Rule: {
	name!:    string
	port!:    int & >=1 & <=65535
	protocol: *"tcp" | "udp"
}

rules: {
	http:  #Rule & {name: "http", port: 80}
	https: #Rule & {name: "https", port: 443}
	dns:   #Rule & {name: "dns", port: 53, protocol: "udp"}
}
```

Save the result, then rewrite the file.

```sh run
pravo eval | tee before.txt
```

```text out
rules: {
	http: {
		name:     "http"
		port:     80
		protocol: "tcp"
	}
	https: {
		name:     "https"
		port:     443
		protocol: "tcp"
	}
	dns: {
		name:     "dns"
		port:     53
		protocol: "udp"
	}
}
```

Add a pattern to `rules`. Remove `#Rule &` and every `name` from the entries. The check fails in three cases. The output differs from `before.txt`. An entry still writes its name. `#Rule &` appears more than once.

```sh file=check.sh
pravo eval | diff before.txt - > /dev/null || { echo "the output changed"; exit 1; }
if grep -q 'name: "' estate.cue; then echo "an entry still writes its name"; exit 1; fi
[ "$(grep -c '#Rule &' estate.cue)" -le 1 ] || { echo "#Rule & appears in more than one place"; exit 1; }
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
an entry still writes its name
```

::hint
Follow the rule in `buckets`. The key is bound with `Name=string`, and the shape is `#Rule & {name: Name}`.

::hint
See [pattern](../reference/glossary.md#pattern). Each entry keeps only `port`, and `protocol` where it is not `"tcp"`.

::solution
```cue file=estate.cue status=valid
package estate

#Rule: {
	name!:    string
	port!:    int & >=1 & <=65535
	protocol: *"tcp" | "udp"
}

rules: [Name=string]: #Rule & {name: Name}

rules: {
	http:  {port: 80}
	https: {port: 443}
	dns:   {port: 53, protocol: "udp"}
}
```

```sh run check
sh check.sh
```
:::

:::deep-dive Patterns that match some keys
The key part of a pattern can be any constraint on a string. A regular expression limits the rule to some entries.

```cue file=deep/estate.cue
package estate

#Bucket: {
	name!:      string
	versioning: *false | bool
}

buckets: [Name=string]: #Bucket & {name: Name}
buckets: [=~"^logs-"]: versioning: true

buckets: {
	"logs-web": {}
	media:      {}
}
```

```sh run
pravo -C deep eval
```

```text out
buckets: {
	"logs-web": {
		name:       "logs-web"
		versioning: true
	}
	media: {
		name:       "media"
		versioning: false
	}
}
```

The entry `logs-web` matches both rules and gets `versioning: true`. The entry `media` matches only the first.
:::

## Recap

- A pattern `[Name=string]: shape` applies the shape to every entry of a struct.
- Entries state only what differs. The key supplies `name`.
- A pattern constrains entries that exist. It never creates one.

:::check
What does `Name=` do in `[Name=string]: #Bucket & {name: Name}`?
::answer
It gives the key of the entry being checked a name. The rule uses it to set `name`.

Does a pattern add entries to a struct that has none?
::answer
No. It constrains entries that exist. A comprehension generates entries.
:::

The next lesson generates the entries themselves from data.
