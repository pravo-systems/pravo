---
title: Hero scenario
description: One CUE file with a typed service definition, two instances and the plan that pravo prints. The hero of the landing page shows this file and this output.
capture: true
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
---

This page is not published. `tools/capture` runs it, and `site/demos/idea/mk.go hero` renders the blocks into `site/landing/00-hero.html`. The file and the output below are what the hero shows.

```cue file=main.cue title="main.cue" id=main status=valid
package estate

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

#Service: {
	name: string
	port: int & >=1024 & <=65535
	file: $local.#Resource.local_file & {
		filename: "\(name).env"
		content:  "PORT=\(port)\n"
	}
}

api: #Service & {name: "api", port: 8080, file: file_permission: "0600"}
web: #Service & {name: "web", port: 8081}
```

```sh hidden
pravo init
```

```sh run id=plan
pravo plan
```

```text out
pravo will perform the following actions:

  # api.file (local_file) will be created
  + api: file: {
...
  +     file_permission:      "0600"
  +     filename:             "api.env"
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
```
