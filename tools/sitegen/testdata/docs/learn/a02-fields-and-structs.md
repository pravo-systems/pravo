---
title: Fields and structs
description: A struct groups fields. A definition names a struct that other values must match.
order: 2
group: Track A, CUE from zero
minutes: 9
needs: A1
learn: How do you describe the shape of a struct once and reuse it?
done_when: "`pravo validate` accepts a value that matches `#Server`."
level: beginner
---

A struct is a set of fields in braces. A definition starts with `#` and describes a shape.

```cue file=main.cue ln
package main

#Server: {
	name: string
	size: int & >=1
}

web: #Server & {name: "web", size: 2}
```

```sh run
pravo eval
```

```text out
package main

#Server: {
	name: string
	size: int & >=1
}
web: #Server & {
	name: "web"
	size: 2
}
```

`web` unifies with `#Server`. Every field of the definition is checked against the value.

```diff
 web: #Server & {
 	name: "web"
-	size: 2
+	size: 0
 }
```

With `size: 0` the file is rejected, because `0` is outside `>=1`.

## Recap

- A struct is a set of fields.
- A definition (`#Name`) describes a shape.
- Unifying a value with a definition checks it.

:::check
What does the `#` in `#Server` mean?
::answer
It marks a definition: a shape that values are checked against.
:::
