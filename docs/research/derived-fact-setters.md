# Setters of derived facts

A derived fact is a stored value that a product write path computes from other writes. No caller should set it directly. I searched the exported methods on ports and stores with:

```
rg -n 'func \([^)]*\) (Put|Set|Record|Observe|Mark|Upsert|Save|Touch|Bump)[A-Z]\w*\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
rg -n '^\s+(Put|Set|Record|Observe|Mark|Upsert|Save|Touch|Bump)[A-Z]\w*\(' pkg internal --glob '!*_test.go' --glob '!**/*test/**'
```

For each candidate I ran:

```
rg -n '\.<Name>\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
```

The `**/*test/**` glob omits the conformance suites. A match is product code. I read the port comment and the service that should call it.

## Known case: `unit_state_versions`

The known case is confirmed. The table `unit_state_versions` has one store writer: `PutUnitStateVersion` at `pkg/store/sqlite/jobs.go:45` and `pkg/store/postgres/jobs.go:48`. The only non-test matches of `.PutUnitStateVersion(` are the calls of the generated query inside those methods. No product code calls the store method.

`jobtest.Domain.SetStateVersion` at `pkg/job/jobtest/jobtest.go:76` is the test hook for the same column. Its callers are the job suite and the two SQL harnesses. `ObserveStateVersion` does not write `unit_state_versions`. In the suite's memory harness it writes the companion fact `unit_final_versions` (`pkg/job/jobtest/mem_test.go:641`), and the SQL store writes that table through `PutFinalVersion` (`pkg/store/internal/jobstore/apply.go:287`). No product code calls `ObserveStateVersion` either.

The state write paths that should derive the state facts exist:
- Stack checkpoint save: `pkg/stack/updates.go:1171` (`PutCheckpoint`).
- Workspace finalize: `pkg/workspace/service.go:1361` and `pkg/workspace/write_state.go:291` (`FinalizeStateVersion`).

Both writes have product callers, but neither calls `PutUnitStateVersion` or `ObserveStateVersion`. So no product write stores `unit_state_versions` or `unit_final_versions`.

## Table

| Method | Location | Fact | Non-test callers | Product path that should derive it |
| --- | --- | --- | --- | --- |
| `PutUnitStateVersion` | `pkg/store/sqlite/jobs.go:45`; `pkg/store/postgres/jobs.go:48` | `unit_state_versions.version_id` (current state version) | none | The state write of the tool: stack `pkg/stack/updates.go:1171`; workspace `pkg/workspace/service.go:1361` and `pkg/workspace/write_state.go:291`. None calls it. |
| `jobtest.Domain.SetStateVersion` | `pkg/job/jobtest/jobtest.go:76` | `unit_state_versions.version_id` | none | Same as above. The test hook writes the fact directly. |
| `job.Store.ObserveStateVersion` | `pkg/job/store.go:318`; `pkg/store/internal/jobstore/apply.go:244` | `unit_final_versions` row; `jobs.body` (`Closed`, `Verification=pending`) | none | The state write transaction of the tool. None found. |
| `jobstore.Tx.PutFinalVersion` | `pkg/store/internal/jobstore/tx.go:167`; `pkg/store/sqlite/jobs.go:485`; `pkg/store/postgres/jobs.go:466` | `unit_final_versions` row | `pkg/store/internal/jobstore/apply.go:287` | `observeIn` computes it from a final state version. The method is the transaction primitive of that path. |
| `selfservice.StateStore.ObserveStateVersion` | `internal/selfservice/ports.go:81` | `selfservice.Revision.State` (`awaiting-state` to `applied`) | none | The state write transaction. None found. |
| `job.Store.SetVerification` | `pkg/job/store.go:323`; `pkg/store/internal/jobstore/apply.go:364` | `jobs.body` (`Verification`) | none | The `KindVerify` executor. None found. |
| `stack.BlameStore.RecordResourceChanges` | `pkg/stack/history.go:91` | Stack blame rows and the blame cursor | none | The blame operation. None found. |
| `workspace.BlameStore.RecordResourceChanges` | `pkg/workspace/history.go:186`; `pkg/store/sqlite/workspace_blame.go:20`; `pkg/store/postgres/workspace_blame.go:21` | `workspace_blame_items`; `workspace_blame_cursors` | none | The blame operation. None found. |
| `calendar.Store.RecordFeedStatus` | `internal/calendar/store.go:122` | `calendar.Feed.Status.ReadAt` and `.Error` | none | The scheduled feed read. None found. |
| `unmanaged.Index.SetDeleted` | `internal/unmanaged/ports.go:139` | Deleted marker of the resource index entries | none | The soft delete and the restore of the unit. None found. |
| `unmanaged.Links.SetNamespaceDeleted` | `internal/unmanaged/ports.go:201` | Deleted marker of the namespace links | none | The soft delete and the restore of the namespace. None found. |
| `dependency.Store.RecordEdge` | `internal/dependency/dependency.go:193` | `dependency.Edge.LastSeen`, `.Reads`, `.Ambiguous` | none | The state check and the source pipeline. None found. |
| `quota.Counter.SetSettled` | `internal/quota/counter.go:144` | Settled count of a quota key | none | The reconcile schedule. None found. |
| `quota.Tx.SetEnd` | `internal/quota/counter.go:107` | `quota.Reservation.EndsAt` | none | The Self-service `Extend` path through the quota transaction. None found. |
| `selfservice.BlueprintStore.SetExprVersion` | `internal/selfservice/ports.go:125`; `pkg/store/sqlite/self_service_blueprints.go:401`; `pkg/store/postgres/self_service_blueprints.go:384` | `self_service_blueprint_versions.expr_version` | none | A compile with a new Expr version. None found. |
| `unitimport.UnitStore.SetCounts` | `internal/unitimport/unitimport.go:601`; `pkg/store/sqlite/unitimport.go:344`; `pkg/store/postgres/unitimport.go:400` | `import_units.versions` and `.committed` counts | none | The writer check after it finds new versions. None found. |
| `cost.BudgetStore.SaveStatus` | `internal/cost/ports.go:175` | Last `cost.Status` of a budget and period, including `Warned` | none | The budget evaluation with `Evaluate` and `MarkWarned`. None found. |
| `storetest.LiveMarkerStore.SetMarker` | `pkg/store/storetest/live_markers.go:26` | `deleted_at_ms` and `revoked_at_ms` markers | none | The delete and revoke writes. The test hook writes the marker with no check. |
| `accesstoken.Store.TouchToken` | `pkg/accesstoken/store.go:60`; `pkg/store/sqlite/accesstokens.go:233`; `pkg/store/postgres/accesstokens.go:211` | `access_tokens.last_used_at_ms` | `pkg/accesstoken/service.go:788` | The token authentication path at `pkg/accesstoken/service.go:788`. |
| `session.Store.TouchSession` | `pkg/session/store.go:44`; `pkg/store/sqlite/sessions.go:253`; `pkg/store/postgres/sessions.go:240` | `sessions.last_seen_at_ms` | `pkg/session/service.go:364` | The session authentication path at `pkg/session/service.go:364`. |
| `mfa.PasskeyStore.RecordUse` | `pkg/mfa/mfa.go:217`; `pkg/store/sqlite/passkeys.go:51`; `pkg/store/postgres/passkeys.go:43` | `passkeys.last_used_at_ms` and `.sign_count` | `internal/mfasvc/service.go:500` | The sign-in assertion path at `internal/mfasvc/service.go:500`. |
| `workspace.StateStore.RecordUploadAttempt` | `pkg/workspace/service.go:240`; `pkg/store/sqlite/workspaces.go:542`; `pkg/store/postgres/workspaces.go:529` | `state_versions.content_key` of a pending version | `pkg/workspace/service.go:2520` | `noteAttempt`, the upload and restore path at `pkg/workspace/service.go:2520`. |
| `workspace.StateStore.SaveSensitiveMarks` | `pkg/workspace/service.go:249`; `pkg/store/sqlite/workspaces.go:596`; `pkg/store/postgres/workspaces.go:580` | `state_versions.sensitive_marks` | `pkg/workspace/service.go:1493`, `pkg/workspace/service.go:2180` | The JSON rendering path at `pkg/workspace/service.go:1493` and the restore copy at `:2180`. |
| `audit.CheckStore.SaveCheckResult` | `pkg/audit/checkstate.go:85`; `pkg/store/sqlite/audit_check.go:63`; `pkg/store/postgres/audit_check.go:67` | `audit_chain_check.checked_at_ms` and `.invalid` | `pkg/dustpan/auditchain.go:1479` | The scheduled chain check at `pkg/dustpan/auditchain.go:1479`. |
| `audit.CheckStore.SaveCheckMark` | `pkg/audit/checkstate.go:93`; `pkg/store/sqlite/audit_check.go:88`; `pkg/store/postgres/audit_check.go:83` | `audit_chain_mark` row for a scope and epoch | `pkg/audit/checkpoint.go:1268`, `pkg/audit/checkpoint.go:1289` | The checkpoint check at `pkg/audit/checkpoint.go:1268` and `:1289`. |
| `profile.IssuerSettingsStore.RecordCopyCheck` | `pkg/profile/issuer_settings.go:333`; `pkg/store/sqlite/issuer.go:296`; `pkg/store/postgres/issuer.go:277` | `issuer_settings.copy_check` | `internal/issuersvc/rotate.go:232` | The issuer rotation copy check at `internal/issuersvc/rotate.go:232`. |
| `principal.RemovalLog.MarkRemovalAlert` | `pkg/principal/removal.go:158`; `pkg/store/sqlite/removals.go:112`; `pkg/store/postgres/removals.go:109` | `member_removal_alerts.alerted_at_ms` | `internal/removalalertsvc/removalalertsvc.go:273` | The removal alert sender at `internal/removalalertsvc/removalalertsvc.go:273`. |
| `tempadmin.Store.MarkTempAdminReviewOpened` | `pkg/tempadmin/tempadmin.go:246`; `pkg/store/sqlite/tempadmin.go:119`; `pkg/store/postgres/tempadmin.go:106` | `temp_admins.review_opened_at_ms` | `pkg/tempadmin/service.go:251` | The review open path at `pkg/tempadmin/service.go:251`. |

## Entries with no non-test caller

### `PutUnitStateVersion`

```
rg -n '\.PutUnitStateVersion\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
pkg/store/sqlite/jobs.go:59:		err = q.PutUnitStateVersion(ctx, db.PutUnitStateVersionParams{
pkg/store/postgres/jobs.go:59:		err = q.PutUnitStateVersion(ctx, db.PutUnitStateVersionParams{
```

The two matches are the store methods' own calls of the generated query. The test callers are `pkg/store/sqlite/jobs_test.go:183` and `pkg/store/postgres/jobs_test.go:185`.

### `jobtest.Domain.SetStateVersion`

```
rg -n '\.SetStateVersion\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test callers are the job suite and the two SQL harnesses. The first lines:

```
rg -n '\.SetStateVersion\(' pkg internal cmd
pkg/job/jobtest/claimer.go:457:	if err := h.Domain.SetStateVersion(t.Context(), f.fence, f.unit, svOne); err != nil {
pkg/job/jobtest/recorder_plans.go:62:	if err := h.Domain.SetStateVersion(t.Context(), f.fence, f.unit, svOne); err != nil {
pkg/store/sqlite/jobs_test.go:183:	return d.s.PutUnitStateVersion(ctx, fence, unit.UnitID(), version)
pkg/store/postgres/jobs_test.go:185:	return d.s.PutUnitStateVersion(ctx, fence, unit.UnitID(), version)
```

### `job.Store.ObserveStateVersion`

```
rg -n '\.ObserveStateVersion\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test callers are `pkg/job/jobtest/writeback.go:125`, `:149`, `:178`, `pkg/job/jobtest/damaged.go:247`, `pkg/job/jobtest/isolation.go:50`, `pkg/store/internal/jobstore/updatetime_paths_test.go:181`, and `internal/selfservice/selfservicetest/requests.go:241`.

### `selfservice.StateStore.ObserveStateVersion`

```
rg -n '\.ObserveStateVersion\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/selfservice/selfservicetest/requests.go:241`. No adapter implements the port.

### `job.Store.SetVerification`

```
rg -n '\.SetVerification\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test callers are `pkg/job/jobtest/mem_test.go:647` and the job service test.

### `stack.BlameStore.RecordResourceChanges`

```
rg -n '\.RecordResourceChanges\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

No store implements the stack blame port. The stack blame table is not in the baseline.

### `workspace.BlameStore.RecordResourceChanges`

```
rg -n '\.RecordResourceChanges\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test callers are `pkg/workspace/workspacehistorytest/mem_test.go:94` and the SQL store suite.

### `calendar.Store.RecordFeedStatus`

```
rg -n '\.RecordFeedStatus\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/calendar/calendartest/mem_test.go:243`. No store implements the port.

### `unmanaged.Index.SetDeleted`

```
rg -n '\.SetDeleted\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/unmanaged/unmanagedtest/index.go:83`. No store implements the port.

### `unmanaged.Links.SetNamespaceDeleted`

```
rg -n '\.SetNamespaceDeleted\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/unmanaged/unmanagedtest/links.go:179`. No store implements the port.

### `dependency.Store.RecordEdge`

```
rg -n '\.RecordEdge\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/dependency/dependencytest/mem_test.go:69`. No store implements the port.

### `quota.Counter.SetSettled`

```
rg -n '\.SetSettled\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/quota/quotatest/mem_test.go:98`. No store implements the port.

### `quota.Tx.SetEnd`

```
rg -n '\.SetEnd\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test callers are `internal/quota/quotatest/mem_test.go:207` and `internal/selfservice/selfservicetest/mem_test.go:368`. No store implements the port.

### `selfservice.BlueprintStore.SetExprVersion`

```
rg -n '\.SetExprVersion\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/selfservicesvc/blueprints_test.go:233`. The SQL stores implement the method.

### `unitimport.UnitStore.SetCounts`

```
rg -n '\.SetCounts\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/unitimport/unitimporttest/mem_test.go:270`. The SQL stores implement the method.

### `cost.BudgetStore.SaveStatus`

```
rg -n '\.SaveStatus\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The test caller is `internal/cost/costtest/mem_test.go:204`. No store implements the port.

### `storetest.LiveMarkerStore.SetMarker`

```
rg -n '\.SetMarker\(' pkg internal cmd --glob '!*_test.go' --glob '!**/*test/**'
(no matches)
```

The suite calls it at `pkg/store/storetest/live_markers.go:94`. The SQL adapters implement it in their test files only.

## Not sure

- `BumpGeneration` (`pkg/session/cookie.go:505`, `pkg/store/internal/generationstore/generationstore.go:32`, `pkg/store/internal/localuserstore/localuserstore.go:128`). It is a counter, but it fences sessions and the security write itself advances it. It is not computed from other writes. I cannot say whether it is a fact or a fence.
- `BumpFailoverVersions` (`pkg/backup/restore_record.go:133`, `pkg/store/sqlite/restore.go:419`, `pkg/store/postgres/restore.go:492`). Same reason: a fence counter that a failover or a restore advances.
- `MarkRestoreApplying` (`pkg/backup/restore_operation.go:447`). It writes the state of the restore. The restore step that performs the transition is the only caller. I cannot say it is a projection rather than operation state.
- `RecordClaim` (`pkg/runner/store.go:153`). It writes the claim record. The runner protocol calls it. The claim may be the authoritative event, not a value computed from other writes.
- `RecordUsage` (`internal/agent/store.go:166`). It adds to the token ledger. No product caller. It may be a counter from model calls, but the agent run path is not wired.
- `PutBuiltCheckpoint` (`pkg/stack/journal_entries.go:234`). It stores a checkpoint built from the journal. The journal build calls it. It is both a derived checkpoint and the save primitive of the replay.
- `SetReferences` (`pkg/store/internal/gatestore/tx.go:139`). It replaces the reference index of a gate object. The object write calls it in the same transaction. It may be an internal transaction primitive.
- `SetRevision` and `SetDigest` (`pkg/setting/change.go:44`, `:47`). They write the revision and the digest. `Change` and `Sync` compute them and are the only callers. They may be internal transaction primitives.

## Checked and excluded

- `SetOverrides` (`pkg/namespace/namespace.go:199`): a namespace setting that a person gives.
- `SetDefaults` (`pkg/unitsettings/ports.go:92`): a unit default that a person gives.
- `SetFilter` (`pkg/source/filters.go:44`): a source filter that a person gives.
- `SetSetting` (`pkg/outputshare/ports.go:125`): an output sharing setting that a person gives.
- `SetNamespaceList` (`pkg/ipallow/ipallow.go:28`): an IP allowlist that a person gives.
- `SetPolicy` (`pkg/accesstoken/service.go:534`, `pkg/session/policy.go:189`): a policy that an administrator gives.
- `SetPassword` (`pkg/localuser/localuser.go:692`): a credential write that a person or a reset path gives.
- `SetChangeRequired` (`pkg/localuser/localuser.go:697`): a credential state that a person or a reset path gives.
- `SetDraining` (`pkg/runner/store.go:123`): a runner setting that an administrator gives.
- `SetClassification` (`internal/incident/store.go:89`): an incident classification that a person gives.
- `SetAPI` (`pkg/objectcontext/store.go:43`): object context that a person gives.
- `SetUnit` (`pkg/collection/service.go:97`): collection membership that a person gives.
- `SetUnitCollection` (`pkg/collection/collection.go:191`): collection membership that a person gives.
- `SetBreakGlassLimit` (`pkg/session/breakglass.go:79`): a reader function, not a stored fact.
- `PutAccessLimit` (`pkg/gate/config_objects.go:182`): gate configuration that an administrator gives.
- `PutApprovalDefault` (`pkg/gate/config.go:145`): gate configuration that an administrator gives.
- `PutApprovalRule` (`pkg/gate/config.go:247`): gate configuration that an administrator gives.
- `PutCarryOver` (`pkg/gate/store.go:162`): gate configuration that an administrator gives.
- `PutGate` (`pkg/gate/config_objects.go:69`): gate configuration that an administrator gives.
- `PutLock` (`pkg/manuallock/manuallock.go:257`): a manual lock that a person gives.
- `PutDraft` (`internal/selfservice/ports.go:92`): a blueprint draft that a person gives.
- `PutEstimate` (`internal/cost/ports.go:121`): an estimate that a client or an estimator adapter gives.
- `PutGroupMapping` (`pkg/principal/groupmapping.go:205`): a group mapping that an administrator gives.
- `PutIssuerSettings` (`pkg/profile/issuer_settings.go:322`): issuer configuration that an administrator gives.
- `PutRegistration` (`pkg/forge/connection.go:354`): forge connection configuration that an administrator gives.
- `PutAssignment` (`pkg/forge/connection.go:375`): forge connection configuration that an administrator gives.
- `PutSink` (`pkg/notify/ports.go:110`): notification configuration that a person gives.
- `PutSubscription` (`pkg/notify/ports.go:202`): notification configuration that a person gives.
- `PutPolicy` (`pkg/drift/ports.go:66`): a drift policy that a person gives.
- `PutRule` (`internal/unmanaged/ports.go:307`): an ignore rule that a person gives.
- `PutPlan` (`pkg/job/record.go:199`): a plan that the runner gives.
- `PutPlanPart` (`pkg/job/planparts.go:54`): a plan part that the runner gives.
- `PutLog` (`pkg/launcher/session/session.go:395`): a log that the runner gives.
- `PutLogChunk` (`pkg/launcher/session/session.go:331`): a log chunk that the runner gives.
- `PutJournalRow` (`pkg/stack/journal_entries.go:195`): a journal row that the CLI gives.
- `PutCheckpoint` (`pkg/stack/updates.go:114`): state that a tool gives.
- `SaveUpdate` (`pkg/stack/updates.go:76`): state that a tool gives.
- `SaveWorkspace` (`pkg/workspace/service.go:75`): state that a tool gives.
- `SaveReleased` (`pkg/workspace/service.go:88`): state that a tool gives.
- `SaveCheckpoint` (`pkg/stack/updates.go:525`): a service method that a client calls with state.
- `SaveCheckpointDelta` (`pkg/stack/delta.go:47`): a service method that a client calls with a delta.
- `PutUnits` (`internal/unitimport/unitimport.go:590`): import input from a source.
- `SetPlan` (`internal/unitimport/unitimport.go:564`): import input from a source.
- `RecordSinkTest` (`pkg/notify/ports.go:129`): an audit entry of a sink test; it stores no other value.
- `RecordJournalRefusal` (`pkg/stack/journal_entries.go:215`): an audit record of a refused journal row.
- `RecordOutOfScope` (`internal/scopeaudit/scopeaudit.go:142`): an audit record of a refused operation.
- `RecordExchange` (`pkg/trustrule/exchange.go:50`): a record of a token exchange.
- `RecordStep` (`pkg/durable/store.go:77`): the result of one operation step.
- `RecordTurn` (`internal/agent/store.go:160`): the messages of one agent turn.
- `RecordToolCall` (`internal/agent/toolset.go:135`): the audit record of one tool call.
- `RecordOverrideRefusal` (`internal/api/tofuunit/tofuunit.go:100`): an audit record of a refused override.
- `RecordTest` (`pkg/notify/sinks/test.go:132`): an audit record of a sink test.
- `RecordRestoreReport` (`pkg/backup/restore_operation.go:422`): the dev-strip report of one restore.
- `RecordRestoreEvent` (`pkg/backup/restore_operation.go:467`): an event record of one restore step.
- `SetRestoreMaintenance` (`pkg/backup/restore_operation.go:430`): the maintenance state of one restore, written by the restore step.
- `SaveChainRecord` (`pkg/audit/chain.go:164`): the audit chain setting and migration state, not a value computed from other writes.
- `SetState` (`pkg/maintenance/maintenance.go:121`): the installation state, which a restore or an administrator sets.
- `SetStateIf` (`pkg/maintenance/maintenance.go:128`): the installation state, which a restore or an administrator sets.
- `SetFreezeEnd` (`pkg/store/internal/jobstore/tx.go:175`): the transaction primitive of the freeze write.
- `BumpFence` (`pkg/job/jobtest/jobtest.go:73` and the harness types): a fence counter, not a fact.
- `BumpFences` (`pkg/durable/durabletest/durabletest.go:75`): a test fixture for a restore fence.
- `SetStatus` (`pkg/forge/forge.go:374`): an outbound call to a forge, not a stored fact.
- `UpsertComment` (`pkg/forge/forge.go:394`): an outbound call to a forge, not a stored fact.
- `ObserveRequest` (`internal/metrics/server.go:212`): an in-memory metric.
- `ObserveSweep` (`internal/metrics/server.go:493`): an in-memory metric.
- `ObserveDeletedSweep` (`internal/metrics/server.go:530`): an in-memory metric.
- `ObserveConfigReload` (`internal/metrics/server.go:547`): an in-memory metric.
- `ObserveGitHostRefused` (`internal/metrics/server.go:669`): an in-memory metric.
- `SetAuditChainInvalid` (`internal/metrics/server.go:262`): an in-memory metric.
- `SetAuditChainInvalidSource` (`internal/metrics/server.go:271`): an in-memory metric.
- `SetAuditSinkLag` (`internal/metrics/server.go:454`): an in-memory metric.
- `SetAuditSinkLagEntries` (`internal/metrics/server.go:465`): an in-memory metric.
- `SetWriteDeadline` (`internal/api/server/server.go:467`): HTTP connection state.
- `RecordExists` (`pkg/store/internal/gatestore/tx.go:156`): a read, not a write.
- `RecordCount` (`pkg/idempotency/idempotencytest/sealed.go:66`): a test read, not a write.
- `SetNow` (`internal/freeze/freezetest/freezetest.go:48`): a test clock.
- `SetFault` (`internal/ldaptest/ldaptest.go:323`): a test fake.
- `SetBindHook` (`internal/ldaptest/ldaptest.go:333`): a test fake.
- `SetLogin` (`internal/castest/castest.go:211`): a test fake.
- `SetUser` (`internal/oauth/oauthtest/oauthtest.go:328`): a test fake.
- `SetFaults` (`internal/oauth/oauthtest/oauthtest.go:336`): a test fake.
- `SetDefaultBranch` (`pkg/forge/forgetest/forgetest.go:107`): fake forge host state.
- `SetMergeState` (`pkg/forge/forgetest/forgetest.go:116`): fake forge host state.
- `SetTokenExpiry` (`pkg/forge/forgetest/forgetest.go:151`): fake forge host state.
- `PutManualLock` (`pkg/job/jobtest/jobtest.go:83`): a manual lock that a person gives.
- `RecordStepBegin` (`pkg/job/jobtest/jobtest.go:96`): an event record of a step begin.
- `SetJobOpen` (`internal/cost/costtest/costtest.go:43`): a harness fixture that builds a job row owned by the job port.
