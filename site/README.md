# pravo site: design system

Static site: HTML from `tools/sitegen`, one style sheet, four shared scripts and one script for each interactive demo, self-hosted fonts. No framework, no npm at build or run time. Every page is complete without JavaScript; scripts add behaviour only. All links are relative, so the output works from the file system and from a sub-path.

## Files

| Path | Purpose |
|---|---|
| `site.json` | Site configuration read by `tools/sitegen`. `tools/sitegen/README.md` describes it. |
| `landing/NN-name.html` | Bands of the front page, one `<section class="band"><div class="wrap">` per file, in file name order: the hero, kept and revisited, plans and applies, where to start and about. |
| `data/*.json` | Recorded output and values read from the sources. A partial or a demo reads them with `{{data file.path}}` and `{{json file.path}}`. Not copied to the output. |
| `demos/NAME/` | One interactive demo, or the hero of the front page. `NAME/page.md` is a capture page; `build.sh` runs `tools/capture -update`, `-check` and `-json site/data/NAME.json` on it and then a Go program that derives the rest. A demo ends in `NAME/embed.html`, which a page of the documentation embeds with `:::demo NAME`. `demos/facts/facts.go` reads the state format and the plugin protocols from their sources into `data/facts.json`. |
| `assets/style.css` | The built style sheet. Generated; edit the parts under `design/css/`. |
| `assets/site.js` | Theme, header, drawer, heading anchors, tables and figures on phones, copy, tabs, scroll-spy, progress, glossary popovers, reveal, counters, pointer light, search loader. |
| `assets/search.js` | Search palette. Loaded by `site.js` on first use. |
| `assets/timeline.js` | Timeline runner for scripted loops. |
| `assets/widgets.js` | Widget registry and the `unify` and `plan` widgets. |
| `assets/demos/NAME.js` | The script of one demo. Its `embed.html` loads it with `<script defer src="assets/demos/NAME.js">`; the generator prefixes the path to the site root. |
| `assets/fonts/` | Newsreader, IBM Plex Sans, IBM Plex Mono as latin woff2 (OFL). |
| `assets/logo.svg`, `assets/apple-touch-icon.png`, `assets/og.png`, `favicon.ico` | The mark, the touch icon, the preview image and the favicon. `favicon.ico` sits in `site/`, not in `assets/`. The head links the icons when the files exist, and `og:image` when `base_url` is set. `python3 tools/logo/gen.py` regenerates all four from the lattice of the landing page; it needs Google Chrome for the raster files. |
| `design/css/NN-*.css` | Source parts of the style sheet. |
| `design/build.sh` | Concatenates the parts into `assets/style.css`. |

## Build and preview

```sh
site/design/build.sh                 # after editing design/css/*.css
go run ./tools/sitegen               # docs + landing + assets to site/public/, scripts and style sheet minified
go run ./tools/sitegen -minify=false # the same, with the scripts and the style sheet as written
go run ./tools/sitegen/contrast      # contrast check of every colour pair
go test ./tools/sitegen/contrast     # the same check as a test
sh site/demos/hero/build.sh bin/pravo   # re-record one demo or the hero; run from the module root
cd site/public && python3 -m http.server 8080
```

`tools/sitegen/testdata/static/` holds hand-written pages that use every element of the HTML contract: `contract.html` (a lesson), `primitives.html` (the landing and demo primitives), `glossary.html`, and a search index. They load the assets from `site/assets/` by relative path and open from the file system. The HTML contract is `tools/sitegen/HTML.md`: it fixes the markup that the generator writes and that the style sheet and the scripts rely on, and a change to either side changes that file.

### Minification

`tools/sitegen` minifies the JavaScript and the CSS that it copies from `site/assets/` to the output, with `github.com/tdewolff/minify/v2`. The sources are not changed: `assets/style.css` is the file that `design/build.sh` writes, the scripts stay as written, and the pages of `tools/sitegen/testdata/static/` load them. The published copy has no comments or layout whitespace, and the local variables of a script have short names. Names that a page or the browser sees stay as they are: globals, properties, strings, class names and custom properties. The minifier writes a few expressions in an equivalent form: `undefined` becomes `void 0`, `Infinity` becomes `1/0` and `Math.pow(a, b)` becomes `a**b`. A shorthand that sets only initial values is written shorter, so `background: transparent` is published as `background:0 0`.

A file with `.min.` in its name is copied as it is, and so is a file whose minified form is not smaller. A script or style sheet that does not parse fails the build with its path, and nothing is written. `-minify=false` copies every file as written; use it to read the published scripts in the browser's inspector. The HTML that the generator writes is not minified.

`TestMinifiedScriptsKeepTheirNames` and `TestMinifiedStyleSheetKeepsItsNames` in `tools/sitegen` compare the free variables, property names, class selectors, custom properties and at-rules of every file before and after.

## Concept: the statute sheet

The product is about rules, so the page is a sheet with legal-drawing devices. The devices decorate; no concept is renamed.

| Device | Where |
|---|---|
| `§` section marks | Kickers, hanging marks beside `h2`, the `applies` line. |
| `fig. N` captions | Figures and the stage caption, in mono. |
| Hairline ruled columns | `.doc-shell` borders, `.band > .wrap` borders; crosshairs where a column rule meets a row rule. |
| Hatched gutters | Either side of the 84 rem sheet (`body.page`), the deep-dive spine. |
| Corner ticks | Code windows, terminals, figures, widgets, the lesson title block, the stage. |
| Title block | `.lesson-meta`, ruled cells like the block of a drawing. |
| Lattice | The mark; a faint lattice in the lesson head (80 rem and wider); `.lattice` draws it in any colour; rail nodes and beat dots are its nodes. |
| One chamfered corner | `.btn`, set by `--cut`. |

## Tokens

All colour is a custom property on `:root`. Dark values are set on `:root[data-theme="dark"]` and, for the system preference, on `:root:not([data-theme="light"])` inside `prefers-color-scheme: dark`. `tools/sitegen/contrast` reads these blocks, requires the two dark blocks to be identical, and checks 100 pairs (text at 4.5:1, interface at 3:1).

Colour is semantic.

| Meaning | Token | Paper | Ink |
|---|---|---|---|
| Valid, added, links, primary | `--accent` | `#0a735a` | `#3ddbb0` |
| Changed, replaced, predict | `--change` | `#7c5000` | `#f5b94a` |
| Conflict, destroyed, error | `--conflict` | `#b32917` | `#ff6b5e` |
| Language structure, focus | `--key` | `#3347c9` | `#8ea2ff` |

| Token | Use |
|---|---|
| `--bg`, `--surface`, `--surface-2`, `--surface-3` | Stepped surfaces. Depth comes from lightness and hairlines, not shadows. |
| `--line`, `--line-strong` | 1 px rules (translucent). |
| `--text`, `--text-2`, `--text-3` | Primary, secondary, tertiary text; tertiary is at least 4.5:1 on every surface. |
| `--accent-wash`, `--change-wash`, `--conflict-wash`, `--key-wash` | Callout and state backgrounds. |
| `--on-accent` | Text on `--accent`. |
| `--glow` | The halo behind the stage. |

Code windows, terminals and the stage are dark in both themes. Their tokens are not redefined per theme: `--code-bg`, `--code-bar`, `--code-edge`, `--code-text`, `--code-dim`, `--code-add`, `--code-chg`, `--code-del`, `--code-key`, `--code-hl`, and the syntax set `--tok-kw` (structure, blue-violet), `--tok-def` (definitions and providers, violet), `--tok-type`, `--tok-fn`, `--tok-str` (green), `--tok-num`, `--tok-com`, `--tok-op`, `--tok-id`.

| Type | Face |
|---|---|
| `--font-display` | Newsreader 400 and 500, italic 500. `h1`, `h2`, ledes, stat numbers, card titles. Tracking -0.022em, `text-wrap: balance`. |
| `--font-sans` | IBM Plex Sans 400, 400 italic, 500, 600. Body 17 px at 1.68 in documentation. |
| `--font-mono` | IBM Plex Mono 400, 500, 600. Code at 13 px; kickers and labels 11 to 12 px in capitals with 0.08em tracking. |

Layout: `--sheet` 84 rem, `--gutter` 16 to 32 px, `--header-h` 3.5 rem, `--side-w` 16 rem (14.5 rem under 80 rem), `--toc-w` 14.5 rem, `--measure` 41 rem for text blocks, `--r` 3 px. Breakpoints: the table of contents is hidden under 68.75 rem (1100 px); the sidebar becomes a drawer and the header gains a scrolling nav row under 60 rem; compare stacks under 51.25 rem; the header search collapses to an icon under 41 rem.

Until a web font arrives, the display and sans faces fall back to system faces scaled to the same advance width and vertical metrics (`Newsreader Fallback`, `IBM Plex Sans Fallback`), so the swap moves nothing.

Motion: `--ease` `cubic-bezier(.16,1,.3,1)`; `--t-fast` 140 ms; `--t-med` 420 ms.

The latin font subset has no arrows, check marks or mathematical symbols. Icons are masks defined as `--i-check`, `--i-x`, `--i-neq`, `--i-ban`, `--i-bang`, `--i-arrow`, `--i-arrow-l`, `--i-copy`, `--i-search`, `--i-sun`, `--i-moon`, `--i-menu`, `--i-play`, `--i-pause`, `--i-replay`, `--i-chev`, `--i-lattice`; they take the colour of the element.

## Components

Every element of `tools/sitegen/HTML.md` is styled. Notes beyond the contract:

- **Code window** `figure.code`. The title bar has a file tab and a status badge. `data-status` is `valid`, `conflict`, `rejected` (dashed badge, hatched body) or `unintended`; it colours the tab, the left stripe and the badge, and the badge carries a glyph as well as a colour. `data-ln` shows line numbers (the empty `.ln` span is filled by a CSS counter). `.line.hl` highlights; `.line.ins` and `.line.del` mark diffs. A figure without a title bar keeps its first line clear of the copy button, and shows the button on hover or focus (always on touch). Lines are `display: block` spans; a newline inside each span is expected, none between spans.
- **Terminal** `figure.term`. `data-exit` other than 0 turns the tab red; the generator's `.code-badge[data-exit]` shows `exit N`. `data-ran="0.1"` prints `ran on 0.1` in the corner. Output spans: `out-add`, `out-change`, `out-replace`, `out-destroy`, `out-err`, `out-dim`.
- **Copy** `button.copy`. Icon only; any text inside is hidden. Copies `data-copy`, else the commands of a terminal, else the lines of a code window without line numbers or prompts. States: `is-copied`.
- **Compare** `div.compare[data-count]` places two to four windows side by side; the windows stretch to equal height.
- **Tabs** `div.tabs`. Without JavaScript the panels stack, each headed by its `data-title`; with it `site.js` adds `is-ready`, hides the other panels and handles arrow, Home and End keys. A link to an id inside a hidden panel selects its tab; a link into a closed `<details>` opens it.
- **Title block** `.lesson-meta`: ruled cells; `.meta-done` (the "Done when" cell) takes the accent label.
- **Predict, exercise, check, deep dive, callout, fig** as in the contract. Hints carry their tier in `data-tier` (`1` is the nudge, `2` the reference); the solution is marked amber.
- **Glossary** `a.term-link`. Hover and focus show a popover with `data-def`, or the text fetched from the glossary page (not available from `file://`, where the link still works).
- **Progress.** `button.mark-done[data-page]` stores the page in `localStorage` (`pravo-progress`, guarded by try/catch). Sidebar links with `data-page` gain `is-done`; the sidebar of `learn` and `migrate` pages shows a rail with a node per page, a summary box (`n of N done`), and filled nodes for done pages.
- **Header.** Sticky; the hairline appears after 24 px of scroll (scroll-driven animation where supported, the class `is-scrolled` otherwise). A 2 px reading-progress line (`.read-progress`) tracks the page on documentation pages.
- **Search** opens with `/` or Ctrl or Cmd K. See below.
- **Print.** The sidebar, table of contents, header, pager and controls are hidden; code windows print light; links print their addresses; details open.

## Landing and demo primitives

Content is written elsewhere; these classes are the vocabulary. The front page is a few bands. Each band is one file, `site/landing/NN-x.html`: `<section class="band" id="x" data-band="x"><div class="wrap">...</div></section>`, with one `.wrap` per band so the column rules run the full height of the band. The generator warns when the id, `data-band` or `.wrap` is missing. It replaces these tokens in a partial: `{{applies}}`, `{{pravo_version}}`, `{{cue_version}}`, `{{start}}`, `{{start_label}}`, `{{repo}}` and `{{name}}` from `site.json`; `{{go_version}}` and `{{start_minutes}}`, derived from `go.mod` and from the page that `start_href` names; `{{data file.path}}`, which writes a string, number or boolean from `site/data/file.json`, escaped for HTML; and `{{json file.path}}`, which writes any value as JSON that is safe inside `<script type="application/json">`. A path that does not resolve, or a `{{data}}` that names an object or an array, fails the build. A figure that comes from a run keeps its recorded output in `site/data` and the program that records it in `site/demos`.

A demo is a block of HTML that a page embeds with `:::demo NAME` (`tools/sitegen/README.md`). It is `site/demos/NAME/embed.html`, it uses the same tokens, and the generator prefixes the path to the site root onto each relative `href` and `src`. A demo is a `div` with an id, not a band: `#idea`, `#replaces`, `#blast` and `#migrate`. A page that embeds one gives itself `toc: false` when the demo needs the width.

The styles sit in five parts of the style sheet: `91-landing-hero.css` (the front page: hero, where to start) and `95-landing-study.css` (kept and revisited, plans and applies, about), `92-landing-ideas.css` (the demos idea and replaces), `93-landing-blast.css` (blast radius), `94-landing-journey.css` (the migration demo) and `95-demos.css` (a demo in the reading column). `96-components.css` holds the components of the documentation pages: mapping tables, cards, the runbook path and the cause and fix lines. The behaviour of a demo is in `assets/demos/x.js`. Every demo is complete without its script; the script adds the classes that set up its motion, such as `.is-scripted`. The front page has no script of its own.

| Primitive | Markup and behaviour |
|---|---|
| `band-grid` | Add to a band for the 24 px blueprint grid faded by a radial mask. Add `data-light` for the pointer light: a brighter grid revealed around the pointer (one `pointermove` listener, `requestAnimationFrame`-throttled, writes `--mx` and `--my`; off on touch and under reduced motion). |
| `.kicker > .kicker-type` | Typed on view with `steps()` and a block caret. `site.js` sets `--n` from the text length. |
| `.typed` | Inline typewriter for code lines: set `--n` (number of steps, normally the characters of the line; tabs count as one), `--per` (ms per step), add `is-on` to start. The line is revealed by clipping, so it never reflows. |
| `.band-head`, `.band-title`, `.band-lede`, `.display` | Serif headings. |
| `.split` | Two columns, 5:7; `.split-even`, `.split-top`. Stacks under 60 rem. |
| `.stage-wrap > .stage` | The dark window: `.tabs` (file tabs in the bar), panels holding `figure.code` or `figure.term` (their own title bars are hidden; the badge floats), `.stage-foot` (put the controls here), `--stage-h` reserves height. `.stage-caption` below. |
| `.beats > button.beat` | Diamond beat dots; `aria-current="step"`, `is-past`. Built by `tl.controls()`. |
| `.anno`, `.anno-ok` | Annotation chips that name the constraint governing a value. |
| `.btn`, `.btn-primary`, `.btn-quiet`, `.btn-sm`, `.btn-lg` | One chamfered corner: `--cut` sets its size. `.arrow` and `.link-arrow` add the arrow. |
| `.stats > .stat` | `<dl class="stats"><div class="stat"><dt>label</dt><dd data-countup="148">148</dd></div></dl>`. The number counts up on view (900 ms, expo-out); the server-rendered text is the final value; `data-countup-decimals` sets decimals. |
| `.cards > a.card.spot` | `.card-kicker`, `.card-title`, `.card-text`, `.card-more`. `.spot` draws a pointer-following border and wash. |
| `.chip`, `.lattice`, `.row`, `.stack`, `.is-linked`, `.metro-head` | Small helpers. |
| `.reveal`, `[data-reveal]`, `[data-stagger]` | Fade-and-rise on view. `data-stagger` on a parent numbers its children (`--i`, 70 ms each). Content is visible without JavaScript; when the reveal script does not run, `.reveal` shows itself after 2.5 s. |
| `.is-shake`, `.is-pulse` | Effects that compose with an element's own transform. |

## Scripts

All scripts are deferred and independent of load order: `widgets.js` loads `timeline.js` itself when a widget needs it, and `site.js` loads `search.js` on first use. `site.js` adds `js` to `<html>` (the generator's inline head script does it before first paint), finds the site root from `body[data-root]`, and exposes `window.pravoUI` (`selectTab(tabs, i)`, `root`, `el`).

Inline page scripts that use `pravoUI` or `pravoWidgets` run on `DOMContentLoaded`, after the deferred scripts. `pravoUI.timeline(function (T) { T.create(...) })` loads `timeline.js` when a page needs it and no `<script>` tag names it. The script of a demo, `assets/demos/NAME.js`, is named by its `embed.html` and deferred like the others.

### Search

`/` and Ctrl or Cmd K (or the header button) open a `<dialog>` palette. Fuzzy scoring: a substring in the title, a heading, the description or the text; a subsequence in titles and headings only. Up, Down, Home, End, Enter and Escape work; hits on a heading link to its anchor. The index is `search-index.json` at the site root; from `file://`, where `fetch` cannot read it, the palette loads `search-index.js`, which sets `window.PRAVO_SEARCH`. Entry shape:

```json
{"title": "Writing a field twice", "section": "Learn", "group": "Track A · CUE from zero", "lesson": "A3", "minutes": 10,
 "url": "learn/a03-writing-a-field-twice.html", "description": "…",
 "headings": [{"text": "Disagreeing copies", "id": "disagreeing-copies"}], "body": "…"}
```

`group`, `lesson` and `minutes` are present when the page has them. `body` is about 1,200 characters of prose, without code, terminals, hints, solutions or answers. The palette also reads `text` in place of `body`.

### Timeline runner

```js
pravoUI.timeline(function (T) {
  var tl = T.create(root, {
    steps:  [[0, fn], [3000, fn]],        // [ms, fn(cx)], one clock
    beats:  [{t: 0, label: "Write", say: "Sentence for the live region."}],
    length: 24000, loops: 2,
    reset:  function (cx) {},             // render the state before time 0
    live:   liveRegionElement,
    onbeat: function (i, beat) {}
  });
  footer.appendChild(tl.controls());      // pause or play, replay, beat dots
});
```

`cx.instant` is true when a state is being rendered rather than played; `cx.after(ms, fn)` schedules on the same clock; `cx.type(el, text, perChar)` types text (whole text when instant). The loop starts when the root is 40% in view; it pauses when the tab is hidden, the pointer is over the root (mouse only), or the root leaves the view. `seek(i)` renders the end state of beat `i` instantly (transitions off through `[data-seeking]`, running animations finished) and pauses; `replay()` restarts; `pause()`, `play()`, `finish()`, `destroy()`. Under `prefers-reduced-motion` the final state is rendered, nothing starts, and the controls still jump between beats. `data-state` on the root is `idle`, `playing`, `paused` or `ended`. Two loops, then rest on the final frame.

### Widgets

`<div class="widget" data-widget="NAME"><script type="application/json">…</script><noscript>…</noscript></div>`. `pravoWidgets.register(name, function (el, data, cx) {})` defines one; `cx.h(tag, class, children, attrs)` builds elements, `cx.timeline(cb)` loads the runner. A widget with no registered code keeps its `noscript` text. Widgets show recorded results only; they never evaluate CUE.

`unify`:

```json
{"title": "Unification",
 "left":  {"tag": "a type", "label": "int"},
 "right": {"tag": "a range", "label": ">=1 & <=10"},
 "merged": "int & >=1 & <=10",
 "chips": [{"value": "3", "ok": true, "result": "3"},
           {"value": "11", "ok": false, "result": "invalid value 11 (out of bound <=10)"}],
 "note": "optional"}
```

`left` and `right` may be plain strings, and `meet` may stand for `merged`. The cards slide together when the widget is 60% in view, then merge; chips drop into the card; an accepted value pulses green, a rejected value shakes red; the result goes to an `aria-live` region. A button resets.

`plan`:

```json
{"title": "terminal", "command": "pravo plan", "exit": 0,
 "lines": [{"k": "add", "t": "+ local_file.hello"}, {"k": "dim", "t": "    filename = \"hello.txt\""}],
 "summary": {"k": "", "t": "Plan: 1 to add, 0 to change, 0 to destroy."}, "speed": 90}
```

`k` is `add`, `change`, `replace`, `destroy`, `err`, `dim` or empty. The command types at 35 ms per character, the lines stream at `speed` ms per line, and the controls are those of the timeline runner.

## Motion rules

1. Everything is visible by default; scripts add motion.
2. One easing for entrances, `--ease`. Micro-interactions 100 to 180 ms; reveals 400 to 900 ms; typing 25 to 40 ms per character; plan lines 90 ms.
3. Stagger with the custom property `--i`.
4. Meaning never lives only in motion: each animated demo has a text alternative and a static end state.
5. Every loop has pause on hover, a replay button, a beat selector, a reduced-motion final frame, and stops when the tab is hidden.
6. Animate `transform` and `opacity`. The exceptions are the typed reveal of kickers (width, `steps()`), the clip reveal of typed code lines, and the one height collapse a page may use.
7. A changed line flashes (10% accent fading over 600 ms).
8. One signature effect per band.
9. `prefers-reduced-motion: reduce` removes transitions and animations, shows every reveal, renders timelines and counters in their final state, stops the marquee, and disables the pointer light and the cross-document view transition.

Cross-document view transitions (`@view-transition`, 120 ms) cover navigation; the header and sidebar keep their own names and do not animate.

## Accessibility

Focus rings are 2 px `--key` with a 2 px offset; the chamfered button keeps its outline rectangular. Every demo works from the keyboard. Tab order follows the document. Colour is never the only carrier: badges, glyphs and labels accompany it. Tap targets are at least 44 px on touch devices. `forced-colors` replaces the layered button shape with a border.

## Sizes

The landing page loads `index.html`, the style sheet, one script and three images. Fonts are separate. `search.js` loads when the reader opens the palette and is not counted. Sizes are in bytes. Gzipped is each file on its own at the highest level, as `compress/gzip` writes it; the `gzip -9` command gives figures within 1 percent of these. An image counts as the smaller of the file and its gzip.

| Asset | Published | Gzipped | Source gzipped |
|---|---|---|---|
| `index.html` | 15,125 | 4,122 | 4,122 |
| `assets/style.css` | 125,580 | 24,938 | 32,874 |
| `assets/site.js` | 16,590 | 5,688 | 8,188 |
| `assets/logo.svg` | 639 | 361 | 361 |
| `assets/apple-touch-icon.png` | 3,950 | 3,578 | 3,578 |
| `favicon.ico` | 4,509 | 3,224 | 3,224 |
| JavaScript, 1 file | 16,590 | 5,688 | 8,188 |
| Everything but fonts, 6 files | 166,393 | 41,911 | 52,347 |
| Budget, JavaScript | | 8,000 | 8,000 |
| Budget, everything but fonts | | 50,000 | 50,000 |

Published is the file in `site/public/`; source gzipped is the same build with `-minify=false`. `index.html` is not minified.

`TestLandingPageBudget` in `tools/sitegen` builds the site, finds what `index.html` loads and fails when the JavaScript exceeds the first budget or everything but fonts exceeds the second. `go test ./tools/sitegen -run TestLandingPageBudget -v` prints this table for the published build and for `-minify=false`.

A documentation page loads the style sheet, `site.js` and, when it has a widget, `widgets.js`; `timeline.js` and `search.js` load on demand. A lesson page is about 40 KB (8 KB gzipped). A page that embeds a demo carries the demo: the page of the blast radius demo is about 125 KB. The head preloads the faces named in the `preload` list of `site.json`.
