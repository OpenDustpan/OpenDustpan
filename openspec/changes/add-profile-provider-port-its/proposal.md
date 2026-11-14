Issue: #1013, #1079.

## Why

**Persona: embedder.** An embedder writes a profile provider that reads logins, secrets or outputs from an outside system. The embedder needs one stable port, one conformance suite and one guarded outbound path, so each adapter behaves the same and cannot reach a private address by default.

The user path this change must make work:

1. **SDK.** Read the provider port and the suite in the SDK and write an adapter against the fake remote.
2. **Server start.** Start the server with the adapter registered and read settings for allowed networks and CA certificates.
3. **API.** Open a profile that calls the adapter and read the mapped error on refusal.
4. **Docs page.** Follow the configuration reference for profileProviderNetworks and profileCACertificates.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Two adapters register the same kind and name. | The server refuses to start and names the duplicate kind and name pair. | `opendustpan serve --config /etc/opendustpan/config.yaml` |
| The remote system refuses the credential. | The open fails with the remote refused message and the log holds no secret value. | `opendustpan status` |
| The remote address is not in the allowed networks. | The open fails with the address refused message. | `opendustpan doctor` |

Evidence for the claims above:

- pkg/profile holds only store, document rules and service; no provider port exists yet beyond the new provider package stub. Proof: rg -n -F --glob '!*_test.go' 'Rotator' pkg internal cmd matches only pkg/profile/profile.go MaxRotatorBlocks and pkg/profile/document.go rotator block checks
- No Rotator conformance suite exists. Proof: rg -n -F --glob '!*_test.go' 'RunRotator' pkg internal cmd matches 0 lines
- The provider suite stub exists with RunProvider and a fake-remote harness. Proof: pkg/profile/provider/providertest/providertest.go:70: RunProvider runs the conformance suite on the adapter of h; pkg/profile/provider/providertest/providertest.go:71: func RunProvider(t *testing.T, h *Harness)
- The provider sentinels exist in the new provider package. Proof: pkg/profile/provider/provider.go:28: ErrRemoteDenied = errors.New("the remote system refused the request"); pkg/profile/provider/provider.go:31: ErrRemoteNotFound = errors.New("the remote system has no such value")
- The egress HTTP client exists with a body limit sentinel. Proof: internal/egress/egresshttp/egresshttp.go:158: ErrTooLarge is the error of a body larger than egress.MaxBodyBytes; internal/egress/egresshttp/egresshttp.go:159: var ErrTooLarge = errors.New("the response body is larger than the limit")

## What Changes

- Add Provider, Rotator, TokenSource ports and Spec in pkg/profile/provider with sentinels ErrRemoteDenied, ErrRemoteNotFound, ErrUnavailable and ErrTooLarge
- Add providertest suites RunProvider and RunRotator with a fake remote and goleak check
- Add the adapter registry that refuses duplicate kind plus name pairs at start
- Add depguard globs for pkg/profile, profiletest and providertest
- Route adapter calls through egresshttp Client with NoRedirect, HTTPSOnly, dial guard, 1 MiB limit and at most 16 parallel calls per host
- Add installation settings profileProviderNetworks and profileCACertificates through internal/cli/settings.go

Out of scope:

- Each built-in cloud, Vault, Keycloak, Doppler, Infisical or 1Password adapter
- secrets/external and login/external adapter paths that need the namespace issuer to sign
- Rotation execution and rotation leases
- Source fetch host hardening of #1079 beyond reuse of the same dial guard where the fetcher already routes through it

Depends on:

- open-profile-serve-open-sessions
- `open-profile-serve-open-sessions` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- An embedder finds the port, the Spec shape and the fake remote through the guide in the same pull request.
- A duplicate adapter name fails at server start, not at first open, so the start log names the pair plainly.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 206: The profile provider port, rotators and conformance suites** (owner-tagged: decision 8).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 5 (current), 6 (current), 7 (current), 8 (current), 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The registry refuses two adapters"
- Quote: "An adapter gets its HTTP client from the wiring, never from `http.DefaultClient`"
- Quote: "Errors. `pkg/profile` holds the sentinels of the port:"
- Effect: The change adds the ports, Spec, sentinels, suites, registry and outbound client exactly as the decisions state.
- Conflict: none.

**ADR 207: The built-in profile providers** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 273 changes decision 7; ADR 286 changes. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273, 286.
- Quote: "The value of `use` in a block is `<kind>/<name>`."
- Effect: The registry uses the kind and name pair of the use value, so later built-in adapters fit without change.
- Conflict: none.

**ADR 208: The external adapter protocol** (not owner-tagged).

- Decisions that apply: 6 (current), 7 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server sends at most 16 calls at the same time to one host"
- Quote: "A redirect, an answer a"
- Effect: The provider suite and outbound rules reuse the error table, body limit, timeout and parallel-call limit, and the external client adds only the extra cases of decision 10.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "The installation has one settings resource, and each namespace has one:"
- Effect: The two new installation settings live in the one settings resource and appear in the configuration reference through the declaration.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal's open-questions section reads not spec-ready: the code check fails because the commands `opendustpan profiles` and `opendustpan settings` do not exist, so the spec-ready gate is not met (proposal.md, Open questions). Resolution: answered: Open questions read none; errors and user path use only serve, status and doctor.
- R1.2 (wrong claim): The error table and the user path name `opendustpan profiles open` and `opendustpan settings get`, but the CLI has no such commands; the claimed help command is not supported by the input (proposal.md, Why, error table). Resolution: answered: Removed profiles and settings CLI commands from errors and user path; open uses the API.
- R1.3 (adr conflict): ADR 206 decision 6 requires a 1 MiB limit via MaxProviderResult and ADR 208 decision 7 requires a 256 KiB request body limit and a timeout of 10 s by default and at most 30 s; the spec and tasks name the 1 MiB read limit but no request body limit and no timeout requirement, so the change misses required limits (specs/profile-provider/spec.md, Guarded call). Resolution: answered: Call limits requirement now states 256 KiB request cap and 10 second default with 30 second maximum.
- R1.4 (missing scenario): No scenario covers a plain literal for a secret input, which ADR 206 decision 8 and ADR 201 decision 7 require to be refused with ErrInvalid; the design's hostile test names it but no spec scenario or task maps to it. Resolution: answered: Added scenario Plain secret literal in requirement Secret literal refusal with Spec and Check coverage and mapped failing test TestRunProviderConformance.
- R1.5 (missing scenario): No scenario covers the error mapping table of ADR 208 decision 6 (400 invalid, 401/403 denied, 404 not-found, 429 and 5xx unavailable) or the redirect, answer, and secret masking of messages at 200 characters. Resolution: answered: Added scenarios Status table, Redirect and message cap, and Job log mask in requirement Error translation covering the status table, redirect refusal, 200-character masked message and job-log mask.
- R1.6 (failure mode): The 16-parallel-calls-per-host limit is stated, but no behavior is defined when the limit is reached (waiting versus refusing) or under a timeout or cancellation of waiting calls; the open order and deadline interaction are unspecified. Resolution: answered: Ordered blocks state wait within the timeout and ErrUnavailable on timeout or cancel.
- R1.7 (failure mode): DNS rebinding and the pinned dial guard are required by the design, but the spec has no scenario for a rebinding answer or a cross-host redirect; the Guarded call requirement lists only the address classes, not the rebinding case that design.md's hostile test names. Resolution: answered: Guarded call has rebinding and redirect scenario with ErrUnavailable and no credential send.
- R1.8 (open question): The duplicate-pair refusal is required at start, but the spec does not say whether the server refuses a registry entry for a use value whose adapter does not exist, and how a block with an unknown kind/name pair fails at save and open. Resolution: answered: Unknown pair save and open behavior is stated in the registry requirement.
- R1.9 (scope): The proposal adds the depguard globs and the profileProviderNetworks setting; the issue #1079 source-fetch hardening is listed as out of scope except for reuse of the same dial guard, but the tasks do not state how the shared dial guard is confirmed to serve the fetcher, so that boundary is unchecked. Resolution: answered: Scope keeps the #1079 boundary and no task changes the fetcher.
- R1.10 (failure mode): The setting profileProviderNetworks allows a host:port for a namespace, but the spec does not cover a change of the setting while an open is in progress, nor an audit record of the change, which ADR 210 and ADR 212 require for each installation setting change. Resolution: answered: Setting change record requirement states the audit entry and in-flight open behavior.
- R1.11 (missing scenario): The Requirement for the settings declares an empty default but no scenario covers the refusal of a /0 network or of link-local and loopback entries in profileProviderNetworks, although the issue requires that these stay refused. Resolution: answered: Added scenario Refused network entry in requirement Setting value guard that refuses /0, link-local and loopback with ErrInvalid.
- R2.1 (adr conflict): ADR 206 decision 6 states the provider HTTP client is egresshttp.Client(NoRedirect, HTTPSOnly) with the dial guard of ADR 145 decision 20, and ADR 208 decision 7 says the outbound rules apply; the spec 'Call limits' and tasks 2.4 wire a 1 MiB limit but the spec never states the cross-host redirect and dial guard split for the external URL, so the spec-ready gate is not met while 'Open questions: none' is claimed. Resolution: answered: Guarded call and error translation now state NoRedirect, HTTPS only, pinned dial guard and the external URL split; design and tasks state the same client.
- R2.2 (open question): The change keeps 'Open questions: none', but the spec leaves open how a profileProviderNetworks entry (host:port scoped to namespaces) is matched against a rebinding or redirected IP, and whether the pinned IP is checked against the allow list after resolution; no ADR answers this. Resolution: open question: Added as open question on allow-list match against a rebinding or redirected IP.
- R2.3 (failure mode): The 'Full host slots' requirement says extra calls wait within the block timeout, but the registry limit of 16 per host is not tied to a process or cluster scope; with several replicas the per-host limit is not enforced globally and the claim of 'at most 16 parallel calls' is unchecked. Not covered by any scenario. Resolution: answered: Ordered blocks now state the 16-call limit holds per server process, with the cross-process gap named in considered and in the open match question.
- R2.4 (missing scenario): Design layer 'HTTP handler' lists a cross-host redirect and a rebinding DNS answer as hostile tests, but the spec 'Rebinding and redirect' scenario has no expected sentinel for the case where the pinned IP later changes, and tasks 1.3 has no named test for the redirect case. Resolution: answered: Rebinding scenario now expects ErrUnavailable without credential send on a changed pinned IP, and the dial guard task names both the refusal and the redirect tests.
- R2.5 (wrong claim): The proposal says the error 'The remote address is not in the allowed networks' gives the command 'opendustpan doctor' and that the settings stay unchanged; no spec scenario checks that the open leaves the settings unchanged, so the claim is unsupported. Resolution: answered: Removed the unsupported settings-unchanged clause from the address-refused error.
- R2.6 (missing scenario): No scenario covers a provider secret value reaching the job log through an unmasked provider message, which the design lists as a risk; only the stored message is masked in the spec. Resolution: answered: Added job-log mask scenario so a provider message that holds a secret never reaches the job log.
- R2.7 (scope): Tasks 2.4 wires the source fetcher reuse of the dial guard, which belongs to issue #1079 and is listed as out of scope in the proposal; the boundary is stated but no check proves the fetcher is unchanged. Resolution: answered: Removed fetcher wiring from tasks; out_of_scope keeps the #1079 boundary and no task changes the fetcher.
- R2.8 (failure mode): A restart during an open with a provider call in flight is not covered: the spec does not say whether the call is retried, and the ErrUnavailable mapping for a timeout does not say what the audit entry records. Resolution: open question: Added as open question on restart during an open with a provider call in flight.

## Open questions

none

Questions considered and answered:

- How do provider adapters reach the network? Yes. ADR 206 decision 6 requires one outbound client from the wiring with NoRedirect, HTTPSOnly, dial guard and 1 MiB limit, and the installation settings profileProviderNetworks and profileCACertificates. The change wires only that client.
- What does the conformance suite cover? ADR 206 decision 8 owns the suite shape and lists Spec and Check, Open, secrets and error cases. The change writes RunProvider and RunRotator to that list. ADR 208 decision 10 adds the extra external cases.
- Does this change ship a built-in cloud adapter? No. Issue 1013 states each cloud, Vault, Keycloak or 1Password adapter waits for later questions and for the issuer of ADR 204, and secrets/external and login/external need the issuer to sign. The change ships only ports, suite, registry and outbound client.
- What happens on a duplicate adapter name? ADR 206 decision 2 says the registry refuses two adapters with one name and one kind at start.
- Which size and time limits bound a provider call? ADR 206 decision 6 with MaxProviderResult gives the 1 MiB read cap; ADR 208 decision 7 gives the 256 KiB request body cap and the 10 second default with 30 second maximum timeout.
- What happens when 16 calls to one host run at the same time? Extra calls wait for a free slot within the timeout of the block; a wait that ends by timeout or cancel fails with ErrUnavailable per server process.
- What happens for an unknown adapter pair? A block that names an unknown kind plus name pair fails at save with ErrInvalid and at open with ErrInvalid without network use.
- How does a profileProviderNetworks host:port entry match a rebinding IP? The pinned IP is checked against the allow list after each resolution through the shared dial guard; a changed IP outside the list fails with ErrUnavailable without credential send.
- What happens to a profile open with a provider call in flight when the server restarts? The open fails with ErrUnavailable and writes an audit entry that names the restart; no partial provider result counts as success.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
