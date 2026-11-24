---
version: 1
---

Issue: #4069.

## Why

**Persona: site administrator.** A site administrator runs OpenDustpan for a team whose people sign in at a SAML 2.0 identity provider. The administrator wants to add that provider through the REST API, and the people of the team want to sign in to the console with it, make an access token, and run `pulumi up` and `tofu apply`.

**Persona: developer.** A developer clicks the provider on the sign-in page, signs in at the identity provider, and comes back to the console with a session.

Today the administrator cannot do this. `pkg/session/providerconfig.go:179-183` refuses a provider in the mode `saml` with "the mode \"saml\" has no sign-in method in this version". The sign-in-methods change lists SAML as out of scope until the owner decided on the XML signature dependency (`openspec/changes/sign-in-methods/proposal.md:43`). The owner decided on 24 Nov 2026: keep SAML, with heavy testing and fuzzing.

The user path this change must make work:

1. The administrator gets the service provider metadata of the provider from `GET /api/server/sign-in/{provider}/saml/metadata` (NEW) for the provider ID `corp-saml` and gives it to the identity provider.
2. The administrator creates the provider with `POST /api/server/sign-in-providers`, mode `saml`, with the metadata URL of the identity provider (or an upload of the metadata) and the subject rule.
3. The server reads the signing certificates from the metadata. At the create, the administrator accepts them, as the upload is an acceptance.
4. A developer opens `GET /api/server/sign-in/{provider}`. The server makes a NEW SAML `AuthnRequest` message, keeps its ID in the sealed sign-in request cookie, and redirects with HTTP-Redirect.
5. The identity provider posts the `SAMLResponse` to `POST /api/server/sign-in/{provider}/saml/acs` (NEW). The server answers a page that posts the same fields to `POST /api/server/sign-in/{provider}/callback` (NEW method) from the origin of the server, so that the `SameSite=Lax` sign-in request cookie goes with it.
6. The callback checks that the POST comes from the origin of the server, checks the response and opens a console session. The developer makes a token and runs the two tools.

Errors and the one command that helps. Every refusal of a callback gives the one generic answer of main: 400 with the text "sign-in failed: start the sign-in again" (`internal/api/server/signin.go:61`, mapped at `:739-740`). The audit entry holds the reason. The administrator reads the reason with the audit list.

| What fails | HTTP answer | Audit reason | The command that helps |
|---|---|---|---|
| The response fails a check (signature, structure, audience, issuer, time) | 400 `sign-in failed: start the sign-in again` | "identity provider answer failed a check" (`signinsvc.ReasonAnswer`) | `opendustpan audit list --action sign-in.refused` |
| The assertion ID was used before | 400 `sign-in failed: start the sign-in again` | "sign-in value was used before" (`signinsvc.ReasonReplay`) | `opendustpan audit list --action sign-in.refused` |
| The `InResponseTo` is not the request ID of the browser | 400 `sign-in failed: start the sign-in again` | "callback does not match the sign-in request" (`signinsvc.ReasonBinding`) | start the sign-in again from the console |
| An unsolicited response while IdP-started SAML is off | 400 `sign-in failed: start the sign-in again` | `sign-in.unsolicited` (NEW) | `curl -X PATCH .../api/server/sign-in-providers/corp-saml -d '{"saml":{"allowIdpStarted":true}}'` |
| An `EncryptedAssertion` while encrypted assertions are off | 400 `sign-in failed: start the sign-in again` | `sign-in.encrypted-off` (NEW) | `curl -X PATCH .../api/server/sign-in-providers/corp-saml -d '{"saml":{"encryptedAssertions":true}}'` |
| The response is signed by a pending certificate | 400 `sign-in failed: start the sign-in again` | `sign-in.certificate-pending` (NEW) | `curl -X POST .../api/server/sign-in-providers/{id}/saml/certificates/{fingerprint}/accept` (NEW route) |
| The metadata URL does not answer or the egress rules refuse it | 502 `identity provider is not available: try again later` | "identity provider is not available" (`signinsvc.ReasonUnavailable`) | `opendustpan doctor` |
| A provider create with an unknown field, a SHA-1 setting, or no subject rule | 400 that names the field | none | the provider section of `docs/guides/saml-sign-in.md` |

Evidence: the owner decision of 24 Nov 2026; the OSV records of `github.com/russellhaering/gosaml2` and `github.com/russellhaering/goxmldsig` (signature bypass, unsigned logout request, short cipher value panic, deflate bomb); the 23 unwritten SAML cases of the conformance suite.

## What Changes

- **SAML adapter.** A NEW package `internal/samlauth` implements `session.RedirectMethod` for the mode `saml`. It uses `github.com/russellhaering/gosaml2` v0.12.0, `github.com/russellhaering/goxmldsig` v1.6.1 and `github.com/beevik/etree` v1.8.1. These add two indirect modules: `github.com/jonboulle/clockwork` v0.5.0 and `github.com/mattermost/xml-roundtrip-validator` v0.1.0. Nothing else goes into the root `go.mod`.
- **Checks of the response, by structure.** After the library checks the signature, the adapter takes the exact canonical signed bytes, parses them again as a new, standalone document with the same limits, and reads each value only from it, into the NEW type `samlauth.signedAssertion` with unexported fields and a constructor. Nothing outside the adapter sees the original tree. The adapter also does each check of the response itself, as if the library failed: one `Assertion` or `EncryptedAssertion` in the whole document; exactly one signature `Reference`, whose `URI` is the `ID` of the signed element; RSA-SHA256 or stronger and ECDSA-SHA256 or stronger for signatures and SHA-256 or stronger for digests, SHA-1 refused for both; no DTD and no entity; depth at most 64; a token limit and a decompressed size limit on top of the 256 KiB body limit; `Destination`, `Recipient`, `Audience`, `Issuer`, `InResponseTo`, `NotBefore` and `NotOnOrAfter` with 120 seconds of skew.
- **Replay.** A NEW table `used_assertions` in both dialects keeps each assertion ID until its `NotOnOrAfter`, with a ceiling of 1 hour. A second use is `session.ErrReplay`.
- **Subject.** A persistent `NameID` by default, or an attribute that the administrator names. A transient or email `NameID` is refused as a subject.
- **Fresh authentication.** A fresh request sends ForceAuthn="true" and checks the AuthnInstant.
- **Signing certificates.** The server pins the accepted certificates. A certificate that is new in fetched metadata is stored as pending, writes an audit entry and an alert, and does not take effect until a site administrator accepts it with a fresh authentication and a reason. An upload is the acceptance.
- **IdP-started SAML.** A setting of each provider, `saml.allowIdpStarted`, off by default, with its effective value in the API. When it is on, an unsolicited assertion needs the four checks of the owner: a one-time assertion ID, a maximum age of 5 minutes, the `Audience` check and the `Recipient` check. It is never a fresh sign-in.
- **Encrypted assertions.** A setting of each provider, `saml.encryptedAssertions`, off by default, with its effective value in the API. When it is on, the server decrypts with a key of the installation that the secret package seals, and publishes the key in its metadata.
- **Routes.** NEW `GET /api/server/sign-in/{provider}/saml/metadata` (public), NEW `POST /api/server/sign-in/{provider}/saml/acs` (public), NEW `POST /api/server/sign-in/{provider}/callback` (public, same origin only), NEW `GET /api/server/sign-in-providers/{id}/saml/certificates`, NEW `POST /api/server/sign-in-providers/{id}/saml/certificates/{fingerprint}/accept`, NEW `POST /api/server/sign-in-providers/{id}/saml/metadata`. The provider routes `POST /api/server/sign-in-providers` and `PATCH /api/server/sign-in-providers/{id}` take the NEW `saml` object.
- **Tests and fuzzing.** Four native Go fuzz targets with seed corpora under `internal/samlauth/testdata/fuzz`, the attack corpus as table tests and as fuzz seeds, one regression test for each known advisory, one hostile table for each layer, and an injected verifier seam that lets a test simulate a library that fails. `make check` runs a short fuzz run of each target (NEW target `make fuzz-saml`, 10 seconds for each of 4 targets).
- **Differential test (CI only).** A NEW target `make saml-diff`, not part of `make check`, sends the attack corpus, the fuzz corpus and a time-boxed fuzz run through independent validators that run in pinned container images: crewjam/saml (in a nested Go module), python3-saml with xmlsec1, the xmlsec1 command for the signature step, OpenSAML, ruby-saml, and node-saml with xml-crypto. The root `go.mod` and `go.sum` get none of their modules. The CI job is a workflow patch in the pull request body.
- **Docs and e2e.** A NEW page `docs/guides/saml-sign-in.md`, an e2e step with an in-process test identity provider, and a usability check. An e2e sign-in against a real SAML identity provider container is a separate task group that waits for the approval of the lead.

## Out of scope

- Single logout (SLO). A console session ends after 12 hours at most.
- SAML group attributes and their mapping. The change that implements the group mapping rule of the owner (ADR 332 decisions 6 to 8) adds them for SAML, as for CAS and the trusted header.
- The long scheduled fuzz job: the new targets go into the matrix of `.github/workflows/fuzz.yml` in a separate tooling change.
- A console screen for the SAML settings. The console uses the REST API in its own repository.
- The SAML artifact binding, the SOAP binding and the HTTP-POST binding for the request.

## Depends on

- The sign-in-methods change, for the shape of `pkg/session/providerconfig.go:179-183` after it lets the CAS and header modes through, and for the callback fields at `internal/api/server/signin.go:544`. Task 0.1 checks both lines on the main of the day.

## Usability

- The administrator copies the service provider metadata from one public URL, and gives the identity provider metadata as a URL or an upload.
- The provider answer shows the effective value of each setting, defaults too: `allowIdpStarted`, `encryptedAssertions`, `subject`, `nameIDFormat`, the accepted and the pending certificates with their fingerprints and expiry.
- Every refusal names its reason in the audit entry, and the docs page lists each reason with the command that helps.
- A browser with no JavaScript still finishes the sign-in: the page of step 5 has a "Continue" button.
- The usability check walks the docs page from a new installation and records each step that needs more than the page says.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The mode `saml` exists as a constant. (`pkg/session/provideroauth.go:22-24`)
- The mode `saml` maps to the method kind `MethodSAML`. (`pkg/session/provideroauth.go:44`)
- The provider validation refuses the modes `saml`, `cas`, `ldap` and `header`. (`pkg/session/providerconfig.go:179-183 (validateMode)`)
- The method kind `MethodSAML` exists. (`pkg/session/method.go:111-112`)
- The field Fresh of `session.BeginInput` means ForceAuthn for SAML. (`pkg/session/method.go:207`)
- The field RequestID of `session.PendingRedirect` keeps the SAML request ID. (`pkg/session/method.go:228`)
- A method refuses a nil `Pending`. (`pkg/session/method.go:254-256`)
- The field SAMLResponse of `session.Callback` holds the response. (`pkg/session/method.go:266-268`)
- The port of a redirect method. (`pkg/session/method.go:275 (type RedirectMethod)`)
- The conformance suite marks the SAML cases unwritten. (`pkg/session/sessiontest/method_redirect.go:86-87`)
- The conformance suite names the 23 SAML cases. (`pkg/session/sessiontest/method_redirect.go:95-118`)
- A used CAS ticket is kept for 1 hour. (`pkg/session/usedtickets.go:14`)
- The record of a used ticket. (`pkg/session/usedtickets.go:24 (type UsedTickets)`)
- The store of used tickets. (`pkg/session/usedtickets.go:68 (type TicketStore)`)
- The conformance suite of the ticket store. (`pkg/session/sessiontest/ticketstore.go:27 (func RunTicketStore)`)
- The CAS adapter is the shape of a redirect adapter with the egress client. (`internal/casauth/casauth.go:136 (func New)`)
- The CAS adapter refuses a `SAMLResponse`. (`internal/casauth/casauth.go:452`)
- The OAuth adapter refuses a `SAMLResponse`. (`internal/oauth/method.go:201`)
- The sign-in request cookie is `__Host-dustpan_signin`. (`internal/api/server/signin.go:34`)
- The sign-in path is `/api/server/sign-in/{provider}`. (`internal/api/server/signin.go:43`)
- The callback path is `/api/server/sign-in/{provider}/callback`. (`internal/api/server/signin.go:47`)
- The route table registers the sign-in and the callback as `GET` routes. (`internal/api/server/signin.go:285-293`)
- The sign-in request cookie is `SameSite=Lax`. (`internal/api/server/signin.go:400`)
- The callback sets `SAMLResponse: nil`. (`internal/api/server/signin.go:544`)
- The generic error text of a failed callback is "sign-in failed: start the sign-in again". (`internal/api/server/signin.go:61`)
- The generic error answers 400. (`internal/api/server/signin.go:739-740 (classifySignIn)`)
- A sign-in request of another site is recorded. (`internal/api/server/signin.go:769 (recordOtherSiteSignIn)`)
- The same-site check of a request. (`internal/sessionauth/sessionauth.go:232 (sameSite)`)
- The refusal reasons of the sign-in service, `ReasonAnswer`, `ReasonUnavailable`, `ReasonReplay` and `ReasonBinding`. (`internal/signinsvc/service.go:70-75`)
- A method error about the answer gives `ReasonAnswer`. (`internal/signinsvc/refuse.go:128-150 (answerReason)`)
- The provider paths. (`internal/api/server/providers.go:32-33`)
- The provider JSON has `mode` and `ldap`. (`internal/api/server/providers.go:77-82`)
- A provider change checks the freshness of the console session. (`internal/api/server/providers.go:278`)
- The API description is built from the route labels. (`internal/api/server/operations.go:136 (func Operations)`)
- The labels of each route group are joined. (`internal/api/server/operations.go:161 (func joinLabels)`)
- The build of a stored provider refuses each mode other than `oidc` and `oauth`. (`pkg/dustpan/storedproviders.go:215`)
- The table `sign_in_providers` allows the modes `oidc` and `oauth` only. (`internal/store/sqlite/migrations/0168_baseline.sql:482`)
- The table `sign_in_providers` in the postgres baseline. (`internal/store/postgres/migrations/0168_baseline.sql:1422`)
- The table `used_tickets`. (`internal/store/sqlite/migrations/0168_baseline.sql:1655`)
- The table `used_tickets` in the postgres baseline. (`internal/store/postgres/migrations/0168_baseline.sql:1691`)
- The store of alerts. (`pkg/session/service.go:125 (type AlertStore)`)
- A fake credential is built at run time. (`internal/fakecred/fakecred.go:20 (func URL)`)
- The LDAP e2e step runs against the in-process directory `internal/ldaptest`. (`e2e/ldap.go:29 (func NewEnvWithLDAP)`)
- The Docker e2e step runs only with `E2E_COMPOSE=1`. (`e2e/compose_test.go:51-55`)
- The targets of `make check`. (`Makefile:15`)
- Other facts: `.github/workflows/fuzz.yml:37-54` lists the weekly fuzz targets; `cmd/s3bench/bench-s3.workflow.yml` is a staging copy of a workflow outside `.github`; `bench/graphindex/extract/go.mod` is a nested module that `go test ./...` at the root does not build; the root `go.mod` has none of the SAML modules.

## Owner decisions that apply

- **Owner decision, 24 Nov 2026: keep SAML, with heavy testing and fuzzing.** This change is the SAML sign-in with that testing: fuzz targets, the attack corpus, advisory regression tests, a hostile table for each layer, and the differential test.
- **Owner decision, 24 Nov 2026: add the two extra validators, with all the additional dependencies, in CI only; none of them go in the repository's go.mod.** The validators run in pinned container images and in a nested Go module. The root `go.mod` gets only the three modules of ADR 285 and their two indirect modules.
- **ADR 285 (owner, 31 Oct 2026).** "The owner also approved the modules [...] `github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig` and `github.com/beevik/etree`."
- **ADR 321 decision 21 (owner, ADR 285).** "`github.com/russellhaering/gosaml2` with `goxmldsig` and `etree`. The owner approved them. The checks of decision 14 hold as if the library failed."
- **ADR 332 (owner, 2026-11-07).** Decision 1: SP-started is the normal path. Decision 3: IdP-started SAML is a setting of each provider, off by default. Decision 4: the four checks. Decision 5: never a fresh sign-in.
- **ADR 297, decision of the owner (b).** "The sign-in request cookie is `__Host-dustpan_signin` with `SameSite=Lax`." This change keeps it.
- **ADR 250 (owner).** Defense in depth for security code.

## ADR check

Precedent search: `rg -n -i 'saml' docs/adr docs/rfc` and `docs/adr/INDEX.md`. The rule today is ADR 321 with ADR 332 and ADR 397; ADR 320 for the ports; ADR 297 for the cookies; ADR 285 for the library.

| ADR | Quoted line | Changed-by chain | This change |
|---|---|---|---|
| 285 | "The owner also approved the modules [...] `github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig` and `github.com/beevik/etree`." | Changed by 297, 301, 312, 320, 333, 372; none of them changes this line. | Uses the three modules, at the pinned versions. |
| 321 d13 | "HTTP-Redirect for the request, HTTP-POST for the response." | Extended by 332. | Follows. |
| 321 d14 | "It counts each `Assertion` and each `EncryptedAssertion` element anywhere in the document, at any depth. The count must be exactly 1." and "It reads each value only from the signed, canonical element that the library returns after the signature check." | Extended by 332. | Follows, and adds the re-parse of the signed bytes as the structure. |
| 321 d15 | "An assertion with no `NotOnOrAfter`, or with a `NotOnOrAfter` more than 1 hour after the receipt, is refused." | Extended by 332. | Follows, with the table `used_assertions`. |
| 321 d16 | "The body has at most 256 KiB before the decode. The XML parser refuses a DTD, an entity and a depth of more than 64." | none | Follows, and adds a token limit and a decompressed size limit. |
| 321 d17 | "Encrypted assertions (PM) are optional, with a key of the installation that the secret package seals." | none | Follows: a setting of each provider, off by default. |
| 321 d18 | "A persistent `NameID` by default, or an attribute that the administrator names." | none (ADR 397 changes d29 and d36, not d18) | Follows. |
| 321 d19 | "Groups (PM) come from an attribute that the administrator names" | Changed by 332 (rule mapping). | Out of scope; the group mapping change adds it. |
| 321 d20 | "A certificate that is new in fetched metadata does not take effect: the server stores it as pending, raises an alert, and keeps the accepted certificates." | none | Follows. |
| 321 d21 | "The checks of decision 14 hold as if the library failed." | Owner, ADR 285. | Follows, with the verifier seam. |
| 321 d22 | "ForceAuthn="true" and a check of AuthnInstant" | Extended by 332. | Follows. |
| 321 d23 | "No single logout (PM). ADR 55 does not list it." | none | SLO is out of scope. |
| 321 consequence | "SAML and CAS each add a table of used values (assertion IDs and tickets), in both dialects." | none | Adds `used_assertions`. |
| 321 consequence | "Each method has an e2e step with an in-process test server before it ships (ADR 55)." | none | The e2e step uses an in-process test identity provider. |
| 332 d3 | "It is a setting of each SAML provider. It is off by default." | Changed by 376 (d9 only). | Follows. |
| 332 d4 | "The assertion ID is one-time." "The assertion is not older than a maximum age of about 5 minutes." "The `Audience` check passes." "The `Recipient` check passes." | Changed by 376 (d9 only). | Follows; the age is 300 seconds. |
| 332 d5 | "A sign-in that starts at the identity provider never counts as a fresh authentication." | Changed by 376 (d9 only). | Follows. |
| 320 d4 | "the pending request (a pointer, nil when the browser holds none)" and "It holds the state, the nonce, the PKCE verifier, the SAML request ID" | Changed by 332 (d12), 372 (wording). | Follows. The port doc of `Pending` gets the IdP-started case. |
| 297 owner (b) | "The sign-in request cookie is `__Host-dustpan_signin` with `SameSite=Lax`." | Changed by 301, 333; neither changes this line. | Keeps `Lax`. The ACS page posts again from the origin of the server, so the `Lax` cookie goes with the second POST. |
| 250 | "Never use only one layer." (CLAUDE.md, from ADR 250) | Changed by 382, 396 (audit of repeated refusals, CLI refusals). | One hostile table for each layer and a test for each guard. |
| 74 | Phone-home is not allowed. | none | The validators run in CI only and send nothing out. |

No requirement of this change contradicts an owner-tagged ADR.

## Open questions

Open questions: none.

Questions considered:

- Reuse `used_tickets` for assertion IDs? No: the consequence of ADR 321 says SAML adds its own table. The new table has the same shape and its own port.
- `SameSite=None` on the sign-in request cookie for the cross-site POST? No: that contradicts the owner decision (b) of ADR 297. The ACS page posts again from the origin of the server.
- A minimal test identity provider written in Go for the container e2e? No: it signs with the same `goxmldsig` and shares its failure modes. The in-process e2e step uses it; the container step uses Keycloak (design.md).
- SimpleSAMLphp as the container identity provider? No: its license is LGPL-2.1, which the license rule of the validators refuses.
