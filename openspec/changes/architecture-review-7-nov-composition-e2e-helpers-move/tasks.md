## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Move and verify

- [ ] 1.1 Move checkMasked, maskedState, maskedFilter, and checkDeltaMasked helpers plus solely-used unexported types from e2e/e2e_test.go to e2e/e2e_masked_test.go with exact build-tag copy and no body edits in e2e/e2e_masked_test.go; BLOCKED by no server route: this move calls no route and uses no credential. Check: go vet ./e2e/ plus empty diff of moved function bodies. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Verify e2e/e2e_test.go and e2e/e2e_masked_test.go carry the same build tags and the same test set compiles; BLOCKED by no server route: this move calls no route and uses no credential. Check: diff of build-tag lines of e2e/e2e_test.go versus e2e/e2e_masked_test.go is empty. Confirmed by: CI (the agent writes the e2e test).

## 2. Definition of done

- [ ] 2.1 Report line counts of e2e/e2e_test.go and e2e/e2e_masked_test.go before and after the move in e2e/e2e_masked_test.go; BLOCKED by no server route: this move calls no route and uses no credential. Confirmed by: CI (go vet plus lint). Check: make lint plus line counts posted in the pull request body
