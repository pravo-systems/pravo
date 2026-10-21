# sitegen

Static site generator for the pravo documentation. Go, goldmark and chroma. No JavaScript toolchain.

```sh
go run ./tools/sitegen                                   # docs/ and site/ to site/public/
go run ./tools/sitegen -pravo-bin ./pravo                # footer version from the binary
go run ./tools/sitegen -docs tools/sitegen/testdata/docs -lint=false   # design work on the kitchen-sink pages
go test ./tools/sitegen
cd site/public && python3 -m http.server 8080            # or open site/public/index.html from the file system
```

| Flag | Default | Meaning |
|---|---|---|
| `-docs` | `docs` | Directory holding one directory per section. |
| `-site` | `site` | Directory with `site.json`, `assets/` and `landing/`. |
| `-out` | `site/public` | Output directory. Emptied first. Refused when it contains the sources. |
| `-config` | `<site>/site.json` | Site configuration. |
| `-pravo-bin` | none | pravo binary. `pravo version` gives X.Y; `go version -m` gives the CUE X.Y. Both replace the values in `site.json`. |
| `-base-url` | none | Absolute site URL for `sitemap.xml`, canonical links and `llms.txt`. Overrides `base_url`. |
| `-lint` | true | Fail on banned phrases. |
| `-banned` | built in | File of banned phrases instead of `tools/sitegen/banned.txt`. |
| `-readme` | `README.md` | File linted along with `docs/` and `site/`. Skipped when absent. |
| `-check` | true | Check every link and anchor of the output. |
| `-minify` | true | Minify the JavaScript and CSS copied from `<site>/assets`. The sources are not changed. `-minify=false` copies them as written. |

The build collects every problem it finds in the sources, prints them as `file:line: message`, and writes nothing when there is one. Warnings (a section directory that is missing, a widget without fallback text, a landing partial that breaks the naming convention) do not fail the build.

## site/site.json

```json
{
  "name": "pravo",
  "title": "<title> of the landing page",
  "description": "Meta description, llms.txt summary, footer text.",
  "pravo_version": "0.1",
  "cue_version": "0.17",
  "repo": "",
  "base_url": "",
  "start_label": "Quick start",
  "start_href": "how-to/quick-start.html",
  "glossary": "reference/glossary.md",
  "footer_note": "Optional. Without it the footer has no legal line.",
  "preload": ["assets/fonts/newsreader-500.woff2"],
  "search_suggest": ["how-to/quick-start.md", "reference/errors.md"],
  "lint_skip": ["design"],
  "sections": [
    {
      "id": "learn", "title": "Learn", "blurb": "One sentence.",
      "nav": true, "progress": true,
      "groups": ["Track A · CUE from zero", "Track B · pravo"],
      "pinned": [{"title": "Glossary", "href": "reference/glossary.md"}]
    }
  ]
}
```

- `sections` fixes the order of the header nav and the footer. Each `id` is a directory below `docs/` and the value of `data-section`. A section needs an `index.md`.
- `nav: false` keeps a section out of the header nav (a section reached only by a button, such as a start page, stays out of the nav).
- `progress: true` adds a "Mark as done" button to the pages of the section.
- `groups` fixes the order of the sidebar groups. Groups it does not list follow in the order of their first page by file name.
- `pinned` lists links shown above the groups of that section's sidebar.
- `search_suggest` lists the docs paths of the pages that the search palette offers before anything is typed, in order. A path that is not a page is an error.
- `repo` is a repository URL. A repository link appears in the header and footer only when it is set.
- `base_url` is the absolute URL of the site. With it, `sitemap.xml`, `llms.txt`, the canonical links and `og:url` are absolute, and `og:image` points at `assets/og.png` when that file exists. `-base-url` overrides it. Without it there is no `sitemap.xml` and no `robots.txt`, since a sitemap lists absolute URLs; `404.html` is written either way, with paths from the root of the site (`/`, or the path of `base_url`).
- `lint_skip` lists files and directories below the site directory that the banned-phrase lint leaves alone.
- Unknown keys are an error.

## Pages

Front matter follows `docs/AUTHORING.md` section 3. Required: `title`, `description`, and `order` for every page except a section's `index.md`. A page with `redirect:` needs nothing else. Optional: `keywords`, words that search should find on the page although the page does not say them. `toc: false` removes the table of contents from a page and gives its column the width. `gate` makes a page a step of a runbook: the path of its group is drawn under its header, with the gate under the node of the page. Unknown keys are an error, except `normalize`, `services` and `env`, which belong to `tools/capture`.

A file whose name is a letter, one to three digits and a hyphen, such as `a03-writing-a-field-twice.md`, is a lesson, here A3; the sidebar, the pager and the kicker show the id, and two files of one section may not share it. Files and directories that start with `_` or `.` are skipped. Pages may sit in subdirectories of a section. `order` ascends within a `group`; two pages of one group with the same `order` are an error.

Markdown extensions are those of `docs/AUTHORING.md` section 4. Where this tool is stricter or more specific:

- A page body has no level-1 heading.
- Containers do not nest. `::hint`, `::solution`, `::reveal`, `::answer` and `::fallback` are parts of `exercise`, `predict`, `check` and `widget`. Callouts may appear inside containers.
- `:::check TITLE` is a list of questions, each a paragraph ending in `?`, each followed by `::answer`. The title defaults to "Check yourself"; its heading has an id, made unique on the page. `:::tabs NAME` names the tab list.
- `:::demo NAME` has no body; the next line is `:::`. It embeds `site/demos/NAME/embed.html`, expanded with the tokens of a landing partial, and prefixes the path to the site root onto each relative `href` and `src` in it. The page loads `widgets.js` when the file has a `data-widget`. The Markdown twin of the page says that an interactive demo appears on the web page. A missing file or a name with characters outside lowercase letters, digits and hyphens is an error.
- `:::mapping`, `:::cards` and `:::runbook GROUP` shape what a page already says (`docs/AUTHORING.md`, section 4.2). A mapping marks a row whose second cell says No equivalent, None or Absent with `class="map-none"`, and a cell that holds Supported, Partial or Not supported with `data-v` of `yes`, `part` or `no`. A table whose second column is headed Status gets `mapping-status`. Cards split at the `<h3>` of the body: text before the first heading, or no heading, is an error. A runbook lists the pages of the group that have a `gate`, and a group without one is an error. The Markdown twin of a page keeps the table and the headings and drops the fences.
- A paragraph that starts with `Cause:` is set as two labelled lines; the text after the first ` Fix: ` is the second.
- `:::widget NAME` takes JSON, plain or in a `json` fence, and an optional `::fallback` part. Without a `::fallback` part the text comes from a `fallback` or `alt` string of the JSON; with neither the build warns and the page says that the figure needs JavaScript.
- `:::predict` shows the text before `::reveal` and hides the part after it; without `::reveal` the whole body is the answer. `:::exercise` takes at most two `::hint` parts and one `::solution`.
- An `out` block must directly follow its `run` block. An `out` block anywhere else is an error. A `hidden` block is not rendered, and a `hidden` run block takes its `out` block with it. `tools/capture` runs hidden blocks, so setup stays verified.
- A `run check` block verifies a solution and is not rendered (`renderCheckBlocks` in `markdown.go`). `tools/capture` runs it.
- `hl` lines must exist in the block.
- Code attributes are `file title hl ln status run out exit hidden check`; others are an error.
- A link to `x.md#id` must name an existing page. The anchor is checked after rendering.

## Landing page

`site/landing/NN-name.html` are concatenated in file name order between the shared header and footer. Each is `<section class="band" id="name" data-band="name"><div class="wrap">...</div></section>`; the generator warns when the file name, the id, `data-band` or the single `.wrap` do not agree with this. Files that start with `_` or `.` are skipped.

In a partial, `{{applies}}`, `{{pravo_version}}`, `{{cue_version}}`, `{{start}}`, `{{start_label}}`, `{{repo}}` and `{{name}}` are replaced by values of `site.json`. `{{go_version}}` is the `go` line of the `go.mod` next to the site directory, and `{{start_minutes}}` is the `minutes` of the page that `start_href` names; a build that uses one without its source fails. `{{data file.path}}` writes a string, number or boolean from `site/data/file.json`, escaped for HTML; the first word is the file name without `.json` and the rest are object keys and array indexes. `{{json file.path}}` writes any value as JSON that is safe inside `<script type="application/json">`. A path that does not resolve is a build error, and so is a `{{data}}` that names an object or an array. Counts and recorded output on the landing page therefore come from files: `tools/capture -json` and the programs in `site/demos` write them, and `site/data` is not copied to the output. Other `{{...}}` text is left alone. HTML.md has the details.

When `site/landing/` has no partials, `site/index.html` is copied unchanged if it exists, with a warning, and otherwise a minimal page is written from `site.json`.

## Banned phrases

`banned.txt` holds one regular expression per line; lines that start with `#` are comments. Each is matched case-insensitively against every line of the `.md`, `.html`, `.json` and `.txt` files below `docs/` and `site/`, and against the file named by `-readme` (`README.md` by default; skipped when absent). It reads one such file: `-readme ARCHITECTURE.md` checks `ARCHITECTURE.md` instead of `README.md`.

Fenced code in Markdown and `<pre>`, `<script>` and `<style>` blocks in HTML are skipped. So are directories named `assets`, `.git` and `node_modules`, the output directory, the entries of `lint_skip`, and every file named `AUTHORING.md`, which names the phrases. A match prints `file:line: banned phrase "..." (rule ...)` and fails the build. `-lint=false` turns the check off; `-banned FILE` replaces the list.

## Files

| File | Role |
|---|---|
| `main.go` | Flags and the build sequence. |
| `config.go`, `site.go`, `frontmatter.go` | `site.json`, page discovery, ordering, groups, redirects, front matter. |
| `markdown.go`, `blocks.go`, `fence.go` | goldmark setup, the container pre-pass, code fences and terminals. |
| `highlight.go`, `output.go` | CUE and HCL scanners, chroma for other languages, plan output colouring. |
| `templates.go`, `build.go`, `landing.go` | HTML shells and views, writing the output. |
| `search.go`, `glossary.go` | Search index, sitemap, llms files, glossary definitions. |
| `lint.go`, `check.go`, `version.go` | Banned phrases, link and anchor check, version detection. |
| `HTML.md` | The HTML contract with `site/assets`. |
| `testdata/` | `docs/`: kitchen-sink pages that use every component, and `guides/stress.md`, every component at its worst case (long words, wide tables, long titles). `site/`: a small site directory for the tests. `static/`: hand-written pages that show the HTML contract and the landing primitives. |
| `contrast/` | Checks every colour pair of the style sheet in both themes. |
