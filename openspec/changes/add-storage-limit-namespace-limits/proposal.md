---
version: 1
---

Issue: #958.

## Problem

An operator sets a fair-use storage cap per namespace so one namespace cannot fill shared storage, and the namespace sees its use against the cap.

## Evidence

- No limits.storageBytes exists on main. Proof: rg -n -F --glob '!*_test.go' 'limits.storageBytes' pkg internal cmd matches 0
- Namespace settings routes exist on main. Proof: ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings in route table or API description

## Fix boundary

- Add limits.storageBytes storage limit per namespace with use read and near-limit record.
- Refuse new Pulumi and OpenTofu state writes above the limit with the tool error shape naming storage limit and numbers.
- Keep deletes and reads working above the limit.

Out of scope:

- billing

## Check

- Add failing unit tests in NEW pkg/namespace/storage_limit_test.go for CheckStorageLimit set-limit, refuse Pulumi and OpenTofu writes, exact boundary, lower-positive-wins, and exempt held lease. Check: `go test ./pkg/namespace/... -run TestStorageLimitRefusal -count=1`
- Add server test in NEW internal/servertest/namespace_storage_limit_test.go for PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token, use read, near-limit record, and per-minute audit. Check: `go test ./internal/servertest/... -run TestNamespaceStorageLimit -count=1`
- Wire installation and namespace limit settings, fenced CheckStorageLimit write-path check with tool error shapes, counter plus background recompute, and use read. Check: PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token returns effective limit and use
- Add e2e step against the real CLIs. Check: e2e step via ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings with an access token then real Pulumi CLI and OpenTofu writes above the limit fail with a shown message naming storage limit with numbers while deletes still work
- Add docs page for the storage limit, numbers in errors, and deletes-reads behavior. Check: `make docs-lint` passes with new storage-limit page
- Run usability check from the docs page, metric check, and full checks. Check: Refusal metric has no namespace label and `make check` with zero findings

Depends on:

- #897

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- E2E drives the real OpenTofu binary from a Go helper. (`e2e/tool.go:110 (func OpenTofu)`)
- Authgate suite holds a case that prunes records above a limit. (`internal/authgate/authgatetest/namelock_records.go:477 (func testPruneEndedAboveLimit)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 220: Isolation between namespaces on shared infrastructure (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 10 (current), 13 (current), 14 (current), 15 (current).
- Changed by: ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1, 2 and 3. Chain to the end: ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 273, 286, 363.
- Quote: "A share of each shared resource. Each of these settings has an installation value, and a namespace value that is only lower: - `rateLimit.requestsPerSecond` and `rateLimit.burst` for the namespace bucket of RFC 68. A request ta"
- Quote: "The state check in the write path. Each write transaction reads the namespace row for its fence (ADR 4). The same read takes the namespace state, so a write in a `suspended` or `disabled` namespace fails in that transaction (AD"
- Quote: "Work in progress is exempt from shares. The calls of an update, a run or a job phase in progress (lease renewal, checkpoints, events, log chunks, the final state write) never wait for a share and never get 429 from a share. The"
- Quote: "Metrics. The metrics of these limits have no namespace label (ADR 32 decision 5). The count of refusals goes to the audit scope of the namespace, one entry for each minute with refusals, so the tenant sees them.  ### Tests that"
- Effect: Follows decision 10 fenced write check, decision 13 installation value with namespace-only-lower share, decision 14 work-in-progress exemption, and decision 15 per-minute audit of refusals.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Effect: Ships the storage-limit docs page in the same pull request as the feature.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the storage limit conflict with ADR 220 isolation or need an RFC? No. ADR 220 decisions 10, 13, 14, 15 answer it: fenced write check, installation value with namespace-only-lower, work-in-progress exemption, per-minute audit.
- Does the change need a docs page? Yes. ADR 214 decision 1 requires it; included in tasks.
- What counts toward namespace use? State, checkpoints, history items, and artifacts named in issue; stored per-namespace byte counter updated in the fenced transaction plus blob sizes; deletes lower the counter; reads never refused.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
