---
version: 1
---

Issue: #2578.

## Problem

An embedder reuses the server validators and must trust their ASCII ranges. Inline loops with no direct tests make an off-by-one a bypass risk in security parsers.

## Evidence

- The five named validators exist at the reported files. Proof: internal/issuersvc/issuersvc.go:138 (func checkPublicURL); internal/sshsig/sshsig.go:765 (func isBase64); pkg/forge/github/trust.go:26 (func validLogin); internal/ldapauth/config.go:264 (func parseURL); pkg/profile/name.go:71 (func validSegment)
- A duplicate validSegment exists in blob and gate packages. Proof: pkg/blob/blob.go:110 (func validSegment); pkg/gate/scope.go:197 validSegment reports whether s can be a profile segment

## Fix boundary

- Add boundary table tests for checkPublicURL, isBase64, validLogin, parseURL, profile validSegment, and the check at internal/baseurl/baseurl.go with bytes '@', '[', backquote, '{', '/', ':', 0x7f, 0x80, multi-byte UTF-8, empty input, NUL, class edges, and length edges.
- Add fuzz targets whose oracle is an equivalent regexp compiled in the test only, anchored with \A and \z over bytes, with URL oracles covering trimming, scheme, host, and port.
- Keep production behavior unchanged; defer the shared helper.

Out of scope:

- Refactor of validators to a shared ident helper; deferred so tests land first
- Behavior change of any validator
- Duplicates validSegment in pkg/blob and pkg/gate; not retargeted in this change

## Check

- Add table test for checkPublicURL in NEW internal/issuersvc/check_public_url_test.go with boundary bytes '@', '[', backquote, '{', '/', ':', 0x7f, 0x80, multi-byte UTF-8, empty, NUL, class edges, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/issuersvc -run TestCheckPublicURLBoundaries` in NEW internal/issuersvc/check_public_url_test.go
- Add table test for isBase64 in NEW internal/sshsig/sshsig_boundary_test.go with the same boundary set plus 'A', 'Z', 'a', 'z', '0', '9', '+', '/', '=', empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/sshsig -run TestIsBase64Boundaries` in NEW internal/sshsig/sshsig_boundary_test.go
- Add table test for validLogin in NEW pkg/forge/github/trust_boundary_test.go with the same boundary set plus letter and digit edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./pkg/forge/github -run TestValidLoginBoundaries` in NEW pkg/forge/github/trust_boundary_test.go
- Add table test for parseURL in NEW internal/ldapauth/config_boundary_test.go with the same boundary set plus scheme, host, and port edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/ldapauth -run TestParseURLBoundaries` in NEW internal/ldapauth/config_boundary_test.go
- Add table test for profile validSegment in NEW pkg/profile/name_boundary_test.go with the same boundary set plus letter and digit edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./pkg/profile -run TestValidSegmentBoundaries` in NEW pkg/profile/name_boundary_test.go
- Add table test for the check at internal/baseurl/baseurl.go in NEW internal/baseurl/baseurl_boundary_test.go with the same boundary set plus scheme, host, and port edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/baseurl -run TestBaseURLBoundaries` in NEW internal/baseurl/baseurl_boundary_test.go
- Add FuzzCheckPublicURL in NEW internal/issuersvc/check_public_url_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, covering trimming, scheme, host, and port with errors as reject, and run it 30 s on demand. Check: `go test ./internal/issuersvc -run FuzzCheckPublicURL` seed corpus passes in NEW internal/issuersvc/check_public_url_test.go
- Add FuzzIsBase64 in NEW internal/sshsig/sshsig_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, and run it 30 s on demand. Check: `go test ./internal/sshsig -run FuzzIsBase64` seed corpus passes in NEW internal/sshsig/sshsig_boundary_test.go

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- checkPublicURL trims raw as the server does for its public URL and checks it (`internal/issuersvc/issuersvc.go:138 (func checkPublicURL)`)
- isBase64 reports whether s holds only characters of standard base64 (`internal/sshsig/sshsig.go:765 (func isBase64)`)
- validLogin checks a login of GitHub with an allow list (`pkg/forge/github/trust.go:26 (func validLogin)`)
- parseURL checks the URL of the directory and returns its endpoint (`internal/ldapauth/config.go:264 (func parseURL)`)
- profile validSegment gates a name segment (`pkg/profile/name.go:71 (func validSegment)`)
- blob validSegment gates a name segment duplicate (`pkg/blob/blob.go:110 (func validSegment)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change touch an accepted ADR decision? No. The issue names no ADR and the pack lists no ADR. The fix adds direct tests only and changes no decision.
- Does this change alter validator behavior or add a compatibility shim? No. Production behavior must not change. The new table tests and fuzz oracles prove byte-for-byte parity with the current validators.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
