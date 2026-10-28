---
title: How-to
description: Task-shaped pages for a reader who knows the basics. Each page shows how to do one thing and stops there, in six groups, from a first plan to a team on one sealed state.
---

How-to pages are for when you know the basics and need to do one thing: share the state, run a pipeline, give someone a role, read a recorded run. Each shows the task and stops there. It does not teach the ideas behind the steps; the last column links to the lesson or the concept page that does. New to pravo? [Learn](../learn/index.md) teaches it in order, and the [Quick start](quick-start.md) takes twelve minutes.

Every page runs its own examples, with no account and no cloud. Where a page needs a bucket, it uses the in-memory one from pravo's source, and where it needs several people, they are directories on one machine.

## Get started

| I want to | Guide | Background |
|---|---|---|
| Install pravo and run a first plan, a change, a conflict and a destroy | [Quick start](quick-start.md) | [Learn](../learn/index.md) |

## Specify

How one specification covers several environments, shares code, and gets its providers and CUE modules. pravo has no module block: shared code is a definition in a package.

| I want to | Guide | Background |
|---|---|---|
| Run one specification for dev, staging and prod | [Run one specification in several environments](environments.md) | [B4, Inputs, tags, environments](../learn/b04-inputs-tags-environments.md) |
| Share CUE between roots in one repository | [Share code between roots in one repository](share-code-between-roots.md) | [B6, Reuse, providers, credentials](../learn/b06-reuse-providers-credentials.md) |
| Publish code for other repositories | [Publish a CUE module](publishing-a-module.md) | [Reuse](../reference/spec-reuse.md) |
| Use code that someone published, and take an upgrade when you choose to | [Use a published CUE module](using-a-module.md) | [Reuse](../reference/spec-reuse.md) |
| Install providers from a registry of my own | [Install providers from your own registry](providers-from-a-registry.md) | [Providers](../concepts/providers.md) |

## Share and protect

A team on one state: where the state lives, who reads it, who writes it, and how a plan waits for a second pair of eyes.

| I want to | Guide | Background |
|---|---|---|
| Keep the state in a bucket, or on an HTTP server, that a team shares | [Keep the state in a shared store](keep-the-state-in-a-shared-store.md) | [State](../concepts/state.md) |
| Encrypt the values in the state to a list of readers | [Seal the state to a list of readers](seal-the-state.md) | [Signatures and roles](../concepts/signatures-and-roles.md) |
| Say who may change the state and who may only read and plan | [Give people roles](give-people-roles.md) | [Signatures and roles](../concepts/signatures-and-roles.md), [pravo protect roles](../reference/roles.md) |
| Let a team list and read a plan before anyone applies it | [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) | [Stored plans and approvals](../concepts/stored-plans-and-approvals.md) |
| Make a risky plan wait for another member's approval | [Approve a plan](approve-a-plan.md) | [Stored plans and approvals](../concepts/stored-plans-and-approvals.md#approvals) |

## Operate

Run pravo from a pipeline, a program or an agent, under rules, and keep the estate honest.

| I want to | Guide | Background |
|---|---|---|
| Run pravo in a pipeline | [Run pravo in a pipeline](ci.md) | [Exit codes](../reference/exit-codes.md) |
| Refuse plans that break a rule, or make them wait for an approval | [Write and roll out policies](policies.md) | [Constraints and policies](../concepts/policies.md), [B10, Constraints on providers, policy on plans](../learn/b10-policy.md) |
| Let an AI agent plan, under the policies and with a key of its own | [Let an agent plan and apply](agents-and-mcp.md) | [pravo mcp](../reference/mcp.md) |
| Plan and apply from a Go program | [Embed pravo in your program](embed-pravo-in-your-program.md) | [The library and the machine interface](../concepts/the-library-and-the-machine-interface.md), [The Go library](../reference/library.md) |
| Handle secrets and credentials | [Keep secrets out of the state](secrets.md) | [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) |
| Rename a resource, move it, or adopt an existing object | [Rename, move and adopt resources](importing-and-moving.md) | [B8, Renames, moves, imports](../learn/b08-renames-moves-imports.md) |
| Find the objects that exist and that nothing manages | [Find what nothing manages](find-what-nothing-manages.md) | [Discovery](../concepts/discovery.md) |

## Observe

See the estate, what the team planned, and what a run did.

| I want to | Guide | Background |
|---|---|---|
| See the estate as a graph, with its history, the team's plans, drift and the policies in force | [Explore an estate in a browser](explore-your-estate.md) | [The viewer](../concepts/the-viewer.md) |
| Keep a trace of every run, find the slow resource and the call that failed | [Record what a run does](record-what-a-run-does.md) | [Traces](../concepts/traces.md) |
| Find out why an evaluation or a plan fails | [Find out why a run fails](debugging.md) | [Reading a CUE error](../learn/reading-a-cue-error.md) |

## Maintain

| I want to | Guide | Background |
|---|---|---|
| Upgrade pravo or a provider | [Upgrade pravo and providers](upgrading.md) | [Compatibility](../reference/compatibility.md) |

## Looking for something else

- A message you do not recognise: the [error catalogue](../reference/errors.md) lists symptom, cause and fix.
- A project that runs on OpenTofu or Terraform today: [Migrate](../migrate/index.md).
- How pravo behaves, with diagrams: [Concepts](../concepts/index.md).
- One command or one specification topic: [Reference](../reference/index.md).
- CUE from the beginning: [Learn](../learn/index.md).
