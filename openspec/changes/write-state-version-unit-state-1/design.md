## Context

Job store needs current and final state versions, but no tool package writes them yet. This part adds the writes and the same-transaction observe call. Later parts start follows plans from these versions.

## Decisions

1. **Wiring over direct import.** Each tool store keeps its own write path and row shape. The wiring injects a small Observer port into each store. On commit the store maps its native version to job.StateVersion and calls Observe. No shared base class and no tool import of job.
2. **Same-transaction observe.** Current version write and final-version observe occur in the fenced transaction of the state write, keyed by stable unit ID. A failure rolls back the state write. No second transaction and no background repair.

## Structure

Unexported field observer in each tool SQL store struct, set only by constructor NewStoreWithObserver; constructors without an observer refuse final writes, which makes a missing wiring path fail at build time.

## Risks

- A missed final flag leaves a write-back job open until a later final write closes it.
