# Review follow-ups (October): store adapters

## 1. Validate decoded SourceKind, AgentMode and Verification values before assigning
PR #1321

- Files: pkg/store/internal/jobstore/body.go:530-534
- Change: Validate decoded SourceKind, AgentMode and Verification values before assigning them to the job. Return a decoding error for unsupported values while preserving supported legacy representations.
- Acceptance: Add table-driven decoding tests showing that an unknown value in each enum field returns an error. Supported values and supported legacy representations must continue to decode successfully.

## 2. Bind each reservation to its originating phase and claim, and verify those
PR #1371

- Files: pkg/store/internal/jobstore/record.go:497
- Change: Bind each reservation to its originating phase and claim, and verify those bindings when filling it. Retain the existing job, item-key and unfilled-reservation checks.
- Acceptance: Store-level tests reject otherwise valid fills from another phase or claim of the same job without changing the reservation. Filling under the originating valid claim succeeds.

## 3. Make same-job deduplication atomic with the result write
PR #1388

- Files: pkg/store/postgres/queries.sql:4829; pkg/drift/drifttest/store.go:18
- Change: Make same-job deduplication atomic with the result write. Preserve namespace/tool isolation and timestamp ordering for results from different jobs.
- Acceptance: A concurrent PostgreSQL Record test using the same job ID and different timestamps must commit exactly one transition, including when the earlier timestamp commits first. A newer result from a different job must remain admissible.

## 4. Apply staging-name allowlist validation at the common import boundary before
PR #1431

- Files: pkg/store/postgres/backup.go:1043; pkg/store/postgres/backup_item_keys.go:51
- Change: Apply staging-name allowlist validation at the common import boundary before any staged query, including sealed-state checks. Retain identifier quoting.
- Acceptance: An invalid staging schema name must return backup.ErrCorrupt before any staged query executes. Valid staging names must continue to import successfully.

## 5. Document that a label-filtered query can examine every match of its first label
PR #1489

- Files: pkg/store/postgres/inventory_query.go:125-157; docs/reference/inventory-api.md:135-144
- Change: Document that a label-filtered query can examine every match of its first label when subsequent labels have no overlap. Distinguish this limitation from the locked-filter case.
- Acceptance: Exercise two nonempty, disjoint label sets on PostgreSQL and record rows examined using EXPLAIN ANALYZE. The query must return no units, and the documentation must explain the possible candidate scan despite the empty result.

## 6. Add shared conformance cases for insertion between pages and pagination across
PR #1489

- Files: pkg/store/storetest/unit_query.go:19; pkg/store/storetest/unit_query.go:43
- Change: Add shared conformance cases for insertion between pages and pagination across Pulumi stacks and OpenTofu workspaces.
- Acceptance: Run both store implementations through the cases. Verify inserts before the cursor are skipped, inserts after it remain discoverable, and mixed-tool pages preserve unit-ID ordering without omissions or duplicates.

## 7. Document that workspaces_live_name excludes deleted workspaces and supports
PR #1491

- Files: pkg/store/postgres/migrations/0168_baseline.sql:2016; pkg/store/sqlite/migrations/0168_baseline.sql:1579
- Change: Document that workspaces_live_name excludes deleted workspaces and supports live-only index traversal for offset pagination. Explain why shared columns with the namespace/name uniqueness index do not make it redundant.
- Acceptance: Comments beside both index definitions state the live-row predicate and pagination purpose, and distinguish that purpose from uniqueness enforcement.

## 8. Add the accepted explanation of workspaces_live_name beside its baseline
PR #1491

- Files: pkg/store/postgres/migrations/0168_baseline.sql:2016; pkg/store/sqlite/migrations/0168_baseline.sql:1579
- Change: Add the accepted explanation of workspaces_live_name beside its baseline definitions without changing the schema.
- Acceptance: Both comments explain that the partial index excludes deleted rows and supports live-only pagination despite overlapping namespace/name uniqueness indexing.

## 9. Document why the live-row partial index is retained alongside namespace/name
PR #1491

- Files: pkg/store/postgres/migrations/0168_baseline.sql:2016; pkg/store/sqlite/migrations/0168_baseline.sql:1579
- Change: Document why the live-row partial index is retained alongside namespace/name uniqueness indexing.
- Acceptance: Both baseline definitions have comments identifying the deleted_at_ms IS NULL predicate and its role in live-only index traversal for offset pagination.

## 10. Replace the timer-based interleaving with deterministic synchronization of read
PR #1499

- Files: pkg/store/storetest/restore_profile_read.go:109; pkg/store/storetest/restore_profile_read.go:112
- Change: Replace the timer-based interleaving with deterministic synchronization of read and swap progress. Avoid waiting for swap completion inside a paused read that blocks the swap, and retain the overall test deadline.
- Acceptance: Run the interleaving test under the race detector with controlled read-first and swap-first ordering. Verify consistent revision data and eventual completion without relying on a local timing window.

## 11. Ensure a template setup failure cleans up the per-binary database and any
PR #1500

- Files: internal/pgtest/pgtest.go:74; pkg/store/postgres/leak_test.go:64
- Change: Ensure a template setup failure cleans up the per-binary database and any template created by that run before returning failure.
- Acceptance: Inject a migration failure after CREATE DATABASE and verify the run returns 1, both databases are removed, and unrelated databases remain.

## 12. Validate the complete generated clone name before executing CREATE DATABASE
PR #1500

- Files: internal/pgtest/pgtest.go:110
- Change: Validate the complete generated clone name before executing CREATE DATABASE. Reject names exceeding PostgreSQL's 63-byte identifier limit.
- Acceptance: Test generated names of 63 and 64 bytes, including multibyte characters. Verify 63-byte names succeed and overlong names fail before database creation.

## 13. Correct the timing comment to associate the approximately 150-second
PR #1500

- Files: pkg/store/postgres/leak_test.go:79
- Change: Correct the timing comment to associate the approximately 150-second measurement with 4,000 schemas rather than 1,000 schemas.
- Acceptance: The comment identifies 4,000 schemas as the measured workload and does not attribute that measurement to 1,000 schemas.

## 14. Surface clone-drop failures when SKIP_FINAL_DROP=1 through a failing cleanup
PR #1512

- Files: internal/pgtest/pgtest.go:123
- Change: Surface clone-drop failures when SKIP_FINAL_DROP=1 through a failing cleanup result or another signal retained by CI, rather than tb.Logf alone.
- Acceptance: Inject a clone-drop failure with SKIP_FINAL_DROP=1 and run without -v. Verify that the failure produces a nonzero test result or an explicit retained CI diagnostic.

## 15. Bound the post-cancellation shared-hold probe by gateWait and release any
PR #1517

- Files: pkg/store/sqlite/write_gate_internal_test.go:328; pkg/store/sqlite/write_gate_internal_test.go:348
- Change: Bound the post-cancellation shared-hold probe by gateWait and release any acquired hold. Keep the original checked write in flight during the probe so the test isolates whether cancellation returned the gate turn.
- Acceptance: A mutation that leaks the canceled enter's turn must fail with a local timeout diagnostic within gateWait, not through the package timeout. The unmodified test must pass.

## 16. Give the remaining post-cancellation hold probe an independently bounded wait
PR #1517

- Files: pkg/store/sqlite/write_gate_internal_test.go:348
- Change: Give the remaining post-cancellation hold probe an independently bounded wait and cancellation cleanup. Preserve the existing idempotent cleanup that releases the held checked write.
- Acceptance: A leaked-turn mutation must make TestEnterCanceledInFlight fail with a diagnostic within gateWait and finish cleanup without a package timeout. The unmodified test must pass.

## 17. Give the probe a context.WithTimeout derived from t.Context and gateWait
PR #1517

- Files: pkg/store/sqlite/write_gate_internal_test.go:147; pkg/store/sqlite/write_gate_internal_test.go:348
- Change: Give the probe a context.WithTimeout derived from t.Context and gateWait. Ensure holdCheckedWrite also registers an idempotent cleanup release.
- Acceptance: A leaked-turn mutation must fail the probe with an assertion at the 30-second bound, and test cleanup must complete without a package-timeout panic.

## 18. Correct the inControlTx nesting warning to distinguish context-cancellable
PR #1517

- Files: pkg/store/sqlite/sqlite.go:1295-1299
- Change: Correct the inControlTx nesting warning to distinguish context-cancellable enter-gate waits from SQLite write-lock contention. Retain the prohibition on nested checked transactions.
- Acceptance: The comment explains that a gate wait ends when its context ends and that SQLite lock contention can fail after the busy timeout, rather than asserting an inevitable permanent deadlock.

---
_Generated by [Claude Code](https://claude.ai/code)_
