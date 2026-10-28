---
title: pravo version
description: Print the version of pravo and the version of CUE that is built into it.
order: 21
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo version` |
| Flags | None. The [global flags](global-flags.md) are accepted, and `--json` has no effect: the output is always text. |
| Exit code | 0 |
| Taught in | [Quick start](../how-to/quick-start.md#1-install-pravo). |

## Output

```sh run
pravo version
```

```text out
pravo 0.1.0-dev
cue v0.17.1
```

| Line | Meaning |
|---|---|
| `pravo X` | The version of pravo. |
| `cue vX` | The version of CUE that is built into the binary. |

The footer of each page of this site names the same two versions. [Compatibility](compatibility.md) lists what each version works with.

## The CUE version and policies

CUE evaluates the specification, and it evaluates every plan policy. A policy behaves as the second line says, so test a policy with a `cue` binary of that version. With a Go toolchain, `go install` installs it:

```sh
go install cuelang.org/go/cmd/cue@v0.17.1
```

Use the version that `pravo version` prints. Save a plan as JSON and check the policy against it:

```sh
pravo plan --json > fixture.json
cue vet -c policy/keep.cue fixture.json
```

`cue vet -c` reports a conflict at the same path that `pravo plan` reports for a policy in force, and exits with a non-zero code. The plan document is not masked, so scrub a fixture before you commit it. See [Policies](policies-overview.md).

## See also

- [Quick start](../how-to/quick-start.md#1-install-pravo) builds the binary.
- [`pravo init`](init.md) shows which provider versions a root resolves.
