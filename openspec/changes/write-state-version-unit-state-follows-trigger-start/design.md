## Context

OpenTofu final state versions already exist in the job store. The OpenTofu writer is the single sender of upstream events in this slice. Dispatcher.Upstream fans out to followers of the source unit.

## Decisions

1. **One verified version fans out to one plan per follower.** Dispatcher.Upstream takes a final state version, verifies it against the job store, then starts one plan per matching follows trigger with when applied, with an idempotency key per trigger and version, and records per-trigger errors without stopping other followers.
2. **Saver check at each read, not at start.** The job carries the trigger ID; the output read handler resolves the last saver of that trigger at each read and checks unit:read-outputs plus unit:read-secrets against current grants, with deny on missing, deleted, or disabled saver. The start itself runs as the namespace service principal under its stored plan-start grant.

## Structure

Make missed or duplicate delivery impossible by structure: unexported followsCursor with constructor newFollowsCursor that loads the last fired final version per trigger and only yields versions after it; Upstream writes the cursor in the same transaction as the job start, so a retry replays the same idempotency key and starts nothing new.

## Risks

- A missed event leaves a follower behind until the next apply; the refire path covers it.
- A stored trigger row that no longer validates must start nothing.
