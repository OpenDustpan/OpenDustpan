# 70. Outputs grow the body reservation

Status: accepted · 2026-10-14

## Context

Decision 2 of [ADR 45](0045-large-bodies-stream-to-blobs.md) says that a body
that streams reserves a fixed cost for its buffers. The fixed cost is
`StreamCost`, 8 MiB. A measurement in `TestUploadMemory`
(`pkg/workspace/upload_memory_test.go`) checked it for the state itself: a
30 MiB state with thousands of resources, or with one 24 MiB string, holds at
most 4.4 MiB at once.

The outputs document of an OpenTofu state is the exception (issue #485). The
server keeps it in the state version row, so the upload holds it whole, up to
16 MiB (`maxOutputsBytes`). Two things multiply its memory:

- The buffer that reads the document doubles as it grows. At each doubling
  the old and the new buffer are live together, about 2 times the final
  capacity.
- A sealing service seals the document and holds the sealed copy beside the
  plain copy. Sealing also allocates short-lived buffers (101.6 MiB in total
  for a 7.9 MiB document), and the garbage collector lets the heap grow to
  about 2 times its live bytes before it frees them.

The measurement, for a document of 7.9 MiB (2000 outputs of 4 KiB), without
the race detector:

| Upload | Peak | Reserved before this ADR |
| --- | --- | --- |
| Not sealed | 14.1 MiB | 8 MiB |
| Sealed | 27.0 to 29.4 MiB | 8 MiB |

The peak is not smaller than twice the document, because the server must
hold the document whole. Two changes to the copies did not lower the measured
peak (a buffer of the right size for the seal, and dropping the plain copy
before the finalize), because the headroom of the garbage collector decides
it. So the fixed cost of 8 MiB is wrong for a large outputs document, and a
different fixed cost is wrong for a small one.

## Decision

1. **The reservation of an upload grows with its outputs.** This supersedes
   decision 2 of ADR 45 for the outputs document and for the seal of it. A
   body that streams still reserves `StreamCost` at the start. The
   reservation is a lease. The buffer of the outputs document asks the lease
   for its new capacity at each doubling, and the seal asks for twice the plain
   size (the sealed copy and its buffers). ADR 45 stays as it is.
2. **A refusal is either busy or permanent.** When the lease cannot grow
   because other bodies hold the budget, the upload fails with 503 and a
   `Retry-After` of 1 to 3 seconds. The server picks the value at random, so
   a burst of refused uploads does not retry at the same second. When the
   total that the upload needs is more than the whole budget, it can never
   succeed. The upload then fails with 413, and the text names the bytes
   needed, the bytes the server has and `--max-body-bytes-in-flight`.
3. **A refusal stops the read.** The reader of the document closes its pipe
   at the first refused growth. The store write stops, the server deletes
   the blob, and it does not read the rest of the body.
4. **The budget is a model.** The charge is the sum of the buffer capacities
   plus twice the plain size for the seal. It is above the measured peak, and
   it does not measure memory. `TestUploadMemory` checks that the peak stays
   under the reservation plus a quarter, for each variant.
5. **No other body changes.** The Pulumi paths, the JSON rendering upload and
   the state version create keep decision 2 of ADR 45.

## Consequences

- A sealed state with 16 MiB of outputs takes about 72 MiB of the default
  128 MiB budget (8 MiB at the start, 32 MiB for the buffers, 32 MiB for the
  seal). A second upload of that size in the same moment gets 503.
  Installations with large outputs must raise `--max-body-bytes-in-flight`.
- A burst of large uploads can starve each other: each holds part of the
  budget, and the server refuses the next growth of each. The random
  `Retry-After` spreads the retries. It does not stop the case.
- A budget below the need of one upload gives 413 for that upload, and the
  operator learns the flag from the text.
- A document of 8 MiB or more cannot use less than 8 MiB. The only way to
  end this cost is to stop keeping the outputs in the row, which needs a
  schema change and a new ADR.
- The memory model is easy to check: `TestUploadMemory` prints the bytes that
  the lease grew in each run.
