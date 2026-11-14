---
version: 2
---

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
| Two adapters register the same kind and name. | The server refuses to start and names the duplicate kind and name pair. Remove one registration and start again. | `opendustpan serve` |
| The remote system refuses the credential. | The open fails with the remote refused message and the log holds no secret value. Fix the credential, then read the profile. | `GET /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}` |
| The remote address is not in the allowed networks. | The open fails with the address refused message. Read the effective networks value. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The provider package holds the refused-request sentinel. Proof: pkg/profile/provider/provider.go:28: ErrRemoteDenied = errors.New("the remote system refused the request")
- The provider package holds the not-found sentinel. Proof: pkg/profile/provider/provider.go:31: ErrRemoteNotFound = errors.New("the remote system has no such value")
- A RunProvider suite with a fake-remote harness exists. Proof: pkg/profile/provider/providertest/providertest.go:71: func RunProvider(t *testing.T, h *Harness)
- No Rotator conformance suite exists on main. Proof: rg -n -F --glob '!*_test.go' 'RunRotator' pkg internal cmd matches 0 lines
- The egress package has the HTTPSOnly guard. Proof: pkg/egress/egress.go:58 holds HTTPSOnly

## What Changes

- Add the Rotator and TokenSource ports and finish Spec in pkg/profile/provider, with the port sentinels of pkg/profile
- Add RunRotator to providertest and complete the RunProvider cases, with a goleak check
- Add the adapter registry that refuses duplicate kind plus name pairs at start
- Add depguard globs for pkg/profile, profiletest and providertest
- Route adapter calls through egresshttp Client with NoRedirect, HTTPSOnly, dial guard, 1 MiB limit and at most 16 parallel calls per host
- Add installation settings profileProviderNetworks and profileCACertificates through internal/cli/settings.go
- Harden source fetch with validHost literal refusal, pinned Fetcher dial and Checkout containment

Out of scope:

- Each built-in cloud, Vault, Keycloak, Doppler, Infisical or 1Password adapter
- secrets/external and login/external adapter paths that need the namespace issuer to sign
- Rotation execution and rotation leases

Depends on:

- open-profile-serve-open-sessions
- `open-profile-serve-open-sessions` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- An embedder finds the port, the Spec shape and the fake remote through the guide in the same pull request.
- A duplicate adapter name fails at server start, not at first open.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- RunProvider suite exists with a fake-remote harness. (`pkg/profile/provider/providertest/providertest.go:71`)
- ErrRemoteDenied exists in the provider package. (`pkg/profile/provider/provider.go:28`)
- ErrRemoteNotFound exists in the provider package. (`pkg/profile/provider/provider.go:31`)
- HTTPSOnly guard exists. (`pkg/egress/egress.go:58`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 206: The profile provider port, rotators and conformance suites** (owner-tagged: decision 8).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 5 (current), 6 (current), 7 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The registry refuses two adapters"
- Quote: "An adapter gets its HTTP client from the wiring, never from `http.DefaultClient`"
- Quote: "Errors. `pkg/profile` holds the sentinels of the port:"
- Effect: The change adds the ports, Spec, the sentinels, the suites, the registry and the one outbound client as the decisions state. Decision 8 is owner-tagged and the change follows it without any wording change.
- Conflict: none.

**ADR 207: The built-in profile providers** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 273 changes decision 7; ADR 286 changes. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273, 286.
- Quote: "The value of `use` in a block is `<kind>/<name>`."
- Effect: The registry uses the kind and name pair of the use value.
- Conflict: none.

**ADR 208: The external adapter protocol** (not owner-tagged).

- Decisions that apply: 6 (current), 7 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server sends at most 16 calls at the same time to one host,"
- Effect: The outbound path reuses the error table, the body limit, the timeout and the parallel-call limit.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "The installation has one settings resource, and each namespace has one:"
- Effect: The two settings live in the one settings resource, and a weaker list needs a fresh MFA step.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Run an e2e step that starts the server and reads the two settings." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: the state diagram after this change: `OpenCalled` does not resolve on main; use the name main has, or mark it NEW; a code check fails: the state diagram after this change: `CallGuarded` does not resolve on main; use the name main has, or mark it NEW; a code check fails: and 3 more.

Questions considered and answered:

- How do provider adapters reach the network? ADR 206 decision 6: one outbound client from the wiring with NoRedirect, HTTPSOnly, dial guard and 1 MiB limit.
- What does the conformance suite cover? ADR 206 decision 8 lists the cases. ADR 208 decision 10 adds the external cases, which are later work.
- Does this change ship a built-in cloud adapter? No. The issue keeps them out of scope.
- What happens on a duplicate adapter name? ADR 206 decision 2: the registry refuses it at start.
- What happens when 16 calls to one host run at the same time? Extra calls wait within the timeout and then fail with ErrUnavailable. The limit holds per server process (ADR 208 decision 7).
- What happens to an open in flight at a restart? No automatic retry. The open fails with no partial values stored, and the caller opens again.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
