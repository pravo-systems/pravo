---
title: Reading a CUE error
description: The four parts of a pravo error, the three stages that produce one, and the messages you meet first.
order: 20
group: Track A · Companions
minutes: 8
needs: A3
learn: Where did pravo stop, which field is it, and which line do you change?
done_when: "`sh check.sh` prints ok after you fixed three mistakes, one error at a time."
level: beginner
keywords: "field not allowed conflicting values incomplete value field error message"
---

A CUE error is a path, two values and two file positions, with no instruction. Every error has the same four parts. Once you can find them, the error tells you which line to open.

Work in an empty directory. This page writes its own files. Two files give one field two values.

```cue file=estate.cue
package estate

buckets: logs: retention_days: 365
```

```cue file=team.cue
package estate

buckets: logs: retention_days: 90
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.logs.retention_days: conflicting values 90 and 365:
    ./estate.cue:3:32
    ./team.cue:3:32
```

:::figure The four parts of one error. The numbers are explained below.
<div style="margin:0">
<pre style="margin:0;padding:1.1rem 1.25rem;background:var(--code-bg);color:var(--code-text);border:1px solid var(--code-edge);border-radius:var(--r);font:14px/2.2 var(--font-mono);overflow-x:auto"><code><span style="border-bottom:2px solid var(--code-chg)">Error: evaluating .:</span>  <b style="display:inline-block;min-width:1.7em;text-align:center;border-radius:99px;background:var(--code-chg);color:#111;font-weight:600;font-size:11px;line-height:1.7">1</b><span style="color:var(--code-dim)"> stage</span>
<span style="border-bottom:2px solid var(--code-key)">buckets.logs.retention_days:</span> <span style="border-bottom:2px solid var(--code-del)">conflicting values 90 and 365:</span>  <b style="display:inline-block;min-width:1.7em;text-align:center;border-radius:99px;background:var(--code-key);color:#111;font-weight:600;font-size:11px;line-height:1.7">2</b><span style="color:var(--code-dim)"> path</span>  <b style="display:inline-block;min-width:1.7em;text-align:center;border-radius:99px;background:var(--code-del);color:#111;font-weight:600;font-size:11px;line-height:1.7">3</b><span style="color:var(--code-dim)"> message</span>
    <span style="border-bottom:2px solid var(--code-add)">./estate.cue:3:32</span>  <b style="display:inline-block;min-width:1.7em;text-align:center;border-radius:99px;background:var(--code-add);color:#111;font-weight:600;font-size:11px;line-height:1.7">4</b><span style="color:var(--code-dim)"> locations</span>
    <span style="border-bottom:2px solid var(--code-add)">./team.cue:3:32</span></code></pre>
</div>
:::

1. **Stage.** The first line says where pravo stopped. `evaluating` means both files are valid CUE and their values do not unify.
2. **Path.** The text before the colon is the path of the field, counted from the top of the data. It is the path in the output, whatever way the files nest it.
3. **Message.** The text after the path says what is wrong with the value. Here two values disagree.
4. **Locations.** Each `file:line:column` is a place that the message is about. Open each one. The column is the start of the value.

pravo does not list every problem in a specification. It reports the first failing value and stops. Messages about that one value can arrive together. Fix it, run the command again, and the next one appears.

Remove the second file. The examples below use one file at a time.

```sh run
rm team.cue
```

## The three stages

| First line | Where pravo stopped | What to check |
|---|---|---|
| `Error: loading .:` | It could not read the files as one package. | Syntax, the `package` line, the file names. |
| `Error: evaluating .:` | The files are valid. Their values do not unify. | The path, the values and both locations. |
| `Error: the value is not concrete:` | The flag `-c` found a value that is not final. | The constraint that the message names. |

The `.` is the directory. When you run `pravo -C DIR eval`, it is `DIR`.

## Errors in loading

### no package name

A file that does not start with a `package` line cannot join the estate.

```cue file=estate.cue status=rejected
buckets: logs: name: "logs"
```

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
build constraints exclude all CUE files in .:
    ./estate.cue: no package name
```

The message names the file. There is no line, because the problem is the whole file. Add `package estate` as the first line. When another file in the directory has a package line, the message is `x.cue has no package clause; the other files are package estate`. [A4](a04-order-does-not-matter.md#break-it) shows that case.

### missing ',' in struct literal

Fields on one line need a comma between them. On separate lines they do not.

```cue file=estate.cue status=rejected hl="3"
package estate

buckets: logs: {name: "logs" encrypted: true}
rules: ssh: port: 22
```

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
missing ',' in struct literal:
    ./estate.cue:3:30
expected '}', found 'EOF':
    ./estate.cue:4:22
```

The first message locates the problem. Column 30 is where `encrypted` starts, the field that follows the missing comma. The second message, `expected '}'`, is a side effect. The parser reached the end of the file while it still read the struct. Fix the first message and the second disappears.

### found packages

Two files in one directory that start with different `package` lines cannot form one package.

```cue file=estate.cue
package estate

buckets: logs: name: "logs"
```

```cue file=prod.cue status=rejected hl="1"
package prod

buckets: logs: encrypted: true
```

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
prod.cue is package prod; the other files are package estate
```

The message names the file and the package of the other files. Give every file the same package line. This page removes the second file instead.

```sh run
rm prod.cue
```

## Errors in evaluation

### reference "platform" not found

A word without quotes is a name. When no field has that name, CUE cannot find it.

```cue file=estate.cue status=rejected hl="3"
package estate

buckets: logs: tags: owner: platform
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.logs.tags.owner: reference "platform" not found:
    ./estate.cue:3:29
```

The column is the first letter of the word. Quote the string. [A1](a01-your-data-is-already-cue.md) explains the rule.

### structural cycle

A word that names a field above it points back into itself.

```cue file=estate.cue status=rejected hl="3"
package estate

buckets: logs: name: logs
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
buckets.logs.name: structural cycle
```

`logs` is the name of the field that contains `name`. CUE cannot resolve a field in terms of itself. This message has no location. Look at the field in the path and quote the string.

### conflicting values, with two types

When the two values differ in type, the message adds the reason in brackets.

```cue file=estate.cue status=conflict hl="3-4"
package estate

size: 3
size: "3"
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
size: conflicting values 3 and "3" (mismatched types int and string):
    ./estate.cue:3:7
    ./estate.cue:4:7
```

`mismatched types int and string` is the reason. A number and a string never unify. [A3](a03-writing-a-field-twice.md) covers conflicts.

### invalid value, out of bound

A value outside a bound names the bound.

```cue file=estate.cue status=rejected hl="3-4"
package estate

port: int & >=1 & <=65535
port: 70000
```

```sh run exit=6
pravo eval
```

```text out
Error: evaluating .:
port: invalid value 70000 (out of bound <=65535):
    ./estate.cue:3:19
    ./estate.cue:4:7
```

The first location is the bound that rejected the value, and the second is the value. [A5](a05-constraints-narrow-a-value.md) covers bounds.

## An error from -c

### incomplete value

The flag `-c` fails while any value is still a constraint.

```cue file=estate.cue status=valid
package estate

buckets: logs: retention_days: int
```

```sh run exit=1 status=rejected
pravo eval -c
```

```text out
Error: the value is not concrete:
buckets.logs.retention_days: incomplete value int:
    ./estate.cue:3:32
```

The message names the constraint that is left. [A2](a02-a-value-can-be-a-type.md) covers concrete values, and [Why is my value incomplete?](why-is-my-value-incomplete.md) covers the causes.

The lessons that follow add three messages. A6 has `empty disjunction`, when a value matches none of the choices. A8 has `field not allowed`, when a name is not declared. A11 has `reference "bse" not found`, the same message as above for a misspelt name.

## Predict

The next file has three mistakes.

```cue file=estate.cue status=rejected
package estate

buckets: logs: {name: "logs" encrypted: true}
buckets: logs: tags: owner: platform

rules: ssh: port: 22
rules: ssh: port: 220
```

:::predict How many of the three mistakes does one run of pravo report?
Decide before you open the answer.

::reveal
One. The missing comma stops pravo while it loads the file, before it evaluates any value. The second message is a side effect of the comma, as in the entry above. The other two mistakes are not reached yet.

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
missing ',' in struct literal:
    ./estate.cue:3:30
expected '}', found 'EOF':
    ./estate.cue:7:23
```

:::

## Exercise

:::exercise Fix three mistakes, one error at a time
`estate.cue` holds the file above. Run `pravo eval`, read the first error, fix that line and run again. Repeat until the command prints the data. The ssh port is 22.

```sh file=check.sh
want() { [ "$(pravo eval -e "$1" 2>/dev/null)" = "$2" ] || { echo "$1 should be $2"; exit 1; }; }
want buckets.logs.name '"logs"'
want buckets.logs.encrypted true
want buckets.logs.tags.owner '"platform"'
want rules.ssh.port 22
echo ok
```

Run the check on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
buckets.logs.name should be "logs"
```

::hint
Fix them in the order pravo reports them: a comma, a pair of quotes, one port number.

::hint
The comma is in [missing ',' in struct literal](#missing--in-struct-literal), the quotes in [reference "platform" not found](#reference-platform-not-found). The port is a conflict, covered in [A3](a03-writing-a-field-twice.md).

::solution
```cue file=estate.cue
package estate

buckets: logs: {name: "logs", encrypted: true}
buckets: logs: tags: owner: "platform"

rules: ssh: port: 22
```

```sh run check
sh check.sh
```
:::

## Recap

- An error has four parts: the stage, the path, the message and the locations. Open each location.
- `loading` means the files are not one package. `evaluating` means the values do not unify. `not concrete` means `-c` found a constraint.
- pravo reports the first failing value. Fix it and run again.

:::check
An error starts with `Error: loading .:`. Did pravo evaluate any value?
::answer
No. It stopped while it read the files: a syntax error, a missing package line or two package names.

What does the first location of `invalid value 70000 (out of bound <=65535)` point to?
::answer
The bound that rejected the value. The second location is the value.
:::

Go back to [A4](a04-order-does-not-matter.md) to continue the track.
