# Spofy changes to REALITY

Base: `github.com/xtls/reality` `v0.0.0-20261014101318-83f621788ca8` (the version pinned by Xray-core v26.10.31),
copied unmodified in the commit that introduced this directory. Every later change is a separate commit
touching only this directory plus its Xray-side wiring.

## Updating to a newer upstream REALITY

1. `go mod download github.com/xtls/reality@<new>` and copy the module over this directory (keep this file).
2. `git diff` must show only upstream changes plus the patches below; re-apply any that were lost.
3. `go test ./third_party/reality/... ./transport/internet/reality/...` and `spofy/test/e2e.sh reality`.

## Patches

- **requireMlkem (default false).** Upstream (8cdf7bf9, 2026-09-08) rejects every Client Hello without an
  X25519MLKEM768 key share placed before X25519 and forwards it to the target. That breaks clients whose
  uTLS fingerprint has no hybrid share: `edge` (= Edge 85), `ios`, `qq`, older `hello*` forms, sing-box.
  With `RequireMlkem == false` such a hello authenticates through its single X25519 share as before the
  upstream change; hellos that do offer X25519MLKEM768 keep upstream's strict ordering rules.
  `RequireMlkem == true` restores upstream behaviour. Same JSON name and protobuf field number (100) as
  Jolymmiles/Xray-core, so configs are interchangeable.
