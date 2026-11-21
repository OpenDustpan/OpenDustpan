# Architecture review, round 18

Range: `4daa46bceabe..909ee0bd4` on main (80 commits). Sources: CLAUDE.md, `docs/adr/INDEX.md` (current rule column), ADR 12, 35, 272, 362, 363, 389 to 392, `GLOSSARY.md`, RFC 133 to 136.

## Findings

| Issue | Severity | Where | Summary |
|---|---|---|---|
| #3912 | Major | `pkg/dustpan/dustpan.go:1473`, `docs/guides/reading-state-contents.md:93` | The masked-read cursor key is made per process. With two or more instances, page 2 can answer 400. This contradicts `docs/guides/high-availability.md:14` ("Each instance serves any request"). |
| #3913 | Minor | `pkg/stack/masked_cursor.go:39-175`, `pkg/workspace/masked_read.go:58-700` | The signed cursor, the refusal sentinels and the refusal reasons of the masked read are copied in two tool packages. Tool-neutral security code must be in one place. |
| #3914 | Minor | `.golangci.yml:486-492`, `CLAUDE.md:95` | The new adapter `internal/api/state` has no depguard rule that stops an import of a tool adapter, and the layout list omits it. |

## Checked, no finding

- Store move to `internal/store` (7467b06e, 909ee0bd): follows ADR 363 and ADR 272. `pkg/store` keeps only `storetest`. The domain depguard rule denies both `pkg/store` and `internal/store`. `scripts/move-store.sh` stays for the port of open branches (its branch mode), as the archived change says.
- Gate routes at the collection, unit and installation scope (39aa7fa7): the installation write uses the zero fence and `audit.ServerScope`; it is not a write inside a namespace. The unit scope keys its rows on the unit ID (`CHECK level <> 'unit' OR scope_key = 'unit/' || unit_id`). The collection scope key holds the collection name; RFC 136 names this under "Not covered" and RFC 133 covers limits and gates, so no new issue. A collection delete purges its gate rows, and collections have no rename.
- Baseline edit for `git_host_event_receipt_count` (59d95b57): an in-place edit of the baseline is the rule before the first tag (`internal/store/AGENTS.md`).
- Lost-maker revoke after the request (d990b01f): the action keeps its audit entry and a bounded context; the paths with no sweep are documented and tested.
- Group requirements on one job (99271f7a): approver groups match by group ID (`job.Gate.GroupIDs`), following ADR 362 and ADR 391.
- Masked read (94fa3c15): route, e2e step (`e2e/coverage_state.go`) and user guide are present. Performance of the re-decode per page is already #3911.

## Not reviewed in depth

The 50 OpenSpec-only and archive commits were read for ADR conflicts by title and diff stat only.
