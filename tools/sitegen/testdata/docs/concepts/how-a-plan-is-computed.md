---
title: How a plan is computed
description: Evaluation, state comparison and the walk over the state graph.
order: 1
minutes: 7
---

A plan has three steps. pravo evaluates the CUE, compares the result with the state graph, and walks the changed part.

## Evaluation

CUE evaluation unifies every file of the package. A conflict stops the run here, before any provider starts.

## The walk

```text
evaluate  ->  compare  ->  walk
```

:::figure Evaluation precedes every provider call.
<svg viewBox="0 0 300 60" role="img" aria-label="Three boxes in a row: evaluate, compare, walk">
  <g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="5" y="15" width="80" height="30"/><rect x="110" y="15" width="80" height="30"/><rect x="215" y="15" width="80" height="30"/></g>
  <g fill="currentColor" font-size="11" text-anchor="middle"><text x="45" y="35">evaluate</text><text x="150" y="35">compare</text><text x="255" y="35">walk</text></g>
</svg>
:::

### Scoped plans

A scoped plan walks only the resources whose inputs changed. The `Graph:` line reports the count.
