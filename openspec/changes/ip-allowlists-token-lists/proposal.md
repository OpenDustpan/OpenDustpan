---
version: 1
---

Issue: #3373 (the token part of IP allowlists).

Split from the change `ip-allowlists`. That change keeps the installation list, the namespace list, the one switch, the actions and the byte limit. This change holds the own list of an access token. Owner decision 8 (20 Nov 2026) answers the one open question, so the change is ready.

## Why

**Persona: site administrator.** A site administrator gives a CI pipeline an access token. If the token leaks, it works from any address. The administrator needs to limit one token to the network of its runner, so that a stolen token fails from another address.

The user path this change must make work:

1. **Console.** A person with a console session sets the own list of one of their personal access tokens (`PUT /api/dustpan/v1/me/tokens/{id}/ip-allowlist`). A person who holds `principal:manage-tokens` on a service principal sets the list of a service token of that principal (`PUT /api/server/service-tokens/{id}/ip-allowlist`).
2. **CLI.** Run the Pulumi CLI and OpenTofu with that token from an address inside its list and from an address outside it. The second one gets 403.
3. **API.** Read the list as the field `ipAllowlist` of the token resource.
4. **Docs page.** Follow docs/guides/ip-allowlists.md to set, deny, and recover when a list excludes a runner.
5. **E2e.** Run the allowlist e2e suite over TLS against the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A token is used from an address outside its list. | 403 with error code access.ip-not-allowed and the scope `token`. The answer names no token. | `GET /api/dustpan/v1/me/tokens` |
| Save of a token list with a bad entry, an empty list, more than 64 entries or more than 4096 bytes. | 400 with error code ip-allowlist.invalid-cidr, or 400 that names the limit. | `GET /api/dustpan/v1/me/tokens` |
| A request made with a token widens the list of that same token, or clears it. | 403 with error code ip-allowlist.widen-refused. The list stays as it was. Narrowing is allowed. | `GET /api/dustpan/v1/me/tokens` |
| A person without the right sets the list of a token. | 404 on the own route for a token of another person. 403 on the service route, the same answer for a token that does not exist. No change is stored. | `GET /api/dustpan/v1/me/tokens` |

Evidence for the claims above:

- No list on an access token. Proof: `rg -n -i 'ip_allow|ipallow' pkg/accesstoken pkg/identity/identity.go --glob '!*_test.go'` finds a match only for the word "allowlist of the kinds" in `pkg/accesstoken/accesstoken.go:87`, and the `access_tokens` table has no list column (`pkg/store/sqlite/migrations/0168_baseline.sql:278-290`, `pkg/store/postgres/migrations/0168_baseline.sql:31-44`).
- `GetTokenByDigest` carries no list. Proof: `pkg/accesstoken/store.go:35-41`.
- The checker holds a namespace slot that no production call fills. Proof: the four calls of `newAllowChecker` outside tests pass `nil` for it (`pkg/dustpan/ipallow.go:178,200,221,224`).

## What Changes

- Add the column `ip_allowlist` to `access_tokens` in both baselines, edited in place, and the port method `SetTokenIPAllowlist` of `TokenStore` with its conformance cases.
- Add `SetIPAllowlist` and `ClearIPAllowlist` to the access token service, with the audit entries `token:set-ip-allowlist` and `token:remove-ip-allowlist`.
- Replace the unused namespace slot of the allow checker with a token slot, checked in `admit` after authentication.
- Add the two routes of the token list (own and service), their actions, and the field `ipAllowlist` of the token resource. A change needs no fresh step, as a revoke needs none (owner decision 8).
- Add the guard that a request made with a token never widens the list of that same token (owner decision 8).
- Update docs/guides/ip-allowlists.md and extend the e2e step.

Out of scope:

- The installation list, the namespace list, the one switch, the actions of the namespace list and the byte limit. The change `ip-allowlists` holds them.
- Token lifetime and expiry email.
- A change of who may make a token (ADR 70 decision 5).
- A route for the holder of `server:manage-tokens` to set the list of a personal token of another person. Owner decision 8 gives the list of a personal token to its maker only.
- An edit route for the name of a token. Main has none; owner decision 8 names it as a peer check only.

Depends on:

- `ip-allowlists`. The token list reads the one switch `listsEnabled` (owner decision 9 of that change), the limit of 4096 bytes and the HTTP table of "deny when any list denies" that it adds.

Shares files with, and so merges in order with (no hard dependency; rebase onto the change that lands first, and regenerate generated files):

- `fresh-auth-enabled-setting` (`pkg/accesstoken/service.go`, `internal/api/server/tokens.go`). This change adds no call to `requireFresh`.
- `token-life-cycle-expiry-email-token-expiry-enforcement` (`Service.AuthenticateToken`, `GET /api/dustpan/v1/me/tokens`).
- `move-caller-routes-api-server` (the `/api/dustpan/v1/me/tokens` routes). The new own route sits next to the route of that table.

## Usability

- A person can read the list of a token in the token resource, `[]` when none.
- A list that excludes the runner blocks the pipeline. The docs page names the recovery: clear the list, or revoke and make the token again.

## ADR check

Each quote is copied from `docs/adr/`. The chains are those of `docs/adr/INDEX.md` on main (checked 20 Nov 2026 with `rg -n -i 'ip.allowlist|ip allowlist' docs/adr` and the rows 53, 55, 70, 212 and 385 of the index).

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6, 7 and 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decisions 1, 6, 7 and 9. Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote (decision 8, `docs/adr/0055-sign-in-methods-in-milestone-4.md:89`): "IP allowlists are off by default, and each scope opts in."
- Quote: "Then each scope opts in on its own: the installation, each namespace and each access token."
- Quote: "When the feature is on, each list applies to console sign-in and to requests with an access token."
- Effect: Decision 8 puts a list on each access token. Main has none. This change adds it.
- Conflict: none. Decision 8 names no setter and no step for a token list. Owner decision 8 of this change names them.

**ADR 53 and ADR 70: access tokens** (not owner-tagged; the ADR 53 text of decision 3 records "The owner wrote this with \"I guess\"").

- Quote (ADR 53 decision 3, `docs/adr/0053-access-token-life-cycle.md:54`): "Who can issue which token is part of the authorization model. Each kind of token has its own action."
- Quote (ADR 53 decision 10, line 81): "A personal CLI access token comes from a sign-in."
- Quote (ADR 70 decision 5, `docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md:135`): "Each person makes only its own personal tokens. No principal can make or rotate a personal token of another person. A holder of `server:manage-tokens` can revoke the personal token of any person."
- Changed by: ADR 53 is changed by ADR 70 (decisions 5, 6 and 10), ADR 237 (decision 9) and extended by ADR 69, 212, 354, 373 (`docs/adr/INDEX.md:66`). ADR 70 is changed by the ADRs of `docs/adr/INDEX.md:83`. Current rule for ADR 53: this ADR, with ADR 69, 70, 212, 237, 354, 373.
- Effect: These decide who makes, rotates and revokes a token. They do not name the list of a token. A change of the list is not a make, a rotation or a revoke, so the sign-in rule of `ErrSignInRequired` (`pkg/accesstoken/scope.go:44`) does not apply to it. The reason of that rule is that a token that makes tokens would outlive its maximum lifetime and a revoke. A token that only narrows its own list adds no token and no lifetime. The guard in this change keeps a token from widening its own reach.
- Effect: Decision 5 gives the revoke of a personal token of another person to `server:manage-tokens`. Owner decision 8 gives the list of a personal token to its maker only, so a revoke right does not give a list right.
- Conflict: none.

**ADR 212: ... one settings API** (not owner-tagged as a whole; decision 7 carries "(owner decision 2026-10-18)").

- Quote (decision 3): "A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure."
- Changed by: ADR 227 changes decision 3; ADR 232 and ADR 235 change decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1; ADR 385 changes decision 3 (owner-tagged, 2026-11-15). Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373, 385.
- Effect: A token list is not a setting of the installation or of a namespace, so decision 3 does not apply. Owner decision 8 says a change of the list uses the check of a revoke, and a revoke needs no fresh step on main (`pkg/accesstoken/service.go:181,459` call `requireFresh` for create and rotate only; `Revoke` at line 338 does not).
- Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged).

- Quote (decision 3, `docs/adr/0385-a-fresh-step-is-opt-in.md:37`): "An access token never gives one. The refusal is `403` with `mfa.fresh-step-required`."
- Quote (decision 2, line 29): "When the setting is off, the server asks a person for no fresh authentication."
- Changed by: none (`docs/adr/INDEX.md:353`, "Changed by" is "-").
- Effect: The routes of this change ask for no fresh step, so the decision has nothing to apply to. It is listed because the earlier version of this change used a step.
- Conflict: none.

**ADR 250: defense in depth** (not owner-tagged). The change follows it: one hostile test for each layer (see the design, "Layers").

## Owner decisions that apply

- **Decision 8 (owner, 20 Nov 2026)**: anyone who may manage the token sets its own IP list. This is the same check as revoke and edit: the maker for a personal token, and the holders of the token-management action for a service token. One guard: a request made with a token can never widen that same token's list. Narrowing is always allowed.
- **Decision 9 (owner)** of `ip-allowlists`: the setting `ipAllowlist.enabled` alone turns the namespace and token lists on.
- **Decision 11 (owner)** of `ip-allowlists`: the list keys have a 4096-byte limit.

## Open questions

Open questions: none.

Questions considered and answered:

- Who sets the list of an access token, and does a change need a fresh step? Owner decision 8. The maker for a personal token. A holder of `principal:manage-tokens` on the service principal for a service token (`pkg/accesstoken/scope.go:190 (authorizeRevoke)`, `:269 (requireManageTokens)`). No fresh step: a revoke needs none on main (see ADR 212 above).
- May a request made with a token set a token list? Yes, when the caller passes the same check as a revoke. A revoke has no sign-in rule (`Revoke` at `pkg/accesstoken/service.go:338` does not call `requireSignIn`). The one guard: the request never widens the list of the token that made it.
- What is "widen"? The new list admits an address that the stored list does not admit. A token with no list admits every address, so any list is a narrowing and a clear is a no-op. A token with a list is widened by a clear, and by a list that `ipallow.List.Covers` (`pkg/ipallow/list.go:136`) does not accept.
- May a token widen the list of another token? Yes, by the same check as its maker or its manager would pass. The guard names the same token only (owner decision 8). The change records the caller and the token in the audit entry.
- Which switch turns the token lists on? The setting `ipAllowlist.enabled`, by owner decision 9 of `ip-allowlists`.
- Which byte limit holds? 4096 bytes, by owner decision 11 of `ip-allowlists`.
- May a token hold an empty list? No. An empty value means "no list". The route clears the list instead.
- Does the holder of `server:manage-tokens` set the list of a personal token of another person? No. Owner decision 8 names the maker only. The service refuses it, and no route exists.
- Does the order of work matter? Yes. The change `ip-allowlists` lands first: it adds `listsEnabled` and the 4096-byte limit that this change reads. See "Depends on".
