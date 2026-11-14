---
version: 3
---

Issue: #897, #952.

## Why

**Persona: site administrator.** A site administrator manages installation settings and a namespace administrator manages namespace settings. Main has the settings routes, but the change needs per-setting validation, locks, fresh authentication and audit in one shape. The temporary administrator lifetime is a fixed 24 hours, so administrators cannot bound it.

The user path this change must make work:

1. **API.** List declared settings with `GET /api/dustpan/v1/settings` and read effective values, sources, locked flags and the revision.
2. **CLI.** Change one database value with the generated CLI command for `PATCH /api/dustpan/v1/settings`, with an Idempotency-Key and the revision. Read the new value and revision.
3. **API.** Try a locked key, an unknown key, a weaker change with no fresh step, a missing Idempotency-Key and a stale revision. Read each refusal.
4. **Console.** Open the settings page of a scope. The console reads the same resource and shows locked keys with no control.
5. **Docs page.** Read the settings guide and the note on the temporary administrator session limit.
6. **E2e.** Run the settings step against the real server: `PATCH /api/dustpan/v1/settings` with a console session cookie that holds a fresh step, then the same weaker patch with an access token, which is refused with 403.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| PATCH reduces security without a fresh authentication | 403 with code mfa.fresh-step-required. The error names the path of a new sign-in. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH writes a key that the static layer locks | 409 with code settings.locked. The error names the switch and says the operator locked it. | `GET /api/dustpan/v1/settings` |
| PATCH names an undeclared key | 422 with code settings.key-unknown naming the key. | `GET /api/dustpan/v1/settings` |
| PATCH writes a sensitive key that only an environment variable or a file can set | 409 with code settings.static-only naming the key. | `GET /api/dustpan/v1/settings` |
| PATCH sets tempadmin.session-limit outside 1 minute to 24 hours | 422 with code settings.value-invalid naming the key and the range. | `GET /api/dustpan/v1/settings` |
| PATCH has no Idempotency-Key header | The refusal of the idempotency rule of the tool-neutral API. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH carries a revision that is not the current revision | 412 with code settings.revision-stale. Nothing changes. | `GET /api/dustpan/v1/settings` |
| The stored session limit is outside the range and the host command refuses to create a temporary administrator | The host command refuses, names the key and the reset option, and writes an audit entry. No record exists. | `opendustpan host create-temp-admin --reset-session-limit --reason "<text>"` |

Evidence for the claims above:

- The host command for recovery exists on main. Proof: The pack lists the path cmd/opendustpan/host.go as existing.
- Temporary administrator domain code lives in pkg/tempadmin. Proof: The pack lists the path pkg/tempadmin as existing. Issue 952 states a fixed 24-hour lifetime.
- Settings routes and error codes exist on main. Proof: The pack main list names ANY/GET/PATCH /api/dustpan/v1/settings and the namespace route, and error codes in internal/errcode/catalog_settings.go.
- The four settings routes have no console and weak audit or refusal cells. Proof: coverage_gaps of the pack lists them with empty cells.

## What Changes

- Complete GET and PATCH on both settings routes with per-setting schema, atomic merge patch, revision, locks and audit entries.
- Enforce fresh authentication for changes that reduce security.
- Add the installation setting tempadmin.session-limit, default 1 hour. A record and its token end at the shorter of the setting and the token policy maximum. A change applies to new records only.
- Add a reset option to the host command, with a reason.
- Carry the amendment of ADR 245 decision 10 as a proposed text in this change; the owner confirms it.

Out of scope:

- Individual new settings beyond the session limit; each feature adds its own key.
- Changes to the recovery modes.
- The console code; it lives in the console repository.

## Usability

Where the path still costs the person time or surprises them:

- Two scopes with two paths can confuse people; the docs page shows both paths with commands.
- The words of ADR 245 decision 10 still say 24 hours until the owner confirms the amendment; the docs page states the 1 hour setting.
- The console page is built in the console repository.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The error codes settings.key-unknown, settings.locked, settings.static-only and settings.value-invalid exist. (`internal/errcode/catalog_settings.go:7`)
- The temporary administrator package exists. (`pkg/tempadmin/authenticator.go:71`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P12)**, Batch 10: #952 temporary administrator session limit: "1 hour default, as a setting; the record and its token end at the shorter of the setting and the token policy maximum. Reason: the owner's own words (21.6, and ADR 306 d1 "session limit 1 hour, as a setting"); short-lived emergency access is the norm (AWS, Microsoft). ADR 245 d10 (24 h) text waits for the owner."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Quote: "A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure."
- Effect: Implements the two resources, the merge patch and the fresh step for weaker changes. With built-in MFA off and no SSO provider, no step is needed.
- Conflict: none.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 5 (current), 10 (current).
- Changed by: ADR 295 changes decision 12. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295.
- Quote: "One record lifetime. A record and its token end at the same time: 24 hours after the create, or the maximum of the token policy when that is shorter."
- Effect: The setting changes the 24 hours to a 1 hour default. Owner decision, 13 Nov 2026 (queue P12, provisional) sets it. This change carries the amendment text of decision 10 for the owner to confirm; the limit key ships with that amendment. The host reset follows decisions 2, 3 and 5.
- Conflict: rfc_needed.

**ADR 219: A lock in the static layer can turn a feature off (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A lock can set a switch to off (owner). The static layer can lock each declared switch to on or to off, also a security switch."
- Quote: "A lock is visible. The settings resource of each scope (ADR 212 decision 7) answers the switch with `locked: true` and `source: static`."
- Effect: A read shows a locked key as locked. A write names the lock.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).**"
- Effect: Both PATCH routes accept the Idempotency-Key.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 3).

- Decisions that apply: 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A change to a longer window needs one, as ADR 212 decision 3 says for a weaker setting."
- Quote: "An access token never gives a fresh authentication."
- Effect: A longer limit is weaker and needs the step. An access token never gives the step, so a weaker patch with a token is refused.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner)."
- Effect: A person with no factor cannot make a weaker change; the refusal names the enrollment page.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: design decision "Temporary administrator session limit": a limit with no number; give the number, the unit and the scope.

Questions considered and answered:

- What is the shape of the API? ADR 212 decision 7 and ADR 219 decision 3.
- What ends a temporary administrator record? Owner decision, 13 Nov 2026 (queue P12, provisional): the shorter of the setting and the token policy maximum.
- Does ADR 245 decision 10 block the limit? Its text says 24 hours. The change carries the amendment text. The key ships with the owner's confirmation of the amendment.
- What if the audit write fails? The audit entry is in the same transaction, so the whole PATCH fails.
- How does an operator recover from a bad stored limit? A null PATCH, or the host reset option with a reason.
- Can an access token or an agent make a weaker change? No. ADR 227 decision 6 and ADR 354 say they never get a fresh step.
- Do audit entries leak sensitive values? No. A sensitive key is static-only and cannot be patched, and audit entries mask any sensitive value.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
