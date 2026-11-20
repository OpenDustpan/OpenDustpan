---
version: 1
---

Issue: #3373 (the token part of IP allowlists).

Split from the change `ip-allowlists`. That change keeps the installation list, the namespace list, the one switch, the actions and the byte limit. This change holds the own list of an access token. It is parked: open question 1 is owner-only security semantics, and no task starts before the owner answers.

## Why

**Persona: site administrator.** A site administrator gives a CI pipeline an access token. If the token leaks, it works from any address. The administrator needs to limit one token to the network of its runner, so that a stolen token fails from another address.

The user path this change must make work:

1. **Console.** A person with a console session sets the own list of one of their access tokens (route: open question 1).
2. **CLI.** Run the Pulumi CLI and OpenTofu with that token from an address inside its list and from an address outside it. The second one gets 403.
3. **API.** Read the list as the field `ipAllowlist` of the token resource.
4. **Docs page.** Follow docs/guides/ip-allowlists.md to set, deny, and recover when a list excludes a runner.
5. **E2e.** Run the allowlist e2e suite over TLS against the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A token is used from an address outside its list. | 403 with error code access.ip-not-allowed and the scope `token`. The answer names no token. | `GET /api/dustpan/v1/me/tokens` |
| Save of a token list with a bad entry, an empty list, more than 64 entries or more than 4096 bytes. | 400 with error code ip-allowlist.invalid-cidr, or 400 that names the limit. | `GET /api/dustpan/v1/me/tokens` |
| A token tries to set a token list, its own or another. | 403. A token cannot make or change a token (`pkg/accesstoken/scope.go:44 (ErrSignInRequired)`). | `GET /api/dustpan/v1/me/tokens` |
| A change needs a fresh step and the session has none (depends on open question 1). | 403 with error code mfa.fresh-step-required. | `GET /api/dustpan/v1/me/tokens` |

Evidence for the claims above:

- No list on an access token. Proof: `rg -n -i 'ip_allow|ipallow' pkg/accesstoken pkg/identity/identity.go --glob '!*_test.go'` finds a match only for the word "allowlist of the kinds" in `pkg/accesstoken/accesstoken.go:87`, and the `access_tokens` table has no list column (`pkg/store/sqlite/migrations/0168_baseline.sql:278-289`, `pkg/store/postgres/migrations/0168_baseline.sql:31-44`).
- `GetTokenByDigest` carries no list. Proof: `pkg/accesstoken/store.go:36`.
- The checker holds a namespace slot that no production call fills. Proof: the four calls of `newAllowChecker` outside tests pass `nil` for it (`pkg/dustpan/ipallow.go:178,200,221,224`).

## What Changes

- Add the column `ip_allowlist` to `access_tokens` in both baselines, edited in place, and the port method `SetTokenIPAllowlist` of `TokenStore` with its conformance cases.
- Add `SetIPAllowlist` and `ClearIPAllowlist` to the access token service, with the audit entries `token:set-ip-allowlist` and `token:remove-ip-allowlist`.
- Replace the unused namespace slot of the allow checker with a token slot, checked in `admit` after authentication.
- Add the route of the token list, its action and its fresh step rule (open question 1), and the field `ipAllowlist` of the token resource.
- Update docs/guides/ip-allowlists.md and extend the e2e step.

Out of scope:

- The installation list, the namespace list, the one switch, the actions of the namespace list and the byte limit. The change `ip-allowlists` holds them.
- Token lifetime and expiry email.
- A change of who may make a token (ADR 70 decision 5).

Depends on:

- `ip-allowlists`. The token list reads the one switch `listsEnabled` (owner decision 9 of that change), the limit of 4096 bytes and the HTTP table of "deny when any list denies" that it adds.

## Usability

- A person can read the list of a token in the token resource, `[]` when none.
- A list that excludes the runner blocks the pipeline. The docs page names the recovery: clear the list, or revoke and make the token again.

## ADR check

Each quote is copied from `docs/adr/`. The chains are those of `docs/adr/INDEX.md` on main.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6, 7 and 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decisions 1, 6, 7 and 9. Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote (decision 8, `docs/adr/0055-sign-in-methods-in-milestone-4.md:89`): "IP allowlists are off by default, and each scope opts in."
- Quote: "Then each scope opts in on its own: the installation, each namespace and each access token."
- Quote: "When the feature is on, each list applies to console sign-in and to requests with an access token."
- Effect: Decision 8 puts a list on each access token. Main has none. This change adds it.
- Conflict: none. Decision 8 names no setter and no step for a token list: open question 1.

**ADR 53 and ADR 70: access tokens** (not owner-tagged; the ADR 53 text of decision 3 records "The owner wrote this with \"I guess\".").

- Quote (ADR 53 decision 3, `docs/adr/0053-access-token-life-cycle.md:54`): "Who can issue which token is part of the authorization model. Each kind of token has its own action."
- Quote (ADR 70 decision 5, `docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md:135`): "Each person makes only its own personal tokens."
- Changed by: see the rows of ADR 53 and ADR 70 in `docs/adr/INDEX.md`.
- Effect: These decide who makes a token. They do not say who sets the list of a token or whether a change needs a fresh step. Open question 1.
- Conflict: none.

**ADR 212: ... one settings API** (not owner-tagged as a whole; decision 7 carries "(owner decision 2026-10-18)").

- Quote (decision 3): "A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure."
- Changed by: ADR 385 changes decision 3 (owner-tagged, 2026-11-15), and the other ADRs of the row in `docs/adr/INDEX.md`.
- Effect: A token list is not a setting. The rule is the model for option B of the question.
- Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged).

- Quote (decision 3): "An access token never gives one. The refusal is `403` with `mfa.fresh-step-required`."
- Quote (decision 2): "When the setting is off, the server asks a person for no fresh authentication."
- Changed by: none (`docs/adr/INDEX.md`, row 385: "-").
- Effect: A token cannot do a step that needs a fresh authentication. Main has no `freshAuthEnabled` yet, so the fresh step on main is always on.
- Conflict: none.

## Open questions

Open questions: 1, parked. The owner decides. This is security semantics, so the PM takes no provisional answer. The question blocks tasks 3.1 and 3.2, and the rows of tasks 1.2 and 2.3 that depend on it. Tasks 0.1, 1.1, 2.1, 2.2 and 2.4 do not depend on it.

1. **Who sets the list of an access token, and does a change need a fresh step?** (owner)
   - Context: ADR 55 decision 8 says each access token opts in on its own. No ADR names who may set the list, or the step. The guide says a token gets `mfa.fresh-step-required` on the namespace routes. ADR 385 decision 3 says a token never gives a fresh step. A token cannot make a token (`pkg/accesstoken/scope.go:44 (ErrSignInRequired)`), so a person must sign in to make one.
   - Option A: The list is set only when the token is created or rotated, by the maker of the token. To change it, rotate the token. No new route and no fresh step. Cost: a longer list needs a new secret.
   - Option B: The maker sets the list with a route under the token (`PUT` and `DELETE`), from a console session only. A change that widens or clears the list needs a fresh step when `freshAuthEnabled` is on (ADR 212 decision 3, ADR 385). Narrowing needs none. Cost: one new route and one new action.
   - Option C: Option B, and a holder of `server:manage-tokens` may also clear (not widen) the list of any token. Cost: a second actor, and the owner must say whether an administrator may weaken a token.
   - Recommendation: Option B. It follows the rule that ADR 212 gives to settings, and a token holder cannot widen its own reach.
   - 3 a.m. summary: a token can limit itself to a network. Who can change that limit, and does it need a recent sign-in? We propose that only its maker can, from a console session, and a recent sign-in is needed only to widen it.
   - Sources: ADR 55 decision 8, ADR 53 decision 3, ADR 70 decision 5, ADR 212 decision 3, ADR 385 decision 3. Benchmark: AWS IAM keeps a source IP condition in the policy of a key, and only a principal that can edit the policy changes it. Google Cloud access levels are set by an administrator, not by the key.

Questions considered and answered:

- Which switch turns the token lists on? The setting `ipAllowlist.enabled`, by owner decision 9 of `ip-allowlists`.
- Which byte limit holds? 4096 bytes, by owner decision 11 of `ip-allowlists`.
- May a token hold an empty list? No. An empty value means "no list". The route clears the list instead.
