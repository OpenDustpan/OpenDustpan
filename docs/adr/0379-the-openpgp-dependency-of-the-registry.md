# 379. The OpenPGP dependency of the registry

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Changes ADR 316 decision 2: the approved versions of the OpenPGP library are the versions in `go.mod` when this ADR merges.
- Changes ADR 349 decision 2: the server registry uses the versions of this ADR.

## Context

The private registry checks the OpenPGP signatures of provider releases at
upload (ADR 269). The adapter in `pkg/registry/pgpverify` and the fixture in
`internal/registry/pgpfixture` use `github.com/ProtonMail/go-crypto`. That
module needs `github.com/cloudflare/circl` and `golang.org/x/crypto`. RFC 101
section F asks for "its own ADR for the dependency".

ADR 316 decision 2 (owner, 2026-10-20) records the approval: "OpenPGP library
APPROVED (owner, 2026-10-20, after provenance check):
github.com/ProtonMail/go-crypto v1.5.2, github.com/cloudflare/circl v1.6.3,
golang.org/x/crypto >= v0.56.0". ADR 349 decision 2 (owner, 2026-11-07) says
for the server registry: "The versions of ADR 316 decision 2 apply. A new
version or a new library needs a new approval." Issue #1293 asks for an ADR
that records the versions that the build uses and the finding GO-2026-5932.

`go.mod` on `main` on 14 November 2026 has these versions (`go list -m`):

- `github.com/ProtonMail/go-crypto v1.5.2`
- `github.com/cloudflare/circl v1.6.5` (indirect)
- `golang.org/x/crypto v0.58.1-0.20261020153306-9f2b28d4331a`, a
  pseudo-version, not a tagged release. The commit "Update Go to 1.27.3 to
  fix a Go CVE" of 21 October 2026 moved it from v0.56.0.
  `pkg/localuser/argon2id` imports `golang.org/x/crypto/argon2` too.

GO-2026-5932 in the Go vulnerability database is about the package
`golang.org/x/crypto/openpgp`: "unmaintained, unsafe by design, and has known
security issues". The code does not import that package. The linter refuses
an import of it (depguard). `github.com/ProtonMail/go-crypto` is the
maintained replacement that the entry names.

The facts about the removal of that package upstream, read on 14 November
2026:

- On 9 October 2026 the Go team carved `openpgp` out of
  `golang.org/x/crypto` into its own module, and then removed it
  (change 847567, `openpgp: delete module`, golang/go#81227). This came after
  the tag v0.58.0.
- The pseudo-version in `go.mod` is the commit `9f2b28d4331a` of 20 October
  2026, after the removal. Its module has no `openpgp` package.
- v0.59.0, tagged on 11 November 2026, is the first tagged release with no
  `openpgp` package.
- The entry GO-2026-5932 now marks `golang.org/x/crypto` as fixed in
  `v0.58.1-0.20261009181718-2f1f834330b2`. The version in `go.mod` is later.
  `make vuln` on 14 November 2026 reported "No vulnerabilities found."

The project owner answered on 14 November 2026:

```text
4. B (there was a PR recently that removed x/crypto's PGP lib. I don't know if it's tagged yet)
```

Option B is: "Approve the three modules at the versions that `go.mod` has
when the ADR merges, accept the GO-2026-5932 residual risk with a review on
2027-02-14 and a new check on each change of these modules, and make the ADR
owner-tagged." The owner did not choose a rule for later version changes.

## Decision

1. **The three modules are approved at the versions in `go.mod` (owner,
   2026-11-14).** The approved versions are the versions that `go.mod` has
   when this ADR merges: `github.com/ProtonMail/go-crypto v1.5.2`,
   `github.com/cloudflare/circl v1.6.5` and
   `golang.org/x/crypto v0.58.1-0.20261020153306-9f2b28d4331a`.
2. **The owner accepts the residual risk of GO-2026-5932 (owner,
   2026-11-14).** The project owner is the owner of the risk. The review date
   is 2027-02-14. Each change of the version of one of these modules checks
   the risk again, with `make vuln`.
3. **A version change of these modules needs the owner's word (owner,
   2026-11-14).** A change of the version of `github.com/ProtonMail/go-crypto`,
   `github.com/cloudflare/circl` or `golang.org/x/crypto` in `go.mod` needs
   the owner's word. The age rule of ADR 7 applies too.

## Consequences

- The follow-up: move `golang.org/x/crypto` from the pseudo-version to the
  tag v0.59.0, the first tagged release with no `openpgp` package. v0.59.0 is
  two weeks old on 25 November 2026, so the move can come on or after that
  date. It needs the owner's word (decision 3), and a clean `make vuln`.
- The question if a version change in the same major version with a clean
  `make vuln` can go on without the owner's word is open for the owner.
- The finding is not in the output of `make vuln` today. The review on
  2027-02-14 still takes place. A new finding for one of these modules goes
  to the owner.
- The port `SignatureVerifier` of the registry keeps its adapter on
  `github.com/ProtonMail/go-crypto`.
