# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| Latest  | Yes       |
| Older   | No        |

Only the latest release receives security fixes. Please upgrade before reporting.

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report vulnerabilities privately via [GitHub Security Advisories](https://github.com/be0x74a/kubectl-xctx/security/advisories/new).

Please include:
- A description of the vulnerability and its potential impact
- Steps to reproduce
- Any suggested fix if you have one

You can expect an acknowledgement within 72 hours and a resolution timeline within 14 days for confirmed issues.

## Scope

kubectl-xctx is a thin wrapper around `kubectl`. Its attack surface is limited to:

- Argument parsing and flag handling
- Spawning `kubectl` subprocesses
- Reading `~/.kube/config` (indirectly, via kubectl)

Vulnerabilities in `kubectl` itself should be reported to the [Kubernetes security team](https://kubernetes.io/docs/reference/issues-security/security/).
