---
title: Order does not matter
description: Lines and files can come in any order. The result is the same, so there is no precedence list to learn.
order: 4
group: Track A · CUE from zero
minutes: 8
needs: A3
learn: Does the order of lines or files change the result?
done_when: "`pravo eval` prints the same data for the estate in three file orders."
level: beginner
---

In HCL, where a value is read decides which value wins. These two commands differ only in the order of their arguments.

```sh
tofu plan -var-file=base.tfvars -var-file=prod.tfvars   # prod.tfvars wins where both set a name
tofu plan -var-file=prod.tfvars -var-file=base.tfvars   # base.tfvars wins
```

OpenTofu reads variable values from several sources and keeps the last one it reads. The order, from first to last:

1. `TF_VAR_` environment variables.
2. `terraform.tfvars`, then `terraform.tfvars.json`.
3. `*.auto.tfvars` and `*.auto.tfvars.json` files, in file-name order.
4. `-var` and `-var-file` options, in the order given.

Override files add a second order for configuration.

CUE has no precedence list. Every line and every file adds constraints, and unification gives the same result in any order. The files of one directory that start with the same `package` line form one **[package](../reference/glossary.md#package)**. pravo evaluates the package as a whole.

## Warm-up

:::check From earlier lessons
From A1: how do you write a struct with one field, `tags` holding `owner`, on one line?
::answer
`tags: owner: "web"`. A struct with one field folds into a path.

From A2: is `>=1` a concrete value?
::answer
No. It allows every number from 1 upward. A concrete value allows exactly one.
:::

## Start from the estate

Work in the directory from A3. If you start here, write these two files.

```cue file=estate.cue
package estate

buckets: {
	logs: {
		name:           "logs"
		versioning:     true // keep old versions
		encrypted:      true
		retention_days: 365
		tags: owner: "platform"
	}
	uploads: {
		name:           "uploads"
		versioning:     false
		encrypted:      true
		retention_days: 30
		tags: owner: "web"
	}
}

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     22
		protocol: "tcp"
	}
}
```

```cue file=team.cue
package estate

buckets: uploads: tags: cost_center: "4711"

buckets: uploads: tags: env: "prod"
```

Save the output. You compare against it later.

```sh run
pravo eval > before.txt
```

## Swap two lines

Add a new field to `uploads` in a scratch file. One line gives it a type and one line gives it a value.

```cue file=scratch.cue
package estate

buckets: uploads: class: string
buckets: uploads: class: "standard"
```

```sh run
pravo eval -e buckets.uploads.class
```

```text out
"standard"
```

Swap the two lines.

```cue file=scratch.cue hl="3-4"
package estate

buckets: uploads: class: "standard"
buckets: uploads: class: string
```

```sh run
pravo eval -e buckets.uploads.class
```

```text out
"standard"
```

The value is the same. In HCL, swapping two assignments to one name changes which one wins. In CUE, a type and a value that agree give the value in either order.

> [!NOTE]
> If the two lines name different fields, the data is the same in both orders. `pravo eval` may print the fields in the order you wrote them. The printed order is not part of the value.

Remove the scratch file.

```sh run
rm scratch.cue
```

## Rename a file

Files behave like lines. pravo reads the files of a directory in the order of their names. Rename `estate.cue` so that it sorts after `team.cue`, and compare the output with the output you saved.

```sh run
mv estate.cue z-estate.cue
pravo eval | diff before.txt - && echo same
```

```text out
same
```

Put the name back.

```sh run
mv z-estate.cue estate.cue
```

Two files that name the same field still have to agree. Order cannot make a conflict disappear.

## Predict

:::predict How many different values do the six orders give?
You write `retention_days` three times: with `365`, with `>=90` and with `int`. The three lines can appear in six orders. Decide before you open the answer.

::reveal
One. Every order gives `365`. Writing a field several times is the same as joining the values with `&`. The six expressions below are the six orders of the three lines.

```sh run
for e in '365 & >=90 & int' '365 & int & >=90' '>=90 & 365 & int' '>=90 & int & 365' 'int & 365 & >=90' 'int & >=90 & 365'; do pravo eval -e "$e"; done
```

```text out
365
365
365
365
365
365
```

:::

## Break it

A file that names another package cannot join the estate. Write `prod.cue` with `package prod`, as if the package were named after the file.

```cue file=prod.cue status=rejected hl="1"
package prod

buckets: logs: retention_days: >=365
```

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
prod.cue is package prod; the other files are package estate
```

Read the error from left to right.

- **Stage.** `loading` says that pravo could not assemble the files into one package. It reported the error before it evaluated any value.
- **Message.** `prod.cue is package prod; the other files are package estate` names the file, its package and the package of the other files. A directory holds one package.
- **Location.** The problem is the `package` line of a whole file, so there is no line number.

Change the first line of `prod.cue` to `package estate`.

```cue file=prod.cue status=valid hl="1"
package estate

buckets: logs: retention_days: >=365
```

```sh run
pravo eval | diff before.txt - && echo same
```

```text out
same
```

The third file adds a bound that `logs` already meets, so the data is unchanged.

The opposite mistake is reported too. Delete the first line of `prod.cue`.

```cue file=prod.cue status=rejected
buckets: logs: retention_days: >=365
```

```sh run exit=6
pravo eval
```

```text out
Error: loading .:
prod.cue has no package clause; the other files are package estate
```

A file without a `package` line cannot join the estate. pravo names the file and stops before it evaluates a value. Remove the file.

```sh run
rm prod.cue
```

## Exercise

:::exercise Split the estate into three files
Split `estate.cue` into `buckets.cue` and `rules.cue`. With `team.cue` you have three files. Delete `estate.cue`. The output must not change, whatever the order of the files.

Save the output before you split.

```sh run
pravo eval > before.txt
```

The check copies the three files into a scratch directory in three different orders and compares each output with `before.txt`.

```sh file=check.sh
[ ! -f estate.cue ] || { echo "estate.cue still exists"; exit 1; }
set -- *.cue
[ $# -eq 3 ] || { echo "expected three .cue files, found $#"; exit 1; }
tmp=$(mktemp -d)
for order in "$1 $2 $3" "$3 $1 $2" "$2 $3 $1"; do
  rm -rf "$tmp/d"; mkdir "$tmp/d"; n=0
  for f in $order; do n=$((n + 1)); cp "$f" "$tmp/d/$n.cue"; done
  if ! pravo -C "$tmp/d" eval | diff before.txt - > /dev/null; then
    echo "different output for the order: $order"; rm -rf "$tmp"; exit 1
  fi
done
rm -rf "$tmp"
echo ok
```

Run the check on the start state. It fails.

```sh run exit=1
sh check.sh
```

```text out
estate.cue still exists
```

::hint
Each new file starts with `package estate`. Move the `buckets` field into `buckets.cue` and the `rules` field into `rules.cue`.

::hint
The files of a package can hold any fields in any order. See [package](../reference/glossary.md#package).

::solution
```cue file=buckets.cue
package estate

buckets: {
	logs: {
		name:           "logs"
		versioning:     true // keep old versions
		encrypted:      true
		retention_days: 365
		tags: owner: "platform"
	}
	uploads: {
		name:           "uploads"
		versioning:     false
		encrypted:      true
		retention_days: 30
		tags: owner: "web"
	}
}
```

```cue file=rules.cue
package estate

rules: {
	https: {
		port:     443
		protocol: "tcp"
	}
	ssh: {
		port:     22
		protocol: "tcp"
	}
}
```

```sh run check
rm estate.cue
sh check.sh
```
:::

## Recap

- Lines and files can come in any order. Unification gives the same value, so there is no precedence list.
- The files of a directory that start with the same `package` line form one package. A directory holds one package.
- Order can change the order of the printed fields. It cannot change a value.
- A file without a package line, or with another one, is an error that names the file. Start every file with the same package line.

:::check
You rename a file so that it loads last. Does any value change?
::answer
No. pravo reads the files in name order, and unification does not depend on the order.

What does `prod.cue is package prod; the other files are package estate` mean?
::answer
`prod.cue` starts with a different `package` line than the other files. Give every file the same package name.

A file has no package line and breaks a bound. What does `pravo eval` print?
::answer
An error that names the file: `prod.cue has no package clause; the other files are package estate`. The bound is not checked until the file has the package line.
:::

The next lesson turns a rule into a value: a range, or text of a given shape.
