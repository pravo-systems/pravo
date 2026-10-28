---
title: Concepts
description: How pravo behaves, with diagrams. Each page explains one part of the system, from a run of a command to the viewer, the sealed state and the interfaces for programs.
---

Concepts explains how pravo behaves. Each page follows one part of the system. A figure replaces text where a picture is shorter, and a run shows what a command prints where the output is the point.

The pages describe behaviour and nothing else. To learn by doing, use [Learn](../learn/index.md). To do one task, use [How-to](../how-to/index.md). To look up a command or a field, use [Reference](../reference/index.md).

## Pages

Read them in this order. Later pages use terms that earlier pages define.

**One run**

1. [How pravo works](how-pravo-works.md). The path of a command from the specification to the providers, the state and the output, in seven stages.
2. [Evaluation](evaluation.md). How pravo turns the CUE files of a directory into values, and where it stops before any provider starts.
3. [Resources and addresses](resources-and-addresses.md). What makes a struct a resource, and how its address identifies it.
4. [Plans](plans.md). How a plan is computed, what unknown values are, how a plan can cover part of a graph, and how a saved plan is checked when it is applied.
5. [State](state.md). What pravo records for each resource, how transactions and locks work, and why the cost of a command follows the change.
6. [What a plan reaches](what-a-plan-reaches.md). Four changes to one estate, shown: which resources a plan reaches and which locks an apply holds.
7. [Providers](providers.md). How pravo finds, starts and talks to a provider.

**A team on one state**

8. [Stored plans and approvals](stored-plans-and-approvals.md). How a plan lives in the state, how it moves from pending to applied, and how a policy makes it wait for the approval of other people.
9. [Policies](policies.md). Two moments for one idea: a rule on a provider and a policy on a plan, what each of them binds, and what neither guarantees.
10. [Secrets and ephemeral values](secrets-and-ephemeral-values.md). How sensitive and short-lived values travel through plans, state and outputs.
11. [Signatures and roles](signatures-and-roles.md). What a sealed state encrypts, what a signature binds, what a role allows, and what none of the three does.

**Reading an estate**

12. [The viewer](the-viewer.md). How `pravo serve` joins the specification, the state, the stored plans and the recorded runs into one page, what a group is to it, and which values it withholds.
13. [Traces](traces.md). What a run records about itself: the spans, the network calls under them, and what a span never contains.
14. [Discovery](discovery.md). How a discovery lists the objects that exist, tells the unmanaged ones from the rest and notices that its scope changed.
15. [The library and the machine interface](the-library-and-the-machine-interface.md). The commands with `--json`, the MCP server and the Go library: what each gives a program, and which is the stable surface.

## Where each idea is taught

| Concept | Lesson |
|---|---|
| Evaluation | [A3, Writing a field twice](../learn/a03-writing-a-field-twice.md) |
| Resources and addresses | [B1, Providers and resources](../learn/b01-providers-and-resources.md) |
| Plans | [B2, Reading a plan](../learn/b02-reading-a-plan.md) |
| State | [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md) |
| Providers | [B1, Providers and resources](../learn/b01-providers-and-resources.md) and [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| Stored plans and approvals | [D2, Store a plan](../learn/d02-store-a-plan.md) and [D6, Ask for approvals](../learn/d06-ask-for-approvals.md) |
| Policies | [B10, Policy on providers and on plans](../learn/b10-policy.md) and [D5, Write a plan policy](../learn/d05-write-a-plan-policy.md) |
| Secrets and ephemeral values | [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| Signatures and roles | [D3, Seal the state](../learn/d03-seal-the-state.md) and [D4, Readers, writers, admins](../learn/d04-readers-writers-admins.md) |
| The viewer | [Explore an estate in a browser](../how-to/explore-your-estate.md) shows every section. [D5, Write a plan policy](../learn/d05-write-a-plan-policy.md) tries a rule in the policy studio, and [D7, Read a run](../learn/d07-read-a-run.md) reads a run in the Traces section. |
| Traces | [D7, Read a run](../learn/d07-read-a-run.md) |
| Discovery | None. [Find what nothing manages](../how-to/find-what-nothing-manages.md) shows it. |
| The library and the machine interface | None. [Embed pravo in your program](../how-to/embed-pravo-in-your-program.md) and [Agents and MCP](../how-to/agents-and-mcp.md) show it. |

The lessons run the commands. The concept pages explain what the commands did.
