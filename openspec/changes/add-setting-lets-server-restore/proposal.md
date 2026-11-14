---
version: 2
---

Issue: #611.

## Why

**Persona: site administrator.** A site administrator manages who can restore access data. The administrator needs an explicit, audited switch that is off by default, so that a restore by a caller without server:restore-access cannot replace the access tables. When the switch is on, each holder of server:restore can replace them in a restore. The writer rule is a provisional decision: the setting changes only through the settings API, by a caller with server:manage-settings plus server:restore-access, with a fresh session step. The owner can reverse it on review.

The user path this change must make work:

1. **Docs page.** Read the docs page for the restore access setting and its default.
2. **API.** Read the setting and change it through PATCH /api/dustpan/v1/settings with a fresh session step.
3. **API.** Start a restore through POST /api/server/restores and check the audit entry for the change.
4. **E2e.** Run the e2e step: change the setting, then restore, against the real binary over TLS.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Restore with only server:restore while the setting is off | 403 server:restore-access required. The access tables stay unchanged. Read the stored value. | `GET /api/dustpan/v1/settings` |
| Setting change without a fresh session step | 403 mfa.fresh-step-required. The change did not save. Check the value after a fresh session step. | `GET /api/dustpan/v1/settings` |
| Setting change by a caller without both actions | 403 access.denied. The value stays unchanged. Ask a holder of both actions to make the change. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The ADR says the setting is off by default and a change writes an audit entry. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The issue asks for the setting definition in the CLI settings file and a route in the server API directory. Proof: internal/cli/settings.go, internal/api/server
- The generic installation settings route exists on main. Proof: route ANY/GET/PATCH /api/dustpan/v1/settings in the pack main list

## What Changes

- Add an installation setting key for the restore of access tables, default off, API only.
- Gate the key on server:manage-settings plus server:restore-access with a fresh session step. Write an audit entry with the old and the new value.
- Let the restore apply step replace the access tables for server:restore holders when the start value is on, with the two-path guard and a restore audit entry.
- List the widened restore state in the admin access report when the setting is on.

Out of scope:

- Default restore behavior without the setting (issue #553, lands first)
- New roles or new actions beyond server:restore-access
- Break-glass mode 2 recovery

Depends on:

- add-one-generic-settings-api
- issue-553-default-restore
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A site administrator must learn that the setting is off by default and that it widens each later restore until turned off.
- A refused restore names the missing action and the one command that shows the value.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- settings.value-invalid error code exists (`internal/errcode/catalog_settings.go:21`)
- restore.blobs-missing error code exists (`internal/errcode/catalog_restore.go:7`)
- access tables backup test exists (`pkg/backup/access_tables_test.go:13`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current), 24 (changed), 29 (current).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "The access tables restore only with their own action. A new action, `server:restore-access`, lets a restore replace the access tables. Of the built-in roles, only `site-administrator` holds it. A restore by a caller with"
- Quote: "A setting can change this. An installation setting can let each holder of `server:restore` replace the access tables. It is off by default. A change of the setting writes an audit entry."
- Quote: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts. The server refuses each change that leaves fewer than two working paths, a restore included."
- Quote: "A periodic admin access report. The API answers it first. Email and webhook delivery come later."
- Effect: Decision 7 keeps the default refusal. Decision 8 permits the setting as the one exception. The widened restore keeps the guard of decision 24. Decision 29 covers the report.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: A change of the setting weakens a restore guard, so it needs the one fresh-authentication window.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: The setting change needs a session fresh step. The e2e uses a console session.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "E2E: console session changes the setting, then a restore runs over TLS" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: threat 1 (A caller with only server:restore turns ): no guard and hostile test for boundary check, encode or contain, limit, fail closed; a code check fails: threat 2 (A stored value of unknown text widens re): no guard and hostile test for boundary check, encode or contain, limit, authorize at point of use; a code check fails: and 1 more.

Questions considered and answered:

- What is the default restore behavior for the access tables? ADR 70 decision 7: refuse without server:restore-access. Decision 8 permits the one exception when the setting is on.
- May a flag, env or file set the setting? No. The change path is the settings API only, with a fresh step (ADR 227 decision 2).
- Does an access token give the fresh step? No (ADR 297 decision 18).
- Does a refused change write a setting-change entry? No. ADR 70 decision 8 requires an entry for a saved change only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
