## Context

Runner steps mask each secret value of the profile open with StreamMasker in pkg/launcher. The local wrapper reuses the same type for display and for the bytes it hands to the log upload of depends_on.

## Decisions

1. **Reuse StreamMasker with a defined usable set.** The wrapper builds the set from each non-empty secret value of the open. Empty values are dropped. A failed open or set build refuses the start. An empty list gives an empty set and the run proceeds.
2. **Flush before upload.** The wrapper flushes the masker on child exit and on a signal and hands only flushed bytes to the upload.

## Structure

Unexported localMaskedLog type with constructor newLocalMaskedLog that takes the mask set. Only the constructor makes the writer chain. A failed set gives a refused start, never a pass-through chain.

## Risks

- A value outside the profile open is not masked.
- Encoded forms such as base64 are not masked.
- Files, dumps and shell history on the laptop are not masked.
- A secret rotated after the open is not masked until the next open.
- A crash or network loss keeps a partial stored log.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| wrapper mask | The wrapper pipes child stdout and stderr through StreamMasker, refuses the start when the open or set build fails, and flushes on exit and signal. | NEW test TestLocalMaskSplitWrites prints the secret split across writes on stdout and stderr; a run with the masker bypassed fails it. |
