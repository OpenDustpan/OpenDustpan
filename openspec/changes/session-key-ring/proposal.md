---
version: 1
---

Issue: #3257.

## Why

Persona: an administrator who runs an installation with more than one instance, and a developer whose destroy approval needs a fresh step (issue 3257).

The server must seal the cookies of a console session (ADR 297 decisions 9 to 11). On main only the ports exist: `session.Sealer`, `session.StateSealer` and `session.KeyStore` (pkg/session/cookie.go:378-455). No code implements them, and no SQL table holds a session key version. So no cookie can be sealed, and the fresh step of a person has no place to live (issue 3257, question 2: a sealed cookie). This change makes the key ring and the sealer. The change `session-fresh-cookie` uses them.

The path after this change:

1. The server starts and finds no key ring. It makes the first version and writes an audit entry.
2. An administrator reads the ring with `GET /api/server/session-keys`: the key IDs, the times and the state of each version, never a key.
3. A version seals after its `SealsFrom`, rotates each 30 days (`session.keyRotationDays`) and on `retireAll`.
4. Errors: a version that does not unwrap stops the start with an error that names the key ID and the wrapper. A cookie of another installation never opens.

## Evidence

- `rg -n 'SealAccess\(|OpenAccess\(' --glob '*.go'` finds only the interface lines in pkg/session/cookie.go on main (16 Nov 2026).
- `rg -n 'session_keys' pkg/store --glob '*.sql'` prints nothing on main.
- `secret.Wrapper` (pkg/secret/secret.go:128) wraps a key with the key-encryption key (ADR 217).

## Fix boundary

- A table `session_keys` in the baseline of each dialect, and the adapters of `session.KeyStore` with the conformance suite.
- The implementation of `session.Sealer` and `session.StateSealer`: AES-256-GCM with the associated data of ADR 297 decision 9, the in-memory ring, the reload and the seal count.
- The rotation (decision 11): each 30 days, at the seal count, and `retireAll`. The setting `session.keyRotationDays`.
- A read route of the ring without keys.

## Files

- `pkg/session/sealer.go` (NEW), `pkg/session/keyring.go` (NEW) and their tests
- `pkg/store/sqlite/migrations/`, `pkg/store/postgres/migrations/`, `pkg/store/sqlite/queries.sql`, `pkg/store/postgres/queries.sql`
- `pkg/store/sqlite/sessionkeys.go` (NEW), `pkg/store/postgres/sessionkeys.go` (NEW), `pkg/session/sessiontest/cookie.go`
- `internal/api/server/sessionkeys.go` (NEW), `pkg/setting` (the setting), `pkg/dustpan` (wiring), `docs/guides/`

## Check

- `go test ./pkg/session/... ./pkg/store/... -count=1`
- `make e2e` with a start of two servers on one database

## What main already does

- The ports and the claim types exist, with the conformance suite as a list of names (pkg/session/sessiontest/cookie.go:32).
- `secret.Wrapper` wraps and unwraps a key with its ID.
- The backup leaves the ring out by design: `KeyStore` says a backup never holds a key version.

## ADR check

- ADR 297 (owner direction, 31 October 2026) decision 9: "The seal is AES-256-GCM from the Go standard library, with a random 96-bit nonce." Decision 10: "A session key version is a random 256-bit key, with an ID with the prefix `sk-`. The key-encryption key wraps each version (ADR 217)." Decision 11: rotation in two phases. This change implements them and changes none.
- ADR 217: no key comes from another key. Each version is random.
- ADR 250: the seal, the ring and the route each have their own hostile tests.

## Open questions

Open questions: none.

Considered and answered:

- Is a session key a secret in a backup? No. ADR 297 decision 11: a backup holds no version and a restore makes a new ring.
- Does this change issue a cookie? No. `session-fresh-cookie` does.
