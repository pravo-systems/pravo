---
title: Writing a field twice
description: Two values for one field either agree, refine each other or conflict.
order: 3
group: Track A, CUE from zero
minutes: 10
needs: A2
learn: What happens when two values disagree, and when do they combine?
done_when: "`pravo eval` prints one value for `size` and an error for the conflict."
level: beginner
---

In HCL, a second assignment replaces the first. The last writer wins and nothing reports the loss.

:::compare
```hcl title="main.tf" hl="2"
module "key" {
  file_permission = "0644"
}
```

```cue title="main.cue" hl="3-4"
package main

key: file_permission: "0600"
key: file_permission: "0644"
```
:::

CUE has no assignment. A field is a constraint, and writing it twice adds a second constraint. The value must satisfy both. This is called [unification](../reference/glossary.md#unification).

## Constraints that agree

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
package main

size: 3
```

The three lines meet in one value. `int` allows any integer, the range keeps `3` between 1 and 10, and `3` satisfies both.

:::predict What does pravo eval print when size is 3 on one line and 4 on the next?
Read the two lines. Decide before you open the answer.

::reveal
It prints an error: `size: conflicting values 4 and 3`. Two different concrete values cannot both hold.
:::

## Constraints that conflict

Replace the file with two concrete values.

```cue file=main.cue status=conflict hl="3-4"
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

The error names the path (`size`), the two values (`4` and `3`), and both places that wrote them (`main.cue` lines 3 and 4).

> [!PITFALL]
> A range is not a value. `size: >=1 & <=10` alone leaves `size` incomplete, and `pravo eval` reports that it cannot print it.

A bound is checked the same way. This file is rejected before any provider starts.

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

A value can be valid and still not what you meant.

```cue file=main.cue status=unintended
package main

size: int
size: 11
```

Nothing is wrong with this file, but the range from the first example is gone. Constraints only restrict what you write. They do not appear on their own.

## Three ways to bound a value

:::tabs
```cue title="Range"
size: int & >=1 & <=10
```

```cue title="Disjunction"
size: 1 | 2 | 3
```

```cue title="Default"
size: *3 | int
```
:::

> [!TIP]
> A disjunction lists the allowed values. A range describes them. Use a disjunction when the set is short.

:::figure Two values meet in a third. The meet of `int` and `>=1` is `int & >=1`.
<svg viewBox="0 0 220 120" role="img" aria-label="A diamond: int and bound at the top, their meet below, bottom at the base">
  <path d="M110 20 L40 60 L110 100 L180 60 Z" fill="none" stroke="currentColor" stroke-width="1.5"/>
  <g fill="currentColor"><rect x="105" y="15" width="10" height="10"/><rect x="35" y="55" width="10" height="10"/><rect x="175" y="55" width="10" height="10"/><rect x="105" y="95" width="10" height="10"/></g>
</svg>
:::

:::widget unify
```json
{
  "left": "int",
  "right": ">=1 & <=10",
  "meet": "int & >=1 & <=10",
  "chips": [
    {"value": "3", "ok": true, "result": "3"},
    {"value": "0", "ok": false, "result": "invalid value 0 (out of bound >=1)"},
    {"value": "11", "ok": false, "result": "invalid value 11 (out of bound <=10)"},
    {"value": "\"3\"", "ok": false, "result": "conflicting values int and \"3\" (mismatched types int and string)"}
  ]
}
```

::fallback
Two constraints, `int` and `>=1 & <=10`, merge into `int & >=1 & <=10`. The value `3` is accepted. The values `0` and `11` are rejected by a bound. The string `"3"` is rejected by the type.
:::

:::deep-dive Why order does not matter
Unification is commutative, associative and idempotent. The result of `a & b & c` does not depend on how the terms are grouped or ordered. Writing a field three times and combining the three values with `&` give the same result.
:::

## Exercise

:::exercise Change the bound
Make `size` accept 1 to 20 and set it to 15.

::hint
Only the range changes. The value stays a plain number.

::hint
See [unification](../reference/glossary.md#unification) for how two bounds combine.

::solution
```cue file=main.cue
package main

size: int & >=1 & <=20
size: 15
```

```sh run check
pravo eval | grep -q 'size: 15'
```

```sh run
pravo eval
```

```text out
package main

size: 15
```
:::

## Recap

- A field written twice adds a constraint. It does not replace the value.
- Two concrete values that differ are a conflict, and the error names both places.
- A bound or a type restricts a value. It does not give one.

:::check
What does CUE do when two lines give a field different concrete values?
::answer
It reports a conflict and names both locations.

Can a range such as `>=1 & <=10` be the final value of a field?
::answer
No. A range restricts the value. A concrete value must still be written.
:::
