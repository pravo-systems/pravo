---
title: A failing page
description: Every kind of difference.
order: 2
---

# A failing page

```cue file=main.cue
package main
```

The first block agrees.

```sh run
pravo eval
```

```text out
evaluating .
real .
file ./main.cue
```

The second differs in the middle.

```sh run
pravo lines 6
```

```text out
line 1
line 2
line 33
line 4
line 5
line 6
```

The third fails without saying so.

```sh run
pravo fail
```

```text out
boom: .
```

The fourth declares a failure that does not happen.

```sh run exit=2
pravo lines 1
```

```text out
line 1
```

The fifth prints output and has no out block.

```sh run
pravo lines 2
```

The sixth elides lines, and the last line is wrong.

```sh run
pravo lines 20
```

```text out
line 1
...
line 21
```

The seventh is missing a line from the middle segment.

```sh run
pravo lines 10
```

```text out
line 1
...
line 5
line 7
...
line 10
```

The eighth ends early.

```sh run
pravo lines 2
```

```text out
line 1
line 2
line 3
```

:::exercise Solve it

Task.

::solution

```sh check
pravo fail
```

:::
