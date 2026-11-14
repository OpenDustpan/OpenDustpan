## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fuzz targets

- [ ] 1.1 Add the NEW file internal/clientip/fuzz_test.go with FuzzClientIP over the parser in internal/clientip/clientip.go, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, commit crashing input as a seed Check: `go test ./internal/clientip/ -run FuzzClientIP -count=1`
- [ ] 1.2 Add the NEW file pkg/blob/s3/fuzz_test.go with FuzzSigV4Canonical over the code in pkg/blob/s3/sigv4.go, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, assert determinism, commit crashing input as a seed Check: `go test ./pkg/blob/s3/ -run FuzzSigV4Canonical -count=1`
- [ ] 1.3 Add the NEW file pkg/source/fuzz_test.go with FuzzSourcePack over the pack and checkout path in pkg/source, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, commit crashing input as a seed Check: `go test ./pkg/source/ -run FuzzSourcePack -count=1`

## 2. CI wiring

- [ ] 2.1 Wire the NEW short fuzz CI job to run each new target in the NEW file internal/clientip/fuzz_test.go with -fuzztime=60s on each pull request and fail the build on a crash Check: `go test -fuzz=FuzzClientIP -fuzztime=60s ./internal/clientip/` passes
- [ ] 2.2 Extend the NEW short fuzz CI job to run the target in the NEW file pkg/blob/s3/fuzz_test.go with -fuzztime=60s and fail the build on a crash Check: `go test -fuzz=FuzzSigV4Canonical -fuzztime=60s ./pkg/blob/s3/` passes
- [ ] 2.3 Extend the NEW short fuzz CI job to run the target in the NEW file pkg/source/fuzz_test.go with -fuzztime=60s and fail the build on a crash Check: `go test -fuzz=FuzzSourcePack -fuzztime=60s ./pkg/source/` passes
- [ ] 2.4 Add the NEW nightly fuzz schedule that runs each target in the NEW files internal/clientip/fuzz_test.go, pkg/blob/s3/fuzz_test.go, and pkg/source/fuzz_test.go with the long budget and files an issue on a crash Check: Nightly schedule entry present for the NEW short fuzz CI job with 600s total budget

## 3. Definition of done

- [ ] 3.1 Update seed corpus in the NEW files internal/clientip/fuzz_test.go, pkg/blob/s3/fuzz_test.go, and pkg/source/fuzz_test.go so seed-only runs pass with no fuzz time Check: `go test ./internal/clientip/ ./pkg/blob/s3/ ./pkg/source/ -count=1`
- [ ] 3.2 Run the definition of done against the NEW file pkg/source/fuzz_test.go: wiring, bounded runs, docs page, usability check from the docs page alone, make check with zero findings; quarantine a flaky fuzz failure and file an issue instead of blocking unrelated work Check: `make check` with zero findings
