## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 Confirm that the dependencies are on main: the dispatcher with routing of `Recipients`, a `notify.Contacts` implementation, an email `Sender` and the SMTP settings. Check: `rg -n "ContactsOf" --glob '!*_test.go' --glob '!pkg/notify/**' .` prints a match. If it prints nothing, tasks 1 to 3 go on, and 4.2 stays BLOCKED.

## 1. Failing tests first

- [ ] 1.1 Add the table test of the service: a personal token at 13 days gets level soon, at 2 days level imminent, at 20 days nothing; an expired, a revoked and a disabled token get nothing; a service, jwt-exchange and mtls-exchange token get nothing; a disabled owner gets nothing. Check: NEW TestTokenWarningsDue in internal/tokenwarnsvc fails on main: the package does not exist, and pkg/notify/notify.go:95-136 has no type `token.expiry-warning`.
- [ ] 1.2 Add a test of the dedupe: two runs, and two services on one outbox, give one notification for each token and level; a rotation that moves the expiry gives a new one. Check: NEW TestTokenWarningsAreDeduped in internal/tokenwarnsvc fails on main: the NEW package does not exist, and pkg/notify/notify.go:95-136 has no type `token.expiry-warning` to append.
- [ ] 1.3 Add a test that the notification holds the fixed attributes and no secret form, with a token name of CR, LF and 1000 characters. Check: NEW TestTokenWarningHoldsNoSecret in internal/tokenwarnsvc fails on main: the NEW package does not exist, and pkg/notify/notify.go:95-136 has no type `token.expiry-warning` to append.
- [ ] 1.4 Add a test that the outbox error fails the run. Check: NEW TestTokenWarningsOutboxError in internal/tokenwarnsvc fails on main: the NEW package does not exist, and pkg/notify/notify.go:95-136 has no type `token.expiry-warning` to append.
- [ ] 1.5 Add a test that the catalog has the NEW type and that `Validate` accepts the notification. Check: NEW case in pkg/notify/notify_test.go fails on main: the catalog at pkg/notify/notify.go:95-136 lacks the type (the unknown-type case at pkg/notify/notify_test.go:55 shows the check).
- [ ] 1.6 Add a test in pkg/dustpan that the schedule list holds the NEW kind once, next to the inactivity kind. Check: NEW case in pkg/dustpan/schedules_internal_test.go fails on main: pkg/dustpan/schedules.go:83 builds no such schedule.

## 2. Implementation

- [ ] 2.1 Add the NEW constant `TokenExpiryWarning` to the catalog in pkg/notify/notify.go and to `Types()`. Check: `go test ./pkg/notify/...` passes.
- [ ] 2.2 Add `internal/tokenwarnsvc` with the contract of the design, the `leak_test.go` file and the constructor that checks the ports. Check: `go test ./internal/tokenwarnsvc/...` passes.
- [ ] 2.3 Add the durable schedule kind and its job next to `kindInactivity` in pkg/dustpan/schedules.go (:57, :83, :153), and wire the service in `pkg/dustpan`. Check: `go test ./pkg/dustpan/...` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Add the type to the notification reference in docs/reference. Check: `make docs-lint` passes and the reference test that lists the catalog passes.
- [ ] 3.2 Confirm that no route, authorization action or schema changes: the change adds a job and a type. Check: the route test lists no new route, and `sqlc diff` is clean.

## 4. Definition of done

- [ ] 4.1 Write the docs in the same pull request: docs/guides/access-tokens.md gets the two warning times, the recipient, the dependencies for SMTP and the fact that service tokens get no email. Check: `make docs-lint` passes.
- [ ] 4.2 BLOCKED until 0.2 passes: add an e2e step in e2e/tokenexpiry_test.go over the built server on SQLite and PostgreSQL. The credential is a personal access token created through `POST /api/dustpan/v1/me/tokens`, the CLI is the Pulumi CLI, the test SMTP server runs over TLS: create a token with a 10 day expiry, run the job, check one email at the level soon, advance to 2 days, check one email at the level imminent. Check: the e2e step runs on both stores.
- [ ] 4.3 Run the usability check. Check: a Developer finds the two emails in the guide and the reason of a skipped delivery.
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings. The notification carries token metadata to a person: the change needs the Claude security review (rev2) on the exact head before the merge queue.
