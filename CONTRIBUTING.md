# Contributing to kubectl-xctx

Thank you for your interest in contributing!

## Prerequisites

- Go 1.22+
- `kubectl` on your PATH
- `golangci-lint` for linting (`brew install golangci-lint` or see [install docs](https://golangci-lint.run/usage/install/))

## Local setup

```bash
git clone https://github.com/be0x74a/kubectl-xctx
cd kubectl-xctx
go mod tidy
make build
```

## Running tests

```bash
make test
```

Tests use a mock `kubectlRunner` and do not require a live cluster.

## Linting

```bash
make lint
```

## Vulnerability scan

```bash
make vuln   # requires: go install golang.org/x/vuln/cmd/govulncheck@latest
```

## Making changes

1. Fork the repo and create a branch from `main`
2. Make your changes
3. Add or update tests — `make test` must pass
4. Run `make lint` and fix any issues
5. Open a pull request against `main`

## Commit style

Use conventional commits:

```
feat: add --output-format flag
fix: handle context names with spaces
docs: update README examples
test: add parallel ordering test
```

## Releasing (maintainers only)

```bash
git tag v1.x.x
git push origin v1.x.x
```

GoReleaser handles the rest: builds, archives, GitHub Release, krew-index update, Homebrew tap update.
