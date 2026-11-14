---
version: 1
---

Issue: #1368, #1378.

## Problem

Operator keeps the installation keys safe. Item keys are wrapped under the key-encryption key, but the start-up check ignores their wrapper IDs, the rewrap command skips their blobs, and the interim wrap uses the name v1, which collides with namespace key version 1. A future rotation or restore could then leave a database that no key opens.

## Evidence

- No production path builds the item sealer yet; item keys appear only in tests and the job recorder. Proof: pkg/store/storetest/item_keys_backup.go:245:	blobs, err := secret.NewItemKeyBlobs(rig.Live, itemKeyNoRefusals{})
- The sealer opens item keys through openItemKey. Proof: pkg/secret/itemsealer.go:252:// openItemKey returns the raw item key of ref and the ID of its record.
- CheckWrapper exists on main. Proof: pkg/secret/secret.go:210 (func CheckWrapper)

## Fix boundary

- CheckWrapper reads ListItemKeyWrapperIDs at start-up and refuses to start on an unknown item key wrapper ID.
- The existing key rewrap command rewraps each live item key blob, and a repeat run is safe.
- The interim wrap uses the NEW blob version token v0 under the namespace prefix, so the name differs from v1.

Out of scope:

- Issue #1378 items 1, 2, 4, 5, 6 and 7 (transaction order, pending row order, GetNamespace conformance, fence argument, shred, nits)
- The ADR 217 namespace key layer and KMS adapters
- Step 3 wiring of the sealer into production writes

## Check

- Add a failing start-up test for an item key with an unknown wrapper ID in NEW pkg/secret/itemkey_startcheck_test.go Check: NEW TestCheckWrapperRefusesUnknownItemKeyWrapperID fails on main
- Add a failing rewrap test for live item key blobs in NEW pkg/secret/itemkey_rewrap_test.go Check: NEW TestRewrapItemKeysRewritesLiveBlobs fails on main
- Add a failing blob naming test for the v0 token in NEW pkg/secret/itemkey_blobkey_test.go Check: NEW TestItemKeyBlobKeyUsesV0 fails on main
- Implement the fail-closed CheckWrapper, the repeatable RewrapItemKeys and ItemKeyBlobKey with v0 Check: `go test -race ./pkg/secret/...` passes
- Run the full check gate for the change. Confirmed by: CI (make check) Check: `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks a wrapper at start through CheckWrapper. (`pkg/secret/secret.go:210 (func CheckWrapper)`)
- Main seals history content through ItemKeySealer.SealContent. (`pkg/secret/itemsealer.go:134 (func ItemKeySealer.SealContent)`)
- Main creates item keys through ItemKeySealer.NewItemKey. (`pkg/secret/itemsealer.go:97 (func ItemKeySealer.NewItemKey)`)
- Main lists item key wrapper IDs through the postgres store query. (`pkg/store/postgres/internal/db/queries.sql.go:15390 (func Queries.ListItemKeyWrapperIDs)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 2 (changed), 9 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "The item key is in the blob store, not in SQL. The `Wrapper` wraps the item key, as it wraps a data key. The associated data binds the wrapped key to its scope, its kind and its item ID. The server writes the wrapped key"
- Quote: "Start-up check. The wrapper IDs of the item key records count in the start-up check of ADR 23 decision 3, as the wrapper IDs of data keys do."
- Effect: The start-up check counts item key wrapper IDs. This change makes CheckWrapper read ListItemKeyWrapperIDs and fail closed.
- Conflict: none.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 21 (current), 29 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "A wrapper ID for each level. Each wrapped key records the ID of each wrapper above it and the KMS key version that the KMS answered. A start check (ADR 23 decision 3) refuses to start when the key-encryption key does not"
- Quote: "The key-encryption key. The rewrap command of ADR 23 decision 4 wraps each namespace key version and each sign-in secret again. It does not touch customer keys. A restore of a backup made before a rotation of the key-enc"
- Effect: The start check refuses unknown wrappers and the rewrap covers wrapped keys. The v0 token is the final pre-tag name of this change and needs no conversion because no tag shipped an item key blob.
- Conflict: none.

**ADR 220: Isolation between namespaces on shared infrastructure (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1, 2 and 3. Chain to the end: ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 273, 286, 363.
- Quote: "A prefix for each namespace. Each blob key of a namespace starts with `namespaces/<namespace ID>/`. The item key blobs of ADR 151 go to `namespaces/<namespace ID>/item-keys/<item key ID>/v<version>`. No tag shipped a blob key (ADR 73), so the server keeps no old layout and no conversion."
- Effect: Item key blob keys keep the namespace prefix. The token v0 sits in the v<version> position and ships before any tag, so no old layout is kept.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must start-up fail when an item key uses an unknown wrapper? Yes. ADR 151 decision 9 counts item key wrapper IDs in the start-up check. ADR 217 decision 4 refuses to start on an unknown wrapper.
- Must the key rewrap command rewrap item key blobs? Yes. ADR 217 decision 21 covers wrapped keys in rewrap. Item key blobs are wrapped keys under the key-encryption key.
- Which interim blob name avoids the v1 string? v0. ADR 220 decision 7 defines the position v<version>. This change fixes the exact NEW token v0 before any tag ships, so it is final for pre-tag blobs and needs no conversion.
- Do the #1378 items block this change? No. They are step 3 and conformance work. This change keeps only the three #1368 blockers.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
