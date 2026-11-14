## 1. Code

- [x] 1.1 Confirm each `path:line` in `proposal.md` on the checkout. Check:
  each one quotes the line that it names.
- [x] 1.2 Write the table test `TestDaysLeft` in
  `pkg/accesstoken/accesstoken_test.go` with the fixture's fixed clock and the
  five rows of "Check". Run it before the code change. Check: the two
  first-day rows fail with 89, and the expiry-minus-1ms row fails with 0.
- [x] 1.3 Add `DaysUntil` to `pkg/accesstoken/accesstoken.go` and make
  `DaysLeft` call it, with both comments. Add `TestDaysUntil` with the
  partial-day rows. Check: `go test ./pkg/accesstoken/ -run 'TestDaysLeft|TestCreatedByAndDaysLeft'`
  passes, and each mutation of "Check" fails its row.

- [x] 1.4 Make the CLI warning in `internal/cli/client/expiry.go` call
  `accesstoken.DaysUntil`, and add its test rows (23 h left: 1; 24 h and 1 ms
  left: 2). Check: `go test ./internal/cli/client/` passes, and the mutation of
  guard 5 fails it.

## 2. Docs

- [x] 2.1 Update the field comment in `internal/api/server/tokens.go` and the
  row in `docs/guides/access-tokens.md`. Check: `make docs-lint` passes, and
  `make clients-check` shows no change.

## 3. Proof

- [ ] 3.1 Run `go test ./e2e/ -run 'TestAccessTokens/sqlite' -count=12`.
  Check: 12 of 12 pass. Paste the output into the pull request.
- [x] 3.2 Run the fast checks (build, vet, lint of the touched packages, the
  unit tests). Paste the output into the pull request.
