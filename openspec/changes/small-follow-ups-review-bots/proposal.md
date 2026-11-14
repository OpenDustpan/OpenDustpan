---
version: 1
---

Issue: #1426.

## Problem

A caller that opens sealed item content but never reads keeps the raw item key in memory until garbage collection, and the job recorder conformance harness reads wall-clock time so record times can only be asserted relatively.

## Evidence

- The issue reports that OpenContent keeps the raw item key until the reader reads the header. Proof: Issue #1426 item 1 names pkg/secret/itemsealer.go and openStreamWith in pkg/secret/stream.go and states the reader clears the key only after it reads the stream header.
- The open port already returns io.ReadCloser on main. Proof: pkg/secret/itemkey.go:251: OpenContent(ctx context.Context, ref ItemRef, binding []byte, r io.Reader) (io.ReadCloser, error)
- The touched files exist at the named paths. Proof: Pack paths: pkg/secret/itemsealer.go, pkg/secret/stream.go, pkg/job/jobtest/recorder.go and pkg/job/jobtest/recorder_keytx.go all exist.
- The issue reports that the job recorder conformance harness uses time.Now. Proof: Issue #1426 item 2 names pkg/job/jobtest/recorder.go and pkg/job/jobtest/recorder_keytx.go and states assertions today are relative so no case is flaky now.

## Fix boundary

- Clear the opened item key when its reader closes or its context ends, even when the caller never reads the header.
- Give the job recorder conformance harness an injectable fixed clock for sealer, recorder and record times with exact assertions.

Out of scope:

- Random test key-encryption key in storetest/item_keys_backup.go (issue marks not real).
- CreateItemKey returning the stored record with ErrShredded (issue marks documented contract).
- Error text naming the caller-given item reference (issue marks not secrets).
- Fix-forward work of #1431.
- Stepped clock; only a fixed clock is in scope.

## Check

- Add a test in NEW pkg/secret/open_clear_test.go: close without read fires the clear hook once. Check: TestOpenContentCloseWithoutReadClearsKey fails on main, passes after the fix.
- Add a test in NEW pkg/secret/open_clear_test.go: close twice is idempotent, close after full read clears, and read after close fails closed. Check: TestOpenContentCloseTwiceAndReadAfterCloseFailClosed fails on main, passes after the fix.
- Add a test in NEW pkg/secret/open_clear_test.go: cancel the open context without close clears the key, including during a read. Check: TestOpenContentContextCancelWithoutCloseClearsKey fails on main, passes after the fix.
- Add a test in NEW pkg/secret/open_clear_test.go: a truncated header clears the key. Check: TestOpenContentHeaderFailureClearsKey fails on main, passes after the fix.
- Add a test in NEW pkg/secret/open_clear_test.go: concurrent Close and Read clear once with no race. Check: TestOpenContentConcurrentCloseAndRead does not report a data race on main with -race semantics and passes after the fix.
- Add a test in NEW pkg/job/jobtest/recorder_clock_test.go: a fixed clock gives exact record times. Check: TestRecorderHarnessFixedClockSetsExactRecordTimes fails on main, passes after the fix.
- Clear the key on Close, failure and context end in pkg/secret/stream.go and pkg/secret/itemsealer.go. Check: go test ./pkg/secret/... passes.
- Add the injectable Now field in pkg/job/jobtest/recorder.go and pkg/job/jobtest/recorder_keytx.go. Check: go test ./pkg/job/jobtest/... passes with exact time assertions.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has an ItemKeySealer OpenContent implementation. (`pkg/secret/itemsealer.go:154 (func ItemKeySealer.OpenContent)`)
- Main has a Service OpenStream helper. (`pkg/secret/stream.go:211 (func Service.OpenStream)`)
- Main has the shared stream opener used by the item sealer. (`pkg/secret/stream.go:228 (func openStreamWith)`)
- Main defines the shredded-key sentinel. (`pkg/secret/itemkey.go:22`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide how long an item key stays in memory or how the recorder harness sets time? No ADR is listed in the pack, so no ADR answers it. The fix stays inside the named files and changes no port signature.
- Should the change also fix the three findings the issue marks as not real? No. The issue text gives a reason for each, so the change does not touch them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
