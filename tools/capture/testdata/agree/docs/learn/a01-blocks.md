---
title: Blocks
description: Every place a run block can sit.
order: 1
group: Track A
---

```sh run
echo top-level
```

```text out
top-level
```

~~~sh run
echo tilde-fence
~~~

~~~text out
tilde-fence
~~~

````markdown
```sh run
echo inside-a-longer-fence
```
````

```sh hidden
echo hidden-setup
```

```bash run title="with a title"
echo titled
```

```text out
titled
```

```sh run exit=3
sh -c 'echo exits-three; exit 3'
```

```text out
exits-three
```

   ```sh run
   echo indented-three-spaces
   ```

   ```text out
   indented-three-spaces
   ```

```sh run
echo first-line
echo second-line
```

```text out
first-line
second-line
```

```sh run
printf 'a\n' ; printf 'b\n'
```

```text out
a
b
```

:::compare

```sh run title="One"
echo in-compare-one
```

```text out
in-compare-one
```

```sh run title="Two"
echo in-compare-two
```

```text out
in-compare-two
```

:::

:::tabs

```sh run title="A"
echo in-tab-a
```

```text out
in-tab-a
```

```sh title="B"
echo in-tab-b-not-run
```

:::

:::predict What prints?

```sh run
echo before-reveal
```

```text out
before-reveal
```

::reveal

```sh run
echo in-reveal
```

```text out
in-reveal
```

:::

:::exercise Do it

```sh run
echo in-exercise-body
```

```text out
in-exercise-body
```

::hint

Use `echo`.

::hint

See the reference.

::solution

```sh run
echo in-solution
```

```text out
in-solution
```

```sh run check
test -d .
```

:::

:::deep-dive Theory

```sh run
echo in-deep-dive
```

```text out
in-deep-dive
```

:::

:::check

Q one?

::answer

An answer.

Q two?

::answer

Another answer.

:::

> [!NOTE]
> A note with a block that is only shown.
>
> ```sh
> echo in-callout-shown
> ```

```run
echo run-in-language-position
```

```text out
run-in-language-position
```

```sh title="run me" hl="1"
echo title-mentions-run-so-it-is-not-run
```

```sh exit=0 title="a b" run
echo attributes-in-any-order
```

```text out
attributes-in-any-order
```

```sh run
echo last
```

```text out
last
```
