# HTML contract

This file is the contract between `tools/sitegen` (which writes the HTML) and `site/assets` (the style sheet and the scripts). A change to either side changes this file. The kitchen-sink content in `tools/sitegen/testdata/docs` uses every element below.

```sh
go run ./tools/sitegen -docs tools/sitegen/testdata/docs -lint=false -out /tmp/pravo-site
```

Rules that hold for every page:

- Everything is visible and usable without JavaScript. Scripts only add behaviour. The generator never writes `hidden` or an inline `display:none`.
- Every URL is relative to the page (`../assets/style.css`, `a02.html`). The site works from `file://` and from a sub-path.
- `<html>` starts with `class="no-js"`. The inline script in `<head>` replaces it with `js` before the first paint. Controls that need a script (`.search-open`, `#theme-toggle`, `.copy`, `.copy-page`, `.mark-done`) are hidden by the style sheet under `.no-js`.
- Heading ids come from the heading text. Ids the generator invents are `fig-N`, `tab-G-N` and `panel-G-N`.
- Scripts are `assets/site.js` on every page and `assets/widgets.js` on pages that contain a `.widget`. Both are `defer`.

## Pages

| Kind | File | `<body>` |
|---|---|---|
| Landing | `index.html` | `class="page page-landing" data-root=""` |
| Documentation | `<section>/<name>.html` (sections may nest directories) | `class="page page-doc" data-section="learn" data-root="../"` |
| All pages | `contents.html` | `class="page page-doc" data-section="contents" data-root=""` |
| Moved page | the old path | `class="page page-redirect"`, no site chrome |

`data-root` is the relative path from the page to the site root. `data-section` is the section id from `site/site.json`.

## Head

```html
<!doctype html>
<html lang="en" class="no-js">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Writing a field twice · Learn · pravo</title>
<meta name="description" content="...">
<meta name="color-scheme" content="light dark">
<meta name="theme-color" content="#f5f2ea" media="(prefers-color-scheme: light)">   <!-- the --bg of each theme -->
<meta name="theme-color" content="#0b0d10" media="(prefers-color-scheme: dark)">
<meta property="og:site_name|title|description|type" ...>
<meta property="og:url" content="https://...">                  <!-- only with base_url; so are og:image (when assets/og.png exists) and canonical -->
<meta name="twitter:card" content="summary_large_image">        <!-- "summary" without assets/og.png -->
<link rel="icon" href="../favicon.ico" sizes="any">             <!-- when site/favicon.ico exists -->
<link rel="icon" type="image/svg+xml" href="../assets/logo.svg">   <!-- only without site/favicon.ico -->
<link rel="apple-touch-icon" href="../assets/apple-touch-icon.png">   <!-- when site/assets/apple-touch-icon.png exists -->
<link rel="canonical" href="https://...">                      <!-- only with base_url -->
<link rel="alternate" type="text/markdown" href="a03-....md">  <!-- documentation pages -->
<link rel="preload" href="../assets/fonts/x.woff2" as="font" type="font/woff2" crossorigin>  <!-- site.json "preload" -->
<script>/* no-js to js, then data-theme from localStorage "pravo-theme" */</script>
<link rel="stylesheet" href="../assets/style.css">
<script src="../assets/site.js" defer></script>
<script src="../assets/widgets.js" defer></script>             <!-- pages with a widget -->
</head>
```

The title is `Page · Section · pravo`, or `Section · pravo` on a section index, or the `title` of `site.json` on the landing page.

## Header

```html
<a class="skip-link" href="#content">Skip to content</a>
<header class="site-header">
<a class="brand" href="../index.html"><img class="brand-mark" src="../assets/logo.svg" alt="" width="24" height="24"> <span class="brand-name">pravo</span></a>
<nav class="site-nav" aria-label="Documentation sections">
<a href="index.html" aria-current="true">Learn</a>
<a href="../migrate/index.html">Migrate</a> ...
</nav>
<div class="site-tools">
<a class="repo-link" href="https://github.com/..." rel="noopener">GitHub</a>   <!-- only when site.json "repo" is set -->
<button class="search-open" type="button">Search <kbd>/</kbd></button>
<button id="theme-toggle" type="button" aria-label="Toggle colour theme"></button>
<a class="btn btn-primary" href="../how-to/quick-start.html">Quick start</a>
</div>
</header>
```

The nav lists the sections of `site.json` that have `"nav"` unset or true, in order. `aria-current="true"` marks the section of the current page, and `aria-current="page"` marks the link to the page that is open (the section index). The `Start` button points to `start_href`.

## Footer

Shared by every page except the moved-page stub.

```html
<footer class="site-footer">
<div class="foot-inner">
<div class="foot-row">
<p class="foot-brand"><span class="foot-title">pravo</span> <span class="foot-tag">First sentence of the site description.</span></p>
<nav class="foot-nav" aria-label="Footer">
<ul class="foot-sections"><li><a href="...">Learn</a></li> ... </ul>
<ul class="foot-project"><li><a href="...">Repository</a></li><li><a href="...">All pages</a></li><li><a href="...">llms.txt</a></li></ul>
</nav>
</div>
<p class="legal"><span class="applies">pravo 0.1 · CUE 0.17</span> · Footer note.</p>   <!-- only when footer_note is set -->
</div>
</footer>
```

The footer is one row. `foot-sections` lists the sections of the header nav, each linking to its overview, and `foot-project` lists the repository (when `repo` is set), the list of all pages and `llms.txt`. The tagline is the text of the description up to its first full stop.

## Documentation page

```html
<body class="page page-doc" data-section="learn" data-root="../">
<!-- skip link and header -->
<div class="doc-shell [no-toc] [no-sidebar]">
<div class="doc-sidebar" id="sidebar">
<nav aria-label="Pages in this section">
<ul class="side-pinned"><li><a href="...">Glossary</a></li></ul>        <!-- site.json "pinned", optional -->
<ul><li><a href="index.html" data-page="learn/index.html">Learn</a></li></ul>   <!-- ungrouped pages, no heading -->
<p class="side-group">Track A, CUE from zero</p>
<ul>
<li><a href="a03.html" data-page="learn/a03.html" aria-current="page"><span class="side-n">A3</span> Writing a field twice</a></li>
</ul>
</nav>
</div>
<main class="doc-main" id="content" tabindex="-1">
<nav class="breadcrumb" aria-label="Breadcrumb"><ol>
<li><a href="index.html">Learn</a></li><li class="crumb-group">Track A, CUE from zero</li><li aria-current="page">Writing a field twice</li>
</ol></nav>
<article class="doc">
<header class="doc-head">
<p class="kicker">§ A3 · Track A</p>
<h1>Writing a field twice</h1>
<p class="lede">...</p>
<dl class="lesson-meta">
<div><dt>Time</dt><dd>10 min</dd></div>
<div><dt>Level</dt><dd>beginner</dd></div>
<div><dt>Needs</dt><dd><a href="a02.html">A2</a></dd></div>
<div><dt>You will learn</dt><dd>...</dd></div>
<div class="meta-done"><dt>Done when</dt><dd><code>pravo eval</code> prints ...</dd></div>
</dl>
<p class="doc-actions"><a class="view-md" href="a03.md" type="text/markdown">View as Markdown</a> <button class="copy-page" type="button" data-md="a03.md">Copy page</button></p>
</header>
<details class="toc-inline"><summary>On this page</summary><nav aria-label="On this page"><ul><li class="toc-l2"><a href="#constraints-that-agree">...</a></li> ...</ul></nav></details>
... content ...
<footer class="doc-foot">
<nav class="pager" aria-label="Previous and next page">
<a class="pager-prev" rel="prev" href="a02.html"><span class="pager-label">Previous</span><span class="pager-kicker">§ A2</span><span class="pager-title">Fields and structs</span><span class="pager-desc">Description.</span></a>
<a class="pager-next" rel="next" href="b01.html">...</a>
</nav>
<button class="mark-done" type="button" data-page="learn/a03.html">Mark as done</button>
<p class="applies">pravo 0.1 · CUE 0.17</p>
</footer>
</article>
</main>
<div class="doc-toc">
<nav aria-label="On this page"><p class="toc-title">On this page</p><ul>
<li class="toc-l2"><a href="#constraints-that-agree">Constraints that agree</a></li>
<li class="toc-l3"><a href="#...">...</a></li>
</ul></nav>
</div>
</div>
<!-- site footer -->
```

- The sidebar shows the current section only. Groups come from the `group` front matter, in the order of `groups` in `site.json` and then of first appearance by file name. Inside a group, pages ascend by `order`. Pages without a group come first, without a heading. The section index is the first entry.
- `.side-n` is the lesson id (`A3`), present for files named `a03-name.md`. `data-page` on every sidebar link and on `.mark-done` is the page path from the site root; the progress script stores these.
- `.crumb-group` is plain text. It is present when the page has a group.
- `.kicker` is `§ A3 · Track A` for a lesson (id and the part of the group before the first comma), and `§ Section` or `§ Section · Group` for other pages.
- `.meta-done` marks the "Done when" cell; the style sheet gives its label the accent colour.
- `.lesson-meta` appears when the page has any of `minutes`, `level`, `needs`, `learn`, `done_when`. Lesson ids in `needs` link to the lesson. The values are inline Markdown, rendered.
- `.pager` links follow the sidebar order, across groups. `.pager-kicker` appears when the target is a lesson. `.pager-prev` or `.pager-next` is missing at the ends. `.pager` is missing when the section has one page.
- `.mark-done` appears on every page except the section index in sections with `"progress": true`.
- `.applies` carries `pravo X.Y · CUE X.Y`, or the `applies_to` of the page.
- `.doc-toc` lists h2 and h3. It is absent when the page has no h2 or h3, and the shell then has class `no-toc`. `.toc-inline` repeats the list as a folded `<details>` after the head of the page. The style sheet shows it where `.doc-toc` is hidden (under 68.75 rem) and hides it above.
- `.doc-actions`: `.view-md` links to the Markdown twin and works without a script. `.copy-page` fetches `data-md` and copies it; hide it when the fetch fails (`file://`).

## Content

Prose is the output of goldmark: `p`, `ul`, `ol`, `blockquote`, `table`, `code`, `img`, `hr`, `em`, `strong`, inline `a`. Raw HTML in a page passes through unchanged.

- Tables are wrapped in `<div class="table-wrap">`.
- Headings carry an id, and the text is the permalink: `<h2 id="x"><a class="anchor" href="#x">Text</a></h2>`. The style sheet draws the `#` after the text with generated content that has an empty text alternative, so the name of the heading is its text alone. A heading that holds a link of its own gets `<a class="anchor-side" href="#x" aria-label="Link to this section">#</a>` after the text instead.
- A link whose text is only a lesson id (`A6`) carries `aria-label="A6, Title of the lesson"`.
- External links are plain `<a href="https://...">`.
- A link to another page is rewritten from `foo.md#id` to `foo.html#id`, and a link to a redirect stub goes to the final page.
- A link to the glossary page with a fragment is a term link: `<a class="term-link" href="../reference/glossary.html#unification" data-term="unification" data-def="First paragraph of the entry, plain text.">unification</a>`. `data-def` is absent when the entry has no paragraph.

### Code

```html
<figure class="code" data-lang="cue" data-status="conflict" data-ln>
<figcaption class="code-title"><span class="code-file">main.cue</span><span class="code-badge" title="Two values for one field disagree">conflict</span></figcaption>
<div class="code-body"><pre><code><span class="line hl"><span class="ln"></span><span class="tok-kw">package</span> main
</span><span class="line"><span class="ln"></span>...
</span></code></pre></div>
<button class="copy" type="button" aria-label="Copy main.cue"></button>
</figure>
```

- `data-lang` is the canonical language: `cue`, `hcl`, `bash`, `json`, `yaml`, `diff`, `text`, or the name of any other language chroma knows (`go`, `toml`, `dockerfile`, ...). Unknown languages are plain text.
- `data-status` is one of `valid`, `conflict`, `rejected`, `unintended` and is absent otherwise. The badge text is the status; its `title` explains it. `data-ln` is present when the block asked for line numbers and is a boolean attribute.
- `figcaption` is absent when the block has neither a title nor a status. `.code-file` is absent when there is no title.
- Each line is `<span class="line">`, with `hl` added for highlighted lines and `ins` or `del` for added and removed lines of a `diff` block. The first child is the empty `<span class="ln"></span>`; draw line numbers with a CSS counter. The line's newline is the last character inside the span, so `textContent` without the `.ln` span is the source, and `display:block` lines do not double the spacing.
- Token classes: `tok-kw` keywords, `tok-str` strings, `tok-num` numbers and the constants `true`, `false`, `null`, `tok-com` comments, `tok-id` field labels, attribute keys and variables, `tok-op` operators (including the `\(` `)` of CUE string interpolation and `${` `}` of HCL), `tok-def` CUE definitions (`#Name`), `tok-type` builtin types and `_`, `tok-fn` function calls and CUE attributes. Everything else is plain text.
- The copy button has no `data-copy`: copy the text of the `.line` elements without `.ln`.

### Terminal

A `run` block and the `out` block after it are one window. A `run` block without an `out` block has no `pre`.

```html
<figure class="term" data-exit="1" data-status="conflict" data-ran="0.1">
<figcaption class="code-title"><span class="code-file">terminal</span><span class="code-badge" data-exit="1" title="Exit code">exit 1</span></figcaption>
<div class="term-body">
<div class="term-line cmd"><span class="prompt">$</span><span class="cmd-text">pravo eval</span></div>
<pre class="term-out"><code><span class="out-err">Error: evaluating .:</span>
<span class="out-dim">    ./main.cue:3:7</span></code></pre>
</div>
<button class="copy" type="button" data-copy="pravo eval" aria-label="Copy command">Copy</button>
</figure>
```

- One `.term-line.cmd` per command. A command that continues over lines with a trailing backslash is one element with newlines in `.cmd-text` (use `white-space: pre-wrap`).
- `data-exit` is always present. The exit badge appears when it is not 0. `data-status` appears when the block declared a status. `data-ran` is the pravo version that produced the output and is absent on pages with `capture: false` and on blocks without output.
- `data-copy` holds the commands, one per line (`&#10;`), without the prompt.
- Output spans, applied by the text of the line: `out-add` (`+`), `out-change` (`~`, `-/+`, `# forces replacement`), `out-destroy` (`-`), `out-err` (an `Error:` line and the message lines after it), `out-dim` (locations `file:line:col`, `...`, `(known after apply)`, plan comment lines). In the `Plan:` and `Apply complete!` lines the counts carry `out-add`, `out-change` and `out-destroy`. The glyph classes apply only to output that looks like a plan.
- Blocks marked `hidden` and `run check` blocks are not in the HTML.

### Compare and tabs

```html
<div class="compare" data-count="2" style="--longest:42"><figure class="code">...</figure><figure class="code">...</figure></div>
```

```html
<div class="tabs">
<div class="tab-list" role="tablist" aria-label="Name of the container, else the tab titles">
<button role="tab" type="button" id="tab-1-1" aria-selected="true" aria-controls="panel-1-1">macOS</button>
<button role="tab" type="button" id="tab-1-2" aria-selected="false" aria-controls="panel-1-2">Linux</button>
</div>
<div class="tab-panel" role="tabpanel" id="panel-1-1" aria-labelledby="tab-1-1" data-title="macOS" data-active>...</div>
<div class="tab-panel" role="tabpanel" id="panel-1-2" aria-labelledby="tab-1-2" data-title="Linux">...</div>
</div>
```

All panels are in the HTML and none is hidden. Without a script they stack; `data-title` can label each panel through `::before { content: attr(data-title) }` under `.no-js`. The script hides the panels that are not selected. `data-active` marks the first panel for styles that run before the script. A tab group holds code blocks (a `run` block with its `out` block is one tab).

### Predict, exercise, check, deep dive

```html
<details class="predict"><summary><span class="predict-label">Predict</span> Question</summary><div class="predict-reveal">...</div></details>
```

When the container has text before `::reveal`, both sit in `<div class="predict-group">` and the text comes first.

```html
<section class="exercise">
<header><span class="exercise-label">Exercise</span><h3 id="exercise-title"><a class="anchor" href="#exercise-title">Title</a></h3></header>
<div class="exercise-body">...</div>
<details class="hint" data-tier="1"><summary>Hint 1</summary>...</details>   <!-- "Hint" when there is one hint -->
<details class="hint" data-tier="2"><summary>Hint 2</summary>...</details>
<details class="solution"><summary>Solution</summary>...</details>
</section>
```

```html
<section class="check"><h3 id="check-yourself"><a class="anchor" href="#check-yourself">Check yourself</a></h3>
<details><summary>Question?</summary><p>Answer</p></details> ...
</section>
```

```html
<details class="deep-dive"><summary>Title</summary>...</details>
```

### Callouts

```html
<div class="callout callout-note" role="note"><p class="callout-title">Note</p>...</div>
```

Types: `note`, `tip`, `warning`, `pitfall`, `check`; titles are `Note`, `Tip`, `Warning`, `Pitfall`, `Check`.

### Figure and widget

```html
<figure class="fig" id="fig-3"><div class="fig-body">...</div><figcaption><span class="fig-n">fig. 3</span> Caption</figcaption></figure>
```

Figures are numbered per page from 1.

```html
<div class="widget" data-widget="unify">
<script type="application/json">{"compact":"json","text":"a \u003c b \u0026 c"}</script>
<noscript><p>Fallback text, rendered Markdown.</p></noscript>
</div>
```

The JSON is the body of the `:::widget` container, validated and compacted. The characters `<`, `>` and `&` are written as JSON escapes (backslash, `u003c`, `u003e`, `u0026`), so the text can never close the script element. The fallback is the `::fallback` part, or the `fallback` or `alt` string of the JSON. `widgets.js` replaces the contents of a `.widget[data-widget]` it knows and leaves the others.

### Mapping, cards, runbook and cause

```html
<div class="mapping">                           <!-- or class="mapping mapping-status" -->
<div class="table-wrap"><table> ... <tr class="map-none"> ... <td class="map-status" data-v="yes|part|no">Supported</td> ... </table></div>
</div>

<div class="cards doc-cards">
<div class="card spot"><h3 class="card-title">Title</h3><p class="card-text">Text.</p><p class="card-text"><a href="#x">Link</a></p></div>
</div>

<nav class="runbook" aria-label="The runbook, step 2 of 9">
<p class="rb-label">The runbook, step 2 of 9</p>
<ol class="rb-list" style="--cols:5">
<li class="rb-step rb-past"><a class="rb-in" href="m1.html"><span class="rb-node" aria-hidden="true"></span><span class="rb-n">M1</span><span class="rb-t">Title</span><span class="rb-g"><span class="sr-only">Gate: </span>The gate</span></a></li>
<li class="rb-step rb-here"><span class="rb-in" aria-current="step"> ... </span></li>
<li class="rb-step rb-ahead rb-end"> ... </li>
</ol>
<p class="rb-lookup"><span class="rb-lookup-l">Lookups</span> <a href="m9.html"><span class="rb-n">M9</span> Pitfalls</a></p>
</nav>

<div class="cause-fix"><p><span class="cf-l">Cause</span> Text.</p><p><span class="cf-l cf-fix">Fix</span> Text.</p></div>
```

A mapping keeps the semantics of its table; the style sheet draws the arrow and the tints with pseudo-elements. `rb-end` marks the last step of a row, which has no connector. The runbook of a page that is a step sits after the header; a page that is not a step embeds it with `:::runbook`, and no step is marked.

### Demo

A page embeds a demo with `:::demo NAME`, which renders `<div class="demo" data-demo="NAME">` around the contents of `site/demos/NAME/embed.html`. The file is written as if it stood on a page at the root of the site, and uses the tokens of a landing partial. The generator prefixes the path to the site root onto each relative `href` and `src`, so `assets/demos/NAME.js` and `learn/index.html` resolve at any depth. A fragment, an absolute path and an address with a scheme are left alone. The page loads `widgets.js` when the file contains a `data-widget=`.

## Landing page

`site/index.html` in the output is the header, the files `site/landing/NN-name.html` in file name order, and the footer, inside `<main class="landing-main" id="content">`. Each partial is one `<section class="band" id="name" data-band="name"><div class="wrap">...</div></section>`: the single `.wrap` is the ruled column of the band. The generator warns when the tag and the file name disagree or when the `.wrap` is missing. The tokens `{{applies}}`, `{{pravo_version}}`, `{{cue_version}}`, `{{start}}`, `{{start_label}}`, `{{repo}}` and `{{name}}` in a partial are replaced by the values of `site.json`. `{{go_version}}` is the `go` line of the `go.mod` next to the site directory, as `1.26`, and `{{start_minutes}}` is the `minutes` of the page that `start_href` names; a build that uses either without its source fails. Other `{{...}}` text is left alone. A page that contains `data-widget=` loads `widgets.js`.

Numbers and recorded output come from files, never from typing. `{{data hero.blocks.1.output}}` reads `site/data/hero.json` (the first word is the file without `.json`; the rest are object keys and array indexes) and writes the string, number or boolean, HTML-escaped. `{{json hero.blocks.1}}` writes any value as compact JSON that is safe inside `<script type="application/json">`, for a widget. A path that does not resolve is a build error. `go run ./tools/capture -json site/data/hero.json site/demos/hero/page.md` writes the file for a page; `site/data` is not copied to the output.

Without partials the generator copies `site/index.html` if it exists, and otherwise writes a minimal page from `site.json`.

## All pages

`contents.html` lists every page by section and group with its description: the way to find a page without the search script.

```html
<div class="doc-shell no-sidebar no-toc"><main class="doc-main" id="content"><article class="doc">
<header class="doc-head">...</header>
<section class="contents-section" id="learn"><h2>Learn</h2><p>Blurb.</p>
<h3>Track A, CUE from zero</h3>
<dl class="contents-list"><dt><a href="learn/a01.html">Title</a></dt><dd>Description.</dd> ...</dl>
</section>
</article></main></div>
```

## Moved page

A page with `redirect: new.md` becomes a stub: `<link rel="canonical">`, `<meta http-equiv="refresh" content="0; url=new.html">`, the style sheet and a link. It is left out of the sidebar, the pager, the search index, the sitemap, the contents page and the llms files.

## Files written next to the pages

| File | Content |
|---|---|
| `<page>.md` | Markdown twin: `# Title`, the description, then the page source without front matter. Links stay valid because the twin sits next to the page. |
| `search-index.json` | Array with one entry per page: `title`, `section`, `group`?, `lesson`?, `minutes`?, `url` (from the site root), `description`, `headings` (`[{text, id}]`, h2 and h3), `body` (about 1,200 characters of prose; no code, no answers, no hints), `terms` (every word of the page source, code and tables included, lower case, once each, sorted, separated by spaces; hashes, long numbers and drawing data are left out), `keywords`? (the `keywords` front matter) and `suggest`? (the position in `search_suggest` of `site.json`, from 1). |
| `search-index.js` | `window.PRAVO_SEARCH=[...];` the same array. Load it with a script element where `fetch` cannot read `file://`. |
| `sitemap.xml`, `robots.txt` | Written with `base_url` only, since a sitemap lists absolute URLs. One `<loc>` per page, the landing page and `contents.html`; `robots.txt` allows everything and names the sitemap. A `robots.txt` in the site directory is copied instead. |
| `404.html` | Written always. A host serves it for an address at any depth, so its stylesheet, script and links are paths from the site path (`/`, or the path of `base_url`), and the page has `noindex`. It offers search, the sections and the contents page. |
| `llms.txt`, `llms-full.txt` | Index of the Markdown twins by section, and every page in one file. |
| `assets/`, `robots.txt`, `CNAME`, `.nojekyll`, `favicon.ico` | Copied from the site directory when present. |
| images next to pages | `svg png jpg jpeg gif webp avif` files below `docs/<section>/` are copied to the same place below the output. |
| shared images | The same kinds of file below `docs/img/` are copied to `img/` at the root of the output, so a page links to `../img/serve/overview-dark.png`. `docs/img` is not a section and raises no warning. |

## Link check

After writing, the generator reads every generated `.html` file and fails on: a relative `href`, `src` or `poster` whose file does not exist; a fragment with no matching id; an id used twice on a page; a `data-page` that is not a page; a refresh target that does not exist; a search index `url` or heading id that does not exist. The check needs `assets/style.css`, `assets/site.js`, `assets/logo.svg` and, on pages with a widget, `assets/widgets.js`. `404.html` is the one page that uses paths from the root; the check resolves them against the site path.

## Style sheet and script notes

`site/README.md` describes the design system. Points that bind the generated HTML:

- `assets/search.js` and `assets/timeline.js` sit next to `site.js` and load on demand (`search.js` on the first use of the search palette, `timeline.js` through `pravoUI.timeline(cb)` or from `widgets.js`). No `<script>` tag names them. A landing partial with an inline script that uses `pravoTimeline` runs it on `DOMContentLoaded` and calls `pravoUI.timeline`.
- `data-count` on `.compare` is the number of windows, and `--longest` in its `style` the width of their longest line in characters, with a tab counted to the next stop of four: the style sheet makes a column at least that wide and stacks the windows when two columns do not fit. The counter attribute for stat tiles is `data-countup`.
- A search entry may carry its excerpt in `text` or `body`.
- Classes set by the scripts, which the generator does not write: `is-ready` (tabs, marquee), `is-in` (reveal), `is-done` (progress), `is-copied`, `is-scrolled`, `is-lit`, `is-on`, `is-paused` (marquee), `is-stack` (a table of three or more columns without merged cells; its cells get `data-label`, the text of their column heading, and the style sheet sets it as a list of records under 40 rem). Elements inserted by the scripts: `.read-progress`, `.drawer-bar`, `.scrim`, `.progress-summary`, `.term-pop`, `dialog.search-dialog`, `button.marquee-toggle` (after the marquee, in its caption), `a.anchor-side` (when a heading has no permalink), `.sr-done` (the text "(done)" in a link to a finished page), a polite live region for copy results, and the `tabindex`, `role="group"` and `aria-label` of a code block, terminal or table that scrolls sideways.
- `.code[data-ln]` shows line numbers from a CSS counter; the `.ln` span stays empty.
- Under `no-js`, tab panels show their `data-title`; the controls named in the rules above are hidden. On a phone without a script the sidebar follows the page instead of preceding it, because there is no drawer.
- `.fig-body svg[viewBox]`: under 40 rem the script widens the drawing (`min-width` in pixels, kept in `data-min0` when the page set one) until its smallest label is 9.5 px, and the figure scrolls sideways.
- A window that scrolls sideways (`.code-body`, `.term-body`, `.table-wrap`) shows a faint edge on the side where its content continues; this is background layers only, no markup.
