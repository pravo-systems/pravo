---
title: Environments scenario
description: One specification with dev and prod, prod following dev, each with a state and a readers file of its own. The environments band of the landing page shows this file and this output.
capture: true
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
  - pattern: '\b[0-9a-f]{12}\b'
    replace: '<release-id>'
  - pattern: '\b[0-9]s ago'
    replace: '0s ago'
---

This page is not published. `tools/capture` runs it, and `site/demos/idea/mk.go environments` renders the blocks into `site/landing/13-environments.html`. The root is the one of [Promote a release from one environment to the next](../../../docs/how-to/promote-a-release.md), with a staging environment in between. The picture of the viewer in the last tab is the shot `environments-flow` of `tools/serveshots`.

```sh hidden
cat > main.cue <<'CUE'
package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

// The environments, and what differs between them.
$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	staging: {workers: 2, $after: "dev"}
	prod: {workers: 3, $after: "staging"}
}

// Each environment has its own state and its own readers.
$settings: pravo.#Settings & {
	state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"
}

$local: loc.#Provider

// Written once. It never names an environment.
conf: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "workers \($environment.workers)\n"
}
CUE
```

The file, cut to what carries the idea. `...` stands for the provider and its import.

```cue title="main.cue" id=spec status=valid
$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	staging: {workers: 2, $after: "dev"}
	prod: {workers: 3, $after: "staging"}
}
$settings: pravo.#Settings & {
	state: path:  "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"
}
...
conf: $local.#Resource.local_file & {
	content: "workers \($environment.workers)\n"
}
```

```sh hidden
mkdir -p .pravo/access/dev .pravo/access/staging .pravo/access/prod
PRAVO_CONFIG_DIR=people/ada pravo id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan > /dev/null
ADA=$(PRAVO_CONFIG_DIR=people/ada pravo id show | tail -n 1 | sed 's/^ *//')
DAN=$(PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//')
for e in dev staging; do
  printf '%s role:admin\n%s role:writer\n' "$ADA" "$DAN" > .pravo/access/$e/readers.txt
done
printf '%s role:admin\n%s role:reader\n' "$ADA" "$DAN" > .pravo/access/prod/readers.txt
pravo init > /dev/null
for e in dev staging prod; do pravo protect init --encrypt --allow-local --yes --env $e > /dev/null; done
PRAVO_CONFIG_DIR=people/dan pravo apply --auto-approve > /dev/null
```

Dan has applied dev. Staging has not taken the release yet, so prod refuses it.

```sh run id=refuse exit=5
pravo promote prod
```

```text out
Error: staging does not run this release: nothing has been applied there yet.

Run these, skipping a step that is already done:

    pravo apply --env dev
    pravo promote staging
    pravo apply --env staging
    pravo promote prod
```

```sh run id=table
pravo environments
```

```text out
dev → staging → prod

ENV      FOLLOWS  RUNS          GIT  SINCE   BY
dev      -        <release-id>       0s ago  dan  runs this directory
staging  dev      -                               nothing applied yet; ready to promote
prod     staging  -                               nothing applied yet

This directory: <release-id>
Next: pravo promote staging
```
