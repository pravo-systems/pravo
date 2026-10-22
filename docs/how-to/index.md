---
title: How-to
description: Task-shaped pages for a reader who knows the basics. Each page shows how to do one thing and stops there.
---

How-to pages are for when you know the basics and need to do one thing: share state, run a pipeline, publish a module. Each shows the task and stops there. It does not teach the ideas behind the steps; the last column links to the lesson or the concept page that does. New to pravo? [Learn](../learn/index.md) teaches it in order.

## What do you want to do?

| I want to | Guide | Background |
|---|---|---|
| Install pravo and run a first plan, a change, a conflict and a destroy | [Quick start](quick-start.md) | [Learn](../learn/index.md) |
| Run one specification for dev, staging and prod | [Environments](environments.md) | [B4, Inputs, tags, environments](../learn/b04-inputs-tags-environments.md) |
| Keep the state in a bucket that a team shares | [Remote state](remote-state.md) | [State](../concepts/state.md) |
| Encrypt the values in the state to a list of readers | [Seal the state](seal-the-state.md) | [What sealing protects](../concepts/sealed-state.md) |
| Share CUE between roots in one repository | [Modules in a repository](modules-in-a-repository.md) | [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| Publish a module, or use one that someone else published | [Publishing a module](publishing-a-module.md), [Using a module](using-a-module.md) | [B6, Modules, providers, credentials](../learn/b06-modules-providers-credentials.md) |
| Use a provider from a registry other than the default | [Providers from a registry](providers-from-a-registry.md) | [Providers](../concepts/providers.md) |
| Run pravo in a pipeline | [CI](ci.md) | [Exit codes](../reference/exit-codes.md) |
| Let a team list, read and apply a plan before anyone applies it | [Share a plan before you apply it](share-a-plan-before-you-apply-it.md) | [Plans](../concepts/plans.md#stored-plans) |
| Refuse plans that break a rule | [Policies](policies.md) | [B10, Policy on providers and on plans](../learn/b10-policy.md) |
| Let an agent plan and apply | [Agents and MCP](agents-and-mcp.md) | [pravo mcp](../reference/mcp.md) |
| Handle secrets and credentials | [Secrets](secrets.md) | [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) |
| Rename a resource, move it, or adopt an existing object | [Importing and moving](importing-and-moving.md) | [B8, Renames, moves, imports](../learn/b08-renames-moves-imports.md) |
| See an estate as a graph of modules and resources, with its source, a saved plan and drift | [Explore an estate in a browser](explore-your-estate.md) | [The viewer](../concepts/the-viewer.md) |
| Find out why an evaluation or a plan fails | [Debugging](debugging.md) | [Reading a CUE error](../learn/c1-reading-a-cue-error.md) |
| Upgrade pravo or a provider | [Upgrading](upgrading.md) | [Compatibility](../reference/compatibility.md) |

## Looking for something else

- A message you do not recognise: the [error catalogue](../reference/errors.md) lists symptom, cause and fix.
- A project that runs on OpenTofu or Terraform today: [Migrate](../migrate/index.md).
- How pravo behaves, with diagrams: [Concepts](../concepts/index.md).
- One command or one specification topic: [Reference](../reference/index.md).
- CUE from the beginning: [Learn](../learn/index.md).
