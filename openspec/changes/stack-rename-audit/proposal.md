## Problem

A developer renames a Pulumi stack with `pulumi stack rename`. The audit log of
the namespace writes no entry for the rename, so a site administrator cannot
tell who renamed the stack, or what its old name was. The only record is an
inventory change of kind `renamed`. The PostgreSQL store has the same gap, and
OpenTofu has no workspace rename.

## Evidence

- `pkg/stack/stack.go:660` — `func (s *Service) Rename(ctx context.Context, oldRef Ref, newName Name) (Stack, error) {` — the method takes no `audit.Entry`.
- `pkg/stack/stack.go:672` — `renameErr := s.store.RenameStack(ctx, oldRef, newName, ns.Fence())` — no entry reaches the store.
- `pkg/store/sqlite/sqlite.go:863` — `return s.inFencedTx(ctx, fence, func(q *db.Queries) error {` — the rename runs in an unaudited transaction.
- `pkg/store/sqlite/sqlite.go:900` — `return publishStackInventory(` — the only record is the inventory change at line 904: `unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeRenamed},`.
- `pkg/store/sqlite/sqlite.go:994` — `return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {` — `DeleteStack` writes its entry in the same transaction. The issue's comparison is correct.
- `pkg/store/postgres/postgres.go:599` — `return s.inFencedTx(ctx, fence, func(q *db.Queries) error {` — the PostgreSQL `RenameStack` has the same gap.
- `pkg/store/postgres/postgres.go:737` — `return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {` — its `DeleteStack` is audited.
- `internal/api/pulumi/pulumi.go:781` — `entry := changeEntry(ctx, changeAudit{` — the delete handler builds an entry. Line 897 calls `h.stacks.Rename(ctx, oldRef, name)` with no entry.
- `pkg/workspace` has no rename. `rg -n "Rename" pkg/workspace` finds only the comment at `workspace.go:110` and a test name. No workspace rename route or service exists, so that gap is absent.

## Fix boundary

**IN**

- `pkg/stack/stack.go`: `Store.RenameStack` and `Service.Rename` take an `audit.Entry`. `Rename` stamps `At` from the injected clock and passes the entry to the store.
- `pkg/store/sqlite/sqlite.go` and `pkg/store/postgres/postgres.go`: `RenameStack` uses `inFencedAuditedTx`, sets `e.ObjectID` from the stack row it reads, and sets `e.Changes` to one change with key `name`, the old stack name and the new stack name.
- `internal/api/pulumi/pulumi.go`: `renameStack` builds the entry with `changeEntry`, the action `unit:manage` and the source unit object, and passes it to `Rename`.
- `pkg/store/storetest/storetest.go`, `pkg/stack` tests and `internal/api/pulumi` tests: the new cases of `## Check`.

**OUT**

- A workspace rename. None exists.
- A new audit action or route. The rename keeps `unit:manage`.
- The inventory change of kind `renamed`. It stays.

## Check

- **Guard: the entry commits with the rename.** Test `TestConformance`, case "rename writes the audit entry", in `pkg/store/sqlite` and `pkg/store/postgres`. Mutation: put `inFencedTx` back in `RenameStack`; the case finds no entry.
- **Guard: the entry names the stable stack ID.** The same case reads `ObjectID` and compares it with the stack ID. Mutation: do not set `e.ObjectID`; the case fails.
- **Guard: the entry holds the old and the new name.** The same case reads `Changes` and compares `name` with both names. Mutation: do not set `e.Changes`; the case fails.
- **Guard: the API passes the entry.** Test `TestRenameStackWritesAuditEntry` in `internal/api/pulumi`. Mutation: call `Rename` with the zero `audit.Entry`; the store refuses it and the route answers 500.
- **Guard: the service stamps the time.** Test `TestRenamePassesTheAuditEntry` in `pkg/stack`. Mutation: pass the entry without `At`; the store refuses it.

## ADR check

**ADR 60 decision 3 (not owner-tagged).** "An entry records each request whose action changes something, reads a secret value in plain text, or reads the audit log." Decision 5 names the object and its fields. Changed by ADR 126 (decision 3), ADR 63 (decision 5), ADR 64, ADR 65, ADR 68, ADR 72, ADR 151, ADR 210, ADR 240 and ADR 334. The current rule is this ADR with those. The rename changes something and writes no entry today. The fix writes the entry that this decision requires. No conflict.

**ADR 19 (not owner-tagged).** "A rename keeps the ID." Changed by ADR 47 and ADR 248. The entry uses the stack ID as its object ID. No conflict.

**ADR 362 decision 1 (owner).** "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only." Changed by ADR 363 (decision 2 only). The entry holds the stable stack ID as `ObjectID` and the names as a change for people to read. No conflict.

**ADR 63 decision 9 (not owner-tagged).** "The entry holds the changed keys, and for each key that is not sensitive, the old and the new value." Changed by ADR 124 (decision 1). The rename records `name` as a `Change`, as a setting does. No conflict.

## Open questions

none
