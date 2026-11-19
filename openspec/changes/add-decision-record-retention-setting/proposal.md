---
version: 2
---

Issue: #3485.

## Why

**Persona: operator.** An operator runs an installation where each OpenTofu lock request writes one state.write decision record. The records of state.write live for the audit retention (ADR 229 decision 5), but main has no setting with that name. The operator cannot read or set how long the records live, and the sweep that prunes them (the follow-up retention-sweep-schedule-lease) has no cutoff to read. The operator needs one setting with an effective value in the API, a safe default and a refusal of a value that a typo could cause.

The user path this change must make work:

1. **API.** The operator reads GET /api/dustpan/v1/settings and sees the item `decisionRecords.retentionHours` with value `0`, source `default` and the doc text. `0` means keep forever.
2. **Change.** The operator sends a merge patch with `If-Match`, for example `{"decisionRecords.retentionHours":8760}`. The value is a JSON number. The server stores it and writes the audit entry of the settings change. A change to a shorter period than the one in force, or from keep-forever to a period, needs a fresh authentication, because the change deletes evidence sooner.
3. **Docs page.** The operator reads docs/guides/decision-record-retention.md. It names the key, the default, the minimum, the PCI DSS v4.0.1 requirement 10.5.1 (at least 12 months) and the GDPR storage limitation principle (ADR 68 decision 4), and says that no job deletes records yet and that the setting never deletes an audit entry.
4. **E2e.** A step reads the item, patches it, reads it again and checks the refusals.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The value is not `0` or a whole number of hours from 24 to 87600. | The answer is 400 and names the key, the value and the allowed range. | `GET /api/dustpan/v1/settings` |
| The change shortens the period and the caller has no fresh authentication. | The answer is the step-up refusal of the settings resource. | `GET /api/dustpan/v1/settings` |
| The revision in `If-Match` is old. | The answer is 412. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- Main has no decision record or audit retention setting. Proof: `rg -in 'audit.?retention|auditRetention|decisionRecords' pkg internal --type go` finds only the text of one denial test. The soft-delete retention and the history retention are other settings, and neither prunes decision records.
- The settings resource takes a new declaration with no new route. Proof: `pkg/dustpan/settings_resource.go:32` (`settingDeclarations`) builds the installation list from `ipallow.SettingDeclarations()` and the drain period.
- A declaration can mark a change as one that reduces security, and the resource then needs a fresh authentication. Proof: the comment of `setting.Change` documents `ErrFreshStepRequired`, and the declarations of the IP allowlist and of the temporary administrator session limit set `ReducesSecurity`.
- The audit entry of a change is written by `setting.Change` in the same transaction. Proof: the comment of `setting.Change`: "writes the values, the new revision and the audit entry".
- No code deletes an audit entry. Proof: ADR 68 decision 2 leaves "how" to a later ADR, and this change adds no delete of an audit entry.

## What Changes

- Add the declaration `decisionRecords.retentionHours` (installation level, kind int, default `0`, database layer, applies live) in NEW function `decisionRecordRetentionDeclaration` in pkg/dustpan/settings_resource.go, built from the setting-free functions of NEW pkg/gate/retention_setting.go (pkg/gate is a leaf of the domain graph and does not import the setting package). `Check` accepts `0` and a canonical whole number from 24 to 87600 (10 years). `ReducesSecurity` is true when the new period ends records sooner than the old one (keep-forever is the longest).
- Add the declaration to the settings resource in pkg/dustpan/settings_resource.go.
- Add the term "Decision record retention" to GLOSSARY.md.
- Extend docs/guides/decision-record-retention.md with the setting, the minimum and the rules of ADR 68 decision 4.
- Add an e2e step for the read, the change and the refusals.

Out of scope:

- The sweep, its schedule and its lease (retention-sweep-schedule-lease). Until it ships, no job deletes a record, and the doc page says so.
- The deletion of audit entries and the chain anchor (the later ADR of ADR 68 decision 2). The setting never touches an audit entry. Its name says what it prunes, so no operator reads it as audit retention.
- A namespace level of the setting.

## Usability

Where the path still costs the person time or surprises them:

- A value of `0` could look like "delete at once". The doc text of the setting and the docs page say "0 keeps records forever".
- The setting alone deletes nothing. The docs page says so and names the follow-up.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The installation settings list is built from declarations. (`pkg/dustpan/settings_resource.go:32 (func settingDeclarations)`)
- A declaration that the server makes itself is added to the same list. (`pkg/dustpan/settings_resource.go:18 (func drainPeriodDeclaration)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 68: Audit log retention, and the audit switch stays a start setting** (owner-tagged: the ADR records the owner's decisions of 2026-10-14).

- Decisions that apply: 1, 2 and 4 (current).
- Changed by: none (the index of ADRs shows "-"). It extends ADR 60 decision 21.
- Current rule: this ADR.
- Quote, decision 1: "The server keeps audit entries forever by default. A new installation deletes no audit entry."
- Quote, decision 2: "Retention is a setting. An operator can set a retention period for the installation. The server then deletes entries that are older than the period. A change of the period writes an audit entry. The deletion keeps the chain check of ADR 60 decision 17 and ADR 64 valid. A later ADR decides how, for example with a retention anchor (RFC 22)."
- Quote, decision 4: "The guides state the rules that apply to retention."
- Effect: The change adds a retention setting with the default keep-forever and an audit entry for each change. It applies to decision records only. It does not delete an audit entry, so the part of decision 2 that waits for the later ADR stays open and the chain check is not touched. The name "decision record retention" keeps it apart from the audit entries.
- Conflict: none. The owner words "Keep forever by default" hold for the new setting.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (decision 3 is current as changed by ADR 385 and ADR 227; ADR 385 is owner-tagged).

- Decisions that apply: 3 (current, with the changes below).
- Changed by: ADR 227 changes decision 3 (the window and the set to 0 item). ADR 385 changes decision 3 (a fresh step is opt-in; owner-tagged, 2026-11-15). ADR 385 also changes ADR 227 decision 3 and extends ADR 227 decision 2.
- Current rule: ADR 212 decision 3 as changed by ADR 227 and ADR 385.
- Quote, ADR 212 decision 3: "A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure. When one key of a `PATCH` reduces security, the whole `PATCH` needs the step."
- Quote, ADR 227 decision 3: "A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR 212 decision 3 says for a weaker setting."
- Quote, ADR 385 decision 2 (owner, 2026-11-15): "When the setting is off, the server asks a person for no fresh authentication." The setting is `freshAuthEnabled`.
- Quote, ADR 385 decision 3 (owner): "A fresh authentication is a built-in MFA step or a factor of ADR 301. ... An access token never gives one. The refusal is `403` with `mfa.fresh-step-required`."
- Effect: "Shorter period needs a fresh authentication" means "needs the fresh step of the settings resource, when that step applies". The settings resource asks for the step when a declaration's `ReducesSecurity` is true. This change sets `ReducesSecurity` and adds no rule of its own. When the change that adds the setting `freshAuthEnabled` lands and the setting is off, the resource asks for no step. The e2e step that expects 403 `mfa.fresh-step-required` for an access token must then change in that change: with the setting off the token is accepted, with the setting on it is refused.
- Conflict: none. The owner-tagged opt-in of ADR 385 is not weakened: this change reuses the step of the settings resource and decides nothing about when it applies.

**ADR 229: Decision records, the simulate route and starter packs** (decision 5 is not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "A record of `state.write` or of an access limit lives for the audit retention (ADR 68)."
- Effect: The setting gives "the audit retention" of this quote a stored value for state.write records. The quote names ADR 68 and not a setting name, so the name `decisionRecords.retentionHours` does not conflict.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the setting need the owner? No. ADR 68 decision 1 and the owner words fix the default (keep forever). The key name and the minimum are not security semantics of an owner-tagged decision.
- Why a minimum of 24 hours and not the PCI DSS minimum of 12 months? PCI DSS v4.0.1 requirement 10.5.1 binds an operator in the scope of PCI DSS, and the server cannot know that. A floor of 12 months would stop an operator outside that scope from a short period (ADR 68 decision 2: "An operator can set a retention period"). The floor of 24 hours stops a typo. The docs page states the 12 months (ADR 68 decision 4).
- Why is shortening a reduction of security? A shorter period deletes evidence sooner. The settings resource asks for a fresh authentication for such a change, as it does for the IP allowlist.
- Is the setting at the namespace level too? No. The sweep runs for the installation and ADR 68 decision 2 says "for the installation".
- Does a value that the store holds but the declaration refuses (after a downgrade) delete records? No. `effectiveOf` ignores a stored value that `Check` refuses and uses the default, which keeps forever (fail closed).

Drafted by the PM from ADR 68, ADR 229, the finding of the M9 foreman on retention-sweep-schedule-lease and the code of main.
