## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Measurement and fix

- [ ] 1.1 Record anonymized real provider states to NEW internal/pulumijournal/testdata/real-states.jsonl with CLI version and provider list, and report the distribution in internal/pulumijournal/corpus_test.go. Check: TestCorpusStateSizeDistribution in internal/pulumijournal/corpus_test.go fails on main and passes after, reporting p50, p95, p99, p1 size and largest per-update total. No route: the test reads the NEW corpus file only.
- [ ] 1.2 Set MaxNewStateBytesPerUpdate and the count bound in internal/pulumijournal/bounds.go by the fixed formula and cite the corpus file and date in each comment. Check: TestNewStateBoundEqualsFormula in internal/pulumijournal/bounds_test.go fails on main and passes after. No route: the test reads the corpus file and the constant only.
- [ ] 1.3 Cover exactly equal acceptance, one byte under acceptance, one byte over rejection, and verifier and replayer agreement. Check: TestVerifierAndReplayerAgreeOnNewStatePatches in internal/pulumijournal/agreement_test.go passes with exactly equal, one byte under, and one byte over cases. No route: the test replays journal bytes in memory only.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Exercise the existing replay check through ANY /api/stacks/{}/{}/{}/update/{}/checkpoint with a personal access token. Check: Servertest against ANY /api/stacks/{}/{}/{}/update/{}/checkpoint with a personal access token still enforces the bound.
- [ ] 3.2 Write the journal bounds docs page at NEW docs/guides/journal-bounds.md in the same pull request. Check: NEW docs/guides/journal-bounds.md lists the corpus source, the distribution and the tuned values.
- [ ] 3.3 Run the Developer path from the docs page alone and run full checks. Confirmed by: CI (make check). Check: `make check` with zero findings.
