<!-- Generated from docs/concepts/templates.md by tools/skillgen. Do not edit. -->

# Templates and self-service

How a template turns a definition into a form, why an instance follows its template, which checks a new instance meets before and after it is pasted, and who needs which key.

A template is a definition that says it is one. Its instances are groups built from it. A platform team writes the template once, with the inputs it asks for and the constraints on them. Anyone on the team opens it in the viewer, fills in a form, and gets the CUE of a new instance to paste into the specification. From there the request is a change like any other: it is planned, judged by the policies and applied.

pravo has no module block, and a template is not one: it is an ordinary definition with a marker, used through instances and shared through a package, as [Reuse](../reference/spec-reuse.md) describes. [Offer a template to your team](../how-to/offer-a-template.md) writes one, [Make a new instance from a template](../how-to/add-an-instance-from-a-template.md) uses one, and [C8, Self-service from a template](../learn/c08-self-service-from-a-template.md) follows a team through both.

*Figure: A request from a template, from the definition to the instance in the graph. The dashed line is a change to the template, which reaches every instance at the next plan.*

## A template and its instances

A definition becomes a template when it embeds `pravo.#Template` and names itself in the marker `$template`:

```cue
#Service: pravo.#Template & {
	$template: {
		name:  "service"
		title: "Web service"
	}
	// Lowercase letters, digits and dashes. It names the config file.
	name:  string & =~"^[a-z][a-z0-9-]{2,30}$"
	owner: string & =~"^team-[a-z0-9-]+$"
	env:   *"dev" | "staging" | "prod"

	secret: $random.#Resource.random_password & {length: 24}
	config: $local.#Resource.local_file & {
		filename: "out/\(name).env"
		content:  "OWNER=\(owner)\nENV=\(env)\n"
	}
}

services: web: #Service & {name: "web", owner: "team-shop"}
```

`services.web` is an instance. It is a group like any other, its resources are `services.web.secret` and `services.web.config`, and nothing about it differs in a plan. The marker is an ordinary field, so it passes into the instance when the definition is unified with it, and that is how the viewer, a plan and a policy know which template an instance came from.

## An instance follows its template

An instance is built from the template, not copied from it. `#Service & {...}` is a unification, which CUE works out on every evaluation, so the instance is whatever the template says now, with the values that the instance gives. Change the template and every instance changes with it: the next plan lists the change of each instance that it reaches, and the apply makes them. Nothing has to be regenerated or upgraded.

A template that the repository's own code defines moves at the commit that changes it. A template that a published CUE module defines moves when a root takes the new version of the module with `pravo mod`, so the roots of dev, staging and prod can take it one at a time. See [Use a published CUE module](../how-to/using-a-module.md).

## The definition is the form

The viewer builds the form from the definition, so the form needs no schema of its own. The type, the default, the comment and the constraint of each input field become the field of the form. `$template.inputs` orders the inputs, gives them labels, and asks for a control that the type alone does not give.

| In the definition | The form shows |
|---|---|
| `string`, `int`, `number` with a bound or a pattern | A text field, with the bound or the pattern beside it, and the default filled in. |
| `"dev" \| "staging" \| "prod"` | A row of buttons, one for each value. |
| `[...string]` | A row of chips, with an empty entry to type the next. |
| `choices: NODE.ATTR` in `$template.inputs` | The values that the state holds at that attribute, such as the regions that a data source reported. |
| `entity: {type, attr}` | A picker of the managed resources of that type, which writes a reference such as `core.network_id.hex`. |
| `as: "text"` | A text area. |
| `as: "secret"` | Where the secret comes from and its name. It never takes the value. |

Any field that takes a value also offers a reference instead of a value: the picker lists the attributes of the estate, such as `network.vpc.id`, with what the state holds for each now. [Templates](../reference/spec-reuse.md#templates) lists every form that `$template.inputs` takes.

## Three checks, and where each runs

A new instance meets three checks. The form runs the first and previews the second. The same checks bind an instance that someone typed by hand: the form is a convenience, not the guard.

| Check | Written as | Runs | In the form |
|---|---|---|---|
| Constraint | CUE on the fields of the template | When the specification is evaluated: `pravo validate`, every plan | Yes. The viewer loads the specification with the new code laid over it in memory, and shows the message of CUE under the field. |
| Policy | `pravo.#Policy` rules, kept in a sealed state | At the plan, and at the apply of a stored plan | A preview. Each policy is tried on an approximate plan, in which every new resource is a create and only the values the code sets are known. A rule that reads a value a provider decides is reported as not decided. |
| Roles | `reader`, `writer` and `admin` in the readers file, pinned in the state | At the apply | No. |

Each change of a plan names the template instance that holds its node, as `template: {name, instance}`, so a policy can hold every instance of one template to a rule without matching their addresses. [Writing policies](../reference/policies-writing.md#rules-for-the-instances-of-a-template) shows one.

## Who needs which key

The person who asks for an instance needs a checkout of the repository and the `pravo` binary. They do not need the right to change the estate.

| Key | May |
|---|---|
| None | Open the catalog and the form, and get the code. On a sealed state the lists of choices and the values beside a reference are withheld, and the form takes a typed value. |
| Reader | All of that with the values, plan the change and store the plan with `pravo plan --store`. |
| Writer | Apply the stored plan by its id. `pravo plan show` names the key that stored it, and the reason. |

So a plan can be prepared by one role and applied by another. A team that wants a pull request in between commits the pasted code first and lets its pipeline plan it, as [Run pravo in a pipeline](../how-to/ci.md) shows.

## What it does not do

- The viewer writes nothing. It shows the code, and a person pastes it. It opens no pull request and applies nothing.
- The preview of the policies is an approximation, and says so. The verdict that counts is the one at the plan.
- A reference is checked against its constraint only at the plan, because its value is not known before.
- The catalog lists the templates of the root. A template that a package defines is listed once a definition of the root embeds it.
- A change of a node outside every instance names no template, and neither does the delete of a whole instance, which the specification does not hold any more.
