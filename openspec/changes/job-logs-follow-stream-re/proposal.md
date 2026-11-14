---
version: 1
---

Issue: #2531.

## Problem

A follow stream of a long growing job log re-reads the whole stored log on each poll. This wastes read work on every poll.

## Evidence

- The job domain package exists on main. Proof: paths: pkg/job exists true
- Main has chunk key coverage in the job package. Proof: main: TestChunkBlobKey at pkg/job/chunkkey_test.go:11
- The issue reports the O(n) cost per poll and asks for an offset reader with a conformance case in each store. Proof: issue #2531 body: Add a reader in pkg/job that reads from a byte offset (or a chunk index) so that each poll reads only the new part, with a conformance case in each store.

## Fix boundary

- Add a NEW offset reader in pkg/job that reads a stored job log from a byte offset across stored chunks.
- Add the NEW offset query value on the follow poll so each poll reads only the part after the last sent offset.
- Add one NEW conformance case in each store for reads from an offset, including zero bytes read before the offset.
- Add the NEW error code `job-log.invalid-offset`, answered as HTTP 400 with the standard error body, for a negative, non-numeric, overflowing, past-end or stale offset.

Out of scope:

- Change of the log write path
- Change of log retention or redaction rules

## Check

- Write the failing conformance case in pkg/job/jobtest for reads from an offset, run by each store adapter. Check: NEW test TestFollowReadsFromByteOffset fails on main
- Add the new file pkg/job/offset_test.go with hostile cases for negative, non-numeric, overflowing, past-end and stale offsets. Check: NEW test TestOffsetCursorRefusesBadInput fails on main
- Add the new file pkg/job/offsetcursor.go for the offset reader and implement it in each store. Check: TestFollowReadsFromByteOffset passes with zero bytes read before the offset
- Add the new file internal/api/jobs/logs_offset.go to wire the offset and generation query values into the job log follow handler, on the existing job log read route with the job log read gate and an access token. Check: NEW test TestFollowPollOffset in internal/api/jobs passes
- Write the docs page for job log follow with the offset query and the invalid offset error. Check: make docs-lint passes
- Run an e2e step with the real CLI against a growing log on route GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}/logs/{name} with an access token through the job log read gate; BLOCKED by NEW offset query support on that route. Check: make e2e passes
- Run the full check and confirm the usability path from the docs page. Check: `make check` with zero findings

Depends on:

- `review-follow-ups-october-jobs` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has chunk blob key tests in pkg/job. (`pkg/job/chunkkey_test.go:11 (func TestChunkBlobKey)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Run an e2e step with the real CLI against a growing log on r" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Run the full check and confirm the usability path from the d" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 2 more.

Questions considered and answered:

- Does any ADR decide how job logs are read? No ADR appears in the pack. The pack lists no ADRs and no RFCs.
- Byte offset or chunk index for the follow cursor? Byte offset wins because it stays stable if chunk sizes change.
- How does the poll guard a log that was truncated and regrew past the old offset? The cursor holds the byte offset and the log generation. A mismatched generation is refused with job-log.invalid-offset.
- Which authorization applies to the follow poll? The poll keeps the action and object of the existing job log read route. The offset adds no new action.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
