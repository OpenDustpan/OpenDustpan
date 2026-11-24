## Context

The sign-in service (`internal/signinsvc`) runs one order for OIDC, plain OAuth, CAS, LDAP and passkeys. The port `session.RedirectMethod` (`pkg/session/method.go:275`) already has the SAML fields: the field Fresh of `session.BeginInput` means ForceAuthn (`:207`), the field RequestID of `session.PendingRedirect` keeps the request ID (`:228`), and the field SAMLResponse of `session.Callback` holds the response (`:266-268`). The conformance suite names 23 SAML cases and marks them unwritten (`pkg/session/sessiontest/method_redirect.go:86-87`, `:95-118`). No adapter exists, and `pkg/session/providerconfig.go:179-183` refuses the mode `saml`.

This change adds the adapter `internal/samlauth`, its storage, its routes, its tests and the differential test.

## Owner decisions that apply

- **Owner decision, 24 Nov 2026: keep SAML, with heavy testing and fuzzing.** This is the reason for the size of the test part of this change: four fuzz targets, the attack corpus, one regression test for each known advisory, a hostile table for each layer, and the differential test against independent validators.
- **Owner decision, 24 Nov 2026: the extra validators, with all their additional dependencies, run in CI only; none of them go in the repository's go.mod.** The validators run in pinned container images and one nested Go module (`test/samldiff/crewjam/go.mod`). The root `go.mod` gets the three modules of ADR 285 and their two indirect modules only.
- **ADR 285 (owner, 31 Oct 2026)** approved `github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig` and `github.com/beevik/etree`.
- **ADR 321 decision 21 (owner, ADR 285):** "The checks of decision 14 hold as if the library failed."
- **ADR 332 (owner, 2026-11-07)** decisions 1, 3, 4 and 5 for IdP-started SAML.
- **ADR 297, owner decision (b):** "The sign-in request cookie is `__Host-dustpan_signin` with `SameSite=Lax`."
- **ADR 250 (owner):** defense in depth.

## Contract

| Route | Auth | Body or query | Answer | Errors |
|---|---|---|---|---|
| `GET /api/server/sign-in/{provider}/saml/metadata` (NEW) | public | none | 200 `application/samlmetadata+xml`: entity ID, ACS URL, the signing want, and the encryption key when `saml.encryptedAssertions` is on | 404 `sign-in provider not found` |
| `GET /api/server/sign-in/{provider}` (exists) | public | `return_to` | 303 to the identity provider with `SAMLRequest`, `RelayState` (HTTP-Redirect); sets `__Host-dustpan_signin` | 404; 502 `identity provider is not available: try again later` |
| `POST /api/server/sign-in/{provider}/saml/acs` (NEW) | public | form: `SAMLResponse`, `RelayState`; body at most 256 KiB | 200 `text/html`: a page that posts the two fields to the callback route of the same origin; `Content-Security-Policy` with the hash of its one script; a "Continue" button | 400 `sign-in failed: start the sign-in again` (body too large, not a form, a field missing or repeated) |
| `POST /api/server/sign-in/{provider}/callback` (NEW method on the path of `internal/api/server/signin.go:47`) | public; `Origin` must be the origin of the server | form: `SAMLResponse`, `RelayState`; body at most 256 KiB | 303 to `return_to`, sets the session cookie | 400 `sign-in failed: start the sign-in again`; 403 `a request with the session cookie comes from another site` (the text of `sessionauth.ErrOtherSite`) |
| `POST /api/server/sign-in-providers` (exists) | site administrator, fresh | `{"id","mode":"saml","saml":{...}}` | 201 provider with the effective `saml` settings | 400 names the field; 403 |
| `PATCH /api/server/sign-in-providers/{id}` (exists) | site administrator, fresh | `{"saml":{...}}` | 200 | 400; 403 |
| `POST /api/server/sign-in-providers/{id}/saml/metadata` (NEW) | site administrator, fresh | `application/samlmetadata+xml`, at most 1 MiB, with `X-Dustpan-Reason` | 200: the certificates of the upload are accepted | 400 metadata not valid; 403 |
| `GET /api/server/sign-in-providers/{id}/saml/certificates` (NEW) | site administrator | none | 200 `{"accepted":[...],"pending":[...]}`, each with `fingerprint` (SHA-256), `notAfter`, `seenAt` | 403, 404 |
| `POST /api/server/sign-in-providers/{id}/saml/certificates/{fingerprint}/accept` (NEW) | site administrator, fresh | `{"reason":"..."}` | 200 | 400 no reason; 403; 404 no pending certificate |

The NEW `saml` object:

| Field | Type | Default | Rule |
|---|---|---|---|
| `metadataUrl` | string | none | `https:` only; fetched through the egress client. One of `metadataUrl` or an upload is required. |
| `entityId` | string | from the metadata | read-only |
| `subject` | `{"source":"nameid"}` or `{"source":"attribute","name":"..."}` | `nameid` | `nameid` needs the persistent format |
| `nameIDFormat` | string | `urn:oasis:names:tc:SAML:2.0:nameid-format:persistent` | read-only |
| `allowIdpStarted` | bool | `false` | owner, ADR 332 d3 |
| `encryptedAssertions` | bool | `false` | ADR 321 d17 |
| `maxAssertionAge` | int (seconds) | `300` | read-only; ADR 332 d4 |
| `clockSkew` | int (seconds) | `120` | read-only; ADR 321 d14 |
| `signatureAlgorithms`, `digestAlgorithms` | list | the allowlist below | read-only |

No field takes a SHA-1 algorithm. A field the API does not know is a 400.

## Decisions

1. **SP-started first (ADR 332 d1).** The start route makes an `AuthnRequest` with an `ID` of 128 random bits, keeps the ID in the field RequestID of `session.PendingRedirect`, and sends it with HTTP-Redirect (ADR 321 d13). A fresh request sets ForceAuthn="true" (ADR 321 d22).
2. **The cross-site POST and the `Lax` cookie.** The identity provider posts the response from its own origin. A browser does not send a `SameSite=Lax` cookie with a cross-site POST, so the sealed sign-in request (`internal/api/server/signin.go:400`) is not there. ADR 297 owner decision (b) keeps `Lax`. The ACS route therefore does only the shape checks (size, form, one value for each field) and answers a page that posts the same two fields to the callback route of the server. That POST is same-site, so the cookie goes with it. The callback route checks the origin of the request with the same-site check of `internal/sessionauth/sessionauth.go:232` (`sameSite`) before it reads the body, answers 403 and records the refusal with `recordOtherSiteSignIn` (`internal/api/server/signin.go:769`) when it fails, and then runs the full checks. The page is an `html/template` with the fields as hidden inputs, one script whose SHA-256 hash is in the `Content-Security-Policy` of that answer, `Cache-Control: no-store`, and a "Continue" button for a browser with no JavaScript. Nothing in the ACS route reads the XML.
3. **Checks as if the library failed (ADR 321 d14, d21).** The adapter does each of these checks itself. Each check has its own hostile test with a verifier seam that simulates a library that accepts everything (decision 9).
   - The body is at most 256 KiB before the decode (ADR 321 d16). The value is standard base64 only. A value that decodes to compressed data or to more than 256 KiB is refused. The HTTP-POST binding has no compression; the gosaml2 setting MaximumDecompressedBodySize is still set to 256 KiB as a second layer.
   - The XML parser is a guarded reader in front of `etree`: it refuses a `DOCTYPE`, an `ENTITY`, a processing instruction other than the XML declaration, a depth above 64, and more than 20,000 tokens (MaximumXMLTokens). `github.com/mattermost/xml-roundtrip-validator` (through gosaml2) runs as a second layer.
   - The whole document has exactly one element in the SAML assertion namespace with the local name `Assertion` or `EncryptedAssertion`, at any depth.
   - The signature has exactly one `Reference`, and its `URI` is `#` followed by the `ID` of the signed element. The signed element is the `Response` or the `Assertion`. An `ID` that is not unique in the document is refused.
   - Signature algorithms: RSA-SHA256, RSA-SHA384, RSA-SHA512, ECDSA-SHA256, ECDSA-SHA384, ECDSA-SHA512. Digest algorithms: SHA-256, SHA-384, SHA-512. SHA-1 is refused for both (B4). The adapter reads SignatureMethod and DigestMethod from the parsed tree itself and refuses before it calls the library.
   - `KeyInfo` is ignored. The key is only an accepted certificate of the provider (decision 7). A response whose `KeyInfo` names a certificate that is not accepted is checked with the accepted certificates and fails.
   - `Destination` of the `Response` equals the ACS URL. `Recipient` of the SubjectConfirmationData equals the ACS URL. `Audience` equals the entity ID of the server. `Issuer` of the `Response` (when present) and of the `Assertion` equals the entity ID of the provider. `InResponseTo` equals the field RequestID of `session.PendingRedirect`. `NotBefore` and `NotOnOrAfter` of the `Conditions` and `NotOnOrAfter` of the SubjectConfirmationData hold with 120 seconds of skew. The StatusCode is `Success`.
4. **The typed signed assertion (C7).** After the library checks the signature, the adapter takes the canonical bytes of the signed element that the library returns, and parses them again, as a new document, with the same guarded reader. It fills the NEW type `samlauth.signedAssertion` from that document only. The type has unexported fields (`id`, `issuer`, `subject`, `nameIDFormat`, `audience`, `recipient`, `inResponseTo`, `notBefore`, `notOnOrAfter`, `authnInstant`, `attributes`) and one constructor, `newSignedAssertion(canonical []byte, ...)`, that refuses a missing field. Every later check and the subject read take a `signedAssertion`. No function that takes the original tree returns a value that becomes part of an identity. This makes the signature-wrapping class (a value read from an unsigned copy) a compile error, not a check.
5. **Replay (ADR 321 d15, consequence).** A NEW table `used_assertions` (`provider_id`, `digest` of the assertion ID, `until`) in both dialects, with a NEW port `session.UsedAssertions` and the NEW interface `session.AssertionStore`, in NEW file `pkg/session/usedassertions.go`, and the NEW conformance suite `sessiontest.RunAssertionStore` in NEW file `pkg/session/sessiontest/assertionstore.go`. They follow `session.UsedTickets` (`pkg/session/usedtickets.go:24`) and `sessiontest.RunTicketStore`. `until` is the `NotOnOrAfter` of the assertion. An assertion with no `NotOnOrAfter`, or one more than 1 hour after the receipt, is refused. The insert and the check are one statement in one transaction; a second use is `session.ErrReplay`. A store error fails closed. ADR 320 (`:86-91`) says that a method keeps its own one-use records, so the adapter calls the store itself.
6. **Subject (ADR 321 d18).** The default is the persistent `NameID`. The administrator can name an attribute instead. A transient, email or unspecified `NameID` is refused as a subject. The subject binding is `saml:` followed by the entity ID of the provider and the value. Groups are out of scope (ADR 332 d6-8 is a separate change).
7. **Signing certificates (ADR 321 d20).** The server keeps the accepted certificates of each provider. A fetch of the metadata URL that finds a certificate that is not accepted stores it as pending, writes an audit entry with the NEW reason `sign-in.certificate-pending` and raises an alert through `session.AlertStore` (`pkg/session/service.go:125`), and the accepted certificates stay in use. A site administrator accepts a pending certificate with a fresh authentication and a reason. An upload of the metadata is the acceptance of its certificates. A response signed only by a pending certificate is refused with the reason `sign-in.certificate-pending`.
8. **IdP-started SAML (ADR 332 d3, d4, d5).** When `allowIdpStarted` is `false`, a response with no `InResponseTo`, or a callback with no pending request, is refused with the reason `sign-in.unsolicited`. When it is `true`, the callback accepts a response with no `InResponseTo` and no pending request only after the four checks: the assertion ID is one-time (decision 5), the IssueInstant of the assertion is not older than 300 seconds, the `Audience` check passes, and the `Recipient` check passes. A response with an `InResponseTo` always needs the matching pending request. The service never marks an IdP-started sign-in as fresh. The port doc of the field Pending of `session.Callback` (`pkg/session/method.go:254-256`) gets this case: a SAML method with the setting on may accept a nil `Pending` for a response with no `InResponseTo`, and the result is never fresh.
9. **Verifier seam (C4).** The adapter calls the library through a NEW unexported interface `verifier` with one method, `verify(doc *etree.Document) (canonical []byte, err error)`. Production uses the gosaml2 verifier. Tests inject `acceptAll`, which returns the bytes of an element the test chooses, to show that each adapter check refuses on its own.
10. **Encrypted assertions (ADR 321 d17, B6).** Off by default. When `encryptedAssertions` is `false`, an `EncryptedAssertion` is refused with the reason `sign-in.encrypted-off` before any decryption. When it is `true`, the server decrypts with a key of the installation that the secret package seals, and publishes its certificate in the service provider metadata. Key transport: RSA-OAEP only. Content: AES-128-GCM and AES-256-GCM only; CBC is refused. A `CipherValue` shorter than the GCM nonce and tag is refused before the library sees it. The decrypted assertion goes through the same guarded reader, the same count and the same signature check.
11. **Errors.** Every callback refusal answers 400 with the one text of `internal/api/server/signin.go:61`, as `classifySignIn` maps it (`:739-740`), and writes the reason to the audit entry. The NEW codes go in `internal/errcode/catalog_signin.go` and are the audit reasons, as the sign-in-methods change does with its codes: `sign-in.unsolicited`, `sign-in.encrypted-off`, `sign-in.certificate-pending`. The NEW sentinels `samlauth.ErrUnsolicited`, `samlauth.ErrEncryptedOff` and `samlauth.ErrCertificatePending` wrap `session.ErrMethodAnswer`, and `answerReason` (`internal/signinsvc/refuse.go:128-150`) maps them before the `ReasonAnswer` case. The other reasons exist: `signinsvc.ReasonAnswer`, `signinsvc.ReasonReplay`, `signinsvc.ReasonBinding` (`internal/signinsvc/service.go:70-75`).
12. **Single logout (B5).** Out of scope (ADR 321 d23). A `LogoutRequest` or `LogoutResponse` at the callback is refused as a document whose root is not `Response`.
13. **Libraries and pins (B1).** `github.com/russellhaering/gosaml2` v0.12.0, `github.com/russellhaering/goxmldsig` v1.6.1 and `github.com/beevik/etree` v1.8.1 in the root `go.mod`. They bring two indirect modules, which the pull request body names: `github.com/jonboulle/clockwork` v0.5.0 and `github.com/mattermost/xml-roundtrip-validator` v0.1.0. `make trivy` and `make licenses` run on them.

## Advisories and their regression tests (C3)

Each test is named for the behavior. The advisory IDs are only here and in the pull request body, not in the code.

| Advisory | Behavior | Test |
|---|---|---|
| CVE-2026-33487 (goxmldsig) | A signature whose `Reference` points to a different element than the one the caller reads is refused. | `TestAReferenceToAnotherElementIsRefused` |
| GHSA-5hj8-q7fv-qrj6 (gosaml2) | A response with a short GCM `CipherValue` is refused with no panic. | `TestAShortCipherValueIsRefusedWithoutAPanic` |
| GHSA-pcgw-qcv5-h8ch (gosaml2) | A compressed body that expands above the limit is refused before it expands fully. | `TestACompressedBodyAboveTheLimitIsRefused` |
| CVE-2022-41912 (gosaml2) | A response with two assertions, one signed and one not, is refused. | `TestTwoAssertionsAreRefused` |
| CVE-2020-29509 (encoding/xml) | An element or attribute name with a colon that does not round-trip is refused. | `TestANameThatDoesNotRoundTripIsRefused` |

## Attack corpus (C2)

The corpus is in NEW directory `internal/samlauth/testdata/attacks/`, one XML file for each case, built at test time from a template and the fakecred keys (no key in the repository). Each case is a row of `TestAttackCorpusIsRefused` and a seed of `FuzzVerifyAndExtract`.

- Signature wrapping XSW1 to XSW8 (the eight placements of a copy of the signed element).
- A comment inside the `NameID` (the text before the comment differs from the text after).
- Two `Assertion` elements; an `Assertion` and an `EncryptedAssertion`; an `Assertion` in `Extensions`.
- A `Reference` with an empty `URI`, a `URI` to another `ID`, two `Reference` elements, a duplicate `ID`.
- A signed `Response` with an unsigned `Assertion`; a signed `Assertion` in an unsigned `Response` with a changed `Destination`; no signature.
- `rsa-sha1` signature, `sha1` digest, an unknown algorithm, `HMAC`.
- `KeyInfo` with an attacker certificate.
- A `DOCTYPE`, an external entity (XXE), billion laughs, depth 65, 20,001 tokens, a body of 256 KiB plus 1 byte, a deflate bomb.
- A GCM `CipherValue` of 1 byte; CBC content encryption.
- Expired `NotOnOrAfter`; `NotBefore` in the future by 121 seconds; no `NotOnOrAfter`; `NotOnOrAfter` 1 hour and 1 second after receipt; a replayed assertion ID; an IdP-started assertion of 301 seconds.
- Wrong `Destination`, wrong `Recipient`, wrong `Audience`, wrong `Issuer`, wrong `InResponseTo`, missing `InResponseTo` with the setting off.

## Fuzz targets (C1)

NEW file `internal/samlauth/fuzz_test.go`, seeds in `internal/samlauth/testdata/fuzz/<target>/`.

| Target | Input | Invariants |
|---|---|---|
| `FuzzDecodeResponse` | the form value | no panic; an accept gives at most 256 KiB; any non-base64 or compressed input is refused |
| `FuzzParseXML` | bytes | no panic; an accept has no `DOCTYPE`, depth at most 64, at most 20,000 tokens; parse then write then parse gives the same tree |
| `FuzzVerifyAndExtract` | a mutation of a signed response | no panic; an accept gives a `signedAssertion` whose ID is the `ID` of an element whose canonical bytes verify with an accepted key; a mutation of a signed byte never accepts |
| `FuzzParseMetadata` | bytes | no panic; an accept gives only certificates with a key of an allowed type and size, and no SHA-1 |

`make check` runs `make fuzz-saml`: each target for 10 seconds (`go test -run '^$' -fuzz '^FuzzX$' -fuzztime 10s ./internal/samlauth/`), 40 seconds of fuzzing in total. The long weekly run goes into `.github/workflows/fuzz.yml` in a separate tooling change.

## Differential test (C8)

The target `make saml-diff` (not in `make check`) runs the driver `test/samldiff` (build tag `samldiff`). The driver makes each case (the attack corpus, the fuzz seeds, the crashers, and a time-boxed fuzz run of 5 minutes), runs the OpenDustpan adapter in process, and sends the case to each validator as one JSON line on its standard input:

```json
{"case":"xsw3","response":"<base64>","acsURL":"https://sp.test/acs","audience":"https://sp.test","idpEntityID":"https://idp.test","certificates":["<PEM>"],"requestID":"id-1","now":"2026-11-24T10:00:00Z","decryptionKey":null}
```

Each validator answers one JSON line: `{"case":"xsw3","verdict":"accept"|"refuse","nameID":"...","assertionID":"...","error":"..."}`. Each validator overrides its clock with `now`. Each container runs as `docker run -i --rm --network none <image>@sha256:<digest>`.

Validators (each pin to be fixed by task 0.x where the table says so):

| Validator | Version and pins | License | Pin source |
|---|---|---|---|
| crewjam/saml | `github.com/crewjam/saml` v0.6.3, in `test/samldiff/crewjam/go.mod` | BSD-2-Clause | proxy.golang.org, 2026-11-08 |
| python3-saml | `python3-saml` 1.16.0, `xmlsec` 1.3.18, `lxml` 6.1.3 (built from source against the same libxml2), `isodate` 0.7.2 | MIT, MIT, BSD-3-Clause, BSD-3-Clause | PyPI; the dates are 2023-10-09, 2026-10-29, 2026-09-02, 2024-10-08 |
| xmlsec1 (signature only) | Debian trixie `xmlsec1` 1.2.41-1, `libxml2` 2.12.7+dfsg+really2.9.14-2.1+deb13u3 | MIT | Debian trixie |
| OpenSAML | `opensaml-saml-impl` 5.2.3 | to be confirmed by task 0.3 (expected Apache-2.0) | build.shibboleth.net, 2026-06-18 |
| ruby-saml | `ruby-saml` 1.18.1, `nokogiri` 1.19.7, `rexml` 3.4.4 | MIT, MIT, BSD-2-Clause | RubyGems; 2025-07-29, 2026-11-02, 2025-09-10 |
| node-saml | `@node-saml/node-saml` 5.2.0, `xml-crypto` 6.4.0, `@xmldom/xmldom` 0.8.15, `xpath` 0.0.35, `xml-encryption` 3.1.0, `xml2js` 0.6.2, `xmlbuilder` 15.1.1, `@xmldom/is-dom-node` 1.0.1 | MIT (all) | npm; lockfile made with `npm install --before` 14 days back |

Base image digests (Debian trixie slim builds for Python, Ruby, Node, Go and the Java runtime) are to be fixed by task 0.2: the newest build at least 14 days old, with the digest and the push date in the table. The Docker Hub tags were pushed again on the day of this design, so no digest is recorded here.

Mismatch rules:

- The OpenDustpan adapter accepts and any full validator refuses: a failure. This is the class that the differential test exists for.
- The OpenDustpan adapter refuses and all full validators accept: a failure, unless the case is in the fixed, in-code list of stricter-by-design classes: SHA-1, depth above 64, more than 20,000 tokens, a `DOCTYPE`, a transient `NameID` as the subject, CBC content encryption, an IssueInstant older than 300 seconds for an IdP-started assertion, a `KeyInfo` certificate that is not accepted, and a comment inside a `NameID`.
- Validators that disagree with each other: a report line, not a failure.
- xmlsec1 checks the signature only. Its accept does not count for "all accept"; its refuse of a signature that the adapter accepted is a failure.
- A validator that crashes or does not answer in 10 seconds: a failure of the run.

The CI job is a workflow patch: the staging copy is NEW file `test/samldiff/saml-diff.workflow.yml`, as `cmd/s3bench/bench-s3.workflow.yml` is for the S3 bench, and the pull request body holds the diff under "Workflow patch (apply locally)". The fixture keys come from NEW helpers `fakecred.SigningKey` and `fakecred.SigningCertificate` in `internal/fakecred`, which make an RSA-2048 key and a self-signed certificate at run time. No PEM goes into the repository.

## Real identity provider e2e (C6)

Needs approval before task 5.x (the lead approves). The in-process e2e step (ADR 321 consequence, ADR 55) does not need it.

| Candidate | Image | License | Last push | Result |
|---|---|---|---|---|
| Keycloak 26.8.2-1 | `quay.io/keycloak/keycloak@sha256:723b1d6e254c0fc96d5cffec860c7e0744d6a3277822b865bb538cf891575e41` (amd64 `sha256:c126f032d43b941ed7c5ccca7d6571596d8eab84d748afd7c86989e48ef07b8f`, 265,049,401 bytes compressed) | Apache-2.0 | 2026-11-09 (15 days) | chosen; its start time is measured by task 5.1 |
| SimpleSAMLphp (cirrusid/simplesamlphp) | `sha256:ae33d1fa...`, 272 MB | LGPL-2.1 | 2026-10-23 | refused: license |
| kristophjunge/test-saml-idp | no digest in the registry API, 190 MB | not checked | 2018-02-04 | refused: not maintained |
| A Go test IdP | none | n/a | n/a | not independent: it signs with `goxmldsig`. Used for the in-process e2e step only. |

The step runs only with `E2E_SAML_IDP=1`, as `e2e/compose_test.go:51-55` runs the Docker step only with `E2E_COMPOSE=1`.

## Threat model (STRIDE)

| Threat | Example | Guard |
|---|---|---|
| Spoofing | Signature wrapping; a `KeyInfo` attacker certificate; a comment in the `NameID` | Decisions 3, 4, 7 |
| Tampering | A changed `Destination`, `Audience` or `Recipient`; algorithm downgrade | Decision 3 |
| Repudiation | A sign-in with no record | Every accept and refusal writes an audit entry |
| Information disclosure | XXE reads a file | Decision 3: no `DOCTYPE` and no entity |
| Denial of service | Billion laughs, depth, token flood, deflate bomb, short `CipherValue` panic | Decisions 3, 10; the fuzz targets |
| Elevation of privilege | Replay of a captured response; an unsolicited response; a pending certificate | Decisions 5, 7, 8 |

## Layers (defense in depth)

| Layer | Guard | Hostile test |
|---|---|---|
| HTTP boundary (ACS route) | 256 KiB, form shape, one value for each field, no XML read | `TestACSRefusals` |
| HTTP boundary (callback route) | `Origin`, 256 KiB, form shape | `TestSAMLCallbackRefusals` |
| Decode | base64 only, size after decode, no compression | `TestDecodeRefusals`, `FuzzDecodeResponse` |
| Parse | no DTD, no entity, depth 64, 20,000 tokens, round trip | `TestParseRefusals`, `FuzzParseXML` |
| Structure (adapter, verifier seam `acceptAll`) | one assertion, one `Reference` to the signed `ID`, algorithm allowlist, `KeyInfo` ignored | `TestStructureRefusalsWithAnAcceptAllVerifier` |
| Signed values (`signedAssertion`) | audience, recipient, destination, issuer, `InResponseTo`, time | `TestSignedAssertionRefusals` |
| Replay store (port adapter) | one use, ceiling of 1 hour, fail closed | `sessiontest.RunAssertionStore` |
| Service | IdP-started never fresh; reasons to the audit | `TestCompleteSAMLReasons` |
| Domain settings | no SHA-1, `https:` metadata URL, subject rule | `TestValidateSAMLProvider` |
| Certificates | a pending certificate does not verify | `TestAPendingCertificateDoesNotVerify` |

## Structure

- `internal/samlauth` (NEW): the NEW files internal/samlauth/samlauth.go (`New`, `Begin`, `Finish`), internal/samlauth/decode.go, internal/samlauth/xmlguard.go, internal/samlauth/structure.go, internal/samlauth/signed.go (`signedAssertion`), internal/samlauth/verifier.go, internal/samlauth/encrypted.go, internal/samlauth/metadata.go and internal/samlauth/acspage.go; the NEW tests internal/samlauth/hostile_test.go, internal/samlauth/advisory_test.go, internal/samlauth/attacks_test.go, internal/samlauth/fuzz_test.go and internal/samlauth/leak_test.go.
- `internal/samltest` (NEW): the in-process test identity provider for the e2e step and the conformance fixture, as `internal/ldaptest` is for LDAP.
- `pkg/session`: `SAMLSettings` in NEW file `pkg/session/providersaml.go`; `pkg/session/providerconfig.go` lets `saml` through with valid settings; the ports of decision 5.
- Store: the mode CHECK of `sign_in_providers` allows `saml`; NEW columns for the `saml` settings; NEW tables `used_assertions` and `sign_in_provider_certificates`; both baselines change in place (no tag shipped a schema, `internal/store/AGENTS.md`).
- HTTP: the routes of the contract in `internal/api/server/signin.go` and `internal/api/server/providers.go`; NEW `samlOperationLabels()` in `internal/api/server/operations.go`, joined by `joinLabels`.
- Build: `pkg/dustpan/storedproviders.go` builds a `samlauth.Method` with the egress client and the assertion store.

## Risks

- XML signature libraries have a history of bypasses. Mitigation: the checks of decision 3 as if the library failed, the typed signed assertion, the fuzz targets and the differential test.
- The ACS page adds a step in the browser. Mitigation: no JavaScript is needed (the "Continue" button), and the page holds only the two fields that the browser already holds.
- A validator image may change behavior when its pin moves. Mitigation: pins by digest, moved only by a reviewed change.
