---
title: Concepts
description: How pravo behaves, with diagrams. Each page explains one part of the system.
---

Concepts explains how pravo behaves. Each page follows one part of the system. A figure replaces text where a picture is shorter.

The pages describe behaviour and nothing else. To learn by doing, use [Learn](../learn/index.md). To do one task, use [How-to](../how-to/index.md). To look up a command or a field, use [Reference](../reference/index.md).

## Pages

Read them in this order. Later pages use terms that earlier pages define.

1. [How pravo works](how-pravo-works.md). The path of a command from the specification to the providers, the state and the output.
2. [Evaluation](evaluation.md). How pravo turns the CUE files of a directory into values, and where it stops before any provider starts.
3. [Resources and addresses](resources-and-addresses.md). What makes a struct a resource, and how its address identifies it.
4. [Plans](plans.md). How a plan is computed, what unknown values are, how a plan can cover part of a graph, and how a plan is saved, stored in the state and collected.
5. [State](state.md). What pravo records for each resource, how transactions and locks work, and why the cost of a command follows the change.
6. [What a plan reaches](what-a-plan-reaches.md). Four changes to one estate, shown: which resources a plan reaches and which locks an apply holds.
7. [Providers](providers.md). How pravo finds, starts and talks to a provider.
8. [Policies](policies.md). Two moments for one idea: a rule on a provider and a policy on a plan, and what each of them binds.
9. [Secrets and ephemeral values](secrets-and-ephemeral-values.md). How sensitive and short-lived values travel through plans, state and outputs.
10. [The viewer](the-viewer.md). How `pravo serve` joins the specification, the state and a saved plan into one graph, what a namespace is to it, and which values it withholds.
11. [What sealing protects](sealed-state.md). What a sealed state encrypts, how a reader opens it, and what it leaves open.

## Where each idea is taught

| Concept | Lesson |
|---|---|
| Evaluation | [A3, Writing a field twice](../learn/a03-writing-a-field-twice.md) |
| Resources and addresses | [B1, Providers and resources](../learn/b01-providers-and-resources.md) |
| Plans | [B2, Reading a plan](../learn/b02-reading-a-plan.md) |
| State | [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md) |
| Providers | [B1, Providers and resources](../learn/b01-providers-and-resources.md) and [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| Policies | [B10, Policy on providers and on plans](../learn/b10-policy.md) |
| Secrets and ephemeral values | [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| The viewer | None. [Explore an estate in a browser](../how-to/explore-your-estate.md) shows it. |
| Sealed state | None. [Seal the state to a list of readers](../how-to/seal-the-state.md) shows it. |

The lessons run the commands. The concept pages explain what the commands did.
