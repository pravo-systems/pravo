---
title: The policy pack
description: The starter rules of pravo.dev/policies, the parameters of each, what each refuses, and how a rule that names one is stored.
order: 44
group: Policies
---

`pravo.dev/policies` is a CUE package that `pravo init` writes to `cue.mod/gen/pravo.dev/policies`, beside `pravo.dev/pravo`. Each rule in it is a definition that a policy names as its `rule` and parameterises by unification:

```cue
import (
	"pravo.dev/pravo"
	"pravo.dev/policies"
)

tagged: pravo.#Policy & {
	rule: policies.#RequiredTags & {#keys: ["owner", "env"]}
}
```

A parameter is a definition of the rule (`#keys`), not a field of the plan document. A parameter with a default can be left out. A parameter without one, such as `#types`, must be set, or the rule judges nothing.

## The rules

| Rule | Refuses | Parameters |
|---|---|---|
| `#RequiredTags` | A managed resource that is created, updated or replaced, that has a `tags` attribute, and that lacks a key, has it empty, or has `tags` null or unknown. A resource without a `tags` attribute is not judged. | `#keys`: the keys, default `["owner"]`. |
| `#KeepTypes` | The deletion or replacement of a resource of a listed provider type. | `#types`: the types, such as `["aws_db_instance"]`. |
| `#ChangeWindow` | A create, update, replace or delete outside the days and hours. | `#days`: the days of the week, default Monday to Friday. `#from`: the first hour, 0 to 23, default 8. `#until`: the hour the window ends, 1 to 24, default 17. |
| `#Freeze` | A create, update, replace or delete from one time up to another. | `#from` and `#until`: times in the form `2026-12-24T00:00:00Z`. Both are required. |

Hours and days are in UTC, and `#until` is the first hour that is outside the window. A `read`, `open`, `call`, `move` or `no-op` is not a change. The refusal names the resource: `server: replace outside the change window, Saturday 10:00 UTC`.

## How a rule is stored

A policy version is standalone text: it imports the CUE standard library and nothing else. When a policy names a rule of the pack, the specification loader copies the body of the rule into the text, with the imports it needs and the parameters you set, and drops the import of `pravo.dev/policies`. The digest and the signature cover the copied text. The stored version is the same text whatever the package holds later, and a new version is written when the specification gives the policy another text.

A policy file for [`pravo policy test`](policy.md) and the policy studio imports `pravo.dev/pravo` only. Try a pack rule through the policy of the specification: `pravo policy test hours --at 2026-11-07T10:00:00Z`.

## See also

- [Start from the policy pack](../how-to/start-from-the-policy-pack.md) runs a window and a kept type.
- [The plan document in a policy](policies-plan-document.md#the-clock) says what `now` holds.
- [Writing policies](policies-writing.md) shows the shapes the pack is written in.
