---
version: 2
---

Issue: #3485.

## Why

**Persona: operator.** An operator runs an installation where each OpenTofu lock request writes one state.write decision record. The records of state.write live for the audit retention (ADR 229 decision 5), but main has no setting with that name. The operator cannot read or set how long the records live, and the sweep that prunes them (the follow-up retention-sweep-schedule-lease) has no cutoff to read. The operator needs one setting with an effective value in the API, a safe default and a refusal of a value that a typo could cause.

The user path this change must make work:

1. **API.** The operator reads GET /api/dustpan/v1/settings and sees the item `decisionRecords.retentionHours` with value `0`, source `default` and the doc text. `0` means keep forever.
2. **Change.** The operator sends a merge patch with `If-Match`, for example `{"decisionRecords.retentionHours":"8760"}`. The server stores it and writes the audit entry of the settings change. A change to a shorter period than the one in force, or from keep-forever to a period, needs a fresh authentication, because the change deletes evidence sooner.
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
