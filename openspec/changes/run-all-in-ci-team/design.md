## Context

Team CI runs the same Terragrunt command as a laptop against the same per-workspace backend. No discovery job, shim or change order takes part. The server cannot tell a CI run from a laptop run.

## Decisions

1. **No CI-specific server path.** Reuse the per-workspace path of local run-all: one backend block per unit, one lock and one state lineage per workspace. The server adds no CI branch and no run-all logic.
2. **Per-workspace authorization with no CI exception.** Check the stored grant for the named workspace at the point of use. A credential granted on workspace A gives no access on workspace B, including when one CI job carries one access token for many units.

## Structure

No new type. The existing workspace identity stays part of the storage key, so a request for workspace A cannot write state of workspace B.

## Risks

- CI parallelism shows lock contention as a normal per-workspace lock refusal.
- A CI job killed while it holds a lock leaves the lock held; expiry and forced release stay out of scope.
- CI logs interleave output from many units.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service checks the stored grant for the named workspace before any lock, read or write. | A credential granted only on workspace A names workspace B; the service denies it and reads and writes no state. |
| port adapter | The adapter passes the workspace identity unchanged to the service and adds no run-all logic. | Two units name one workspace address; both requests reach one workspace with one lock and one lineage, with no cross-workspace write. |
