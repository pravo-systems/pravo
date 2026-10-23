---
title: Learn
description: Two tracks in order. Track A teaches CUE from zero. Track B teaches pravo. Each lesson takes between 6 and 20 minutes.
---

Learn has two tracks. Track A teaches CUE with pravo as the only tool. Track B teaches pravo on top of it.

Learn teaches, in order. To do one task on a project you already understand, use [How-to](../how-to/index.md).

Each lesson takes between 6 and 20 minutes. It shows a problem you know from HCL, the idea that solves it, and a command you run yourself. It ends with something you can check.

## Which door

<div class="cards" style="grid-template-columns: repeat(auto-fit, minmax(12.5rem, 1fr))">
<div class="card">

### I run OpenTofu or Terraform

Start with [Migrate](../migrate/index.md). It links to the Track A lessons that a page needs, at the point where it needs them.

</div>
<div class="card">

### I have never seen CUE

Start Track A at [A1, Your data is already CUE](a01-your-data-is-already-cue.md). Then continue with Track B.

</div>
<div class="card">

### Show me the reference

Open the [Reference](../reference/index.md), or the [CUE cheat sheet](cue-cheat-sheet.md) for the language on one page.

</div>
</div>

If you write CUE already, begin Track B at [B1, Providers and resources](b01-providers-and-resources.md).

## The two tracks

| Track | Lessons | Time | You finish with |
|---|---|---|---|
| **Track A · CUE from zero** | [A1](a01-your-data-is-already-cue.md) to [A14](a14-project-three-environments.md) and five companion pages | about 2 hours | One definition that produces three environments, checked by `pravo eval`. |
| **Track B · pravo** | [B1](b01-providers-and-resources.md) to [B10](b10-policy.md), three labs and a [project](b11-project.md) | about 2 hours, plus the labs | A specification that you plan, apply, change, move, check against a policy and destroy. |

Do Track A first. Track B assumes the CUE that Track A teaches. The [quick start](../how-to/quick-start.md) runs a plan before you know any CUE, and Track A explains what you saw there.

The companion pages sit beside Track A. Open them when a lesson sends you there, or when an error confuses you.

- [Reading a CUE error](c1-reading-a-cue-error.md)
- [Why is my value incomplete?](c2-why-is-my-value-incomplete.md)
- [Closed or open?](c3-closed-or-open.md)
- [eval and validate](c4-eval-and-validate.md)
- [CUE cheat sheet](cue-cheat-sheet.md)

The [glossary](../reference/glossary.md) defines every term. Track A defines at most two new terms in a lesson, where they first appear.

## What you need

- The `pravo` binary on your `PATH`. See the [quick start](../how-to/quick-start.md#1-install-pravo).
- A terminal, a text editor and an empty directory. A1 to A5 build on one directory. Each later lesson says where to work.
- For Track A, nothing else. `pravo eval` and `pravo validate` evaluate plain CUE, so almost every example needs no provider, no account and no network. A10, C2, C3 and C4 each run one example with a provider from [Quick start](../how-to/quick-start.md). `pravo init` fetches it once.
- For Track B, providers that run on your machine. A lesson that needs more says so in its header, under "Needs".

Examples use a POSIX shell. The output in the lessons comes from a real run of the examples. Ids and times are normalised, and absolute paths are shortened to `./`. Your terminal prints the full path.

## How a lesson works

Every lesson opens with a header. It gives the time, the lesson you need first, what you will learn and when you are done. The same parts follow in the same order.

1. **The problem**, in HCL, in a few lines.
2. **The idea**, in one or two sentences.
3. **An example** that you run, and its output.
4. **A failure** that you cause. It is labelled before you run it, and the error is read line by line: path, value, rule, location.
5. **A prediction** in some lessons. You decide, then open the answer.
6. **An exercise** with up to two hints and a collapsed solution. Every lesson except the project A14 has a script that checks your work.
7. **A recap** of three points and two questions to check yourself.

The sidebar lists the lessons in order, and the foot of each page links to the next one.

## How progress works

Every lesson, lab and companion page of Learn ends with a **Mark as done** button. Your browser keeps the marks in its local storage. They stay on your machine, so another browser or device starts empty.

The sidebar marks each page you finished and shows a count of the form `n of N pages done`. The button toggles: press it again to clear the mark. Without JavaScript the button is absent and the pages work without it.
