## Context

Two small review-bot follow-ups: the item-key open path keeps the raw key until the header is read, and the job recorder conformance harness reads wall-clock time.

## Decisions

1. **Clear key on reader end without signature change.** OpenContent already returns io.ReadCloser on main and keeps that signature. The unexported reader built by openStreamWith in pkg/secret/stream.go owns one cloned key and zeroes it once on the first of Close, header failure, read failure, or end of the open context. Later calls are idempotent and fail closed. Service.OpenStream uses the same reader and gets the same behavior with no caller change beyond the existing duty to close.
2. **Fail closed after context end without holding lock across IO.** The reader holds its mutex only to check and set the cleared flag and to take the key copy for one decrypt step. A Read after clearing returns a fixed sealed-read error and no content. Close stops the context registration. An already canceled open context returns a cleared failing reader. A caller that never closes and whose context never ends keeps the key until garbage collection; the existing duty to close remains.
3. **Ownership of the original key copy.** The sealer in pkg/secret/itemsealer.go clones the key into the reader and zeroes its own copy. The caller slice is caller-owned and is not zeroed.
4. **Fixed clock in job recorder harness.** One field Now func() time.Time on the harness in pkg/job/jobtest/recorder.go defaults to time.Now. The same value feeds the sealer, the recorder, and the record times in pkg/job/jobtest/recorder_keytx.go. Only a fixed clock is in scope; a stepped clock is out of scope. The wall-clock default keeps the existing relative assertions.
5. **Test oracle for key clearing.** Tests use an internal clear hook and counter plus fail-closed Read. They do not prove that garbage-collector copies are gone.

## Structure

Keep the unexported reader built by openStreamWith and give it clearing owned by that reader; keep the harness clock as an unexported field that defaults to wall-clock time.

## Risks

- Close without a read must still clear the key and stay idempotent.
- A fixed clock must not leak between cases.
- Context end must not corrupt an in-progress Read.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Memory hygiene and test determinism only; no user-visible latency, storage or rate change |  |  |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Close of the opened reader | The underlying close returns an error | A second Close is a no-op and returns no error | The key is zeroed and each read fails closed |
| Context end while a Read is in progress | The clear fires during the Read | The caller opens a new reader | The key is zeroed and the Read returns the fixed sealed-read error |
| Header read | The header is truncated or invalid | The caller opens again with a valid stream | The key is zeroed and no content is returned |
