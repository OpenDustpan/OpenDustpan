## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Reproduce

- [ ] 1.1 Run the YAML program of the issue with the pinned Pulumi CLI of e2e/tool.go, with and without journaling, and record the first differing field of the checkpoint and the journal entries. Check: The recorded diff names one field, or records no server difference.
- [ ] 1.2 Add a failing replay test in NEW pkg/stack/journal_stackreference_test.go that feeds the journal entries of that run and compares the built checkpoint with the engine snapshot. Check: The NEW test fails on main with the mismatch, or records that replay matches the engine snapshot.

## 2. Fix, only when the diff shows a server difference

- [ ] 2.1 Fix the decoder or replayer in internal/pulumijournal and pkg/stack/journal_build.go for the named field. This task runs only when the diff shows a server difference. Check: NEW pkg/stack/journal_stackreference_test.go passes.
- [ ] 2.2 Add a hostile test that stored events and logs hold no plain-text secret value after the journaled up. Check: The hostile test in NEW pkg/stack/journal_stackreference_test.go passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step with the real Pulumi CLI and the issue program in NEW e2e/journal_stackreference_test.go. The CLI uses an access token and the route ANY /api/stacks/{}/{}/{}/update/{}/journalentries. The step reads the stored events and checks for no mismatch event. Check: make e2e passes the new step. Confirmed by: CI (NEW e2e/journal_stackreference_test.go).
- [ ] 4.2 Add the NEW page docs/guides/pulumi-journaling.md with the StackReference behavior and the command to compare with journaling off. Check: A person runs the page commands as written.
- [ ] 4.3 Run the Developer path from the page alone and run make check. A person's step. Check: `make check` reports zero findings.
