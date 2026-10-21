---
title: The idea in three sentences
description: Four values written against one constraint, evaluated by the real binary, behind the unify widget of the demo that lesson A3 embeds.
order: 1
---

Each value is written as a second `size` field below the constraint. The attribute `id` is read by `site/demos/idea/mk.go`, which records the value and the output of the run that follows.

## Accepted

```cue file=main.cue title="main.cue" id=v3 status=valid
package main

size: int & >=1 & <=10
size: 3
```

```sh run id=r3
pravo eval
```

```text out
size: 3
```

## Out of bound, below

```cue file=main.cue title="main.cue" id=v0 status=conflict
package main

size: int & >=1 & <=10
size: 0
```

```sh run id=r0 exit=6
pravo eval
```

```text out
Error: evaluating .:
size: invalid value 0 (out of bound >=1):
    ./main.cue:3:13
    ./main.cue:4:7
```

## Out of bound, above

```cue file=main.cue title="main.cue" id=v11 status=conflict
package main

size: int & >=1 & <=10
size: 11
```

```sh run id=r11 exit=6
pravo eval
```

```text out
Error: evaluating .:
size: invalid value 11 (out of bound <=10):
    ./main.cue:3:19
    ./main.cue:4:7
```

## A string

```cue file=main.cue title="main.cue" id=vs3 status=conflict
package main

size: int & >=1 & <=10
size: "3"
```

```sh run id=rs3 exit=6
pravo eval
```

```text out
Error: evaluating .:
size: conflicting values int and "3" (mismatched types int and string):
    ./main.cue:3:7
    ./main.cue:4:7
```
