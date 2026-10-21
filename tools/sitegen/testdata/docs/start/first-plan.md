---
title: Install and run a first plan
description: Install pravo and plan two local resources.
order: 1
minutes: 10
learn: How do you run pravo for the first time?
done_when: "`pravo version` prints a version and `pravo plan` prints a plan."
level: beginner
---

Build pravo from source. The commands run in a terminal.

:::tabs
```sh title="macOS"
go build -o pravo ./cmd/pravo
./pravo version
```

```sh title="Linux"
go build -o pravo ./cmd/pravo
./pravo version
```

```sh title="Windows"
go build -o pravo.exe .\cmd\pravo
.\pravo.exe version
```
:::

```sh run
pravo version
```

```text out
pravo 0.1.0-dev
```

Then continue with [Writing a field twice](../learn/a03-writing-a-field-twice.md).

```sh run
pravo init
pravo plan
```

```text out
Initializing providers...
...
Plan: 2 to add, 0 to change, 0 to destroy.
```

```sh run hidden
mkdir -p setup
```

```sh run exit=5
pravo plan -t env=qa
```

```text out
Error: policy refused the plan.
```
