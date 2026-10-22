# pravo brownfield design

This folder is a design for discussion. It describes one way pravo could find the cloud resources that already exist (the shadow estate), remember them without managing them, and help a team adopt them one at a time. It is explained for a total newcomer first and for engineers second.

Nothing here is implemented. There is no code, no command in the pages exists yet, and every sample output is a sample. The names of commands and flags are proposals.

## How to view it

It is a static site with no build step and no network dependency. From this directory:

```
python3 -m http.server 0
```

The command prints the port it chose; open `http://localhost:PORT/` in a browser. Opening `index.html` directly from disk also works. Add `?static` to any page address to switch off motion and reveals.

## Pages

| File | What it is |
|---|---|
| `index.html` | Start here: the toy-room story, the map, the design in five verbs |
| `01-the-problem.html` | What brownfield means and why today's ways are painful |
| `02-the-idea.html` | The life of one object, and the rule that pravo never touches what it was not given |
| `03-a-worked-example.html` | One account, start to finish, by hand and with an agent |
| `04-how-it-works.html` | The moving parts: ledger, rules, gate, commands, agent layer, cost |
| `05-what-providers-can-do.html` | What the provider plugin protocol offers, and what was probed on this machine |
| `06-where-it-gets-messy.html` | What is impossible, impractical or unknown |
| `07-decisions-and-roadmap.html` | The open decisions, the phases, and what the review changed |
| `glossary.html` | Every hard word in plain language |
| `_components.html` | The catalogue of page pieces (not in the navigation) |

## Source of truth

`DESIGN.md` is the source of truth. The site is a reading of it. Where the two disagree, `DESIGN.md` wins and the site should be fixed. `CONVENTIONS.md` says how to write and add pages. The page list lives in the `PAGES` array at the top of `assets/site.js`.

## Status

Draft of 22 October 2026. The design has been reviewed once by a newcomer, a skeptic and a visual checker, and fixed once; the weak points that remain are named in chapter 6 of the site and in the design's own list of what is not solved.
