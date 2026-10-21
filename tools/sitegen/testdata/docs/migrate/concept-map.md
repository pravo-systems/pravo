---
title: Concept map
description: What each OpenTofu or Terraform construct becomes in pravo.
order: 1
minutes: 6
level: intermediate
---

Each row pairs a construct with its CUE form.

| HCL | CUE |
|---|---|
| `variable` with `validation` | a constraint on a field |
| `locals` | a `let` or a hidden field |
| `count`, `for_each` | a comprehension |
| module | a definition |

:::compare
```hcl title="HCL"
variable "env" {
  type = string
  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env must be dev or prod."
  }
}
```

```cue title="CUE"
env: "dev" | "prod"
```
:::

pravo has no workspaces. Select the environment with `-t env=prod`.

```diff
-  count = var.enabled ? 1 : 0
+  for i, _ in [if enabled {1}] {
```
