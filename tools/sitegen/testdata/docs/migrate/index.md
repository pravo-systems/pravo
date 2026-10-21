---
title: Migrate
description: Move an OpenTofu or Terraform project to pravo. One recommended path, two alternatives.
---

The recommended path translates the configuration and converts the state. The gate is an empty plan.

1. Back up the state file.
2. Translate the configuration. See the [concept map](concept-map.md).
3. Convert the state with `pravo state import-tfstate terraform.tfstate`.
4. Run `pravo plan`. The plan is empty.
5. Apply and cut over.

> [!CHECK]
> Do not apply until `pravo plan` prints `Plan: 0 to add, 0 to change, 0 to destroy.`
