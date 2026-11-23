---
version: 6
---

## Why

**Persona: developer.** A developer runs the Pulumi CLI or OpenTofu against the server and then needs to see what changed, who changed each resource, what a stack looked like at a past time, and how secrets leave history. Today the ports exist in pkg/history but no store or route serves them.

The user path this change must make work:

1. **Console.** List the history of one stack or workspace with the NEW units/{unit_id}/history route and an access token.
2. **API.** Read the blame of one resource key and a point-in-time view with the NEW units/{unit_id}/history/blame route and the NEW query at.
3. **CLI.** Redact one history item with the NEW units/{unit_id}/history/redact route and review the audit entry.
4. **Docs page.** Read the retention and safe delete rules.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Read history of a unit reference that never existed | Server returns 404 with the NEW error code unit.not-found. Check the spelling of the reference. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Read history without a read grant on the namespace | Server returns 403 with the NEW error code history.denied. Ask a namespace administrator for read access. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Read an item that redaction destroyed | Server returns 410 with the NEW error code history.redacted and metadata only for a direct read. List and point-in-time reads return 200 with redacted true. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Redact with no reason or an unknown reason | Server returns 400 with the NEW error code history.bad-reason. Retry with secret-exposure, personal-data or other. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- Main has a history domain package and a conformance test Proof: pkg/history with TestHistoryConformance at pkg/inventory/inventorytest/mem_test.go:164
- Main has a blame conformance test for workspaces Proof: TestWorkspaceBlameConformance at internal/store/postgres/workspace_blame_test.go:10
- Main refuses a short retention at start Proof: TestNewServerRefusesShortRetention at pkg/dustpan/dustpan_test.go:832
- Main runs a scheduled artifact sweep Proof: TestJobScheduleArtifactSweepRuns at pkg/dustpan/schedules_internal_test.go:538

## What Changes

- SQL adapters for pkg/history in both dialects with fence checks.
- The NEW tool-neutral routes GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/history, GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/history/blame, the NEW query at on the unit read, and the NEW POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/history/redact.
- Retention of job artifacts at 30 days per namespace through the existing job artifact schedule, and Restore in the window through the existing ANY /api/inventory/deleted-units/{}/{}/restore route with its existing action.
- The NEW actions namespace:read-history and namespace:redact-history.
- A docs page and audit entries for Redact, Restore and each retention expiry.

Out of scope:

- job model 100-103
- runners and launcher 130-138
- context graph and sweep 160-165

Depends on:

- 100-103

## Usability

Where the path still costs the person time or surprises them:

- History pages at 100 items, so a long history takes many calls.
- Redacted items show metadata only; the developer reads the redaction record.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The history conformance suite exists. (`pkg/inventory/inventorytest/mem_test.go:164 (func TestHistoryConformance)`)
- The workspace blame conformance suite exists. (`internal/store/postgres/workspace_blame_test.go:10 (func TestWorkspaceBlameConformance)`)
- The server refuses a short retention at start. (`pkg/dustpan/dustpan_test.go:832 (func TestNewServerRefusesShortRetention)`)
- The scheduled artifact sweep runs. (`pkg/dustpan/schedules_internal_test.go:538 (func TestJobScheduleArtifactSweepRuns)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 12 (current), 16 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "Upload. The runner streams the report to the server through the runner protocol, under its claim. The server writes it to the blob store as a stream (ADR 45), encrypted with the data key of the namespace, because tags can hold names and t"
- Effect: No ADR in the pack governs unit history. This change touches no sweep rule. It follows decision 12 as analogy for encrypted blob storage and decision 16 as analogy for scheduled expiry.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: This change runs no tool on the server and no sweep. It stores history and expires stored artifact blobs.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change alter the accepted history domain model? No. The roadmap row says the ports and suites exist and the adapters are missing. This change adds only the adapters and the wiring.
- Which ADR defines the history actions? No ADR in the pack does. ADR 166 decision 19 gives the pattern for actions in the data family. This change adds NEW actions in that family.
- Does redaction need a second approver? No. One writer with an audit entry gives review after use and does not block the cleanup of an exposed secret.
- How does artifact expiry differ from tombstone expiry? Artifact expiry deletes job artifact blobs after 30 days through the job artifact schedule. Tombstone expiry ends restore after 30 days through the deleted-units sweep. The two schedules stay separate.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
