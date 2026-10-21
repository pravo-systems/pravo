---
title: A value is a type
description: In CUE, `3` and `int` are both values, and one is narrower than the other.
order: 1
group: Track A, CUE from zero
minutes: 8
learn: What is a value, and how does a type fit in?
done_when: "`pravo eval` prints a struct with a string, a number and a bool."
level: beginner
---

HCL separates a variable's type from its value. CUE has one kind of thing, the value, and types are the widest values.

```cue file=main.cue
package main

name:    "web"
size:    3
enabled: true
```

```sh run
pravo eval
```

```text out
package main

name:    "web"
size:    3
enabled: true
```

The next lesson turns these fields into a struct with rules.

> [!NOTE]
> The file starts with a `package` clause. `pravo eval` loads every file with the same package name in the directory.

## Recap

- A field has a value.
- A type such as `string` is also a value, and it is wider than `"web"`.
- `pravo eval` prints the value of the whole package.

:::check
Is `int` a value?
::answer
Yes. It is the widest integer value.
:::
