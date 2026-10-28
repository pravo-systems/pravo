<!-- Generated from docs/learn/reading-a-cue-error.md by tools/skillgen. Do not edit. -->

# Reading a CUE error

The four parts of a pravo error, the three stages that produce one, and the messages you meet first.

A CUE error is a path, two values and two file positions, with no instruction. Every error has the same four parts. Once you can find them, the error tells you which line to open.

Work in an empty directory. This page writes its own files. Two files give one field two values.

File `estate.cue`:

```cue
package estate

buckets: logs: retention_days: 365
```

File `team.cue`:

```cue
package estate

buckets: logs: retention_days: 90
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
buckets.logs.retention_days: conflicting values 90 and 365:
    ./estate.cue:3:32
    ./team.cue:3:32
```

*Figure: The four parts of one error. The numbers are explained below.*

1. **Stage.** The first line says where pravo stopped. `evaluating` means both files are valid CUE and their values do not unify.
2. **Path.** The text before the colon is the path of the field, counted from the top of the data. It is the path in the output, whatever way the files nest it.
3. **Message.** The text after the path says what is wrong with the value. Here two values disagree.
4. **Locations.** Each `file:line:column` is a place that the message is about. Open each one. The column is the start of the value.

pravo does not list every problem in a specification. It reports the first failing value and stops. Messages about that one value can arrive together. Fix it, run the command again, and the next one appears.

Remove the second file. The examples below use one file at a time.

```sh
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

File `estate.cue`:

```cue
buckets: logs: name: "logs"
```

```sh
pravo eval
```

Output:

```text
Error: loading .:
build constraints exclude all CUE files in .:
    ./estate.cue: no package name
```

The message names the file. There is no line, because the problem is the whole file. Add `package estate` as the first line. When another file in the directory has a package line, the message is `x.cue has no package clause; the other files are package estate`. [A4](https://pravo.systems/learn/a04-order-does-not-matter.html#break-it) shows that case.

### missing ',' in struct literal

Fields on one line need a comma between them. On separate lines they do not.

File `estate.cue`:

```cue
package estate

buckets: logs: {name: "logs" encrypted: true}
rules: ssh: port: 22
```

```sh
pravo eval
```

Output:

```text
Error: loading .:
missing ',' in struct literal:
    ./estate.cue:3:30
expected '}', found 'EOF':
    ./estate.cue:4:22
```

The first message locates the problem. Column 30 is where `encrypted` starts, the field that follows the missing comma. The second message, `expected '}'`, is a side effect. The parser reached the end of the file while it still read the struct. Fix the first message and the second disappears.

### found packages

Two files in one directory that start with different `package` lines cannot form one package.

File `estate.cue`:

```cue
package estate

buckets: logs: name: "logs"
```

File `prod.cue`:

```cue
package prod

buckets: logs: encrypted: true
```

```sh
pravo eval
```

Output:

```text
Error: loading .:
prod.cue is package prod; the other files are package estate
```

The message names the file and the package of the other files. Give every file the same package line. This page removes the second file instead.

```sh
rm prod.cue
```

## Errors in evaluation

### reference "platform" not found

A word without quotes is a name. When no field has that name, CUE cannot find it.

File `estate.cue`:

```cue
package estate

buckets: logs: tags: owner: platform
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
buckets.logs.tags.owner: reference "platform" not found:
    ./estate.cue:3:29
```

The column is the first letter of the word. Quote the string. [A1](https://pravo.systems/learn/a01-your-data-is-already-cue.html) explains the rule.

### structural cycle

A word that names a field above it points back into itself.

File `estate.cue`:

```cue
package estate

buckets: logs: name: logs
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
buckets.logs.name: structural cycle
```

`logs` is the name of the field that contains `name`. CUE cannot resolve a field in terms of itself. This message has no location. Look at the field in the path and quote the string.

### conflicting values, with two types

When the two values differ in type, the message adds the reason in brackets.

File `estate.cue`:

```cue
package estate

size: 3
size: "3"
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
size: conflicting values 3 and "3" (mismatched types int and string):
    ./estate.cue:3:7
    ./estate.cue:4:7
```

`mismatched types int and string` is the reason. A number and a string never unify. [A3](https://pravo.systems/learn/a03-writing-a-field-twice.html) covers conflicts.

### invalid value, out of bound

A value outside a bound names the bound.

File `estate.cue`:

```cue
package estate

port: int & >=1 & <=65535
port: 70000
```

```sh
pravo eval
```

Output:

```text
Error: evaluating .:
port: invalid value 70000 (out of bound <=65535):
    ./estate.cue:3:19
    ./estate.cue:4:7
```

The first location is the bound that rejected the value, and the second is the value. [A5](https://pravo.systems/learn/a05-constraints-narrow-a-value.html) covers bounds.

## An error from -c

### incomplete value

The flag `-c` fails while any value is still a constraint.

File `estate.cue`:

```cue
package estate

buckets: logs: retention_days: int
```

Status: rejected.

```sh
pravo eval -c
```

Output:

```text
Error: the value is not concrete:
buckets.logs.retention_days: incomplete value int:
    ./estate.cue:3:32
```

The message names the constraint that is left. [A2](https://pravo.systems/learn/a02-a-value-can-be-a-type.html) covers concrete values, and [Why is my value incomplete?](why-is-my-value-incomplete.md) covers the causes.

The lessons that follow add three messages. A6 has `empty disjunction`, when a value matches none of the choices. A8 has `field not allowed`, when a name is not declared. A11 has `reference "bse" not found`, the same message as above for a misspelt name.

## Predict

The next file has three mistakes.

File `estate.cue`:

```cue
package estate

buckets: logs: {name: "logs" encrypted: true}
buckets: logs: tags: owner: platform

rules: ssh: port: 22
rules: ssh: port: 220
```

## Exercise

## Recap

- An error has four parts: the stage, the path, the message and the locations. Open each location.
- `loading` means the files are not one package. `evaluating` means the values do not unify. `not concrete` means `-c` found a constraint.
- pravo reports the first failing value. Fix it and run again.

Go back to [A4](https://pravo.systems/learn/a04-order-does-not-matter.html) to continue the track.
