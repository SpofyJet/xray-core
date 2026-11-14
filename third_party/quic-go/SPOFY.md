# Spofy changes to quic-go

Base: `github.com/apernet/quic-go` `v0.65.1-0.20261109005002-2895f039137a` (commit 2895f039, branch
`v0.65.0-mod-rename`: upstream quic-go v0.65.0 + the Hysteria fork modifications), copied unmodified in
9aa52e09. Every later change is a separate commit touching only this directory (plus its test).

Two test fixtures are not in git because of the repository `.gitignore` (`*.key`):
`internal/testdata/priv.key` and `http3/internal/testdata/priv.key`. Tests that need a TLS certificate
(`http3`, `integrationtests`) take them from the module cache copy:
`go mod download github.com/apernet/quic-go@v0.65.1-0.20261109005002-2895f039137a`.

## Updating to a newer apernet/quic-go

1. `go mod download github.com/apernet/quic-go@<new>` and copy the module over this directory (keep this file).
2. `git diff` must show only upstream changes plus the patches below; drop a patch once upstream has it.
3. `go test . ./internal/...` inside this directory, `go test ./transport/internet/hysteria/...` in the
   repository root, then `spofy/test/run-e2e.sh <dir> hysteria`.

## Patches
