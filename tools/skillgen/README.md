# skillgen

`skillgen` builds the reference files of the pravo skill from the documentation, and checks that the files in the repository are the ones it would build.

```sh
go run ./tools/skillgen                 # write skills/pravo/references
go run ./tools/skillgen -check          # fail when a file is missing, stale or extra
go run ./tools/skillgen -zip OUT.zip -version 0.2.0   # pack the skill for a release
```

## What is written by hand and what is generated

| Path | Source |
|---|---|
| `skills/pravo/SKILL.md` | Written by hand. The short guide that an agent reads first. |
| `skills/pravo/references/INDEX.md` | Generated: every reference file with its description and size. |
| `skills/pravo/references/<section>/<page>.md` | Generated from `docs/<section>/<page>.md`. |

`skills/embed.go` embeds the directory in the binary. `pravo skill install` writes it out. The release workflow packs it as `pravo-skill.zip`.

## What the conversion does

A page of the documentation is plain Markdown plus the extensions of `docs/AUTHORING.md` section 4. The skill needs the content and not the site, so for each page `skillgen`:

- turns the front matter into a heading and a description line, under a comment that says the file is generated;
- removes the attributes of code fences (`title`, `hl`, `ln`, `exit`), and says what the rest meant: `file=NAME` becomes a line "File `NAME`:", an `out` block is introduced by "Output:", `status=conflict` by "Status: conflict.";
- drops the blocks that are `hidden` or `check`, with the output of a hidden block;
- drops `:::predict`, `:::exercise`, `:::check`, `:::widget`, `:::demo` and `:::runbook`, keeps the text of `:::compare`, `:::tabs`, `:::deep-dive`, `:::mapping` and `:::cards`, and replaces a `:::figure` by its caption;
- removes raw HTML and turns an image into its description;
- points a link to a page that is in the skill at that file, and a link to a page that is not at `https://pravo.systems/`.

A container or a fence attribute that the program does not know is an error, so a new construct of the documentation has to be decided on here.

## What is left out

Track A of Learn (CUE from zero), the labs, the project B11 and the section indexes. The list is `skipped` in `main.go`. A pattern that matches no page is an error, so the list cannot go stale. A page that is added to Reference, Concepts, How-to, Learn or Migrate is in the skill from the next run.

## Keeping it current

- `go test ./tools/skillgen` fails when a page of the documentation changed and the skill was not rebuilt (`TestTheSkillInTheRepositoryIsCurrent`). CI runs it in the job that tests the tools.
- `go test ./cmd/pravo` fails when `SKILL.md` names a command or a flag that does not exist (`TestSkillNamesOnlyRealCommandsAndFlags`), and `go run ./tools/skillgen -check` fails when it names a reference file that does not exist.
- A change to `SKILL.md` itself is reviewed like code: it is the part that is not derived.

The front matter of `SKILL.md` needs `name: pravo` and a description of at most 1024 characters. The description decides when an agent loads the skill: it lists the commands and the files that say a directory is about pravo.

Whether the skill helps an agent is a separate question, and `eval/README.md` holds a small evaluation for it: three tasks done with and without the skill, and a script that checks the result with the real binary.
