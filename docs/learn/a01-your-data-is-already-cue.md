---
title: Your data is already CUE
description: A CUE file may contain plain JSON. You shorten it one piece of punctuation at a time, and the data never changes.
order: 1
group: Track A · CUE from zero
minutes: 6
needs: "A `pravo` binary on your path ([Quick start](../how-to/quick-start.md#1-install-pravo)). No provider, no account."
learn: Is JSON valid CUE? Which punctuation can you drop without changing the data?
done_when: "`pravo eval` prints the same data for the JSON you pasted and for the short CUE you wrote."
level: beginner
---

Your estate is data: buckets, tags, rules. That data often arrives as a JSON export. In HCL, a function decodes the export into a value, and the configuration reads the value.

```hcl title="main.tf"
locals {
  estate = jsondecode(file("${path.module}/estate.json"))
}
```

The export and the configuration are two syntaxes joined by a function call.

In CUE, a JSON file is already a CUE file. Everything else in the language is a convenience that you adopt one piece at a time.

## Paste the JSON

Work in an empty directory named `estate`. Save the export as `estate.cue`. Put `package estate` on the first line, and paste the JSON under it without changing a character. The `package` line makes the file part of a [package](../reference/glossary.md#package). [A4](a04-order-does-not-matter.md) explains it. Until then, write it first in every file.

```cue file=estate.cue status=valid
package estate

{
  "buckets": {
    "logs": {
      "name": "logs",
      "versioning": true,
      "tags": { "owner": "platform" }
    },
    "uploads": {
      "name": "uploads",
      "versioning": false,
      "tags": { "owner": "web" }
    }
  }
}
```

Evaluate the file.

```sh run
pravo eval
```

```text out
buckets: {
	logs: {
		name:       "logs"
		versioning: true
		tags: {
			owner: "platform"
		}
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: {
			owner: "web"
		}
	}
}
```

The file is valid as pasted. `pravo eval` prints the evaluated data. It prints CUE, not JSON: labels have no quotes, fields have no commas, and the file has no outer braces. You are about to write the file in that form.

A **field** is a label and a value, such as `name: "logs"`. A **[struct](../reference/glossary.md#struct)** is a set of fields in braces. Here `buckets` is a field whose value is a struct. That struct holds the fields `logs` and `uploads`.

## Drop what CUE does not need

Save the output first. You compare against it after every change.

```sh run
pravo eval > before.txt
```

CUE accepts the JSON you pasted and four shorter forms of it.

| In JSON | In CUE | Rule |
|---|---|---|
| `"name": "logs"` | `name: "logs"` | A label that is a plain word needs no quotes. |
| `},` at the end of a line | `}` | A newline ends a field. A comma is optional. |
| `{ ... }` around the file | nothing | A file is a struct. |
| `"tags": { "owner": "web" }` | `tags: owner: "web"` | A struct with one field folds into a path. |

JSON has no comments. CUE has `//`, which runs to the end of the line. Replace `estate.cue` with the short form.

```cue file=estate.cue status=valid hl="6-7,12"
package estate

buckets: {
	logs: {
		name:       "logs"
		versioning: true // keep old versions
		tags: owner: "platform"
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: owner: "web"
	}
}
```

The file has no quotes on labels, no commas and no outer braces. Compare its output with the output you saved.

```sh run
pravo eval | diff before.txt - && echo same
```

```text out
same
```

`diff` succeeds when its two inputs match, and then `echo same` runs. The file changed and the data did not. The comment is not part of the output.

## Break it

Quotes are optional on labels. They are not optional on string values. Remove the quotes from `"platform"` and run `pravo eval`.

```cue file=estate.cue status=rejected hl="7"
package estate

buckets: {
	logs: {
		name:       "logs"
		versioning: true // keep old versions
		tags: owner: platform
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: owner: "web"
	}
}
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.logs.tags.owner: reference "platform" not found:
    ./estate.cue:7:16
```

Read the error from left to right.

- **Path.** `buckets.logs.tags.owner` is the field that failed, counted from the top of the file.
- **Message.** A word without quotes is a name. CUE looks for a field called `platform` and finds none. A later lesson calls such a name a [reference](../reference/glossary.md#reference).
- **Location.** `./estate.cue:7:16` is the file, the line and the column. Column 16 is the first letter of the bare word.

> [!NOTE]
> Your terminal prints the full path of the file where this page prints `./estate.cue`. This site shortens every path to `./`.

Put the quotes back.

```cue file=estate.cue status=valid
package estate

buckets: {
	logs: {
		name:       "logs"
		versioning: true // keep old versions
		tags: owner: "platform"
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: owner: "web"
	}
}
```

```sh run
pravo eval | diff before.txt - && echo same
```

```text out
same
```

## Exercise

:::exercise Shorten the rules
The rules arrived as JSON. Keep the buckets as they are and shorten the `rules` field without changing the data.

```cue file=estate.cue status=valid
package estate

buckets: {
	logs: {
		name:       "logs"
		versioning: true // keep old versions
		tags: owner: "platform"
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: owner: "web"
	}
}

"rules": {
	"https": {
		"port": 443,
		"protocol": "tcp"
	},
	"ssh": {
		"port": 22,
		"protocol": "tcp"
	}
}
```

Save the output before you edit.

```sh run
pravo eval > before.txt
```

The check compares the output with `before.txt`. It also fails while any label has quotes or any comma remains.

```sh file=check.sh
pravo eval | diff before.txt - > /dev/null || { echo "the data changed"; exit 1; }
if grep -Eq '"[a-z_]+":' estate.cue; then echo "a label still has quotes"; exit 1; fi
if grep -q ',' estate.cue; then echo "a comma is still there"; exit 1; fi
echo ok
```

Run it on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
a label still has quotes
```

Edit `estate.cue` until `sh check.sh` prints `ok`.

::hint
Remove the quotes from the labels first. Then remove the commas. Run `sh check.sh` after each edit.

::hint
The table in [Drop what CUE does not need](#drop-what-cue-does-not-need) lists every short form. The [CUE cheat sheet](cue-cheat-sheet.md) has them on one page.

::solution
```cue file=estate.cue status=valid
package estate

buckets: {
	logs: {
		name:       "logs"
		versioning: true // keep old versions
		tags: owner: "platform"
	}
	uploads: {
		name:       "uploads"
		versioning: false
		tags: owner: "web"
	}
}

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     22
		protocol: "tcp"
	}
}
```

```sh run check
sh check.sh
```
:::

## Recap

- A CUE file may hold plain JSON. Put `package estate` above it.
- Quotes on labels, commas and the outer braces are optional. A struct with one field folds into a path. `//` starts a comment.
- A word without quotes in a value position is a name. String values keep their quotes.

:::check
Which punctuation can you drop from a JSON file without changing the data?
::answer
The quotes on labels that are plain words, the commas at the end of lines and the outer braces. A struct with one field also folds into a path.

Why does `owner: platform` fail while `owner: "platform"` works?
::answer
A word without quotes is a name. CUE looks for a field called `platform` and finds none. A string value needs its quotes.
:::

The next lesson shows that `string` is a value too.
