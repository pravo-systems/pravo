---
title: Remote state
description: Store the state graph in an object store and lock it for each run.
order: 1
minutes: 15
needs: B1
level: intermediate
---

State lives in a bucket. Each run takes a lock first.

```cue file=backend.cue title="backend.cue"
package main

backend: {
	type:   "s3"
	bucket: "state-example"
	key:    "prod/pravo.state"
}
```

```bash
export PRAVO_ACTOR=ci
pravo plan -t env=prod --json > plan.json
echo "exit: $?"   # 0, 1 or 5
```

```yaml
name: plan
on: [pull_request]
jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - run: pravo plan -t env=prod
```

```go
package main

func main() { println("pravo") }
```
