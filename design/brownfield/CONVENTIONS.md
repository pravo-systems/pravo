# Conventions for the brownfield design site

This is a small static site. It explains one design to a total newcomer and to engineers, and it is a tool for discussion. There is no build step and no network dependency: open `index.html` in a browser, or serve the folder with `python3 -m http.server 0`.

Read this file, then open `_components.html`. Every reusable piece is shown there, rendered, with the exact markup to copy. If a piece you need is missing, say so in your hand-off note instead of inventing one-off CSS.

## Files

```
index.html                       Start here: the story
01-the-problem.html
02-the-idea.html
03-a-worked-example.html
04-how-it-works.html
05-what-providers-can-do.html
06-where-it-gets-messy.html
07-decisions-and-roadmap.html
glossary.html
_components.html                 not in the nav; the catalogue of pieces
assets/site.css                  one stylesheet: tokens, base, every component
assets/site.js                   nav, footer, pager, theme, reveal, stepper, tabs, code, copy
assets/fonts/                    IBM Plex Sans, IBM Plex Mono, Newsreader (woff2, latin)
```

The page list lives in one place: the `PAGES` array at the top of `assets/site.js`. The header, the mobile menu, the footer and the previous/next pager are all built from it. To rename a page or change its nav label, edit that array only.

Do not edit `assets/site.css` or `assets/site.js` for one page. If you need a style, add it to `site.css` in the matching numbered section and add the piece to `_components.html`.

## Adding a page

Copy this skeleton. Only the `title`, `description`, `data-page`, the page header and the sections change.

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>The idea | pravo brownfield design</title>
<meta name="description" content="One sentence for search results and link previews.">
<script>(function(){var r=document.documentElement;r.classList.add('js');try{var t=localStorage.getItem('bf-theme');if(t==='light'||t==='dark')r.setAttribute('data-theme',t)}catch(e){}})()</script>
<link rel="stylesheet" href="assets/site.css">
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='7' fill='%230a735a'/%3E%3Cpath d='M16 7v18M8 12l8-5 8 5M8 20l8 5 8-5' stroke='white' stroke-width='2' fill='none'/%3E%3C/svg%3E">
</head>
<body data-page="02-the-idea.html">
<a class="skip-link" href="#content">Skip to content</a>
<header data-site-header></header>

<main id="content" tabindex="-1">
  <header class="page-header">
    <div class="wrap">
      <p class="kicker">Chapter 2 of 7</p>
      <h1>The idea</h1>
      <p class="lede">One or two sentences that say what this page gives you.</p>
      <ul class="page-meta"><li class="chip">6 minute read</li></ul>
      <nav data-toc></nav>
    </div>
  </header>

  <div class="wrap page-body">
    <section class="section" id="first-idea" data-reveal>
      <h2>A heading that says the point</h2>
      <p>Text.</p>
    </section>
  </div>
</main>

<footer data-site-footer></footer>
<script src="assets/site.js" defer></script>
</body>
</html>
```

Rules for the skeleton:

- `<title>` is `Page name | pravo brownfield design`.
- Keep the inline theme script in `<head>`. It stops a flash of the wrong theme.
- `data-page` is the file name. It highlights the current page in the nav.
- The `h1` appears once, inside `.page-header`. Section headings are `h2`, sub-headings `h3`. Do not skip levels.
- Every `section.section` that another page might link to gets an `id`. Headings without an id get one from the script.
- `<nav data-toc>` fills itself with chips for each `h2`. Leave it out of short pages.
- Put `data-reveal` on a `section` or a figure to fade it in as it scrolls into view. Do not put it on the page header.
- Add `?static` to any page URL to switch off all motion and reveals. Use it for screenshots.

## Layout

- `.wrap` is the centred column (78rem maximum). `.page-body` goes on the wrap that holds the sections.
- Inside `.section`, text elements (`p`, `ul`, `ol`, `h2` to `h4`, callouts) are held to a readable measure of 44rem. Figures (`.window`, `.stepper`, `.tabs`, `.decision`, `.pushback`, `.cmp-wrap`, `.facts`, `.timeline`, `.diagram`, `.cards`) use the full width. Add `.wide` to let a text block go wide.
- One idea per section. If a section needs more than about five paragraphs, split it.

## Class names

| Piece | Markup | Notes |
|---|---|---|
| Page header | `header.page-header > .wrap > p.kicker, h1, p.lede, ul.page-meta, nav[data-toc]` | One per page |
| Section | `section.section#id[data-reveal] > h2, p, ...` | |
| eli5 box | `aside.eli5 > .eli5-icon, .eli5-body > p.eli5-label, p` | The icon is drawn by CSS. One or two short paragraphs. |
| Analogy card | `article.analogy > p.analogy-label, h3, p, dl.analogy-map` | The map pairs the analogy with the real thing |
| Plain-words callout | `aside.plain > span.plain-icon, .plain-body > p.plain-label, p` | One sentence |
| For engineers | `details.eng > summary, .eng-body` | Collapsed by default. Native, works without script. |
| Terminal window | `figure.window > .window-bar, pre[data-lang="term"]` | See below |
| File window | `figure.window > .window-bar, pre[data-lang="cue"\|"json"\|"hcl"\|"sh"]` | |
| Diff | `figure.window > .window-bar, pre[data-lang="diff"]` | |
| Step list | `ol.steps > li > h4, p` | Static, numbered, with a connecting line |
| Stepper | `section.stepper[data-stepper] > .stepper-stage > svg.dg, .stepper-panel > ol.stepper-steps > li > h4, p` | Script adds the controls |
| Tabs | `.tabs[data-tabs][data-sync] > .tab[data-tab-title][data-tab-icon][data-key]` | Icons: `cursor` (manual), `spark` (AI) |
| Decision card | `article.decision#id > .decision-head, p.decision-why, .options > .option, p.recommendation` | `.is-recommended` on the option |
| Pushback card | `article.pushback[data-sev] > .pushback-head, h3, dl.pushback-body` | `data-sev`: impossible, impractical, uncertain |
| Status pill | `span.pill.pill--seen\|claimed\|managed\|ignored\|gone` | Also `--good --warn --bad --struct --neutral` |
| Comparison table | `.cmp-wrap > table.cmp` | Cells: `yes`, `no`, `part`; column `hl` |
| Definition list | `dl.defs > div > dt, dd` | |
| Key-fact tile | `.facts > figure.fact[data-status] > p.fact-value, figcaption.fact-label, .fact-foot` | Always a source |
| Timeline | `ol.timeline[.timeline--h] > li[data-state] > time, h4, p` | `data-state`: done, now |
| Diagram | `figure.diagram > svg.dg, figcaption` | See below |
| Glossary term | `span.term[tabindex="0"][data-def]` | Hover or tap for a short gloss |
| Icons | `span.ico.ico-check` and friends | Drawn as masks, so they take the text colour |

### Terminal, file and diff windows

Write plain text inside the `pre` and name the language in `data-lang`. The script colours it and adds a copy button. Escape `<` as `&lt;` and `&` as `&amp;`.

- `term`: lines starting with `$ ` are commands (the copy button copies only these); lines starting with `#` are dim comments; lines starting with `+ `, `~ ` or `- ` colour the whole line green, amber or red. Inline colour: `{g|text}` green, `{a|text}` amber, `{r|text}` red, `{k|text}` blue-violet, `{d|text}` dim.
- `cue`, `json`, `hcl`, `sh`, `html`: syntax colouring. `diff`: lines starting `+`, `-` and `@@`.
- `data-hl="2,4-6"` highlights lines. `data-numbers` shows line numbers. `data-nocopy` on the figure removes the copy button.
- The window bar holds three dots, a title (a file name or "terminal"), a kind label and the copy button.

Sample commands that do not exist yet must say so: put `# sample output; placeholder commands` as the first line of the terminal.

### Stepper

The stage is an SVG with `class="dg"`. Each step is an `li` with an `h4` and one short paragraph. Inside the SVG:

- `data-show="2-4"` shows the element only in steps 2 to 4. `data-hide="4"` hides it in step 4.
- `data-cls="1:node--soft 2:node--seen 4:node--managed"` sets the class from that step on. Colours cross-fade.
- `data-text="1:unknown 2:seen"` swaps the label.
- Add `data-autoplay` to start when the stepper scrolls into view. `data-interval="3000"` sets the pace in milliseconds.

Keep a stepper to three to six steps. Each step changes one thing in the picture. Draw the stage on a `viewBox` no wider than about 480 units (for example `0 0 480 340`) and keep text at 11 units or more: on a phone the picture is scaled down to the screen width, and a wide viewBox makes the labels unreadable.

### Diagrams

Draw inline SVG, not images. Use `g.node` plus a state class, `path.arrow` plus a colour class, `.zone` for a boundary, `.flow` for marching dashes and `.flow-dot` with `<animateMotion>` for a dot travelling a path. Reduced-motion readers get the static picture. Rules:

- Give every SVG `role="img"` and an `aria-label` that says what it shows.
- No emoji and no symbol characters in SVG text or anywhere on the site: the bundled latin fonts have no glyphs for arrows, ticks or crosses. Use `.ico` masks or draw the shape.
- Dashed outline means "not managed by pravo". Solid means managed or structure.
- Keep a picture readable at 320 px wide: at most seven nodes, labels of three words or fewer.

### Tabs

The manual path and the AI path are always shown as sibling tabs of the same task, with `data-sync="path"` so the reader's choice carries across the site. The manual path comes first and must be complete without the other.

### Decision cards

Give each card a stable `id` (`d-scope`, `d-identity`, ...) and a number (`D1`, `D2`). The script adds a stance and a note box that stay in the reader's browser. State the question, two or three options, the trade-offs, and a recommendation. In trade-offs, a plain `li` is a benefit, `li.cost` a cost and `li.risk` a risk.

### Key-fact tiles and sources

A tile makes one claim and cites one source. The source is either a link to a URL you fetched in this study, or the path of a file you read (in `<code>`, inside `span.fact-src`). Set `data-status="verified"` only if you checked it; otherwise `data-status="unverified"` with the amber pill. Never cite what you did not open.

## Voice

- Short sentences. One idea per paragraph.
- An analogy before a mechanism. Say what it is like, then what it is.
- Define a term the first time it appears on a page, in plain words, in the same sentence or the next. Link it to `glossary.html#term` where it helps.
- No jargon without a gloss. If an engineer needs the detail, put it in a "For engineers" box.
- No hype. No exclamation marks. No "simply", "just", "easily", "seamless", "powerful".
- Say what is not known. Mark unverified claims as unverified, and say what would verify them.
- The name is lowercase: pravo. Say OpenTofu first and Terraform second.
- British spelling. Dates as `22 October 2026`.
- Headings say the point ("Listing is the missing half"), not the topic ("Listing").
- Write for a person who has never used infrastructure as code first, and an engineer second. The engineer finds the detail in the boxes and in `For engineers`.

## Colour semantics

Colour is a language here. Do not use it for decoration.

| Colour | Token | Means | Pill |
|---|---|---|---|
| Green | `--accent`, `--managed` | managed, good, safe, done | managed, good |
| Amber | `--change`, `--attention` | needs attention, in progress, claimed | claimed, warn |
| Red | `--conflict`, `--danger` | gone, danger, impossible | gone, bad |
| Blue-violet | `--key`, `--struct` | structure, discovered, seen | seen, struct |
| Neutral grey | `--text-3`, `--ignored` | ignored, background, unknown | ignored, neutral |
| Warm peach | `--note-*` | a teaching aid (eli5, analogy). No status meaning. | none |

The five resource states, in the order a resource usually moves through them: **seen** (blue-violet, dashed), **declared** (amber, dashed; the CSS class is still `claimed`, the word on the page is always "declared"), **managed** (green, solid), with **ignored** (grey, dotted) and **gone** (red, dotted) as the two ways out of the main path.

Light and dark themes both work. The reader's choice is saved in `localStorage` under `bf-theme`, inside try/catch, and the system preference is the default. Code windows are dark in both themes on purpose. Never hard-code a colour: use the tokens.

## Accessibility and motion

- Every interactive piece works with the keyboard. Tabs use arrow keys. The stepper uses arrow keys, Back and Next.
- Without script the page is a plain document: tabs show both panels, the stepper steps show as a list, details elements still open.
- Everything that moves respects `prefers-reduced-motion`.
- Contrast: use `--text` for body, `--text-2` for secondary, `--text-3` only for labels and metadata.
- Links say where they go. No "click here".

## Check before you hand over

1. Serve the folder: `python3 -m http.server 0` inside `design/brownfield`, then open the page with `?static` for a still view.
2. Look at it at 1440 px, 768 px and 375 px wide, in both themes. Nothing scrolls sideways at 375 px except code windows and tables, which scroll inside their own box.
3. The browser console is empty.
4. Every claim about a provider, a protocol version, OpenTofu or Terraform is either verified in this study from a source you can name, or marked unverified.
5. `pravo` is lowercase everywhere, OpenTofu is named before Terraform, and no competitor product is named.
