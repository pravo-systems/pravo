---
title: Self-service scenario
description: The Web service template of the how-to pages, the code that its form gives for a new instance, and the plan of that code, prepared with a reader key. The self-service band of the landing page shows this file and this output.
capture: true
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/eve
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
---

This page is not published. `tools/capture` runs it, and `site/demos/idea/mk.go self-service` renders the blocks into `site/landing/14-self-service.html`. The root is the one of [Make a new instance from a template](../../../docs/how-to/add-an-instance-from-a-template.md), and the picture of the form between the two tabs is its shot `templates-form`, from `tools/serveshots`.

```cue file=service.cue title="service.cue" id=template status=valid
package estate

import "pravo.dev/pravo"

#Service: pravo.#Template & {
	$template: {
		name:        "service"
		title:       "Web service"
		description: "A service with a generated secret and a rendered config file."
		inputs: {
			name: {}
			owner: {label: "Owning team", description: "The team that gets paged for it. It starts with team-."}
			env: {}
			port: {}
		}
	}
	// Lowercase letters, digits and dashes. It names the config file.
	name: string & =~"^[a-z][a-z0-9-]{2,30}$"
	// The team that answers for the service.
	owner: string & =~"^team-"
	// Where it runs.
	env: *"dev" | "staging" | "prod"
	// The port it listens on.
	port: *8080 | int & >1024 & <65536

	secret: $random.#Resource.random_password & {length: 24}
	config: $local.#Resource.local_file & {
		filename: "out/\(name).env"
		content:  "OWNER=\(owner)\nENV=\(env)\nPORT=\(port)\n"
	}
}
```

```sh hidden
mkdir -p .pravo/access
cat > main.cue <<'CUE'
package estate

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider & {$version: "~> 2.5"}
$random: rnd.#Provider & {$version: "~> 3.6"}

services: web: #Service & {name: "web", owner: "team-shop"}
services: api: #Service & {name: "api", owner: "team-shop", env: "prod"}
CUE
cat > policy.cue <<'CUE'
package estate

import "pravo.dev/pravo"

service_secrets: pravo.#Policy & {
	description: "The password of every Web service has special characters."
	rule: changes: [...{
		type: string
		template?: {name: string, instance: string}
		if template != _|_ {
			if template.name == "service" && type == "random_password" {
				after: special: true
			}
		}
	}]
}
CUE
PRAVO_CONFIG_DIR=people/ada pravo id new --name ada > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo id new --name dan > /dev/null
PRAVO_CONFIG_DIR=people/eve pravo id new --name eve > /dev/null
{
  echo "$(PRAVO_CONFIG_DIR=people/ada pravo id show | tail -n 1 | sed 's/^ *//') role:admin"
  echo "$(PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//') role:writer"
  echo "$(PRAVO_CONFIG_DIR=people/eve pravo id show | tail -n 1 | sed 's/^ *//') role:reader"
} > .pravo/access/readers.txt
export PRAVO_CONFIG_DIR=people/ada
pravo init > /dev/null
pravo protect init --encrypt --yes --allow-local > /dev/null
pravo protect roles --yes > /dev/null
pravo apply --auto-approve > /dev/null
```

The code that the form gives for the checkout service, pasted into a file of its own:

```cue file=checkout.cue title="checkout.cue" id=instance status=valid
package estate

services: checkout: #Service & {
	name:  "checkout"
	owner: "team-checkout"
	env:   "prod"
}
```

Eve, a reader, plans it. The page runs as Eve; the hidden setup runs as Ada.

```sh run id=plan
pravo plan
```

```text out
pravo will perform the following actions:

  # services.checkout.secret (random_password) will be created
  + services: checkout: secret: {
...
  +     special:     true
...
  # services.checkout.config (local_file) will be created
  + services: checkout: config: {
  +     content:              "OWNER=team-checkout\nENV=prod\nPORT=8080\n"
...
  +     filename:             "out/checkout.env"
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 · skipped 4 · not read 4
Policies: 1 satisfied.
```
