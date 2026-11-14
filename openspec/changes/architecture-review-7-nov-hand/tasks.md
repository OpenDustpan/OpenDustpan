## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Boundary tests

- [ ] 1.1 Add table test for checkPublicURL in NEW internal/issuersvc/check_public_url_test.go with boundary bytes '@', '[', backquote, '{', '/', ':', 0x7f, 0x80, multi-byte UTF-8, empty, NUL, class edges, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/issuersvc -run TestCheckPublicURLBoundaries` in NEW internal/issuersvc/check_public_url_test.go
- [ ] 1.2 Add table test for isBase64 in NEW internal/sshsig/sshsig_boundary_test.go with the same boundary set plus 'A', 'Z', 'a', 'z', '0', '9', '+', '/', '=', empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/sshsig -run TestIsBase64Boundaries` in NEW internal/sshsig/sshsig_boundary_test.go
- [ ] 1.3 Add table test for validLogin in NEW pkg/forge/github/trust_boundary_test.go with the same boundary set plus letter and digit edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./pkg/forge/github -run TestValidLoginBoundaries` in NEW pkg/forge/github/trust_boundary_test.go
- [ ] 1.4 Add table test for parseURL in NEW internal/ldapauth/config_boundary_test.go with the same boundary set plus scheme, host, and port edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/ldapauth -run TestParseURLBoundaries` in NEW internal/ldapauth/config_boundary_test.go
- [ ] 1.5 Add table test for profile validSegment in NEW pkg/profile/name_boundary_test.go with the same boundary set plus letter and digit edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./pkg/profile -run TestValidSegmentBoundaries` in NEW pkg/profile/name_boundary_test.go
- [ ] 1.6 Add table test for the check at internal/baseurl/baseurl.go in NEW internal/baseurl/baseurl_boundary_test.go with the same boundary set plus scheme, host, and port edges, empty, NUL, and length edges, and prove it catches a one-byte range mutation. Check: `go test ./internal/baseurl -run TestBaseURLBoundaries` in NEW internal/baseurl/baseurl_boundary_test.go

## 2. Fuzz oracles

- [ ] 2.1 Add FuzzCheckPublicURL in NEW internal/issuersvc/check_public_url_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, covering trimming, scheme, host, and port with errors as reject, and run it 30 s on demand. Check: `go test ./internal/issuersvc -run FuzzCheckPublicURL` seed corpus passes in NEW internal/issuersvc/check_public_url_test.go
- [ ] 2.2 Add FuzzIsBase64 in NEW internal/sshsig/sshsig_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, and run it 30 s on demand. Check: `go test ./internal/sshsig -run FuzzIsBase64` seed corpus passes in NEW internal/sshsig/sshsig_boundary_test.go
- [ ] 2.3 Add FuzzValidLogin in NEW pkg/forge/github/trust_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, and run it 30 s on demand. Check: `go test ./pkg/forge/github -run FuzzValidLogin` seed corpus passes in NEW pkg/forge/github/trust_boundary_test.go
- [ ] 2.4 Add FuzzParseURL in NEW internal/ldapauth/config_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, covering host, port, and scheme with errors as reject, and run it 30 s on demand. Check: `go test ./internal/ldapauth -run FuzzParseURL` seed corpus passes in NEW internal/ldapauth/config_boundary_test.go
- [ ] 2.5 Add FuzzValidSegment in NEW pkg/profile/name_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, and run it 30 s on demand. Check: `go test ./pkg/profile -run FuzzValidSegment` seed corpus passes in NEW pkg/profile/name_boundary_test.go
- [ ] 2.6 Add FuzzBaseURL in NEW internal/baseurl/baseurl_boundary_test.go with a regexp oracle compiled in the test only, anchored with \A and \z over bytes, and run it 30 s on demand. Check: `go test ./internal/baseurl -run FuzzBaseURL` seed corpus passes in NEW internal/baseurl/baseurl_boundary_test.go

## 3. Definition of done

- [ ] 3.1 Run lint and unit tests including the fuzz seed corpus, change no production file, and add no docs page. Confirmed by: the driver's gates. Check: `make check` passes with zero findings and no production file changed
