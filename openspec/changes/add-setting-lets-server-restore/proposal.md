Issue: #611.

## Why

**Persona: site administrator.** Site administrator manages who can restore access data. The admin needs an explicit, audited switch that is off by default so a restore by a caller without server:restore-access cannot replace the access tables, and that when on lets each holder of server:restore replace them during a restore.

The user path this change must make work:

1. **Docs page.** Read the docs page for the restore access setting and its default.
2. **API.** Read the setting and change it through the installation settings API with a fresh step.
3. **CLI.** Run a restore through the CLI and check the audit entry for the change.
4. **E2e.** Run the e2e restore step against the real binary over TLS.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Restore with only server:restore while the setting is off | 403 server:restore-access required and the access tables stay unchanged; run the settings read to check the value. | `GET /api/dustpan/v1/settings` |
| Setting change without a fresh authentication step | 403 mfa.fresh-step-required and the change did not save; do a fresh sign-in step then repeat the change. | `POST /api/dustpan/v1/me/fresh-step` |
| Setting change by a caller without the gated actions | 403 forbidden and the value stays unchanged; ask a site administrator with the restore-access grant to make the change. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- ADR 70 decision 8 states the setting is off by default and a change writes an audit entry, without fixing the entry format. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The issue asks for the setting in the CLI settings file and a REST route in the server API. Proof: internal/cli/settings.go, internal/api/server

## What Changes

- Add installation setting for restore of access tables, default off, API only.
- Add REST route with declared actions and object plus audit entry with old and new value.
- Let restore apply step replace access tables for server:restore holders when the setting is on, with the two-path guard.
- List the widened restore state in the admin access report when the setting is on.

Out of scope:

- Default restore behavior without the setting from issue #553, which must land first
- New roles or new actions beyond server:restore-access
- Break-glass mode 2 recovery

Depends on:

- add-one-generic-settings-api
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A site administrator must learn that the setting is off by default and that turning it on widens each later restore until turned off.
- A refused restore must name the missing action and the one settings command that shows the value.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current), 24 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "The access tables restore only with their own action. A new action, `server:restore-access`, lets a restore replace the access tables. Of the built-in roles, only `site-administrator` holds it. A restore by a caller with"
- Quote: "A setting can change this. An installation setting can let each holder of `server:restore` replace the access tables. It is off by default. A change of the setting writes an audit entry.  ### Trust rules (D5, #554)"
- Quote: "A guard keeps two working paths. The first setup cannot finish without two break-glass accounts. The server refuses each change that leaves fewer than two working paths, a restore included. It counts only an account that"
- Effect: Decision 7 keeps the default refusal without server:restore-access. Decision 8 permits the setting as the only exception that lets server:restore replace the access tables. The widened restore still keeps the two-working-path guard of decision 24.
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business day** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 149 extends decisions 12 and 15; ADR 210 changes; ADR 222 changes decision 9; ADR 227 changes decision 9; ADR 297 changes decisions 1, 3 and 9. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 149, 210, 222, 227, 297.
- Quote: "The opt-in of the installation. The installation setting `breakGlassReview.enabled` is off by default. A site administrator changes it with `server:manage-settings`."
- Effect: Decision 2 is precedent for one installation boolean that is off by default and changed through the API with an audit record. It does not state the writer rule for this setting; the writer rule stays an open owner question.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes). Each step that an ADR names as "fresh MFA", "a fresh MFA step" or "a fresh authentication" uses it: token creation and rotation, a secret reveal, the changes of ADR 70 decisions 16, 17 and 23, the switches of ADR 125 decision 3, a confirmation, a break-glass review, a weaker setting, an approval when ADR 225 decision 5 or its approval rule asks for one, and a ChatOps approval (ADR 146) on the same terms."
- Effect: A change of the setting needs the one fresh-authentication window because it weakens a restore guard.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "The map of each action. Operator actions keep the area `installation:`. A namespace action takes the area `namespace:` and has the namespace as its object. "Both" means that the installation action stays for objects of t"
- Effect: The new setting route declares an installation action with the installation as object.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now). This changes ADR 70 decision 24. The server refuses only a change that leaves no enabled break-glass principal. The accepted text: "a site admin may disable a break-glass principal, except the last enabled one (refused in the write transaction, 409)". "The guard still applies". "each disable is audited and notifies the other admins". "re-enable stays checked". "disable works in maintenance mode". "delete and sweep follow the same 'not the last one' rule". (PM, for the owner): the server writes th"
- Effect: The two-path count on the widened restore runs in the same write transaction, so a concurrent disable cannot bypass the guard.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change lets a caller with server:restore replace access tables when the setting is on, but ADR 70 decision 7 (quoted in the ADR check) says access tables restore only with server:restore-access, and the proposal's own error table gates restore on server:restore-access; the spec's 'Restore with setting on' requirement contradicts the action map and the issue's point 4 is unresolved, so the gating action is inconsistent across proposal, spec and ADR. Resolution: answered: Spec now states off needs server:restore-access and on accepts server:restore as the decision 8 exception; errors and design use the same rule.
- R1.2 (open question): The open questions section reads 'none' while it lists a provisional decision on who may change the setting. The issue states the owner's decision must be obtained before work starts, so the spec-ready gate is not met and the provisional answer cannot stand as settled. Resolution: answered: Open questions keeps the writer question for the owner, so the change is not marked spec-ready.
- R1.3 (adr conflict): The proposal states the setting is changed through an API with server:restore-access and a fresh MFA step, but ADR 142 decision 2 says a site administrator changes installation settings with server:manage-settings; the change does not reconcile which action gates the setting write. Resolution: answered: Added ADR 142 check: the write needs server:manage-settings plus server:restore-access with a fresh step; the writer question stays open for the owner.
- R1.4 (missing scenario): No scenario covers a setting change without a fresh authentication step, although ADR 227 decision 2 lists a weaker setting as a fresh-step action and the error table names 403 mfa.fresh-step-required. Resolution: answered: Added refused-without-fresh-step scenario to the setting change requirement.
- R1.5 (missing scenario): No scenario covers a refused setting change by a caller without the right, though tasks 1.1 names a refused-change test. Resolution: answered: Added refused-without-action scenario to the setting change requirement.
- R1.6 (failure mode): A restore that replaces access tables while the setting is on can remove or disable break-glass accounts; ADR 70 decision 24 says the two-working-path guard applies to a restore, and the spec does not require that check when the widened restore runs. Resolution: answered: Added two-working-path requirement for the widened restore.
- R1.7 (failure mode): The design reads the setting in the restore apply transaction but does not say what happens on a concurrent change during a restore or when the stored value cannot be read; the branch may apply a stale value. Resolution: answered: Design snapshots the value at apply start and fails closed on read failure in the same transaction.
- R1.8 (open question): The Risks section names an operator who forgets the setting is on, but no requirement sets an expiry, a periodic report or an alert for the widened state, though ADR 70 decision 29 provides the admin access report. Resolution: answered: Added effective-value read requirement so the widened state stays visible; no expiry added without an ADR.
- R1.9 (wrong claim): The proposal cites ADR 70 as defining the audit entry for the setting change, but the quoted decision 8 only says a change writes an audit entry and does not define its format, so the evidence line overstates the quote. Resolution: answered: Evidence claim now states decision 8 requires an audit entry without fixing its format; old and new value come from the issue.
- R1.10 (scope): The dependency check-reach-when-grant-membership is listed but the issue says it is blocked by #553 and the tasks do not show it is merged, so the work may start on an unmet dependency. Resolution: answered: Removed the unmet dependency; the #553 default behavior is now an explicit predecessor in out_of_scope.
- R1.11 (open question): The design's structure (installSettings constructor) does not say how the constructor rejects unknown values when the setting is read from storage, so the bug class of a stored invalid value is not covered. Resolution: answered: Structure now rejects unknown stored text with an error and fails closed.
- R2.1 (owner decision conflict): The issue states the owner's decision on who may change the setting must be obtained before work starts, and the change takes a provisional answer (server:manage-settings plus server:restore-access, API only, fresh step) while marking Open questions as none; the spec-ready gate is not met for the writer rule. Quote from ADR 70 decision 8 and issue #611 'Get the owner's decision before the work starts'. Resolution: open question: Writer rule stays an open owner question with decides owner, so the change is not spec-ready until the owner decides.
- R2.2 (adr conflict): The spec 'Restore with setting on' lets a caller with server:restore replace access tables, while ADR 70 decision 7 says access tables restore only with server:restore-access; the widened path is the decision 8 exception, but the spec does not state that the setting read happens for each restore caller, and the gating of the off path (server:restore-access) is not written in the spec requirement text. Resolution: answered: Off-path text now states without server:restore-access, on-path text states the decision 8 exception, and design states the read happens at the start of each apply.
- R2.3 (failure mode): The two-working-path guard is required only as a restore result rule; the design and spec do not say how a concurrent disable of a break-glass principal (ADR 306 decision 3, owner-tagged) during the widened restore is serialized with the count, so the guard can be bypassed by a race. Resolution: answered: Design and spec now run the two-path count in the same write transaction as the apply, with ADR 306 check; a concurrent disable serializes with the count.
- R2.4 (missing scenario): No scenario covers a setting change that would itself be refused or turned on while a restore is running, or a restore that starts after the setting is turned off mid-restore; the snapshot rule covers only one case. Resolution: answered: Added snapshot scenarios: restore uses the start value when the setting changes mid-restore and when it turns off mid-restore.
- R2.5 (failure mode): The design says a read failure fails closed, but the spec has no scenario for an invalid stored value on the restore path; only the service constructor is named in the layers table, so the restore apply path with unknown stored text is untested. Resolution: answered: Added invalid stored value scenario on the restore path that fails closed.
- R2.6 (wrong claim): The ADR check says ADR 142 decision 2 gates installation settings with server:manage-settings, but ADR 142 decision 2 covers only the breakGlassReview.enabled setting; the general rule for all installation settings is not in the quoted decision, so the claim is overstated. Resolution: answered: ADR 142 effect now states precedent only for one installation boolean, not a general rule for all settings.
- R2.7 (scope): Tasks 1.2 and 2.3 and the issue point 1 place the setting in internal/cli/settings.go, while the spec says API only and no CLI layer; the CLI file is named as a place for the setting, which conflicts with the API-only decision. Resolution: answered: Design states settings.go holds only the stored definition with no flag, env, or file layer; the change path is API only.
- R2.8 (open question): The Risks section names an operator who forgets the setting is on, and the answer is only an effective-value read; no requirement covers a periodic report of the widened state, though ADR 70 decision 29 provides the admin access report that should list it. Resolution: answered: Added admin access report requirement that lists the widened state when the setting is on, plus the effective-value read.
- R3.1 (owner decision conflict): The issue states the writer rule must be decided by the owner before work starts, yet proposal.md lists Open questions as none and takes a provisional writer rule (server:manage-settings plus server:restore-access, fresh step); the spec-ready gate is not met and the change cannot be built right without the owner's answer. Resolution: open question: Writer rule stays an open owner question with decides owner; the change is not spec-ready until the owner decides.
- R3.2 (adr conflict): ADR 70 decision 8 says the setting lets each holder of server:restore replace access tables, but the proposal's Why and error table gate restore on server:restore-access, and the design's layer table states the off path needs server:restore-access; the gating action for the default path is stated inconsistently across proposal, design and spec. Resolution: answered: Why, errors, design and spec now state one rule: off needs server:restore-access and on accepts server:restore as the decision 8 exception.
- R3.3 (failure mode): The two-path guard is said to run in the same transaction as the apply, but no scenario covers a concurrent disable of a break-glass principal racing a widened restore; ADR 306 decision 3 (owner-tagged) requires the count in the write transaction, and the spec has no requirement for that serialization. Resolution: answered: Added serialization requirement and race scenario: the two-path count runs in the same write transaction as the apply.
- R3.4 (missing scenario): No scenario covers a restore whose backup has access tables while the setting is off and the caller holds server:restore-access; the default-off scenario only covers the refusal path. Resolution: answered: Added scenario where setting off and caller holds server:restore-access replaces the access tables.
- R3.5 (open question): The spec does not say what the audit entry records for a change refused for a missing action or a missing fresh step; ADR 70 decision 8 requires an audit entry only for changes, so refused attempts have no stated record. Resolution: answered: Spec now states a refused setting change writes no setting-change entry; only a saved change writes one with old and new value.
- R3.6 (wrong claim): Tasks 1.2 and the issue put the setting in internal/cli/settings.go, while the design says the file holds only the stored definition; the proposal's evidence cites that file as where the setting is requested, which the issue supports but the proposal's API-only claim does not reconcile. Resolution: answered: Design states settings.go holds only the stored definition with no flag, env, or file layer; the change path is API only.
- R3.7 (scope): Task 2.1 and the 'Widened state reported' requirement add an admin access report change that is not in the proposal's What Changes list, so the change does work beyond its stated scope. Resolution: answered: What Changes now lists the admin access report listing for the widened state.
- M.18 (dependency): The change adds its own REST route for one installation setting, which duplicates the generic settings API. It needs issue #553, which is not in this milestone. Resolution: answered: Removed the separate route; the key is registered in the generic settings API with a locked API-only fresh-step write, depends_on names add-one-generic-settings-api, and issue #553 stays as the predecessor that must land first.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Who may change the setting, and may a flag, env, or file set it?
   - Options: API only with server:manage-settings plus server:restore-access and a fresh step; API only with server:restore-access and a fresh step.
   - Provisional decision (owner review 14 Nov): API only with server:manage-settings plus server:restore-access and a fresh step.
   - Reason: A setting that widens restore of access tables is an escalation path, so it needs the narrowest writer and no silent layer. Benchmark: AWS: sensitive account recovery settings change only through the control plane with MFA and an audit record.
   - Persona impact: Site administrator does one API call with a fresh step; no other persona gains a path. Escape hatch: A site administrator turns the setting off again; each change has an audit entry with old and new value.

Questions considered and answered:

- What is the default restore behavior for the access tables? ADR 70 decision 7 keeps the default refusal without server:restore-access, and decision 8 permits the single exception where the setting is on and the caller holds server:restore. The restore reads the stored value at the start of each apply.
- May a flag, env, or file set the setting without a fresh step? No. ADR 227 decision 2 gives the one window for a weaker setting, and the issue asks for API only with a fresh step and no flag, env, or file layer.
- Does a refused setting change write an audit entry? ADR 70 decision 8 requires an entry only for a change. A refused setting change writes no setting-change entry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
