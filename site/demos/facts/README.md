# facts

`facts.go` reads the Terraform state format that `internal/tfstate` accepts, the first Terraform release that writes it, and the provider plugin protocols of `docs/reference/compatibility.md`. It writes `site/data/facts.json` for the migration demo (`site/demos/migrate/embed.html`).

```sh
go run site/demos/facts/facts.go          # rewrite site/data/facts.json, from the module root
go run site/demos/facts/facts.go -check   # fail when the file is not what the sources give
```

`go test ./tools/sitegen` runs the check (`TestFactsAreCurrent`), so a stale file fails the tests.
