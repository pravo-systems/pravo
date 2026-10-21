# capture

`capture` runs the examples in the documentation and compares what they print with what the pages say. It builds `pravo`, then for each page creates an empty workspace, writes the `file=` blocks, runs the `run` blocks and compares their output with the `out` block that follows each of them. It fails with the page, the block, the line, the expected text and the actual text when anything differs. A page that needs a server, such as an S3 bucket or a container registry, lists it in its front matter and `capture` starts it for the page.

The Markdown grammar it reads is defined in `docs/AUTHORING.md`, sections 4 and 5. This file describes what `capture` does with it.

## Usage

```sh
go run ./tools/capture -check docs/learn/a03-writing-a-field-twice.md   # one page
go run ./tools/capture -check ./docs                                    # every page
go run ./tools/capture -update docs/learn/a03-writing-a-field-twice.md  # rewrite the out blocks
go run ./tools/capture -json hero.json docs/how-to/quick-start.md         # record the run blocks
```

`PATH` is a Markdown file or a directory. A directory is searched recursively for `.md` files, in path order, without entering directories whose names start with a dot. Without a path, `capture` reads the `docs` directory of the module that contains the current directory. Run it inside the pravo module, or pass `-bin`.

| Flag | Meaning |
|---|---|
| `-check` | Compare and report. This is the default. |
| `-update` | Rewrite the `out` blocks from a real run. Commands and files are never changed. |
| `-bin PATH` | The `pravo` binary to run. By default `capture` builds `./cmd/pravo` once into a temporary directory. |
| `-services DIR` | A directory with the executables of the services that pages list (`fakes3`, `fakeoci`). By default `capture` builds them from the module once. |
| `-j N` | Run N pages at a time. `0` means one per CPU. Default 1. |
| `-json OUT` | Write the run blocks of a page to the file `OUT`; for several pages, `OUT` is a directory. |
| `-timeout D` | The longest one `run` block may take. Default 5 minutes. |
| `-keep` | Keep the workspaces and print where they are. |
| `-v` | Print every block as it runs, and the pages that are skipped. |

Exit status: `0` when every page agrees, `1` when a page differs or has an authoring error, `2` when `capture` cannot do its work (a missing file, a failed build, a bad flag).

## How a page runs

1. A page with `capture: false` in its front matter is skipped. A page without a `run` block is skipped.
2. `capture` creates an empty workspace in a temporary directory. It is the working directory of every command. Nothing else of the page's surroundings is visible: each page starts empty.
3. It starts the services that the page lists (see [Services](#services)) and waits until each accepts connections.
4. It goes through the fenced blocks in document order. A `file=NAME` block writes `NAME` into the workspace (parent directories are created, a name written twice is replaced). A `run` block is executed. A `check` block is executed and must exit 0.
5. Each `run` block is one `sh -c` script, run in the workspace with the directory of `pravo` first on `PATH`. Standard output and standard error arrive in one stream, in the order they were written. Standard input is empty.
6. The exit status is compared with `exit=N` (default 0) and the output with the `out` block that follows the `run` block.
7. It stops the services and removes the temporary directory.

A visible `run` block runs as it would in the reader's shell: every line runs, and the status of the block is that of the last command. A `hidden` block and a `check` block stop at the first command that fails, as with `sh -e`.

The commands inherit the environment of the caller, so `HOME`, `PRAVO_HOME`, `PRAVO_RECORD`, `PRAVO_REPLAY`, proxies and credentials reach `pravo`. `PRAVO_LOG`, `PRAVO_ACTOR`, `PRAVO_REASON` and `NO_COLOR` are removed from it, and `NO_COLOR` is then set to `1`. A page that needs one of the others sets it in the command: `PRAVO_ACTOR=ci pravo plan`. Variables that every command of one page needs, such as `CUE_REGISTRY` or the address of a service, come from the `services` and `env` front matter keys.

## Services

A page that needs a server names it in its front matter. `capture` starts a new one for the page, before the first block, and stops it after the last, so every page starts with an empty bucket or registry.

```yaml
services:
  - fakes3:9000      # the in-memory S3 server on 127.0.0.1:9000
  - fakeoci          # the in-memory OCI registry on a port the system chooses
env:
  AWS_ACCESS_KEY_ID: test
  CUE_REGISTRY: example.com=localhost:{{FAKEOCI_PORT}}+insecure
```

| Service | Source | Serves |
|---|---|---|
| `fakes3` | `tools/fakes3` | An S3 bucket, path style. |
| `fakeoci` | `tools/fakeoci` | An OCI registry for modules and providers. |

An entry is a name, or a name and a port separated by a colon. A page that shows an address, such as `endpoint: "http://127.0.0.1:9000"`, names the port. A page that has no address to show leaves the port out and `capture` takes a free one. A service listens on `127.0.0.1` only.

### Variables

Every command of the page, and the values of `env`, see these variables for each service, with the name in capitals:

| Variable | Value |
|---|---|
| `FAKES3_ADDR` | `127.0.0.1:PORT` |
| `FAKES3_URL` | `http://127.0.0.1:PORT` |
| `FAKES3_PORT` | `PORT` |
| `FAKES3_PID` | The process id of the service. `kill "$FAKES3_PID"` stops it before the end of the page, to show what a command does while the server is down. |

`FAKEOCI_ADDR`, `FAKEOCI_URL`, `FAKEOCI_PORT` and `FAKEOCI_PID` are the same for the registry.

A `file=` block and an `env` value can hold the placeholders `{{FAKES3_ADDR}}`, `{{FAKES3_URL}}` and `{{FAKES3_PORT}}`, and the same three for `FAKEOCI`, where the page needs the address in the contents of a file. They are replaced when the file is written. Any other `{{...}}` text is left as it is, and so is a placeholder in a `run` block: commands use the variables, `$FAKES3_URL`. A placeholder of a known service that the page does not list is an authoring error.

A port that the system chose differs between runs. `capture` replaces it in the output with `<NAME-port>`, as in `127.0.0.1:<fakes3-port>`, so that the page, `-update` and `-json` do not hold a number that changes. A port that the page names is not replaced.

### The env key

`env` is a map from variable names to values. The variables are set for every command of the page, after the environment of the caller and the variables of the services, so a page can override either. A value is text; a number or a boolean is written as its text. `PATH` and `PWD` cannot be set. The `export` lines that a page shows to its reader are not run, because each `run` block is a shell of its own: the same assignments go in `env`.

A page with at least one service gets `CUE_CACHE_DIR` set to a directory of its own, removed at the end, so that a module that CUE cached from an earlier registry on the same address cannot be mistaken for the module that the page publishes. A page can set `CUE_CACHE_DIR` in `env` to use another.

### Lifetime

- Before the page: `capture` takes the fixed ports of the page, starts each service with `NAME -addr 127.0.0.1:PORT -exit-on-eof`, and waits for the line `listening on ADDR` on its standard output. It then connects to the address. A service that exits, prints no such line within 30 seconds or does not accept the connection fails the page, with the last lines it wrote, and no command of the page runs. A fixed port that another program holds is tried again for a few seconds.
- During the page: the services are shared by every block, including the copies that `::solution` parts, `::reveal` parts and deep dives run in. Their data is not copied.
- After the page, however it ended: `capture` closes the standard input of each service and kills its process group, then waits for it. The services also exit when their standard input ends, so a `capture` that is killed does not leave them behind. An interrupt (Ctrl-C) ends the commands that are running, stops the services and removes the workspaces before `capture` exits with status 2.

The executables are built once per run, from `tools/fakes3` and `tools/fakeoci` of the module, and only when a page that runs lists them. `-services DIR` names a directory that already holds them. A service is any executable that takes `-addr` and `-exit-on-eof` and prints `listening on ADDR` once it accepts connections; the tests use a stand-in.

### Pages that name the same port

Two servers cannot hold one port, so pages that name the same port run one after another, whatever `-j` says. Pages that name other ports, or none, run at the same time. A lock file per port in the temporary directory (`capture-port-N.lock`) does the same between two `capture` processes, so a second run waits for the first instead of failing. A page takes all its ports before it starts any service, lowest port first.

### Blocks and containers

The info string is the language followed by attributes: `sh run exit=1`, `cue file=main.cue title="main.cue"`. `run`, `out`, `check` and `hidden` are accepted anywhere in it, including in the language position. A `hidden` block without `file=` and without `out` is a command that runs and is not shown, so `sh hidden` is enough. Unknown attributes are ignored here.

| Where a block is | What `capture` does with it |
|---|---|
| Top level, `:::compare`, `:::tabs`, `:::figure`, `:::predict` before `::reveal`, `:::exercise` before `::hint` | Writes it or runs it in the page workspace. |
| `::reveal` | Runs it in a copy of the workspace made at that point, discarded at the end of the part, so the answer a predict shows is verified and cannot change the state the page depends on. |
| `::hint`, `::answer` | Ignores it. Nothing is written or run. A `run`, `check` or `out` block there draws a warning: its output is not verified. Use `title=` and not `file=` for a snippet that is only shown. |
| `::solution` | Runs it in a copy of the workspace made at that point. The copy is discarded at the end of the part, so the workspace of the page is the same with and without the solution. |
| `:::deep-dive` | Runs it in a copy of the workspace, discarded at the end of the container. A deep dive is never on the path. |
| Inside a callout (`> [!NOTE]`) | Not looked into. A block there that carries `run`, `out`, `check` or `file=` is an error. |

An `out` block belongs to the `run` block directly before it. Only blank lines may lie between them; prose, another block or a `::` marker in between makes the `out` block an error. A `run` block without an `out` block must print nothing. A `hidden` or `check` block without an `out` block may print anything, which is not compared.

Fences follow CommonMark: three or more backticks or tildes, indented by at most three spaces, closed by a fence of the same character that is at least as long. A `run` block shown inside a longer fence is text and does not run.

`capture` reports these as authoring errors, with the line, before it runs anything on the page: an unclosed fence or container, nested containers, a `::` part outside a container or unknown in it, an `out` block with no `run` block, a `file=` name that is absolute or leaves the workspace, `exit=` on a block that is not a `run` block, a `check` block with a non-zero `exit=`, a command that starts with a `$ ` prompt, and an empty `run` block. A `cd` in a command is a warning: the workspace is the working directory.

## What is compared

Output is normalised before it is compared, written by `-update` and recorded by `-json`, in this order:

1. `\r\n` becomes `\n`; terminal escape sequences are removed.
2. Absolute paths are replaced: the workspace by `.`, the temporary directory by `<tmp>`, the home directory by `~`, and the directory that the caller names in `PRAVO_HOME`, if it sets one, by `~/.pravo`. A path is replaced only when it is a whole path, and both the spelling that was passed and its symbolic-link-free form are recognised (`/var` and `/private/var` on macOS). The workspace of a `::solution` copy is also `.`.
3. The `normalize` rules of the page, in order. Each is a Go regular expression applied to the whole output, so `(?m)` and `\n` work, and `replace` may use `$1`:

   ```yaml
   normalize:
     - pattern: "id=[a-z-]+"
       replace: "id=<id>"
     - pattern: '(?m)^Downloading .*\n'   # a provider download: only the first run prints it
       replace: ""
   ```

4. Trailing spaces and tabs are removed from every line. Blank lines at the start and the end of the output are removed.

The `out` block is trimmed in the same way before the comparison, so a `.` in the page and a trailing space in the file do not matter.

A line that is exactly `...` in an `out` block stands for any number of lines, including none. Text before the first `...` must start the output, text after the last must end it, and the text between two of them must appear in the output in that order.

```text
Plan: 3 to add, 0 to change, 0 to destroy.
...
```

### Failures

A difference prints the page and the line of the block, the block number (the position of the fenced block in the page, counted from 1), the command, the first output line that differs with the line of the page it is compared with, and a diff:

```text
FAIL    docs/learn/a03.md
  docs/learn/a03.md:57: block 6: output differs from the out block at line 62
      $ pravo eval
      output line 2 differs
        expected (page line 64): size: conflicting values 11 and 3:
        actual   (output line 2): size: conflicting values 11 and 5:
      diff (- expected, + actual):
          Error: evaluating .:
        - size: conflicting values 11 and 3:
        + size: conflicting values 11 and 5:
```

A wrong exit status, a missing `out` block, a command that runs longer than `-timeout` and a failing `check` block are reported the same way. A failing block does not stop the page: later blocks still run, and one run shows every difference.

## Updating

`-update` runs the page and rewrites only `out` blocks:

- an `out` block that differs is replaced; its opening fence line is kept, so the info string stays as written;
- a visible `run` block without an `out` block gets one, after a blank line, tagged `text out`;
- an `out` block of a command that now prints nothing is removed;
- an `out` block that already matches is not touched, whatever its `...` lines hide;
- when an `out` block with one `...` stops matching and the new output is longer than the lines kept around it, the new block keeps the same number of lines before and after the `...`;
- a fence is made longer when the new text contains a line that would close it.

Commands, `file=` blocks, prose and attributes such as `exit=` are never changed. A wrong exit status is still reported and makes `-update` exit with status 1; add `exit=N` to the block yourself. Read the diff before keeping the result. `-update` does not rewrite a file with CRLF line endings.

## Recording runs

`-json OUT` writes the `run` and `check` blocks that ran, in order, with their normalised output. With one page, `OUT` is a file. With several pages, `OUT` is a directory and each page gets `<path below the walked directory>.json`.

```json
{
  "page": "docs/how-to/quick-start.md",
  "title": "A first plan",
  "version": ["pravo 0.1.0-dev", "cue v0.17.1"],
  "blocks": [
    {
      "block": 6,
      "line": 31,
      "stage": "main",
      "command": "pravo eval",
      "expect_exit": 1,
      "exit": 1,
      "output": "Error: evaluating .:\nsize: conflicting values 11 and 3:\n    ./main.cue:4:7\n    ./main.cue:5:7",
      "ok": true
    }
  ]
}
```

`stage` is `main`, `solution`, `reveal` or `deep-dive`. `hidden` and `check` are present when true. `ok` is false for a block that failed. `version` is the output of `pravo version`. The file is written whether or not the page agrees.

## Parallel runs and the shared home

Every page runs in its own workspace, but all pages share `PRAVO_HOME`, so a provider is downloaded once. With `-j`, blocks that run `pravo init` take turns, and so do two `capture` processes that use the same `PRAVO_HOME`, through a lock file in the temporary directory. Installing a provider writes into the shared home. Other commands run at the same time. Pages that list services keep to the rules in [Services](#services).

The first run with a cold cache prints download lines that later runs do not. A page whose output includes them removes them with a `normalize` rule, as in the example above. Warm the cache with one sequential run before a parallel one.

## Tests

```sh
go test ./tools/capture             # unit tests, end-to-end tests with a stand-in pravo, one run of the real binary
go test -short ./tools/capture      # without the real binary
go test ./tools/capture -golden     # rewrite testdata/golden after a deliberate change to a report
```

`services_test.go` uses the test binary as the stand-in for the services: `TestMain` looks at the name the binary was started under, and the tests link it as `fakes3`, `fakeoci`, `httpget` and `capture` in a directory. The stand-in serves a line of text, can be told to fail, to stay silent or to announce another port, and writes its process id to a file, so that the tests can check that no service outlives its page, a failing page, an interrupt (the test sends `SIGINT` to a `capture` process) or a `SIGKILL`. `TestRealFakes3` builds `tools/fakes3` and uses the bucket with `curl`; `go test -short` skips it.

`testdata/fakepravo` is a shell script that stands in for `pravo`. `testdata/pages` holds a page that passes (`ok.md`), one that fails in every way (`fail.md`), one with authoring errors (`bad.md`), one with warnings, one that `-update` rewrites (`stale.md`) and pages that are skipped. `testdata/tree` is a directory with a hidden directory and a page with `capture: false`.

`capture` uses the standard library and `goldmark-meta` for the front matter, the same YAML reader as `tools/sitegen`.
