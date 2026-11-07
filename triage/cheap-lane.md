# Cheap lane triage, 7 Nov 2026

Base: main (fetched 2026-11-07). Open issues read: 538 (pull requests skipped).

Counts: CHEAP 9, STALE 2, OTHER 527.

Scope of the check: I read the title of all 538 issues and the body of about 60 likely
candidates. I checked the files of each CHEAP and STALE issue on main. I put every
issue that I did not read in OTHER. Most of them are features, tracking issues, RFC
follow-ups, security or shared-core work. This list is therefore a safe subset, not a
complete one.

## CHEAP

Size: S under 50 lines, M under 200 lines.

| # | Title | Files | Size | Fix |
|---|-------|-------|------|-----|
| 2534 | Flaky: TestWorkLayer asserts run() under 1s | internal/expr/timeout_internal_test.go | S | Replace the 1 s wall-clock bound with a check that the work budget stops the run, with a wide margin. |
| 986 | Add "home" and "home map" to the glossary | GLOSSARY.md | S | Add two entries with Avoid lists; "home" is the first screen of a person. main has no entry now. |
| 985 | Write the docs ownership rule into the contributing guide | CONTRIBUTING.md, .github/CODEOWNERS | S | Add the rule section; add a CODEOWNERS line for the structure files. Run make docs-lint. |
| 2051 | e2e: policy for the pinned CLI matrix and a red "latest" entry | e2e/doc.go | S | Write the policy next to E2E_PULUMI_VERSIONS. Needs the owner's numbers for "how many pins" and "in what time"; the issue gives none. |
| 1654 | forbidden-error-codes guide: session.csrf vs session.other-site order | docs/reference/forbidden-error-codes.md | S | Change the documented order to the real order, in the docs only. Do not touch sessionauth. Check the real order on main first. |
| 2581 | internal/api helper layering is implicit | .golangci.yml | S | Add depguard rules that deny upward imports: faultlog < apidesc < refusal < admit, idempotent < problem < route, stategate < tool adapters. Stop and report if lint finds a hit. |
| 2561 | e2e contract: a skip of one run excuses a line of another run | e2e/contract_test.go (skippedScenarios, line 74), e2e/harness.go if Run has no mode field | M | Match the skip to the run that recorded it. Add a test with a mixed directory. |
| 2484 | Runner management API: validation errors name no field | pkg/runner/runner.go, internal/api/runnerapi/errors.go, docs/reference/runner-api.md | M | Wrap runner.ErrInvalid with the field and the rule (name, purpose, lease, concurrency, ttl). Keep status 400 and code runner.invalid. Test each text. |
| 2580 | e2e/e2e_test.go split (pure moves) | e2e/e2e_test.go, new e2e/e2e_masked_test.go | M | Group 1: move checkMasked*, maskedState*, maskedFilter*, checkDeltaMasked* (42 functions) to e2e_masked_test.go, same build tags, no body edit. Later groups touch the same file, so run them one at a time. |

### Batches

No two issues in one batch touch the same file.

- Batch 1: 2534, 986, 985, 1654, 2581, 2561, 2484, 2580 (group 1)
- Batch 2: 2051 (after the owner gives the numbers), 2580 (group 2 and later, one per run)

2580 shares e2e/e2e_test.go with nothing else in batch 1. 2561 and 2051 touch other
e2e files, so they stay out of the same run as 2580 only if the lane edits e2e/harness.go.

## STALE

| # | Evidence |
|---|----------|
| 2586 | .golangci.yml has `default: all` and no `bidichk` in the disable list, so bidichk is already on. Run golangci-lint to confirm no hit, then close. |
| 1048 | The text "not homed in this region" is not in internal/ or pkg/ on main. The adapters changed. Verify the 421 text, then close. |

## OTHER

All other open issues (527). Not detailed, as asked. Notes on borderline ones that I
kept out of CHEAP:

- 2578, 2599, 2576, 2485, 2591, 2602: security, auth, runner exchange or pkg/dustpan/dustpan.go.
- 1208: baseline SQL schema. 1852, 1330, 1282: API shape the console reads.
- 1017, 1038: need a port change in pkg/stack and the store.
- 1472: gate service (authorization). 1857, 2015: test files not located; store suites take 60 min.
- 2603: mixes a passkey route with CLI texts. Split it before any cheap run.
