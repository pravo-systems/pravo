---
title: Layout stress page with a long title that has to wrap on a phone without overflow
description: Every component at its worst case; test content for the generator and the style sheet.
order: 9
minutes: 12
needs: B1, A3
learn: How does each component behave with long lines, long words and many columns?
done_when: nothing scrolls sideways on a phone, and every control can be reached with the keyboard.
level: advanced
capture: false
---

A paragraph with a very long unbreakable token: `PRAVO_REMOTE_STATE_BACKEND_CONFIGURATION_BUCKET_NAME_WITH_A_LONG_SUFFIX_THAT_DOES_NOT_WRAP`, a link to [the glossary entry](../reference/glossary.md#unification), a <kbd>Ctrl</kbd> + <kbd>K</kbd> key pair, and a URL https://registry.example.org/providers/hashicorp/aws/5.31.0/docs/resources/s3_bucket_server_side_encryption_configuration.

## A second-level heading with `inline code` and a long enough wording to wrap on narrow screens

### A third-level heading

Text under the third-level heading.

#### A fourth-level heading

Text under the fourth-level heading.

## Callouts

> [!NOTE]
> A note with `code` and a [link](index.md).

> [!TIP]
> A tip.

> [!WARNING]
> A warning that is long enough to wrap onto a second line in a narrow column, so that the left rule and the title stay aligned.

> [!PITFALL]
> A pitfall.

> [!CHECK]
> A check.

## Code

```cue file=wide.cue ln hl="2,4-5"
package main

#Bucket: {
	name:        string & =~"^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$"
	versioning:  *"Enabled" | "Suspended"
	encryption?: {algorithm: "AES256" | "aws:kms", kms_master_key_id?: string & !=""}
	tags:        [string]: string
}

buckets: [for n in ["alpha", "bravo", "charlie"] {name: "\(n)-state-bucket-with-a-long-name-for-horizontal-scroll-testing-0123456789"}]
```

```diff
 package main
-size: 3
+size: 4
 name: "example"
```

```hcl title="main.tf" ln
resource "aws_s3_bucket" "state" {
  bucket = "${var.prefix}-state-bucket-with-a-long-name-for-horizontal-scroll-testing-0123456789"
  tags = merge(var.tags, { Name = "state", Environment = var.environment })
}
```

```json
{"format": 1, "summary": {"add": 0, "change": 1, "destroy": 0}, "graph": {"planned": 1, "total": 340}, "note": "a long string that makes the line wider than a phone screen"}
```

```sh run
pravo plan -t env=prod --json --out plan.json --refresh=false --parallelism 10 --lock-timeout 30s
```

```text out
Plan: 2 to add, 1 to change, 1 to destroy.
  + aws_s3_bucket.state
  ~ aws_s3_bucket_versioning.state
  -/+ aws_s3_bucket_policy.state  # forces replacement
  - aws_s3_bucket_acl.state
Graph: planned 4 of 340, 336 skipped. A line long enough to need horizontal scrolling on a phone.
```

## Tables

| Flag | Type | Default | Meaning of the flag, which is described by a long sentence |
|---|---|---|---|
| `-t` | `key=value` | none | Sets a tag value for the whole run, so a long description wraps inside its cell |
| `--json` | boolean | `false` | Prints the plan document as JSON |
| `--lock-timeout` | duration | `0s` | How long to wait for the state lock |
| `--parallelism` | integer | `10` | Number of concurrent provider calls |

## Lists

1. A first step with a long wording that wraps to a second line so the marker stays aligned with the first line.
2. A second step.
   - A nested bullet.
   - Another nested bullet with `code`.
3. A third step.

- A bullet.
- A bullet with a paragraph.

  The paragraph inside the bullet.

## Tabs with terminals

:::tabs

```sh run title="macOS"
brew install pravo
```

```text out
==> Installing pravo
```

```sh title="Linux"
curl -fsSL https://example.org/install.sh | sh
```

```sh title="Windows"
winget install pravo
```

:::

## Three windows

:::compare

```hcl title="HCL"
variable "size" {
  type    = number
  default = 3
}
```

```cue title="CUE"
size: *3 | int
```

```json title="JSON"
{"size": 3}
```

:::

## Deep dive with code

:::deep-dive A deep dive whose title is long enough to wrap onto a second line on a phone screen
Text inside the deep dive.

```cue title="inside.cue"
a: 1
```

A second paragraph.
:::

## Predict with code in the question

:::predict What does `pravo eval` print when `size: 3` and `size: 4` appear in one file, and the file also carries a long comment?

::reveal

The error `conflicting values 4 and 3`, with two locations.
:::

## Exercise with two hints

:::exercise Fix the conflict

Edit `main.cue` so that `pravo eval` prints `size: 4`.

::hint
Remove one of the two lines.

::hint
See [writing a field twice](../learn/a03-writing-a-field-twice.md).

::solution
```cue file=main.cue
size: 4
```
:::

## Check

:::check

What does a conflict name?

::answer
The path, both values and both locations.

Why is `size: >=1` not a value?

::answer
A bound restricts a value. It does not give one.
:::

## Plan widget

:::widget plan

```json
{"title": "terminal", "command": "pravo plan", "exit": 0, "speed": 90,
 "fallback": "pravo plan lists two resources to add and no change.",
 "lines": [
  {"k": "dim", "t": "pravo will perform the following actions:"},
  {"k": "empty", "t": ""},
  {"k": "add", "t": "  + random_pet.pet"},
  {"k": "add", "t": "  + local_file.hello"},
  {"k": "dim", "t": "      content = (known after apply)"},
  {"k": "change", "t": "  ~ local_file.old"},
  {"k": "destroy", "t": "  - local_file.gone"}
 ],
 "summary": {"k": "", "t": "Plan: 2 to add, 1 to change, 1 to destroy."}}
```

:::

## Blockquote and rule

> A plain blockquote that is not a callout, with a second line
> that continues the thought.

---

The end.
