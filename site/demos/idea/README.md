# Demos built by mk.go: hero, idea

Three capture pages record the runs behind the hero of the front page and two demos. Every output, count and number comes from a run of the real binary. Nothing is typed by hand.

| Page | Output | Recorded |
|---|---|---|
| `hero/page.md` | `site/landing/00-hero.html` | A CUE file with a typed service definition, two instances and the output of `pravo plan`. |
| `idea/page.md` | `site/demos/idea/embed.html` | Four values written against `int & >=1 & <=10`, each evaluated with `pravo eval`. A page embeds it with `:::demo idea`. |

## Files

| File | Purpose |
|---|---|
| `NAME/page.md` | The capture page. Attributes `id` and `beats` on a block are read by `mk.go`; `tools/capture` ignores them. |
| `NAME/partial.html` | The template. `{{fig ID}}` and `{{win SIDE BEAT}}` are filled by `mk.go`; `{{data ...}}` and `{{json ...}}` are filled by `tools/sitegen`. |
| `idea/mk.go` | Adds the derived keys to `site/data/NAME.json` and writes the output from the template. Run it from the module root: `go run site/demos/idea/mk.go hero|idea`. |
| `idea/build.sh` | Runs `tools/capture` (update, check, json) and `mk.go` for `idea`. `hero/build.sh` does the same for the hero. |
| `site/data/NAME.json` | The output of `capture -json` plus the derived keys. |
| `site/assets/demos/idea.js` | The scripts of the demos. |
| `site/design/css/92-landing-ideas.css` | The styles of the demos. |

Edit `partial.html`, never the generated file.

## What `mk.go` derives

| Demo | Key | Source |
|---|---|---|
| hero | none | The figures of the blocks `main` and `plan`. |
| idea | `widget`, `runs`, `constraint` | The `size:` lines of the blocks and the recorded output of each run, in the shape of the `unify` widget. |

The code windows and terminals are the figures that `tools/sitegen` renders for the blocks of the page, so the highlighting is the generator's own. `mk.go` runs it in a private directory.
