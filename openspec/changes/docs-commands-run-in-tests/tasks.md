## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing corpus test in e2e/docs_commands_test.go (NEW) that lists each fenced sh, bash and shell block with page path and block index. Check: TestDocsCommandsCorpusListsBlocks fails on main: no corpus test exists. BLOCKED by no server route: the corpus test lists shell text only with no authorization gate and no credential. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add failing runner test in e2e/docs_commands_test.go (NEW) that runs each listed block text unchanged in a per-page directory. Check: TestDocsCommandRunsAsWritten fails on main: guide blocks do not run as written. BLOCKED by no server route: the runner test runs shell text only with no authorization gate and no credential. Confirmed by: CI (the agent writes the e2e test; it runs the shell blocks of the guides, not routes of the e2e test server). Confirmed by: CI (the agent writes the e2e test).

## 2. Build

- [ ] 2.1 Build the corpus extractor and per-page runner fixture in e2e/docs_commands.go (NEW) with the closed fence allowlist, the closed skip reason allowlist and the 300 second per-block timeout. Check: `make docs-commands-check` (NEW target) passes with zero unrun blocks and zero stale markers. BLOCKED by no server route: the runner runs shell text only with no authorization gate and no credential.
- [ ] 2.2 Add runner coverage in e2e/docs_commands_test.go (NEW) for the production install page blocks in page order. Check: The corpus runner test runs the install page blocks as written in a clean work directory with no edits. BLOCKED by no server route: the runner runs shell text only with no authorization gate and no credential. Confirmed by: CI (the agent writes the e2e test).
- [x] 2.3 Add the make target docs-commands-check to Makefile (NEW) with its failure report format. Check: `make docs-commands-check` prints page path, block index, command text and captured output for a failing block.

- [ ] 2.4 SECURITY. Add hostile runner tests in e2e/docs_commands_test.go (NEW) named TestDocsCommandsRefuseSudo, TestDocsCommandsTimeoutKillsProcessGroup, TestDocsCommandsOutputCap and TestDocsCommandsUnknownSkipReason. Each test sends hostile input straight to RunCommands or CollectCommands: a block with sudo, a block with sleep 600 and a background child, a block that prints more than 1048576 bytes, and a skip marker with a reason outside the closed allowlist. Check: TestDocsCommandsRefuseSudo requires a refusal without host change. TestDocsCommandsTimeoutKillsProcessGroup requires both processes dead. TestDocsCommandsOutputCap requires at most 1048576 bytes of captured output. TestDocsCommandsUnknownSkipReason requires a gate failure with page path and block index. BLOCKED by no server route: the tests run shell text only with no authorization gate and no credential. Confirmed by: CI (the agent writes the e2e test).

## 3. Definition of done

- [ ] 3.1 Fix pages whose commands fail and document the skip markers. Check: The operator install page shows only commands covered by the corpus test.
- [ ] 3.2 Wire the check into the e2e suite next to TestComposeQuickstart at e2e/compose_test.go:56. BLOCKED by no server route: this step runs shell text in a work directory with no authorization gate and no credential kind, and blocks that need a token carry needs-secret. Check: The e2e step runs the corpus with E2E_COMPOSE=1 and passes; with E2E_COMPOSE unset, Docker blocks report skipped. BLOCKED by no server route: the runner runs shell text in a work directory with no authorization gate and no credential. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Run the usability check of the copied commands from a clean work directory. Check: Operator completes the install path from the docs page alone and each error names `make docs-commands-check`. A person's step.
- [ ] 3.4 Run the full gate. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
