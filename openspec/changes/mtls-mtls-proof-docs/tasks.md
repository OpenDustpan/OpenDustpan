## 1. Failing e2e first

- [ ] 1.1 Add failing e2e step: valid SPIFFE ID exchange plus pulumi up and tofu apply with the trust token. Check: e2e TestMtlsProofPulumiTofu fails on main, passes with the change.
- [ ] 1.2 Add failing e2e refusals: foreign CA, expired certificate, unmatched ID, no certificate, two SANs, other trust domain, wrong path. Check: e2e TestMtlsProofRefusals lists foreign CA, expired, unmatched ID and no-certificate 401 cases with audit entries and body checks.

## 2. Definition of done

- [ ] 2.1 Wire the e2e step with test CA, SPIFFE ID certificate, trust bundle and trust rule fixtures. Check: `make e2e MTLS=1` runs the new step against the real binary over TLS.
- [ ] 2.2 Write the mTLS docs page with bundle, rule, exchange, CLI use and TLS proxy rule. Check: `grep -r mtls docs/guides` shows the new page and each shell block runs as written.
- [ ] 2.3 Run a usability check and record where the path costs time. Check: A usability run by the persona from the docs page alone reaches both CLI runs.
- [ ] 2.4 Pass the definition of done: wiring, route actions unchanged, e2e step, docs page, usability check, make check. Check: `make check` reports zero findings.
