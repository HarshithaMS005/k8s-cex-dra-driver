# Contributing

Contributions have to be submitted under the Apache License, Version 2.0.
See also the [LICENSE](./LICENSE) file.

## Developer's Certificate of Origin

When making contributions to this project, you certify the [Developer Certificate of Origin](https://developercertificate.org/).
Sign off every commit (`git commit -s`).

## Submitting changes

Create GitHub pull requests to contribute changes to this project.
Create separate pull requests for each logical enhancement, feature, or problem fix.

You can use GitHub Issues to report problems.

Commit messages follow [`.gitmessage`](./.gitmessage), which carries the format and the sources it derives from.
You can install it as your commit template to get the rules in your editor:

    git config commit.template .gitmessage

## Development environment

The tasks in this file run through make targets, and make uses the tools on your `PATH`.
You do not need Nix to contribute.
Go alone builds the driver, and each check target names the tool it needs in the table under [Checks](#checks).

Nix is offered for reproducibility, not as a requirement.
[`shell.nix`](./shell.nix) pins the whole toolchain, so every contributor and the CI pipeline run the same versions:

    nix-shell                     # enter a shell with every tool
    nix-shell --run "make check"  # or run a single command in it

Any command in this file works either way.

## Building

The driver targets linux/s390x and builds with `CGO_ENABLED=0`.

    make            # build the kubelet plugin
    make install    # install it to $PREFIX/bin

Go code follows [Go Style Decisions](https://google.github.io/styleguide/go/decisions) and [Common comments for Code Review](https://go.dev/wiki/CodeReviewComments).

## Checks

`make check` is the local gate, and it needs only the tools the table names.
`nix flake check` is the acceptance gate, and it runs the same checks against pinned tool versions.
Its checks are defined in [`checks.nix`](./checks.nix), and each one shells out to a make target, so the tool flags live in one place.
`nix-build checks.nix -A all` is equivalent for a non-flake checkout.

`make check` runs that set plus the one the flake sandbox cannot host:

| Target            | What it checks                                                   | In `nix flake check`  |
| ----------------- | ---------------------------------------------------------------- | --------------------- |
| `fmt-check`       | `gofmt`, `goimports`                                             | yes                   |
| `lint`            | `golangci-lint`                                                  | yes                   |
| `test`            | `go test -race -cover`                                           | yes                   |
| `manifests-check` | every Kustomize overlay renders                                  | yes                   |
| `bash-check`      | `bash -n`, `shellcheck`, `shfmt` over the `deploy/` scripts      | yes                   |
| `md-fmt-check`    | prettier over `docs/`                                            | yes                   |
| `vulncheck`       | `govulncheck`                                                    | no, needs the network |

`make fmt` and `make md-fmt` write the corresponding fixes.

Targets prefixed `ci-` produce artifact files and machine-readable output for a pipeline.
They are not meant for local use.

## Documentation

The documentation set lives under [`docs/`](./docs/_index.md), which holds it and nothing else.
Every page has to read correctly as plain markdown, because the markdown is the deliverable.
That means relative `.md` links, no renderer-specific syntax, and hand-authored navigation on the section index pages.

Prose is written one sentence per line.
`make md-fmt` normalizes the rest (lists, tables, spacing) and preserves those line breaks.

Two pages carry generated files rather than prose.
The architecture diagrams under `docs/architecture/diagrams/` are committed SVG, referenced as plain markdown images.
`docs/usage/claim-generator.html` is a self-contained page: opening it from a checkout in a browser gives the complete generator, with no toolchain and no network.
Both are regenerated from their sources rather than edited, so report a problem with either through an issue.

## Repository layout

| Path                | Contents                                                    |
| ------------------- | ----------------------------------------------------------- |
| `cmd/`              | the kubelet plugin binary                                   |
| `internal/`         | AP scanning, device state, mdev handling, the DRA plugin    |
| `deploy/kustomize/` | deployment manifests: base, components, overlays            |
| `docs/`             | the documentation set                                       |
| `vendor/`           | committed Go dependencies, so the build resolves offline    |
