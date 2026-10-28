# Authoring the pravo documentation

This file is for people who write documentation. It is not published. It fixes the structure of the site, the Markdown extensions that `tools/sitegen` understands, the way `tools/capture` runs the examples, and the checks a page passes. The pages under `docs/` describe the current behaviour of pravo and nothing else.

## 1. Structure

| Section | Directory | Purpose |
|---|---|---|
| Learn | `docs/learn/` | Track A, CUE from zero (A1 to A14), with four companion pages and a cheat sheet. Track B, pravo (B1 to B11), three labs and the project B12. Track C, a team on one state (C1 to C7). |
| Migrate | `docs/migrate/` | From OpenTofu or Terraform: an index with the gate demo, a runbook (M1 to M11) and three annexes. |
| How-to | `docs/how-to/` | Task-shaped pages for a reader who knows the basics: how to do one thing. The group Get started holds the quick start: install, a first plan, a change, a conflict and a destroy, with no cloud account. |
| Concepts | `docs/concepts/` | Explanation of how pravo behaves, with diagrams. Behaviour only. |
| Reference | `docs/reference/` | Lookup: one page per command, one per specification topic, tables first. |

The sections, their order and their blurbs are listed in `site/site.json`. Each section has an `index.md` that routes the reader.

The sidebar groups of a section come from the `group` front matter field. The groups in use are `Track A · CUE from zero`, `Track A · Companions`, `Track B · pravo`, `Track B · Labs and project` and `Track C · A team on one state` in Learn; `Runbook` and `Annexes` in Migrate; `Commands`, `Language`, `Policies`, `Data and formats` and `Lookup` in Reference.

A file named `a03-name.md` is lesson A3: a letter, one to three digits and a hyphen make the lesson id, which the sidebar, the pager and links from `needs` show.

The landing page is not Markdown. It is assembled from `site/landing/NN-name.html`. Numbers and recorded output on it come from `site/data/*.json` through `{{data file.path}}` and `{{json file.path}}` tokens, never from typing. `site/README.md` describes both.

URLs are stable. A page that moves keeps a stub (`redirect: new-page.md` in its front matter) that points to the new page.

## 2. Voice

- Present tense. Current behaviour only. Second person. Imperative steps. Sentences of 20 words or fewer in lessons.
- Dry and institutional. No exclamation marks, no hype, no "we", no jokes, no metaphors of war or politics.
- The lowercase name `pravo` everywhere. "OpenTofu" first; "Terraform" or "legacy Terraform" only as the name of the audience's tool.
- The recorded resources are the *state*. A rule that judges a plan is a *policy*. The setting that decides when a record is read from its provider again is the *refresh age*. `docs/reference/glossary.md` defines the rest.
- *Module* means a CUE module, the tree of packages under `cue.mod/module.cue` that `pravo mod` publishes and fetches. Write "CUE module" where a page first says it. What a `module` block does in OpenTofu is done by a *definition* that contains resources, used through an *instance* and shared through a *package*. Never call a definition, a package or an instance a module, and say once per page that teaches reuse that pravo has no module block.
- A feature that pravo does not have is stated as a fact with its replacement: "pravo has no workspaces. Select the environment with `-t env=prod`." No reason, no apology, no defence.
- No history, no former names, no "no longer", "previously", "formerly", "instead of the old", no migrations from earlier versions of pravo, no stories about why a decision was taken. The build fails when a line matches `tools/sitegen/banned.txt`. The list is a net: a sentence that tells the history of a decision is wrong whether or not a rule catches it.
- No claim without a run. Every output is captured from the real binary. No command, flag or behaviour that does not exist.
- Inline code is for a name, a flag, a path or a short value. An expression or a line of a file stands in a fence of its own, where it can be read and copied, also in a question.
- At most two new terms per lesson in Track A, defined once where they first appear. A glossary link (`../reference/glossary.md#unification`) renders as a hover definition.

## 3. Front matter

```yaml
---
title: Writing a field twice          # required
description: One sentence for search and previews.   # required
order: 3                              # required except on a section's index.md
group: Track A · CUE from zero        # optional; sidebar group heading
---
```

`order` ascends within a group. Two pages of one group with the same `order` are an error. Pages without a group come first, without a heading, after the section index.

Lesson and guide pages add:

```yaml
minutes: 10                           # reading and doing time, a positive integer
needs: A2                             # what to have done first; a lesson id becomes a link
learn: What happens when two values disagree?   # "you will learn", one or two questions
done_when: pravo eval prints one value for size and an error for the conflict.
level: beginner                       # beginner | intermediate | advanced
```

`title`, `description`, `needs`, `learn` and `done_when` are inline Markdown: code marks and links render.

Other optional fields:

| Field | Meaning |
|---|---|
| `redirect: new-page.md` | The page becomes a stub that points to `new-page.md`. It needs no other field. |
| `applies_to` | Replaces the footer line `pravo X.Y · CUE X.Y` on this page. |
| `keywords` | Words a reader types that the page does not say, such as the OpenTofu name of a construct: `keywords: "workspace tfvars -var-file"`. Search finds every word of a page, code and tables included, so list only a word that the page answers and does not contain. Keep `description` at 160 characters or fewer. |
| `gate` | The check that ends a step of a runbook, in a few words. A page that has one is a step: the path of its group is shown under its header, with this text under the page's node. |
| `toc: false` | The page has no table of contents and its column is wider. Use it for a page that embeds a demo. |
| `capture: false` | `tools/capture` skips the page. Use it for a page that needs credentials or a server that `services` does not offer, and say so in its text. |
| `normalize` | Rules that rewrite output before `tools/capture` compares it. See section 5. |
| `services` | Servers that `tools/capture` starts for the page: `fakes3`, `fakehttp`, `fakeoci`, each optionally with a port, `fakes3:9000`; and `fakeprovider`, the provider binary that it builds for the page and pravo starts. See section 5. |
| `env` | Variables that `tools/capture` sets for every command of the page. See section 5. |

A key that is not listed is an error. A page body does not start a level-1 heading: the title comes from the front matter.

## 4. Markdown extensions

Everything below is plain Markdown that renders readably without the site.

### 4.1 Code fences

The info string is the language followed by attributes. A `run` block with no language is `bash`; an `out` block with none is `text`.

````text
```cue file=estate.cue title="estate.cue" hl="2-3"
```
````

| Attribute | Meaning |
|---|---|
| `file=NAME` | Writes the block to `NAME` in the page workspace when the page is run. Shown with that file name. Writing the same name again replaces the file: each stage of a lesson is a complete snapshot. A `run` block cannot also have `file=`. |
| `title="TEXT"` | Label shown on the window. Defaults to `file`. |
| `hl="2-3,7"` | Highlights lines. Each line must exist in the block. |
| `ln` | Shows line numbers. |
| `status=conflict` | Badge: `valid`, `conflict`, `rejected` (rejected before any provider starts), `unintended` (valid but not what was meant). Declares in advance what the block does. |
| `run` | A shell block: one `sh -c` script, run in the page workspace with `pravo` on the path. The next block, tagged `out`, is its expected output. |
| `out` | Expected output of the preceding `run` block. It must follow that block directly. |
| `exit=N` | Expected exit code of the `run` block, 0 to 255. Default 0. |
| `hidden` | Run but not shown. Use for setup only; a lesson never hides a step the reader needs. A hidden `run` block takes its `out` block with it. |
| `check` | A `run` block that must exit 0 and is not shown. Use it in `::solution` to verify the answer. |

Any other attribute is an error.

A `run` block followed by its `out` block renders as one terminal window. The copy button copies the commands without a prompt. Commands never use `cd`; the workspace is the working directory. Lessons say once: "work in an empty directory". A `run` block without an `out` block must print nothing.

Long output: keep the first and last lines and write a line that is exactly `...` for the elided middle. `tools/capture` compares with the same elision.

### 4.2 Containers

A container starts with `:::name arguments` on its own line and ends with `:::`. The argument is inline text: a title that starts like a list item shows as written. Containers do not nest. A callout may sit inside a container.

| Container | Use |
|---|---|
| `:::compare` | Two or more code blocks for the same intent, each with a `title` or a `file`. Typically HCL and CUE. They stand side by side when a column holds their longest line, and stack otherwise, so that no line is cut; the third of three takes the full width. |
| `:::tabs NAME` | Two or more code blocks as tabs, one per `title`. A `run` block with its `out` block is one tab, titled `Terminal` unless it has a title. The optional name labels the tab list for screen readers. |
| `:::predict Question text` | A question the reader answers before looking. The lines before `::reveal` are the premise, framed with the question: a listing that the question is about goes there as a fence, never inside the question. |
| `:::exercise Title` | A task. The state of the workspace at that point is the start. On a page with several exercises the label counts them: Exercise 1, Exercise 2. |
| `:::check TITLE` | Questions at the end of a lesson. Each question is a paragraph ending in `?`; its answer follows after `::answer`. Collapsed. The title is optional and defaults to "Check yourself"; a page with two checks gives the first a title. |
| `:::deep-dive Title` | Optional theory, collapsed, never on the path. |
| `:::figure Caption` | A figure, usually inline SVG, with an auto-numbered caption (`fig. 3`). A screenshot is a link with an `img` inside: `<a class="shot" href="../img/serve/NAME.png"><img src="../img/serve/NAME.png" width="1440" height="900" alt="..."></a>`. A view that exists in a dark and a light picture is two such links in one figure, with the classes `shot shot-dark` and `shot shot-light`: the style sheet shows the one that matches the theme of the page. `tools/serveshots` makes the pictures of `pravo serve`, and its tests check the `width`, the `height` and the `alt`. |
| `:::widget NAME` | An animated widget from `site/assets/widgets.js` (`unify` and `plan`). The body is the JSON that drives it, plain or in a `json` fence. |
| `:::demo NAME` | An interactive demo from `site/demos/NAME/embed.html`, with the data tokens of the landing page. The container has no body: the next line is `:::`. The demos keep their own scripts in `site/assets/demos/`. Give the page `toc: false` when the demo needs the width. |
| `:::mapping` | A table set as a map from its first column, what the reader has, to its second, what replaces it. A second cell that says No equivalent, None or Absent hatches the row. A table whose second column is headed Status is a matrix: the cells Supported, Partial and Not supported become marks. The body is the table, and it reads the same without the site. |
| `:::cards` | Cards in a grid. Each `###` heading and the text under it make one card, and the last link of a card covers the whole card. |
| `:::runbook GROUP` | The path through the pages of a group that state a `gate`, as nodes on a line with the gate under each step. The group defaults to Runbook. The container has no body: the next line is `:::`. A page that has a `gate` shows the path under its header without the container. |

A line that holds only `::name` starts a part of the container that holds it:

| Part | In | Meaning |
|---|---|---|
| `::reveal` | `predict` | Starts the answer, which stays hidden until the reader opens it. Text before `::reveal` stays visible. Without `::reveal`, the whole body is the answer. |
| `::hint` | `exercise` | A hint. At most two: a nudge, then a reference link. |
| `::solution` | `exercise` | The solution. At most one. |
| `::answer` | `check` | The answer to the question before it. |
| `::question` | `check` | Starts a question with a premise: the paragraphs and listings before the question, which is the last paragraph of the part. Without it, a question is the last paragraph before `::answer`. |
| `::fallback` | `widget` | The text shown without JavaScript. Without it, the JSON needs a `fallback` or `alt` string, and the build warns when it has neither. |

What `tools/capture` does with the blocks in each place is in section 5.

### 4.3 Callouts

```markdown
> [!NOTE]
> Text.
```

Types: `NOTE`, `TIP`, `WARNING`, `PITFALL`, `CHECK`. Use sparingly: at most two per page. A callout holds text, not runnable blocks: `tools/capture` does not look inside it, and a `run`, `out`, `check` or `file=` block there is an error.

A paragraph that starts with `Cause:` is set as two labelled lines, and the text after `Fix:` in the same paragraph is the second line. The pitfall pages write each entry this way: the failing command, then `Cause: ... Fix: ...`.

### 4.4 Links

A link to another page is written as a relative path to the Markdown file, `[A3](a03-writing-a-field-twice.md)` or `[Plans](../concepts/plans.md)`. `tools/sitegen` rewrites it to the HTML page and fails when the page or the fragment does not exist. A link to a page that is a redirect stub goes to the final page.

### 4.5 Lesson structure

1. Header contract (rendered from front matter): minutes, needs, "you will learn", "done when".
2. The pain, in HCL, in three to eight lines.
3. The idea in one or two sentences.
4. A runnable example and its output.
5. Cause the failure. The failing block carries `status=conflict` (or similar) and the error is decoded: path, value, rule, location.
6. `:::predict` where the lesson lists one.
7. `:::exercise`.
8. `## Recap` with three bullets.
9. `:::check` with two questions.
10. The pager links the previous and the next page.

## 5. Running the examples

`tools/capture` builds pravo once (or uses the binary named by `-bin`), then for each page in order:

1. skips a page with `capture: false` and a page without a `run` block;
2. creates an empty workspace in a temporary directory and starts the `services` of the page;
3. goes through the fenced blocks in document order. A `file=` block writes its file. A `run` block runs as one `sh -c` script, standard output and standard error merged, standard input empty. A `hidden` block and a `check` block stop at the first command that fails;
4. compares the exit code with `exit=` and the output with the `out` block that follows;
5. runs the blocks of a `::reveal` part, a `::solution` part and a `:::deep-dive` in a copy of the workspace that is discarded afterwards, so that a printed answer is verified and cannot change the state the page depends on. It never writes or runs the blocks of a `::hint` or `::answer` part. A file that a later block needs sits at the top level, in a `:::tabs` container or in the body of an exercise, because a `file=` block in a `::solution`, `::reveal` or `:::deep-dive` part does not exist for the blocks that follow.

The commands inherit the environment of the caller. `PRAVO_LOG`, `PRAVO_ACTOR` and `PRAVO_REASON` are removed from it and `NO_COLOR=1` is set. A page that needs one of the others sets it in the command: `PRAVO_ACTOR=ci pravo plan`. A page that needs a variable in every command lists it under `env`, because each `run` block is a shell of its own and the `export` lines that a page shows are not run.

Output is normalised before it is compared: terminal escapes are removed; absolute paths become `.` (the workspace), `<tmp>` (the temporary directory) or `~` (the home directory, and `~/.pravo` for the directory in `PRAVO_HOME`); the page's `normalize` rules apply; trailing spaces are removed. A line that is exactly `...` in an `out` block stands for any number of lines.

```sh
go run ./tools/capture -check docs/learn/a03-writing-a-field-twice.md   # one page
go run ./tools/capture -check ./docs                                    # everything
go run ./tools/capture -update docs/learn/a03-writing-a-field-twice.md  # rewrite the out blocks from a real run
```

`-update` only rewrites `out` blocks; it never changes commands or files. Run it, read the result, and keep what is true. A page passes when `-check` passes. `-bin PATH` runs a binary you built, `-j N` runs N pages at a time, and `-json OUT` records the blocks that ran, which `site/demos` uses for the landing page. `tools/capture/README.md` has every flag, the authoring errors it reports and the exit statuses.

A page declares normalisation rules for values that differ between runs:

```yaml
normalize:
  - pattern: "id=[a-z-]+"
    replace: "id=<id>"
```

Each `pattern` is a Go regular expression applied to the whole output, so `(?m)` and `\n` work, and `replace` may use `$1`.

A page that needs a server lists it under `services`. `tools/capture` starts a new one for the page, so that every page begins with an empty bucket or registry, and stops it after the last block. `fakes3` is the in-memory S3 server of `tools/fakes3`, `fakehttp` the in-memory state server of `tools/fakehttp` and `fakeoci` the in-memory registry of `tools/fakeoci`. A page that shows an address names the port, as the reader types it; a page that does not show one leaves the port out and gets a free port.

A page that needs a provider which can list objects, for a discovery, lists `fakeprovider`. It is not a server: `tools/capture` builds `tools/fakeprovider` as `terraform-provider-demo` and pravo starts it. The page sees `FAKEPROVIDER_BIN`, the path of the binary, which a specification names as `$path: "{{FAKEPROVIDER_BIN}}"`, and `FAKEPROVIDER_DIR`, the directory `objects` of the workspace, where a `file=objects/catalog.json` block lists the objects that exist, as `{"type": "demo_bucket", "id": "...", "name": "...", "region": "..."}` (the type `demo_server` when `type` is left out). The provider adds the objects it creates to the file. The page says that the provider stands in for a cloud.

```yaml
services:
  - fakes3:9000       # http://127.0.0.1:9000
  - fakeoci:5078      # 127.0.0.1:5078
env:
  AWS_ACCESS_KEY_ID: test
  CUE_REGISTRY: example.com=localhost:5078+insecure
```

Commands see `FAKES3_ADDR` (`127.0.0.1:9000`), `FAKES3_URL`, `FAKES3_PORT` and `FAKES3_PID` for each service, and `FAKEOCI_ADDR`, `FAKEOCI_URL`, `FAKEOCI_PORT` and `FAKEOCI_PID`. `kill "$FAKEOCI_PID"` in a block stops the service before the end of the page, to show a command that runs while the server is down.

A `file=` block or an `env` value writes `{{FAKES3_PORT}}` where it needs a number that capture chooses, and `{{FAKEPROVIDER_BIN}}` where it needs the path of the provider; the placeholder is replaced when the file is written. A port that capture chooses is shown as `<fakes3-port>` in output. A port that the page names is shown as written.

Pages that name the same port run one after another. A page with a service has a `CUE_CACHE_DIR` of its own. `tools/capture/README.md` describes the lifetime of the services.

## 6. Checks

```sh
go run ./tools/capture -check ./docs
go run ./tools/hclcheck -j 4
go run ./tools/sitegen -pravo-bin bin/pravo
go run ./tools/sitegen/contrast
```

`tools/sitegen` collects every problem, prints it as `file:line: message` and writes nothing when there is one. It reports a missing or unknown front matter key, two pages with the same `order` in a group, a level-1 heading in a body, an unknown or unclosed container, a part that its container does not take, an unknown code attribute, an `hl` line that does not exist, widget JSON that does not parse, a link to a page that does not exist and an unresolved data token in a landing partial. After it writes the site it checks every link, fragment and id in the output.

The phrase lint reads every `.md`, `.html`, `.json` and `.txt` file under `docs/` and `site/`, and `README.md`, against the regular expressions in `tools/sitegen/banned.txt`. It skips fenced code in Markdown, `<pre>`, `<script>` and `<style>` blocks in HTML, the `assets` directories and this file. `ARCHITECTURE.md` is outside its reach unless you name it: `go run ./tools/sitegen -readme ARCHITECTURE.md -out <scratch directory>`.

`tools/sitegen/contrast` checks every colour pair of the style sheet in both themes.

### 6.1 OpenTofu listings

An ```` ```hcl ```` fence is a listing that OpenTofu has to accept. `tools/hclcheck` writes the `hcl` blocks of a page into a scratch root in page order, runs `tofu fmt -check`, `tofu init` and `tofu validate` on each, and prints one row per block. It needs `tofu` on the path and the network for the first `tofu init`. The file of a block is its `file=` attribute or the first file name in its `title`; a title with `lines 8-41` appends the block to that file. Where a listing shows part of a root, the tool adds the variables, resources and providers that the rest of the root would hold, and the table names them. A block that has to fail carries `<!-- hclcheck: expect=error -->` on the line above the fence; a block that cannot be checked carries `<!-- hclcheck: skip=REASON -->`. `tools/hclcheck/README.md` has the flags and the statuses. `sh tools/hclcheck/claims.sh` runs `tofu` again on the statements about OpenTofu behaviour that the lessons and the Migrate section make.

## 7. Review checklist

- Every claim is something the binary does. Every command exists.
- Every output block comes from `tools/capture -update`, then read.
- No history, no justification of a decision, no apology, no comparison that disparages another tool.
- The words are *state*, *policy* and *refresh age*.
- Failures are shown, labelled in advance, and decoded.
- The page ends with a recap and a way forward.
- Every `hcl` block passes `go run ./tools/hclcheck`. Every statement about how OpenTofu behaves comes from a run of `tofu`.
- `go run ./tools/capture -check` and `go run ./tools/sitegen` pass.
