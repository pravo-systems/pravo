---
title: Authoring errors
description: Each construct below is reported with its line.
order: 3
---

An out block with no run block before it:

```text out
nothing
```

A prompt in a command:

```sh run
$ pravo eval
```

A file outside the workspace, and an exit code on a file:

```cue file=../escape.cue
x: 1
```

```cue file=ok.cue exit=1
x: 1
```

A callout with a block that would run:

> [!TIP]
> ```sh run
> pravo eval
> ```

Prose between a run block and its out block:

```sh run
pravo eval
```

Some prose.

```text out
evaluating .
```

A check block that expects a failure:

```sh check exit=1
pravo fail
```

An empty run block, and an unterminated quoted attribute:

```sh run
```

```cue title="unterminated
x: 1
```

:::exercise One
::solution
:::deep-dive Nested
:::
:::

::hint

:::exercise Two
::wrong
