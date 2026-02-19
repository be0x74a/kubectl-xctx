# kubectl-xctx

[![CI](https://github.com/be0x74a/kubectl-xctx/actions/workflows/ci.yml/badge.svg)](https://github.com/be0x74a/kubectl-xctx/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/be0x74a/kubectl-xctx)](https://github.com/be0x74a/kubectl-xctx/releases/latest)
[![License](https://img.shields.io/github/license/be0x74a/kubectl-xctx)](LICENSE)

A kubectl plugin to execute commands across multiple Kubernetes contexts matching a regular expression.

![demo](demo.gif)

## Install via krew

```bash
kubectl krew index add be0x74a https://github.com/be0x74a/krew-index
kubectl krew install be0x74a/xctx
```

Or directly from the manifest:

```bash
kubectl krew install --manifest-url=https://raw.githubusercontent.com/be0x74a/krew-index/main/plugins/xctx.yaml
```

## Usage

```
kubectl xctx [flags] <pattern> <kubectl args...>
```

xctx flags must come before the pattern. Everything after the pattern is passed directly to kubectl.

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--parallel` | `-p` | false | Run across all contexts concurrently |
| `--list` | `-l` | false | List matching contexts without executing |
| `--timeout` | `-t` | 0 | Per-context timeout (e.g. `10s`, `1m`). 0 = no timeout |
| `--fail-fast` | | false | Stop after first failure (sequential mode only) |
| `--header` | | `### Context: {context}` | Header template. Use `{context}` as placeholder, `""` to suppress |
| `--version` | | | Print version |

### Examples

```bash
# Get pods in all contexts matching "prod"
kubectl xctx "prod" get pods

# Get pods in a specific namespace
kubectl xctx "prod" get pods -n kube-system

# Get nodes across staging and dev contexts, in parallel
kubectl xctx --parallel "staging|dev" get nodes

# List which contexts would be selected
kubectl xctx --list "prod"

# Run with a per-context timeout (skip unreachable clusters)
kubectl xctx --timeout 10s "." get pods -n kube-system

# Stop immediately on first failure
kubectl xctx --fail-fast "prod" apply -f deployment.yaml

# Suppress headers (useful for piping)
kubectl xctx --header "" "prod" get pods -o json | jq .
```

### Output

Each context's output is grouped under a labeled header:

```
### Context: prod-us-east-1
NAME                    READY   STATUS    RESTARTS   AGE
my-app-abc123-xyz       1/1     Running   0          2d

### Context: prod-eu-west-1
NAME                    READY   STATUS    RESTARTS   AGE
my-app-def456-uvw       1/1     Running   0          2d
```

## Build from source

```bash
git clone https://github.com/be0x74a/kubectl-xctx
cd kubectl-xctx
go build -o kubectl-xctx .
```

## License

MIT
