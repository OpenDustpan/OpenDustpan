---
version: 1
---

Issue: #1084.

## Problem

Operator runs a server that parses attacker-influenced proxy headers and SigV4 input. A panic in those parsers risks a crash. Seed-corpus tests miss the panic, so bounded fuzz runs in CI catch it before release.

## Evidence

- A native Go fuzz target file exists for gate. Proof: Pack path exists: pkg/gate/fuzz_test.go
- The trusted-proxy header parser exists at internal/clientip/clientip.go. Proof: Pack path exists: internal/clientip/clientip.go
- The hand-written S3 SigV4 file exists at pkg/blob/s3/sigv4.go. Proof: Pack path exists: pkg/blob/s3/sigv4.go
- The source pack and checkout code lives in pkg/source. Proof: Pack path exists: pkg/source
- OSS-Fuzz onboarding is blocked until the repo is public. Proof: Issue #1084: OSS-Fuzz accepts **public** open-source projects only, so this part is blocked until the repo is public.

## Fix boundary

- Add FuzzClientIP in the NEW file internal/clientip/fuzz_test.go for internal/clientip/clientip.go
- Add FuzzSigV4Canonical in the NEW file pkg/blob/s3/fuzz_test.go for pkg/blob/s3/sigv4.go
- Add FuzzSourcePack in the NEW file pkg/source/fuzz_test.go for pkg/source
- Run each target in the NEW short fuzz CI job with a 60s bound on each pull request plus a nightly long run; fail on crash and commit the crashing input as a seed

Out of scope:

- OSS-Fuzz build integration and continuous fuzzing until the repo is public
- Fuzz target for git credential-helper handling, which has no path in the pack
- Changes to parser behavior; targets assert no panic plus determinism and store crashing input as seeds

## Check

- Add the NEW file internal/clientip/fuzz_test.go with FuzzClientIP over the parser in internal/clientip/clientip.go, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, commit crashing input as a seed Check: `go test ./internal/clientip/ -run FuzzClientIP -count=1`
- Add the NEW file pkg/blob/s3/fuzz_test.go with FuzzSigV4Canonical over the code in pkg/blob/s3/sigv4.go, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, assert determinism, commit crashing input as a seed Check: `go test ./pkg/blob/s3/ -run FuzzSigV4Canonical -count=1`
- Add the NEW file pkg/source/fuzz_test.go with FuzzSourcePack over the pack and checkout path in pkg/source, cap fuzzer input at 4096 bytes, pass full bytes with no trimming, commit crashing input as a seed Check: `go test ./pkg/source/ -run FuzzSourcePack -count=1`
- Wire the NEW short fuzz CI job to run each new target in the NEW file internal/clientip/fuzz_test.go with -fuzztime=60s on each pull request and fail the build on a crash Check: `go test -fuzz=FuzzClientIP -fuzztime=60s ./internal/clientip/` passes
- Extend the NEW short fuzz CI job to run the target in the NEW file pkg/blob/s3/fuzz_test.go with -fuzztime=60s and fail the build on a crash Check: `go test -fuzz=FuzzSigV4Canonical -fuzztime=60s ./pkg/blob/s3/` passes
- Extend the NEW short fuzz CI job to run the target in the NEW file pkg/source/fuzz_test.go with -fuzztime=60s and fail the build on a crash Check: `go test -fuzz=FuzzSourcePack -fuzztime=60s ./pkg/source/` passes
- Add the NEW nightly fuzz schedule that runs each target in the NEW files internal/clientip/fuzz_test.go, pkg/blob/s3/fuzz_test.go, and pkg/source/fuzz_test.go with the long budget and files an issue on a crash Check: Nightly schedule entry present for the NEW short fuzz CI job with 600s total budget
- Update seed corpus in the NEW files internal/clientip/fuzz_test.go, pkg/blob/s3/fuzz_test.go, and pkg/source/fuzz_test.go so seed-only runs pass with no fuzz time Check: `go test ./internal/clientip/ ./pkg/blob/s3/ ./pkg/source/ -count=1`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Server exposes the trusted-proxy header option through WithTrustedProxyHeader. (`pkg/dustpan/area_proxy.go:36 (func WithTrustedProxyHeader)`)
- Pulumijournal keeps corpus order covered by a corpus test. (`internal/pulumijournal/corpus_test.go:124 (func TestCorpusSequenceOrder)`)
- Stack lifecycle is covered by the dustpan server test. (`pkg/dustpan/dustpan_test.go:154 (func TestStackLifecycle)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change onboard to OSS-Fuzz now? No. Issue #1084 states OSS-Fuzz accepts public projects only, so onboarding stays out of scope until the repo is public.
- Which parsers get new fuzz targets? Three parsers with existing pack paths: internal/clientip/clientip.go, pkg/blob/s3/sigv4.go, and pkg/source. Git credential-helper handling has no path in the pack, so it stays out of scope.
- Does the harness trim oversize input before the parser sees it? The harness passes the full fuzzer bytes to the parser entry point. The 4096-byte cap bounds the fuzzer input only. Parser length limits stay under test as parser behavior, not harness trimming.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
