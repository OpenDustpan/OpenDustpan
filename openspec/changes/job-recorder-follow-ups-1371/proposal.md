---
version: 1
---

Issue: #1377.

## Problem

A runner operator ends steps with large logs. A hash error on the log read shows only once, and a restore does not check the chunk rows.

## Evidence

- The recorder ends a step in the runner session handler. Proof: internal/api/runnerapi/session.go:529:	if err = h.recorder.EndStepChecked(ctx, sp.claim, rec, want); err != nil {
- The log is read by the type logReader. Proof: pkg/job/artifactreader.go:269:type logReader struct {
- A chunk row checker exists in the job store. Proof: pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow)

## Fix boundary

- logReader keeps the hash error sticky across Read and Close calls
- The restore check of a chunk row checks the sha256 format and the job and namespace agreement

Out of scope:

- Incremental hash of the log in EndStep: needs a design of its own
- Backup copy of plan and log blobs
- Sweep and limit of reserved plans: needs an owner decision on the limit and the retention value
- One clock for the recorder
- namespace_id indexes on the three tables

## Check

- Add TestLogReaderCloseIsSticky in pkg/job/artifactreader_test.go (NEW file). Check: `go test ./pkg/job/ -run TestLogReaderCloseIsSticky -count=1` fails on main
- Add TestCheckChunkRowRefusesBadRows in pkg/store/internal/jobstore/record_test.go (NEW file). Check: `go test ./pkg/store/internal/jobstore/ -run TestCheckChunkRowRefusesBadRows -count=1` fails on main
- Make the hash error sticky in pkg/job/artifactreader.go:294 (func Read). Check: `go test ./pkg/job/ -count=1`
- Check the sha256 format and the job and namespace agreement in pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow) by extending the shared helper from follow-ups-1431-review-one after it lands. Check: `go test ./pkg/store/internal/jobstore/ -count=1`
- Check the restore path through ANY /api/server/backups/check with an access token. Check: `go test ./pkg/backup/ -run TestCheckRestoreMissingBlob -count=1` through ANY /api/server/backups/check with an access token
- Run the docs and usability check for the restore error. A person's step. Check: `make check` with zero findings

Depends on:

- follow-ups-1431-review-one
- `follow-ups-1431-review-one` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The job package has a logReader type that reads log chunks in order. (`pkg/job/artifactreader.go:269 (func openLogVersion)`)
- The job store checks chunk rows. (`pkg/store/internal/jobstore/record.go:772 (func CheckChunkRow)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the logReader error or the restore row checks? The pack lists no ADR, so none does. Both are code fixes.
- Should the reserve limit and the retention value be set here? No. They need numbers and an effective value in the API, so they stay out of scope in a later change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
