# scale

`scale` measures plans and applies of pravo and OpenTofu on estates of `local_file` resources, counts the records each run reads from its provider, and writes the medians of a run as JSON. `bench.sh` is the harness. `main.go` is its helper: it times a command, records the machine and the tools, and assembles the file.

## Usage

```sh
go build -o bin/pravo ./cmd/pravo
sh tools/scale/bench.sh 2000 10000      # sizes; the default is 500 2000 5000 10000
sh tools/scale/bench.sh -s results.tsv  # print the table of a finished run
```

The harness needs `pravo` and `tofu`, the provider `hashicorp/local` for both, and network access for `tofu init` unless `PLUGIN_CACHE` holds the plugin. It writes nothing to the module. Everything goes to a scratch directory, `WORK`, which it creates under `/tmp`.

| Variable | Meaning | Default |
|---|---|---|
| `PRAVO_BIN` | The pravo binary. | `bin/pravo` of the module. |
| `TOFU_BIN` | The tofu binary. | `tofu` on the path. |
| `TOOLS` | Tools to run, `pravo`, `tofu` or both. | `pravo tofu` |
| `SHAPES` | Estates to run: `flat`, `hub`, `layered`. | all three |
| `REPS` | Repetitions of each scenario. The file holds the medians. | `3` |
| `WORK` | The scratch directory. | a new `/tmp/pravo-bench-work.*` |
| `OUT` | The JSON file. | `$WORK/scale-timings.json` |
| `PLUGIN_CACHE` | A directory that holds the `hashicorp/local` plugin for tofu. | `$WORK/plugin-cache` |
| `TOFU_LAYERED_MAX` | The largest size at which tofu runs the layered estate. | `2000` |
| `PRAVO_LAYERED_MAX` | The largest size at which pravo runs the layered estate. | `5000` |
| `MAX_LOAD` | The load average above which the run warns that the machine is busy. | `2` |
| `WAIT_IDLE` | Seconds to wait before the run for the load average over one minute to stay at `MAX_LOAD` or below on three readings in a row. The run starts when the time is up, busy or not. | `0` |
| `RESULTS_FROM` | The `results.tsv` of an earlier run whose rows join this run's. | none |

Run it on a machine that does nothing else. The file records the load average at the start and at the end. A run that began above `MAX_LOAD` says so in `notes`. The load at the end includes the load of the run itself, so it is recorded and not judged.

## Estates

`N` is the number of resources.

| Estate | Resources | A change |
|---|---|---|
| `flat` | `N` files and one more, `release`, that carries a version. | A new version: one resource is replaced. |
| `hub` | `N` files that each read the content of one more, `hub`. | A new content of the hub: every resource is replaced. |
| `layered` | Three layers of `N/3` files. Each reads the id of the file with the same index in the layer below. | A new version in the first file of the first layer: one file of each layer is replaced. |

## Scenarios

| Scenario | Runs |
|---|---|
| `init` | `pravo init`, `tofu init`. |
| `apply_create`, `destroy` | The create and the destroy of the estate, `REPS` times in turn (flat), or once (hub, layered). |
| `plan_nochange_MODE`, `plan_change_MODE`, `apply_change_MODE` | A plan of the unchanged estate, a plan of a change, an apply of a change (flat). |
| `plan_out_change_default` | `plan --out FILE` of a change. The row has the size of the file. |
| `hub_*`, `layered_*` | The same names with the prefix, for the hub and the layered estate. `plan_nochange` runs for the layered estate only. |

`MODE` is `default`, `6h` or `norefresh`. `default` is pravo with the default refresh age, `always`, and tofu with its default. `6h` is pravo with `$settings` set to `refresh: "6h"`. `norefresh` is `-refresh=false` for both.

An extra run of `apply_create` and of each `apply_change_MODE` of pravo, with `-v`, counts the locks. Its rows have the repetition number 0, and they are not in the medians.

## The file

```text
date, machine, build, tools, providers, reps, sizes, notes
rows[]   one object for each tool, size and scenario: wall_s, user_s, sys_s, rss_mb,
         reads, locks, plan_file_bytes, exit, wall_s_all
by       the same rows by size and scenario: by.n2000.plan_change_default.pravo.wall_s,
         with speedup (the wall time of tofu divided by the wall time of pravo) where both ran
```

`build` names what ran: `pravo_sha256` is the digest of the pravo binary and `revision` the commit of the module, with `-dirty` when the tree held changes that the commit lacks. `machine` holds the load average at the start and at the end, and a run that began above `MAX_LOAD` says so in `notes`.

`reads` is the number of lines `Refreshing state` of the run: the records the tool read from its provider. `locks` is the number of locks an apply took, and `plan_file_bytes` the size of the saved plan. Both are null where they do not apply. `rss_mb` is the peak resident set of the command and its children. The medians are of `REPS` runs; `wall_s_all` lists them.

`scale-timings.json` in this directory is the file of the last full run. No page of the site shows it. The plans band of the landing page shows counts of reads from `site/demos/scale/page.md`, which `tools/capture` records, and no timings.

## What it does not measure

It measures a local state directory, the provider `hashicorp/local` and the machine that runs it. It measures no remote backend, no other provider and no other system. The evaluation of the specification follows the size of the estate and is part of every pravo number.
