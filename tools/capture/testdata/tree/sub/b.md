---
title: Second page
description: Starts from an empty workspace.
order: 2
---

The file written by the first page does not exist here.

```sh run
ls
echo "workspace: $(basename "$PWD")"
```

```text out
workspace: ws
```
