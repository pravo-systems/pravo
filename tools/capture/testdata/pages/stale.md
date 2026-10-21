---
title: Stale outputs
description: Outputs that -update rewrites, adds or removes.
order: 7
---

```cue file=main.cue
package main
```

A wrong output is rewritten.

```sh run
pravo eval
```

```text out
evaluating /nowhere
```

A missing out block is added.

```sh run
pravo lines 2
```

An elision that still holds is left alone.

```sh run
pravo lines 40
```

```text out
line 1
line 2
...
line 39
line 40
```

An elision keeps the number of lines around it when the middle changes.

```sh run
pravo lines 12
```

```text out
line 1
line 2
...
line 99
line 100
```

An out block is removed when the command prints nothing.

```sh run
true
```

```text out
this used to print
```

Hidden and check blocks get no out block.

```sh run hidden
pravo lines 3
```

```sh check
pravo lines 3
```

A line that looks like a closing fence makes the new fence longer.

```sh run
printf '%s\n' a '```' b
```

A tilde fence stays a tilde fence, and the info string is kept.

```sh run
echo tilde
```

~~~text out title="kept"
old
~~~

A block right before the end of a container gets a blank line after it.

:::deep-dive Extra

```sh run
echo in container
```
:::

The last block of the file.

```sh run
echo last
```
