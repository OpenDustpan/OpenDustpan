---
version: 1
---

Issue: #1055.

## Problem

A namespace administrator must rotate keys on schedule and re-encrypt after a compromise without loss of access to old sealed content.

## Evidence

- The scope asks for rotation by rewrap on schedule namespace-key.rewrap and full re-encryption as an audited durable operation. Proof: roadmap.step: This part specifies rotation by rewrap on schedule namespace-key.rewrap and full re-encryption as an audited durable operation.
- Issue 1055 asks for an RFC on one wrap for each installation that holds the namespace. Proof: issues[0].body: each installation has its own key-encryption key, and the namespace key record of a replicated namespace holds one wrap for each installation that holds the namespace.
- Item key blobs sealed under an old version must stay readable after a rewrap. Proof: roadmap.step: scenario that item key blobs sealed under an old version stay readable after a rewrap

## Fix boundary

- Add the NEW route POST /api/dustpan/v1/namespaces/{namespace}/key/re-encrypt and the NEW route GET /api/dustpan/v1/namespaces/{namespace}/key.
- Schedule namespace-key.rewrap calls Rewrap for each namespace key version only on the primary installation.
- Rotate makes a new active namespace key version without touching data keys or item keys.
- POST re-encrypt runs full re-encryption as an audited durable operation that re-seals wraps only.
- RFC answers per-installation wraps and old-version reads after rewrap.
- Add the new file docs/rfc/0089-namespace-key-per-installation-wraps.md with per-installation wrap options.

Out of scope:

- Key record shape and management routes from parts 1 and 2.
- Customer key set, change, and clear flows.
- Automatic failover or merge of replicated keys.

## Check

- Add failing store and service tests for Rewrap, RotateNamespaceKey, and old-version reads in pkg/durable/durabletest/durabletest_test.go:35. Check: TestRewrapKeepsOldItemKeysReadable in pkg/durable/durabletest/durabletest_test.go:35 fails on main, passes after.
- Add failing API tests for the NEW route POST re-encrypt as a durable operation with audit entries in pkg/dustpan/context_test.go:746. Check: TestReEncryptRunsAsDurableOperation in pkg/dustpan/context_test.go:746 fails on main, passes after.
- Write the RFC for one wrap for each installation in the new file docs/rfc/0089-namespace-key-per-installation-wraps.md. Check: Add the new file docs/rfc/0089-namespace-key-per-installation-wraps.md with options and owner questions for the NEW RFC.
- Build Rewrap and RotateNamespaceKey on the namespace key record in pkg/namespace/namespace.go. Check: `GET /api/dustpan/v1/namespaces/{namespace}/key` on the NEW route shows new active version after rotate in pkg/namespace/namespace.go.
- Build StartReEncrypt as a durable operation with fence checks and audit entries in pkg/store/postgres/state_close.go:91. Check: The NEW route POST /api/dustpan/v1/namespaces/{namespace}/key/re-encrypt answers 202 with an operation ID in pkg/dustpan/context_test.go:746.
- Keep old versions while a backup record names them in pkg/store/postgres/durable.go:44. Check: Old item key blob opens after rewrap in sqlite and postgres suites in pkg/store/postgres/durable.go:44.
- Wire the NEW routes, actions, audit kinds, and docs page. Check: Route declares namespace:manage-key on the namespace.
- Run usability check and record friction. Check: Persona completes rotate and re-encrypt from the docs page alone.

Depends on:

- add-customer-managed-keys-per-key-record-wrapper
- add-customer-managed-keys-per-key-management-routes
- write-item-key-blobs-no
- `add-customer-managed-keys-per-key-management-routes` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Durable operations port exists for background work. (`pkg/store/postgres/durable.go:44 (func Store.DurableOperations)`)
- Durable helper starts an operation in the store. (`pkg/store/postgres/state_close.go:91 (func startDurableOperation)`)
- In-memory schedule test exists. (`pkg/durable/durabletest/durabletest_test.go:35 (func TestMemSchedules)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 11 (current), 13 (current), 14 (current), 17 (current), 18 (changed), 19 (current), 20 (current), 25 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "The probe runs again at each rewrap. The daily rewrap of decision 17 runs the probe of decision 10 again. When the probe now passes for the random ID, the server keeps the key in use, writes an audit entry in the scope o"
- Quote: "The customer key (owner). A rotation in the KMS makes a new key version with the same key reference. The server needs nothing for it. The schedule `namespace-key.rewrap` calls `Rewrap` for each namespace key version ever"
- Quote: "Data keys and item keys do not change at a rotation. A rotation above them changes their wrap only. A data key of a Pulumi stack never changes, because the Pulumi CLI keeps ciphertexts under it (ADR 23 decision 4)."
- Quote: "**A full re-encryption runs only after a compromise or on request (owner).** A namespace administrator starts it with `POST /api/dustpan/v1/namespaces/{namespace}/key/re-encrypt`, with a fresh MFA step. The operator can"
- Quote: "Entries in the audit scope of the namespace. The server writes an entry for: a set, change or clear of the customer key, each failed check of decision 10, each probe of decision 11 that passes for the random ID, each rot"
- Effect: This change follows the owner decisions: schedule namespace-key.rewrap with probe again, rotation schedule and keep of data keys, full re-encryption only after compromise or on request, and audit entries in the scope of the namespace.
- Conflict: none.

**ADR 23: Secrets and the built-in key-encryption key** (not owner-tagged).

- Decisions that apply: 4 (changed).
- Changed by: ADR 41 changes decisions 2, 3 and 6; ADR 41 extends decision 1; ADR 45 changes decision 6; ADR 47 extends decision 5; ADR 151 extends decision 3; ADR 217 changes decisions 4 and 8; ADR 237 changes decision 2. Chain to the end: ADR 41, ADR 45, ADR 47, ADR 151, ADR 217, ADR 237, ADR 60, ADR 103, ADR 146, ADR 238, ADR 240, ADR 352, ADR 62, ADR 153, ADR 346, ADR 154, ADR 210, ADR 220, ADR 252, ADR 285, ADR 324, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 41, 45, 47, 151, 217, 237.
- Quote: "Wrapper ID. The wrapper ID of the built-in key is `builtin-` and the first 16 hexadecimal characters of HMAC-SHA256, keyed with the key-encryption key, over the fixed text `opendustpan wrapper id v1`. A one-way function makes it, so it does not contain the key. This ADR fixes the form now, so that a later rewrap command can rotate the key-encryption key with no format change. The rotation itself is a follow-up. Data keys never rotate, because their ciphertexts live in the stack config of the users."
- Effect: This change uses the rewrap shape for rotation of the key-encryption key without change to customer keys.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 3 (current), 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The primary installation. Each namespace has one primary installation. It owns the namespace and accepts its writes. The namespace row names it. The installation that creates a namespace is its primary installation until a promotion. Only the primary installation writes the rows and blobs of the namespace. Another installation answers `ErrNotHome`."
- Quote: "Background work runs on the primary installation. This follows from ADR 72 and decision 3: durable operations, schedules and retention of a namespace run only on its primary installation, with the lease of ADR 72 decision 2. That lease is local to one database."
- Effect: Rewrap schedule and re-encryption durable work run only on the primary installation, with no writes from a replica.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "The data key of the unit does not change. The stack data key of ADR 23 stays as it is, for `unit:encrypt`, `unit:decrypt`, the secret values in the stack config and the secret values in each checkpoint. The Pulumi CLI ne"
- Effect: This change keeps item keys unchanged at rotation and wrap only change at re-encryption.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the record shape for a replicated namespace key, and how does a new replica get its wrap?
   - Options: One wrap for each installation that holds the namespace; One wrap by the primary installation only, replicas forward opens; One wrap by a customer global key, same bytes on each installation.
   - Provisional decision (owner review 14 Nov): One wrap for each installation that holds the namespace, made by unwrap on the primary and rewrap on the new replica.
   - Reason: Issue 1055 lists it as not decided in ADR 248 and asks for an RFC with options and open questions for the owner. Benchmark: AWS KMS multi-Region keys: each replica wraps with its own regional key, and the record holds one wrapped copy for each replica.
   - Persona impact: Namespace administrator sees no new step; the docs tell the customer where each wrap lives and the latency of each KMS location. Escape hatch: With no federation, the single-wrap record works with no change. With federation, the RFC names the migration path.
   - Owner-tagged ADRs involved: ADR 217 (their words do not change).

Questions considered and answered:

- Does rotation re-encrypt data keys or item keys? No. ADR 217 decisions 17, 18, and 19 answer it: schedule namespace-key.rewrap calls Rewrap, rotation makes a new namespace key version, data keys and item keys change only in wrap. Full re-encryption re-seals wraps only and never changes Pulumi ciphertexts. The RFC in docs/rfc holds the per-installation choice.
- Is full re-encryption a background task with audit entries? Yes, as an audited durable operation on the primary installation only per ADR 248 decision 14, started by the NEW route POST re-encrypt with a fresh MFA step per ADR 217 decision 20, with audit kind change per ADR 217 decision 25.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
