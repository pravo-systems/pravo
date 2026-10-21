---
title: Writing a field twice
description: Two values for one field either agree or conflict.
order: 1
group: Track A, CUE from zero
minutes: 5
learn: What happens when two lines give a field different values?
done_when: pravo eval prints one value for size and an error for the conflict.
level: beginner
---

Work in an empty directory. Write a file with a type, a range and a value.

```cue file=main.cue ln hl="3-5"
package main

size: int
size: >=1 & <=10
size: 3
```

```sh run
pravo eval
```

```text out
size: 3
```

The three lines meet in one value. Replace the file with two concrete values.

```cue file=main.cue status=conflict
package main

size: 3
size: 4
```

```sh run exit=1
pravo eval
```

```text out
Error: evaluating .:
size: conflicting values 4 and 3:
    ./main.cue:3:7
    ./main.cue:4:7
```

:::predict What does pravo eval print when size is 11 and the range is 1 to 10?
Decide before you open the answer.

::reveal

The value is rejected, and the error names the bound.

```cue file=main.cue status=rejected
package main

size: int & >=1 & <=10
size: 11
```

```sh run exit=1
pravo eval
```

```text out
Error: evaluating .:
size: invalid value 11 (out of bound <=10):
    ./main.cue:3:19
    ./main.cue:4:7
```

:::

:::exercise Change the bound

Make `size` accept 1 to 20 and set it to 15.

::hint
Change the number after `<=`.

::solution
```cue file=main.cue
package main

size: >=1 & <=20
size: 15
```

```sh run check
pravo eval | grep -q 'size: 15'
```
:::

## Recap

- A field written twice adds a constraint.
