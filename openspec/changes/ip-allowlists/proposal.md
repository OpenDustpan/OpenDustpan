---
version: 2
---

Roadmap: `docs/roadmap.md:183` (IP allowlists).

## Why

**Persona: site administrator.** A site administrator shares one installation between people and CI pipelines. The administrator needs to limit console sign-in and CLI access by network address. The installation, each namespace and each access token opt in on their own.

The user path this change must make work:

1. **API.** Turn the feature on and set the installation list with `PATCH /api/dustpan/v1/settings`.
2. **API.** Opt in a namespace with `PATCH /api/dustpan/v1/namespaces/{namespace}/settings`, and an access token with its own list.
3. **Console.** Sign in from an allowed address and from a denied address. Read the 403 reason.
4. **CLI.** Run the Pulumi CLI and OpenTofu with an access token from an allowed address and from a denied address.
5. **Docs page.** Follow docs/guides/ip-allowlists.md to enable, opt in, deny and allow.
6. **E2e.** Run the allowlist e2e suite over TLS against the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Console sign-in or token request from an address outside an opted-in list. | 403 with error code ip-allowlist.denied that names the scope that refused. | `GET /api/dustpan/v1/settings` |
| Save of a list with an entry that is not a valid CIDR or address. | 400 with error code ip-allowlist.invalid-cidr that names the bad entry. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- An e2e file for IP allowlists exists. Proof: e2e/ipallow_test.go
- An e2e file for principals names IP allowlists. Proof: e2e/principals_test.go
- A guide for IP allowlists exists. Proof: docs/guides/ip-allowlists.md

## What Changes

- The installation flag and list keys move to the generic settings API with CIDR checks. This change adds no new route.
- Namespace and access token lists use the same key shape.
- Enforcement on console sign-in and on requests with an access token gives 403 and an audit entry.
- The docs page covers enable, opt in, deny and allow.

Out of scope:

- New sign-in methods
- Token lifetime and expiry email
- mTLS client certificate handling

Depends on:

- add-one-generic-settings-api
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- An administrator finds the refusing scope from the 403 body without reading logs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A test covers the installation grant for allowlists. (`pkg/dustpan/ipallow_access_test.go:237 (TestIPAllowlistNeedsInstallationGrant)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current), 11 (changed).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "IP allowlists are off by default, and each scope opts in."
- Quote: "An administrator of the installation turns the feature on first."
- Quote: "A scope that did not opt in has no list."
- Quote: "Each change writes an audit entry."
- Effect: The change follows decision 8 for off-by-default opt-in lists and decision 11 for an audit entry on each change.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Operator actions keep the area `installation:`."
- Effect: The action names follow the map of decision 3. The choice is an open question for the owner.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "One answer, and the reason in the audit log (PM)."
- Effect: An allowlist denial is a network check, not a refused sign-in of a method. It has its own 403.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which authorization actions guard the flag, the lists and their reads?
   - Options: installation:manage-sign-in for the flag and installation list, namespace settings action for namespace lists; a new installation network action.
   - Provisional decision (owner review 14 Nov): installation:manage-sign-in for the flag and installation list, and the namespace settings action for namespace lists.
   - Reason: AWS splits network controls into read and write actions on the object. ADR 216 reserves installation actions for operators. Benchmark: AWS: IAM separates read and write actions for network controls.
   - Persona impact: No persona loses access. Escape hatch: Rename the actions later; grants stay as stored.
   - Owner-tagged ADRs involved: ADR 216 (their words do not change).
2. Does the server re-check an existing sealed-cookie console session on each request and on refresh?
   - Options: Check sign-in start and token requests only; Re-check each cookie request and each refresh.
   - Provisional decision (owner review 14 Nov): Re-check each cookie request and each refresh.
   - Reason: Google Cloud re-checks network controls on each request, not only at sign-in. Benchmark: Google: access levels are evaluated on every request.
   - Persona impact: An allowed person sees no change. A denied person sees the 403 reason. Escape hatch: Turn the feature off in settings.
   - Owner-tagged ADRs involved: ADR 297 (their words do not change).

Questions considered and answered:

- Which scopes hold a list? ADR 55 decision 8: the installation, each namespace and each access token.
- What audit entries are written? ADR 55 decision 11: each change writes an entry. A denial also writes one.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
