---
title: A passing page
description: Exercises every block kind.      # a comment after a value
order: 1
normalize:
  - pattern: "took [0-9]+ms"
    replace: "took <n>ms"
---

# A passing page

Work in an empty directory.

```cue file=main.cue title="main.cue"
package main

size: 3
```

```sh run
pravo eval
ls
```

```text out
evaluating .
real .
file ./main.cue
main.cue
```

A hidden step writes a file. Its output is not compared.

```sh run hidden
echo setup > setup.txt
echo noise
```

Exit codes: the expected status is declared.

```sh run exit=3
pravo fail
```
```text out
boom: .
```

Elision keeps the first and last lines.

```sh run
pravo lines 30
```

```text out
line 1
line 2
...
line 29
line 30
```

Rules from the front matter apply to the output.

```sh run
echo "took 123ms"
```

```text out
took <n>ms
```

Trailing spaces, blank lines and terminal escapes are normalised.

```sh run
pravo spaces
pravo color
```

```text out
trailing


red text
```

The home and temporary directories are hidden.

```sh run
pravo home
```

```text out
cache at ~/.pravo and <tmp>/x
```

The environment of the commands is clean of presentation variables.

```sh run
PRAVO_LOG=debug pravo env
pravo env
```

```text out
PRAVO_LOG=debug NO_COLOR=1
PRAVO_LOG=unset NO_COLOR=1
```

A file in a subdirectory, and a file written twice.

```text file=sub/dir/note.txt
first
```

```text file=sub/dir/note.txt
second
```

```sh run
cat sub/dir/note.txt
```

```text out
second
```

A block with a language that has no attributes, in a callout and in a longer
fence, is not run.

> [!NOTE]
> A command in a callout:
>
> ```sh
> pravo fail
> ```

````text
```sh run
pravo fail
```
````

:::compare

```hcl title="main.tf"
variable "size" {}
```

```cue file=compare.cue title="compare.cue"
size: 1
```

:::

:::predict What does the next step print?

Think first.

::reveal

An answer. Blocks here are display only.

```cue file=main.cue title="answer"
package main

size: 4
```

:::

:::exercise Add a file

Create `extra.cue` with a second field.

::hint

Use the same shape as `main.cue`. A hint may show a file without writing it.

```cue file=main.cue title="hint only"
package main

hinted: true
```

::solution

```cue file=extra.cue
package main

extra: 1
```

```sh check
pravo eval
test -f extra.cue
```

```sh run
ls
```

```text out
compare.cue
extra.cue
main.cue
setup.txt
sub
```

:::

The solution ran in a copy: `extra.cue` is not in the workspace, and the hint
and the reveal wrote nothing.

```sh run
ls
cat main.cue
```

```text out
compare.cue
main.cue
setup.txt
sub
package main

size: 3
```

:::deep-dive Overwriting a file

```cue file=main.cue
package main

size: 99
```

```sh run
cat main.cue
```

```text out
package main

size: 99
```

:::

A deep dive also runs in a copy.

```sh run
cat main.cue
```

```text out
package main

size: 3
```
