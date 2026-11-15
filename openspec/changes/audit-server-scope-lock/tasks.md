## 1. Failing tests

- [ ] 1.1 Add `TestAppendAuditEntryLocksServerScope` (NEW) and `TestAppendAuditEntryLockErrorStoresNothing` (NEW) in the NEW file `pkg/store/postgres/audit_server_lock_internal_test.go`, as the layer table says. Check: `DUSTPAN_TEST_POSTGRES_URL=<url> go test ./pkg/store/postgres/ -run AppendAuditEntry` fails on main.
- [ ] 1.2 Add `TestServerAuditConcurrentWriters` (NEW) in the NEW file `pkg/store/postgres/audit_server_concurrency_test.go`: one case for each caller family (provider configs through `CreateProviderConfig` at pkg/store/postgres/providers.go:101, `RecordUse` at pkg/store/postgres/passkeys.go:43 with a counter regression, `BumpGeneration` at pkg/store/postgres/principalstate.go:140, name locks, access tokens), 16 parallel writers each; each write succeeds, and `audit.CheckScopes` (pkg/audit/chain.go:624) over `audit.ServerScope` returns no error. Check: the provider, passkey and generation cases fail on main.
- [ ] 1.3 Add `TestServerAuditConcurrentWritersSQLite` (NEW) in the NEW file `pkg/store/sqlite/audit_server_concurrency_test.go` with the 16 parallel provider writes. Check: `go test -race ./pkg/store/sqlite/ -run ServerAudit` passes on main (the IMMEDIATE transactions already serialize the appends).

## 2. Fix

- [ ] 2.1 In `appendAuditEntry` (pkg/store/postgres/audit.go:239), call `q.LockServerAuditScope` first when `e.Namespace == audit.ServerScope`, and return the error wrapped. Update its comment: the function takes the lock of `@server` itself. Check: the tests of 1.1 and 1.2 pass.
- [ ] 2.2 Give each existing caller lock of `@server` (pkg/store/postgres/audit.go:66, :111, :178, :728; accesstokens.go:301; access.go:412; preferences.go:118; settings.go:198, :261; hostrecovery.go:44; breakglass.go:422; restore_operation.go:485; audit_chain.go:66, :226) a comment that says it is a defense-in-depth layer: the caller reads state under the lock before the append, and the append takes the lock too. Remove no call. Check: `rg -c LockServerAuditScope pkg/store/postgres --glob '!internal/db/*'` gives the same count as on main plus one.

## 3. Definition of done

- [ ] 3.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
