## Context

One server binary serves each route and runs each schedule of an installation. One process runs one or more components (`api`, `worker`), chosen by the current `--components` flag; all components run by default. Each process of an installation shares one SQL store, one blob store and one key-encryption key. No change here splits a new role into its own process: the runner need that forces a new role has not arrived (see "Builds on" in `proposal.md`).

## Decisions

1. **Hold one binary with shared storage.** Make no code change. The operator runs one process by default and learns no new flag. A later RFC names the new component when a runner need forces it: a later component gets its own RFC (ADR 249 decision 2).
2. **Keep the lease and write rules across a future split.** A future role split keeps leased durable operations (ADR 72 decision 2) and the write-path installation check (ADR 72 decision 4). Background work of a namespace stays on its primary installation (ADR 248 decision 14). A Kubernetes operator stays out of the core server binary (ADR 305 decision 9).
3. **Add no component value.** The `--components` flag keeps its two values, `api` and `worker`. A process that sets a flag that no selected component reads keeps the current behavior: it ignores the flag and logs that at start (`internal/cli/components.go`).

## Structure

No new file, no new type and no new store. The current `cli.ComponentAPI` and `cli.ComponentWorker` names with `dustpan.ParseComponents` already express which parts one process runs, so no new structure is needed to hold this position. This change adds no port, no endpoint, no stored schema and no CLI output, so it has no Contract section.

## Risks

- A reader mistakes the `api` and `worker` components for separate roles and plans a split that breaks the lease or the write-path check. The decisions above name the rules that a split must keep.
- A future runner need arrives and this hold position blocks it. The proposal names the RFC that then specifies the new role.
