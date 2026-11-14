## Context

Legacy god file e2e/e2e_test.go holds the stateful TestCLIs chain. New work already goes to per-feature files. This part moves only masked-state helpers by name prefix.

## Decisions

1. **Pure move by name prefix.** Move only functions whose names start with checkMasked, maskedState, maskedFilter, or checkDeltaMasked plus unexported types used solely by them from e2e/e2e_test.go to e2e/e2e_masked_test.go. Copy build-tag lines exactly. Edit no function body. Shared symbols and imports stay in the source file.
2. **Live-secret prerequisite rule.** Any line flagged as a live secret stays in place until its owning fix lands with a fixture marker or run-time value. The move does not carry flagged lines into the target file.

## Structure

Same package e2e, same unexported helper names; no new exported type. File split only.

## Risks

- Accidental body edit changes e2e behavior.
- Mismatched build tags break e2e builds.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Pure test-file move with no runtime, storage, or latency change |  |  |
