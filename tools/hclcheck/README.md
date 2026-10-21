# hclcheck

`hclcheck` runs the OpenTofu listings of the documentation through `tofu`. It reads every ```` ```hcl ```` fence of the pages under `docs/` and `site/demos/` (the landing page takes its HCL from `site/demos`), writes the blocks of one page into a scratch root in page order, and for each block runs `tofu fmt -check`, `tofu init -backend=false` and `tofu validate` (and `tofu console` when the root holds a `.tfvars` file). Where a listing shows part of a root, it adds the missing declarations in a file named `_hclcheck.tf` and says so in the table. It prints one row per block: page, block, file, status and note.

```sh
go run ./tools/hclcheck                      # docs/ and site/demos/
go run ./tools/hclcheck -j 4 -v docs/migrate # one directory, four pages at a time, failing output in full
go run ./tools/hclcheck -list                # the blocks and the files they are written to; runs nothing
```

`tofu` must be on the path. `tofu init` downloads the providers into a plugin cache (`-cache`, default `$TMPDIR/hclcheck-plugins`), so the first run needs the network and later runs do not. `validate` needs no credentials; `AWS_REGION` is set for the `aws` provider.

| Flag | Meaning |
|---|---|
| `-tofu PATH` | The `tofu` binary. Default `tofu`. |
| `-j N` | Check N pages at a time. `0` means one per CPU. Default 1. |
| `-cache DIR` | Provider plugin cache. |
| `-timeout D` | The longest one `tofu` command may take. Default 10 minutes. |
| `-keep` | Keep the scratch roots and print where they are. |
| `-list` | List the blocks and the files they are written to. |
| `-v` | After the table, print the diagnostics of every failing block. |

Exit status: `0` when every block is valid or fails as it is marked to, `1` when a block fails, `2` when `hclcheck` cannot do its work.

## How a page is assembled

- The file of a block is its `file=` attribute, or the first file name in its `title` (`.tf`, `.tfvars`, `.tf.json`), or `main.tf`. The root of a block is the directory of its file. A title that names two files is an error: write one block for each file.
- A block that names a file again replaces it, as `file=` does elsewhere in the documentation. A title with `lines A-B` (`platform/main.tf, lines 8-41`) appends the block to the file instead.
- The files that earlier blocks of the page wrote to a directory are part of the root of the next block there.
- A block that is marked `expect=error` is removed from the root after it is checked, so that it does not break the blocks after it. A block that fails without the mark stays, and the blocks after it report `carried over` with the first error of the root.
- The completion adds only what the root lacks: `required_providers` for the providers it uses (a root that has its own `required_providers` is left alone), a `variable` for each `var.NAME` that no block declares, a variable-backed `local`, a resource or data source for each address that the root reads but does not declare (with the arguments the resource requires), a base resource for an `override.tf` file, a file for each literal `file("...")`, and a stub module for each local module source that no block wrote. `moved` `from` addresses are not read.

## Statuses

| Status | Meaning |
|---|---|
| `ok` | Valid as written. A note `added providers ...` only lists `required_providers`. |
| `ok+stubs` | Valid once the root was completed. The note lists the stubs. |
| `expected error` | The block is marked `expect=error` and fails in its own file. |
| `skipped` | The block is marked `skip=REASON`. The reason is the note. |
| `FAIL fmt` | `tofu fmt -check` would change the block. |
| `FAIL init`, `FAIL validate`, `FAIL console` | The command failed. The note is the first error. |
| `FAIL assemble` | The block cannot be written into a root: two files in one title, or a module source outside the page. |
| `FAIL validate` with the note `in completion:` | A stand-in that `hclcheck` wrote is not valid, because a resource type needs arguments that the stub table in `complete.go` does not know. Add the type to the table, or mark the block `skip=REASON`. |
| `FAIL validate` with the note `carried over:` | An earlier block of the page left the root invalid. Fix that block first. |

## Markers

A block that is meant to fail, or cannot be checked, carries a marker in its info string or in an HTML comment on the line above the fence. `tools/sitegen` rejects an unknown attribute in an info string, so a page that it renders uses the comment. The comment is invisible on the page, and it stays in the Markdown copy of the page and in `llms-full.txt`.

```markdown
<!-- hclcheck: expect=error match="Missing resource to override" -->
```

| Marker | Meaning |
|---|---|
| `expect=error` | The block must fail in its own file. |
| `match=TEXT` | With `expect=error`: the error must contain `TEXT`. |
| `skip=REASON` | The block is listed and not checked. |

## Statements about OpenTofu

`claims.sh` runs `tofu` and checks the statements about OpenTofu behaviour that the pages make: the order of variable sources, a caller that replaces a module default, `count` against `for_each` when a list is reordered, `override.tf`, workspaces, the exit codes of `plan -detailed-exitcode`, `moved` and `import` blocks, `state rm`, `state push` and its serial check, the one state lock, the message of a failed `validation` block, the keys of `show -json`, the constructs of the construct matrix, sample functions of the function map, and that the function map has a row for each function of `tofu metadata functions`. It prints `ok` or `FAIL` for each check and exits with the number of failures. It needs `tofu`, `jq` and the network for the providers `local` and `random`.

```sh
sh tools/hclcheck/claims.sh
```
