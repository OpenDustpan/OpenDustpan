---
version: 1
---

Issue: #1074.

## Problem

Operator keeps each namespace sealed under its own key so one customer key opens one namespace only. This part adds the stored record and the wrap seam that later routes and rotation use.

## Evidence

- Value sealing uses a random 96-bit GCM nonce under the per-scope data key. Proof: pkg/secret/secret.go:292 (func sealValue)
- The stream path derives a per-stream key with HKDF and a random salt. Proof: pkg/secret/stream.go:156 (func Service.SealStream)

## Fix boundary

- Namespace key record with one wrap per version, wrapper IDs, and KMS key version.
- Wrapper port for Wrap and Unwrap with per-namespace identity and context.
- Confused-deputy probe before use and GCM nonce bound note in code comment.

Out of scope:

- Key management routes and new key actions (part 2).
- Rotation by rewrap and audited re-encryption (part 3).
- Namespace key cache tuning and namespace-ID blob prefix (later part).

## Check

- Add failing tests in NEW file `pkg/secret/namespacekey_test.go` for Wrap double wrap, single wrap, per-namespace context, ProbeConfusedDeputy fail-closed, hostile KMS answers, and concurrent wrap. Check: NEW file `pkg/secret/namespacekey_test.go` TestNamespaceKeyWrap fails on main, passes after.
- Record the GCM 2^32 bound and the per-stream HKDF contrast in a comment in `pkg/secret/secret.go`. Check: NEW file `pkg/secret/namespacekey_test.go` TestGCMBoundNote checks the 2^32 bound text in `pkg/secret/secret.go`.
- Add the NEW Wrapper port with Wrap, Unwrap, and ProbeConfusedDeputy in NEW file `pkg/secret/namespacekey.go` and the namespace key record with wrapper IDs and KMS key version. Check: `go test ./pkg/secret -run TestNamespaceKey -count=1` passes.
- Verify wiring, usability check, and `make check` with zero findings; no route, no e2e step, and no docs page in this part. Check: `go test ./pkg/secret -count=1` passes and `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main seals each value with random 96-bit GCM nonce under per-scope data key. (`pkg/secret/secret.go:292 (func sealValue)`)
- Main seals streams with per-stream HKDF key and random salt. (`pkg/secret/stream.go:156 (func Service.SealStream)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 23: Secrets and the built-in key-encryption key** (not owner-tagged).

- Decisions that apply: 8 (changed).
- Changed by: ADR 41 changes decisions 2, 3 and 6; ADR 41 extends decision 1; ADR 45 changes decision 6; ADR 47 extends decision 5; ADR 151 extends decision 3; ADR 217 changes decisions 4 and 8; ADR 237 changes decision 2. Chain to the end: ADR 41, ADR 45, ADR 47, ADR 151, ADR 217, ADR 237, ADR 60, ADR 103, ADR 146, ADR 238, ADR 240, ADR 352, ADR 62, ADR 153, ADR 346, ADR 154, ADR 210, ADR 220, ADR 252, ADR 285, ADR 324, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 41, 45, 47, 151, 217, 237.
- Quote: "Customer-managed keys. RFC 33 builds on the `Wrapper` port as its only seam. A namespace may choose its wrapper later. This ADR does not decide that."
- Effect: Follows the only seam: this part adds the namespace key record and the Wrapper port only, with no route and no rotation.
- Conflict: none.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 9 (current), 10 (current), 13 (current), 14 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "Four levels (owner). From the top:  | Level | One for each | Wraps | Where it is kept | | --- | --- | --- | --- | | Key-encryption key | installation | each namespace key version, the data keys of `@server`, and the sign"
- Quote: "The wrap of a namespace key version. With no customer key, the key-encryption key wraps it. With a customer key, the customer key wraps it, and the key-encryption key wraps the result. So the server needs both keys to op"
- Quote: "One construction for each KMS. The KMS services do not all bind associated data. So each adapter wraps a fresh random 256-bit key with the KMS, and the server seals the namespace key version with AES-256-GCM under that k"
- Quote: "Checks before the change. The server changes nothing until each check passes: - It wraps and opens a test value with the key and the identity of the namespace. - **A confused deputy probe.** Where the KMS policy can test"
- Effect: Follows the four-level hierarchy, double wrap, per-namespace KMS identity, probe before use, and KMS error mapping. Routes, cache tuning, and rotation stay in later parts.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does a namespace key live and how is it wrapped? ADR 23 decision 8 names the Wrapper port as the only seam; ADR 217 decisions 1-3 define the four levels, the double wrap, and the fresh-key plus AES-256-GCM construction because KMS services do not all bind associated data. This part builds that seam only.
- Does the GCM nonce note need a code change? Issue 1074 records the random 96-bit GCM nonce bound near 2^32 values per scope key; this part records the bound and the per-stream HKDF contrast in a comment in pkg/secret/secret.go with no behavior change.
- Who calls the confused-deputy probe in this part? No. Part 1 adds no route and starts no schedule, so the caller of the probe is part 2. Part 1 defines the probe on the port, its fail-closed mapping to ErrUnavailable and ErrRevoked per ADR 217 decisions 13-14, and the start check per ADR 217 decision 4 and ADR 23 decision 3.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
