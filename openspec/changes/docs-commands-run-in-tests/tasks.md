## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing corpus test that lists each fenced sh, bash and shell block with page path and block index. Check: TestDocsCommandsCorpusListsBlocks fails on main: no corpus test exists. BLOCKED by no server route: the corpus test lists shell text only with no authorization gate and no credential.
- [ ] 1.2 Add failing runner test that runs each listed block text unchanged in a per-page directory. Check: TestDocsCommandRunsAsWritten fails on main: guide blocks do not run as written. BLOCKED by no server route: the runner test runs shell text only with no authorization gate and no credential.

## 2. Build

- [ ] 2.1 Build the corpus extractor and per-page runner fixture with the closed fence allowlist, the closed skip reason allowlist and the 300 second per-block timeout. Check: `make docs-commands-check` (NEW target) passes with zero unrun blocks and zero stale markers.
- [ ] 2.2 Add runner coverage for the production install page blocks in page order. Check: The corpus runner test runs the install page blocks as written in a clean work directory with no edits. BLOCKED by no server route: the runner runs shell text only with no authorization gate and no credential.
- [ ] 2.3 Add the make target docs-commands-check with its failure report format. Check: `make docs-commands-check` prints page path, block index, command text and captured output for a failing block.

## 3. Definition of done

- [ ] 3.1 Fix pages whose commands fail and document the skip markers. Check: The operator install page shows only commands covered by the corpus test.
- [ ] 3.2 Wire the check into the e2e suite next to TestComposeQuickstart at e2e/compose_test.go:56. BLOCKED by no server route: this step runs shell text in a work directory with no authorization gate and no credential kind, and blocks that need a token carry needs-secret. Check: The e2e step runs the corpus with E2E_COMPOSE=1 and passes; with E2E_COMPOSE unset, Docker blocks report skipped. BLOCKED by no server route: the runner runs shell text in a work directory with no authorization gate and no credential.
- [ ] 3.3 Run the usability check of the copied commands from a clean work directory. Check: Operator completes the install path from the docs page alone and each error names `make docs-commands-check`.
- [ ] 3.4 Run the full gate. Check: `make check` with zero findings.
