# NAME

A pravo module. Use this directory as a template repository.

## Set up

1. Edit `cue.mod/module.cue`: replace `OWNER` and `NAME`.
2. Replace `example/` with your packages. A module is a definition that
   contains resources, written as they are in a root. Take the provider as a
   required input (`provider!:`) and build the resources through it. The
   provider's type is imported through its canonical path, which every
   consumer's `pravo init` generates:

   ```cue
   import awsp "pravo.dev/providers/hashicorp/aws"

   #Bucket: {
   	provider!: awsp.#Provider
   	name:      string
   	bucket:    provider.#Resource.aws_s3_bucket & {bucket: name}
   }
   ```

   A consumer declares the provider and passes it in:
   `$aws: awsp.#Provider & {region: "eu-north-1"}` and
   `logs: pkg.#Bucket & {provider: $aws, name: "logs"}`. The resource is at
   the address `logs.bucket`. A module cannot name a provider struct of its
   consumer's root, which is why the provider is an input. An instance that
   leaves the field out is an error at evaluation: `provider logs.provider was
   not supplied`. A provider field without `!` that the consumer leaves out
   makes a second provider of its own, with no configuration, and nothing
   warns.
3. Commit, tag and push: `git tag v0.1.0 && git push origin v0.1.0`. The
   workflow publishes `ghcr.io/<owner>/cue/github.com/<owner>/NAME`.

Publish by hand with `pravo mod publish v0.1.0` (needs `docker login ghcr.io`
with a token that has `write:packages`).

## Make it public (once)

A first publication is private and GitHub has no API for visibility. Open
`https://github.com/users/<owner>/packages/container/package/cue%2Fgithub.com%2F<owner>%2FNAME`
(or the organisation equivalent), then Package settings, Danger Zone, Change
visibility, Public. It can take a few minutes to show.

## Use it

```bash
pravo mod get github.com/OWNER/NAME@v0.1.0
pravo init
```

Declare in your root a provider struct (`$aws: awsp.#Provider & {...}`) for
every provider whose type your module takes as an input, and pass it to the
module.
