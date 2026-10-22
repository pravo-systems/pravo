<!-- Generated from docs/reference/spec-tags-and-variables.md by tools/skillgen. Do not edit. -->

# Tags and variables

Inputs are fields with a @tag attribute. -t and -T inject their values. Defaults and constraints are plain CUE.

An input is an ordinary field with the attribute `@tag(name)`. `-t name=value` sets it for one run. Defaults, allowed values and types are written in CUE on the field.

| Form | Meaning |
|---|---|
| `env: *"dev" \| "prod" @tag(env)` | A default and the allowed values. `-t env=qa` fails. |
| `region: string & =~"^eu-" @tag(region)` | No default. The value must be injected and must start with `eu-`. |
| `count: *1 \| int @tag(count,type=int)` | A typed tag. `type=` is `string`, `int`, `number` or `bool`. |
| `user: string @tag(user,var=username)` | A system tag, injected by `-T`. |
| `-t name=value` | Sets the field tagged `name`. Repeatable. |
| `-T` | Injects the system tags. |

These commands take `-t` and `-T`: `init`, `gen`, `validate`, `eval`, `plan`, `apply`, `destroy`, `drift`, `output`, `import`, `providers`, `schema`, `why` and every `pravo state` subcommand. The state commands evaluate the specification to find the state, so `-t env=prod` selects the state of `prod`. A saved plan carries its tags, so `show` takes none, and `apply FILE` refuses them. There is no variables file. A set of values goes into a `.cue` file of the package.

Work in an empty directory.

File `main.cue`:

```cue
package demo

env:    *"dev" | "staging" | "prod" @tag(env)
region: string & =~"^eu-"           @tag(region)
count:  *1 | int                    @tag(count,type=int)
debug:  *false | bool               @tag(debug,type=bool)

_prefix: "acme-\(env)"
name:    "\(_prefix)-\(region)"
```

```sh
pravo init > /dev/null
pravo eval
pravo eval -t region=eu-west-1 -t count=3
```

Output:

```text
env:    "dev"
region: =~"^eu-"
count:  1
debug:  false
name:   "\(_prefix)-\(region)"
env:    "dev"
region: "eu-west-1"
count:  3
debug:  false
name:   "acme-dev-eu-west-1"
```

`env` has the default `"dev"` and allows three values. `region` has no default, so it prints as its constraint until a value arrives. With `-t region=eu-west-1`, `name` resolves. `pravo eval -c` fails while a value is not concrete.

## Errors

| Cause | Where |
|---|---|
| A value outside the allowed values or the constraint. | Evaluation, before any provider starts. |
| A tag that no field declares. | Loading: `no tag for "nope"`. |
| A value that does not parse as the declared type. | Loading: `invalid number for injection tag "count"`. |
| A field that stays non-concrete. | `pravo eval -c`, or the plan: `value depends on ... which is not concrete (is a -t tag missing?)`. |

```sh
pravo eval -t region=eu-west-1 -t env=qa
pravo eval -t region=us-east-1
pravo eval -t region=eu-west-1 -t nope=1
pravo eval -t region=eu-west-1 -t count=x
```

Output:

```text
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./main.cue:3:10
env: conflicting values "prod" and "qa":
    ./main.cue:3:30
env: conflicting values "staging" and "qa":
    ./main.cue:3:18


Error: evaluating .:
region: invalid value "us-east-1" (out of bound =~"^eu-"):
    ./main.cue:4:18
    ./main.cue:4:9


Error: loading .:
no tag for "nope"


Error: loading .:
invalid number for injection tag "count": illegal number start "x"
```

## System tags

`-T` injects the variables that CUE defines. A field reads one with `@tag(name,var=variable)`.

| Variable | Value | Type |
|---|---|---|
| `now` | The current time, RFC 3339 in UTC. | string |
| `os` | The operating system, such as `darwin`. | string |
| `arch` | The architecture, such as `arm64`. | string |
| `cwd` | The working directory. | string |
| `username` | The name of the operating-system user. | string |
| `hostname` | The host name. | string |
| `rand` | A random integer. | int |

File `main.cue`:

```cue
package demo

os:       string @tag(os,var=os)
operator: string @tag(operator,var=username)
```

```sh
pravo eval -T -e 'len(os) > 0 && len(operator) > 0'
```

Output:

```text
true
```

`now` and `rand` change on every run. A specification that uses them plans a change every time.

## Tags are not secret

A tag value is written into saved plans and feeds the configuration hash. Use tags for configuration such as an environment name. Read credentials with [`pravo.#SecretEnv`](spec-env.md#secret-env).

A saved plan carries its tags. `pravo apply <planfile>` applies the plan with the values it was made with:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

env: *"dev" | "prod" @tag(env)

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2, prefix: env}
```

```sh
pravo init > /dev/null
pravo plan -t env=prod --out change.plan > /dev/null
jq .tags change.plan
```

Output:

```text
[
  "env=prod"
]
```

## Defaults

A default is marked with `*` in a disjunction:

```cue
retention: *30 | int & >0
```

When nothing else constrains the field, the default is used. A value from another file or from `-t` wins as long as it satisfies the disjunction.

## Locals

Any field is a local. A field whose name starts with `_` is a hidden field: it is not exported, not searched for resources and never sent to a provider. A hidden field can sit inside a resource body too.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

env: *"dev" | "prod" @tag(env)

$random: rnd.#Provider

_prefix: "acme-\(env)"
_common: {Environment: env, ManagedBy: "pravo"}

pet: $random.#Resource.random_pet & {
	prefix:  _prefix
	keepers: _common
}
```

```sh
pravo plan | grep -E 'prefix|Environment|ManagedBy'
```

Output:

```text
  +         Environment: "dev"
  +         ManagedBy:   "pravo"
  +     prefix:    "acme-dev"
```

## The standard library

The CUE standard library is available with `import`. The packages used most in infrastructure are `strings`, `list`, `encoding/json`, `math` and `net`. `json.Marshal` serialises a value, `strings.Join` joins a list, `len` counts, and a constraint such as `net.IPCIDR` checks a value. An import that is not used is an error.

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	"strings"
	"list"
	"encoding/json"
	"math"
)

_zones: ["a", "b", "c"]

zone_count: pravo.#Output & {value: len(_zones)}
first_zone: pravo.#Output & {value: list.Take(_zones, 1)}
policy:     pravo.#Output & {value: json.Marshal({Version: "2012-10-17", Zones: _zones})}
upper:      pravo.#Output & {value: strings.ToUpper("read-logs")}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
imported and not used: "math":
    ./main.cue:8:2
```

The error names the import that nothing uses. Remove it, or use it, and the specification evaluates.

## See also

- [Run one specification in several environments](../how-to/environments.md) builds an environment table on a tag.
- [Environment and remote outputs](spec-env.md) reads credentials and other environment variables.
- [`pravo eval`](validate-eval-fmt-gen.md#pravo-eval) prints the value of a field.
