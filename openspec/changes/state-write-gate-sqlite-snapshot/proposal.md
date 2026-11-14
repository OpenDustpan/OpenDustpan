Issue: #1283.

## Why

**Persona: operator.** The operator runs a server where state.write gates must see stable facts. Separate reads under a shared lock mix rows from before and after a commit, which makes gate input hard to explain.

The user path this change must make work:

1. **Server start.** Start the server with and without a gate service and read the start-up log for the one gate line.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Fact read fails: state.write gate did not read deleted-unit facts because the read transaction is busy or cancelled. Run `opendustpan dev logs`. | The gate refuses the write and reports the store error. Retry the write; if the store stays busy, check server logs and storage health. | `opendustpan dev logs` |

Evidence for the claims above:

- Issue #1283 asks for a SQLite snapshot read, a start-up log, and removal of the blank-assign rollback pattern. Proof: Follow-ups from the final review of #1259. None blocks the merge.
- The issue reports that a shared lock does not give a snapshot because writers take the same lock, so a commit lands between the reads. Proof: SQLite `GetDeletedUnitFacts`: `s.mu.RLock` does not give a snapshot, because writers take `RLock` too, so a commit can land between the reads. Use one read-only transaction. Impact is low.
- The store has a GetDeletedUnitFacts path covered by gate fact tests. Proof: internal/store/storefault/storefault.go:911:// GetDeletedUnitFacts returns the facts of one deleted unit, unless a fault
- Gate fact tests call GetDeletedUnitFacts for a unit. Proof: pkg/store/storetest/gate.go:809:	got, err := facts.GetDeletedUnitFacts(t.Context(), "acme", tc.tool, id)
- No non-test occurrence of the gate service constructor name is in the pack. Proof: rg -n -F --glob '!*_test.go' 'newGateService' pkg internal cmd matches 0 with first_lines []
- The context pack lists no ADR for this item. Proof: The pack adrs list is empty, so there is no decision text to quote.

## What Changes

- SQLite GetDeletedUnitFacts reads all deleted-unit rows in one read-only transaction scoped to the call with deferred close.
- Fault wrapper and conformance suite path updated with no behavior change.
- Server logs one fixed INFO line per start when there is no gate service.
- Touched rollback paths no longer use the blank-assign pattern with unchanged close behavior.

Out of scope:

- New gate facts, new decision points, or atomic check-and-write of the gate and the state write.
- Postgres read path changes beyond the open question.
- Gate route, console, or user docs changes.

## Usability

Where the path still costs the person time or surprises them:

- The operator path is one start-up log line; there is no new command to learn, but snapshot behavior is not visible from the API.
- Log parsers match the fixed substring key for the absent gate service, not the full line, so future wording stays safe.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): design.md says GetDeletedUnitFacts opens its read-only transaction 'in its constructor path', but the method is per call and Decision 1 asks for one transaction per read. The change does not say where the transaction begins, when it ends, or who closes it. Name the scope of the transaction before the code is written. Resolution: answered: Answered in design decision One snapshot read per call: transaction starts at method entry and closes before return, per call, never exposed to the caller.
- R1.2 (failure mode): The snapshot covers only the fact read. The gate decision can still be stale when a commit lands after the read and before the write it gates. The spec does not say whether the check and the write run in one transaction, so the gate can still act on facts that no longer hold. Resolution: answered: Answered in design decision No atomic check-and-write and out_of_scope: snapshot covers only the read; check and write stay separate.
- R1.3 (missing scenario): No scenario covers a failed or busy read transaction, a context cancel during the reads, or a rollback error. The spec and the tasks do not say what the gate returns in these cases. Resolution: answered: Answered by specs scenario Failed read transaction: when the read-only transaction is busy, times out, cancelled, or rollback fails, the server returns the store error, uses no partial facts, and refuses the write path.
- R1.4 (failure mode): design.md names a risk that a long read transaction blocks a writer checkpoint. No requirement bounds the transaction duration or the number of rows read, and no test covers a slow reader. Resolution: answered: Answered in design risks and spec text: bounded small set of selects for one unit, no transaction held across calls, plus slow-reader task check.
- R1.5 (open question): The snapshot covers SQLite only. The change does not say what Postgres does for the same gate read, so the gate can give different facts on the two dialects. The Postgres read path is out of scope, but no question records that decision. Resolution: open question: Recorded as the one open question on Postgres parity; SQLite-only scope kept in this change.
- R1.6 (missing scenario): The start-up requirement covers only the no-gate-service case. It does not cover a server that has a gate service, and it does not name the log level, the text format or the stability of the line for log parsers, although design.md names that risk. Resolution: answered: Answered by specs scenarios Server starts with no gate service and Repeat start with no gate service: one INFO line per start with fixed text, none when a gate service exists, never twice per start.
- R1.7 (wrong claim): The evidence does not show the SQLite GetDeletedUnitFacts code or its current separate reads. It cites storefault, storetest and two RLock lines in other files. The claim that the gate reads in separate calls under a shared lock is not proven at the cited location. Resolution: answered: Answered by narrowing evidence to what the pack proves: issue text plus storefault and storetest paths; no claim about SQLite internals beyond the issue report.
- R1.8 (wrong claim): The ADR check says 'No ADR is touched' and gives no quoted decision lines. The input lists no decisions, so the 'No ADR conflict' claim has no proof, as the review rules require. Resolution: answered: Answered in considered and evidence: pack adrs list is empty, so adr_check is empty with proof stated.
- R1.9 (scope): Task 2.2 checks the blank assign pattern with a repo-wide rg, but the change claims only the touched paths. The check can fail on code the change does not own, and the task does not say which paths count as touched. Resolution: answered: Answered by scoping the rollback task check to the touched GetDeletedUnitFacts and gate start-up paths only.
- R1.10 (scope): The failing test name in task 1.1 (TestDeletedUnitFactsSnapshotSeesOneCommit) does not match the scenario-to-test mapping in task 1.2 (TestDeletedUnitFactsSnapshot). The pull request must also show the failure on main, which no task requires. Resolution: answered: Answered by using one test name in both tasks and adding a task that shows the failure on main in the pull request.
- R1.11 (scope): The user path ends at a start-up log line that an operator reads. The change adds no docs page, and no usability check is named for the path. The delight rules require a docs page for a feature a user sees. Resolution: answered: Answered in user_path and usability: no docs page because no user-visible behavior changes except one operational log line; usability check is the operator start-up check.
- R1.12 (missing scenario): The rollback change has no scenario for the close behavior it must keep. The spec does not say what happens when close fails after a rollback error. Resolution: answered: Answered by specs scenario Rollback then close: when rollback runs on the touched fact-read path the method closes the transaction and returns the first error with close behavior unchanged.
- R2.1 (open question): proposal.md ADR check states no ADR is touched and the pack adrs list is empty, yet design.md says the store has a single write path and the spec scope relies on no decision; the change never states whether the SQLite store's transaction or locking rules are governed by any ADR, so the no-conflict claim lacks quoted proof as the review rules require. Resolution: answered: Answered in considered and evidence: the pack adrs list is empty so there is no decision text to quote, and no store transaction rule is in the pack.
- R2.2 (failure mode): design.md decision 1 starts one read-only transaction at method entry, but the structure section says the helper is created by the GetDeletedUnitFacts constructor path; the change does not define who closes the transaction on panic or on a cancelled context before the first select, so a leaked read transaction can block writers. Resolution: answered: Answered in design decision One snapshot read per call and structure: deferred close runs on success, error, cancelled context, and panic before the first select.
- R2.3 (missing scenario): specs/state-write-gate/spec.md has a failed-read scenario but no scenario for a read that returns rows while a writer holds the lock for the full transaction, nor for the busy-timeout value; the bounded-duration claim in design risks has no requirement or test. Resolution: answered: Answered in design decision Bounded wait on writer lock and specs snapshot deleted-unit facts writer-holds-lock scenario with bounded timeout fail-closed behavior.
- R2.4 (wrong claim): proposal.md evidence cites internal/store/storefault/storefault.go:911 and pkg/store/storetest/gate.go:809 as the gate fact path, but the change never shows the SQLite GetDeletedUnitFacts code; the claim that the current code uses separate reads under a shared lock is taken from the issue text only. Resolution: answered: Answered in evidence: the separate-reads claim is now phrased as the issue report with the issue sentence as proof, with no claim about unseen SQLite code.
- R2.5 (scope): tasks.md 2.1 changes the fault wrapper and conformance suite with no behavior change, but the proposal does not list those files under What Changes; the touched-file list is incomplete. Resolution: answered: Answered in what_changes: fault wrapper and conformance suite touch listed with no behavior change.
- R2.6 (open question): The Postgres parity question is provisionally answered as SQLite-only, but the gate may then return different facts on the two dialects; the provisional answer is not checked against the rule that a gate result must not depend on the dialect. Resolution: open question: Recorded as the one open question with dialect-content parity stated: same logical rows, only snapshot strength differs.
- R2.7 (missing scenario): specs/state-write-gate/spec.md start-up requirement has no scenario for a start-up log write failure or for a logger that drops INFO lines, so the required single log line has no failure behavior. Resolution: answered: Answered in design decision One fixed start-up log line and specs start without gate service log-write-failure scenario: log failure never blocks start.
- R2.8 (failure mode): design.md risks accept that a substring key is matched by parsers, but no scenario covers a rename of the key or a second gate-less start in the same process, which would log the line twice despite the once requirement. Resolution: answered: Answered in specs start without gate service: once per server start with fixed key text, plus repeat-start scenario.
- R2.9 (missing scenario): The user path in proposal.md lists the error for a busy read, but the spec scenario for the busy case does not name the one command that helps, so the error text required by the voice rule has no requirement. Resolution: answered: Answered in errors and specs failed-read scenario: the refusal error names `opendustpan dev logs` as the one command that helps.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Should the Postgres GetDeletedUnitFacts path get the same one-transaction snapshot in this change or as follow-up work?
   - Options: Keep the snapshot fix SQLite-only and track Postgres parity separately; Include the Postgres snapshot read in this change.
   - Provisional decision (owner review 14 Nov): Keep the snapshot fix SQLite-only and track Postgres parity separately.
   - Reason: The issue names only SQLite with low impact, and the pack proves only the SQLite concern, so the narrow fix ships first and the gate result does not depend on the dialect for content. Benchmark: Google: transactional snapshots give repeatable reads for small fact reads; dialect parity is tracked as follow-up work, not done in a cleanup.
   - Persona impact: No persona is harmed; the gate returns the same logical rows with a stronger read guarantee on SQLite. Escape hatch: Operators on Postgres keep current behavior with no config change; both dialects return the same logical rows and only the snapshot strength differs.

Questions considered and answered:

- Does this change need a wider gate or store redesign? No. The issue lists three review follow-ups from #1259: one SQLite snapshot read plus two small cleanups. The gate read and the gated state write stay in separate steps; atomic check-and-write is a larger redesign and stays out of scope.
- Does this change conflict with an ADR, including any rule on SQLite transactions or locking? No decision text is in the pack to quote. The pack adrs list is empty, so adr_check is empty. The fix keeps the same logical fact rows with a stronger read guarantee inside one call.
- Should Postgres get the same snapshot read? SQLite only by intent of the issue. Postgres keeps its current read path. The dialect difference is recorded as an open question with the same logical rows on both dialects.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
