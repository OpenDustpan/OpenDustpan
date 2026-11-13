## 1. Code

- [ ] 1.1 Confirm each `path:line` in `proposal.md` on the checkout. Check:
  each one quotes the line that it names.
- [ ] 1.2 Write the table test `TestDaysLeft` in
  `pkg/accesstoken/accesstoken_test.go` with the fixture's fixed clock and the
  five rows of "Check". Run it before the code change. Check: the two
  first-day rows fail with 89, and the expiry-minus-1ms row fails with 0.
- [ ] 1.3 Change `DaysLeft` in `pkg/accesstoken/accesstoken.go` to round up,
  with its comment. Check: `go test ./pkg/accesstoken/ -run 'TestDaysLeft|TestCreatedByAndDaysLeft'`
  passes, and each mutation of "Check" fails its row.

## 2. Docs and generated files

- [ ] 2.1 Update the field comment in `internal/api/server/tokens.go` and the
  row in `docs/guides/access-tokens.md`. Check: `make docs-lint` passes.
- [ ] 2.2 Run the generators of the API description, the clients and the golden
  files. Edit no generated file by hand. Check: `make clients-check` and the
  golden tests pass.

## 3. Proof

- [ ] 3.1 Run `go test ./e2e/ -run 'TestAccessTokens/sqlite' -count=12`.
  Check: 12 of 12 pass. Paste the output into the pull request.
- [ ] 3.2 Run the fast checks (build, vet, lint of the touched packages, the
  unit tests). Paste the output into the pull request.
