## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing job start test for stored effective pool ID and code location in `NEW pkg/job/effective_test.go` Check: `go test ./pkg/job/ -run TestJobEffectiveValues -count=1` fails on main, passes after the fix
- [ ] 1.2 Add failing audit test for server actor and reason shape in `NEW pkg/audit/automatic_test.go` Check: `go test ./pkg/audit/ -run TestAutomaticEntryActorReason -count=1` fails on main, passes after the fix

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Store effective pool ID and code location at StartJob and return the stored snapshot on job reads. Confirmed by: CI (`go test ./pkg/job/ -run TestJobEffectiveValues -count=1`). Check: `go test ./pkg/job/ ./pkg/audit/ -count=1`
- [ ] 3.2 Wire effective values and RecordAutomaticAction through the existing audit entries route with its authorization actions. Confirmed by: CI (`go test ./pkg/audit/ -run TestAutomaticEntryActorReason -count=1`). Check: `GET /api/audit/entries` shows the server actor and reason for the applied default
- [ ] 3.3 Document effective pool and code location plus the server actor and reason. Confirmed by: the driver's gates. Check: Docs page for job effective values renders and each command runs as written
- [ ] 3.4 Run usability check from the docs page and full checks. A person's step. Check: `make check` with zero findings
