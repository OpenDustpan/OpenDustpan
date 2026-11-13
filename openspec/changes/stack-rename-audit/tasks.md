## 1. Port and service

- [x] 1.1 Add `audit.Entry` to `stack.Store.RenameStack` and `stack.Service.Rename`. `Rename` stamps the entry time and passes it to the store. Check: `TestRenamePassesTheAuditEntry` in `pkg/stack`.
- [x] 1.2 Build the entry in `internal/api/pulumi.renameStack` with `changeEntry`, action `unit:manage` and the source unit object. Check: `TestRenameStackWritesAuditEntry` in `internal/api/pulumi`.

## 2. Stores

- [x] 2.1 Change `RenameStack` in `pkg/store/sqlite/sqlite.go` to `inFencedAuditedTx`. Read the stack row before the append, set `e.ObjectID` to its ID, and set `e.Changes` to key `name`, old name and new name. Check: `TestConformance` case "rename writes the audit entry" in `pkg/store/sqlite`.
- [x] 2.2 Do the same in `pkg/store/postgres/postgres.go`. Check: the same case in `pkg/store/postgres`.
- [x] 2.3 Add the case to `pkg/store/storetest/storetest.go`, next to the other rename cases. Check: both dialect packages run it and pass.

## 3. Documentation and verification

- [x] 3.1 Add one sentence to `docs/guides/audit-log.md`: a stack rename writes an entry with the old and the new name. Check: `make docs-lint` exits 0.
- [ ] 3.2 Run `go test ./pkg/stack/... ./pkg/store/sqlite/... ./pkg/store/postgres/... ./internal/api/pulumi/...` with `-timeout 60m` for each store package. Check: the packages pass.
- [ ] 3.3 Run `make check`. Check: zero findings.
