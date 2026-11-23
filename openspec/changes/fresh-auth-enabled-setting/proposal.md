---
version: 6
---

Issue: #3257.

## Why

Persona: site administrator of an installation with an SSO provider, and a developer who approves a `destroy`.

Today a fresh step is on for everyone. An installation with an SSO provider cannot approve a `destroy` with an access token or with a sign-in that has no fresh step. The server refuses each destroy approval (issue 3257). The project owner decided on 15 November 2026 that the step is opt-in. The decision is ADR 385, and no code implements it.

The path after this change:

1. The administrator reads the effective value of `freshAuthEnabled` with `GET /api/dustpan/v1/settings`. It is `false`, with its source.
2. A developer approves a `destroy` with an access token that holds the grant. The server accepts it and writes the audit entry.
3. The administrator turns the setting on with `PATCH /api/dustpan/v1/settings` (no step needed from off to on). From then on, each step of ADR 227 decision 2 asks for a fresh authentication.
4. Errors: with the setting on, a request with no fresh step gets 403 `mfa.fresh-step-required` and the path of a new sign-in. The turn from on to off needs a fresh step itself.

## Evidence

- No code reads a setting `freshAuthEnabled`. Proof: `rg -n 'freshAuthEnabled' --glob '*.go' .` prints nothing on main (15 Nov 2026).
- Each fresh-step call site is on for everyone today. The sites that `rg` finds on main (the full list is task 1.1):
  - `internal/api/server/freshness.go`: `requireFresh` (:112), `checkTokenFreshness` (:146)
  - `internal/api/server/tokens.go` (:478, :592, :797, :849), `tokenpolicy.go` (:153), `sessionpolicy.go` (:146), `providers.go` (:271, :333, :365), `githost_app.go` (:210, :263, :444, :487), `localusers.go` (:691), `settings.go` (:428, :445)
  - `internal/api/tfe/freshness.go` (:54), `internal/api/pulumi/freshness.go` (:66), `internal/api/profile/freshness.go` (:62), `internal/api/issuer/freshness.go` (:62)
  - `pkg/accesstoken/service.go` (:165, :418, :805), `pkg/profile/service.go` (:498), `pkg/profile/issuer_settings.go` (:303), `internal/issuersvc/settings.go` (:142)
  - `pkg/setting/change.go` (:153), `pkg/dustpan/auditchain.go` (:557)
  - `pkg/job/service_approve.go` (:87, `newDestroyFresh`), `pkg/job/gate.go` (:279, `freshAuth`), `internal/store/jobstore/events.go` (:18, `checkDestroyProof`)
  - `internal/api/server/gate_config.go` (:120, :130, :365, :560, `freshAuthSeconds`)
  - `internal/trustrulesvc/service.go`: `requireFresh` (:586), called by `requireWriter` (`service_bundle.go:164`) and by the rule writes at `:263` and `:395`; `internal/api/trustrules/handlers.go` maps `ErrFreshRequired` and `ErrStaleToken` to the refusal (:208-211, :546-551)
- Open work adds one more such check: the session-limit check of #3286. The trust rule check of #3283 is in the list above.

## Fix boundary

- Declare the installation setting `freshAuthEnabled`: boolean, effective default `false`, reduces security when it goes from on to off. Its settings API shows the effective value and its source. A flag, an environment variable or the configuration file may set it.
- One decision point, the NEW `freshstep.Policy` (in the NEW package `pkg/freshstep`), that every fresh-step call site asks. With the setting off it answers "no step". The handler, the service and the store each read the same effective value. None keeps its own rule.
- Gate each call site that the `rg` of task 1.1 finds, except the sites that ADR 385 decision 5 keeps.
- The refusal of the approval route (`internal/api/jobs/approve.go`, "no session MFA proof yet") is not the answer with the setting off. With the setting on, the fresh fact of #3257 is a separate change that depends on this one.
- A stored `freshAuthSeconds` of an approval rule and `freshAuthForApprovals` of a namespace keep their values with the setting off. The settings API and the rule API show `"enforced": false` for them.
- A change of `freshAuthEnabled` from on to off needs a fresh step while it is on. From off to on needs none. Both write an audit entry with the old and the new value.
- A guide section at the setting says in one sentence that with the setting off an access token with the right grant may approve a `destroy` and make a setting weaker.

Out of scope:

- The first-administrator code, the change or reset of a factor, the issue of an enrollment code, and the proof of a break-glass pass (ADR 385 decision 5). RFC 132 holds the question for the last two.
- The levels of the federation and the namespace (ADR 385 decision 8 says "may exist"; RFC 132 holds the place of the federation value). This change adds the installation level only, and its read function takes the strictest value when a lower level exists later.
- The fresh fact of the sealed session cookie for the approval of a `destroy` (the change of issue 3257 that follows this one).
- The window `freshAuthSeconds` and its values (ADR 227 decision 3 keeps them).

## Files

New:

- pkg/freshstep/freshstep.go, pkg/freshstep/freshstep_test.go, pkg/freshstep/leak_test.go: `Policy`, `Guard`, `NewGuard`.
- pkg/job/freshstep_policy_test.go, internal/api/server/freshstep_policy_test.go, internal/store/jobstore/events_freshstep_test.go, pkg/profile/profiletest/freshstep_policy_test.go: the table of each layer with the setting off and on.

Edited (each from the `rg` list of "Evidence"; task 1.1 confirms the list on current main):

- pkg/setting/change.go, pkg/setting/setting.go: the on-to-off change is a weaker change.
- internal/api/server/freshness.go, internal/api/server/tokens.go, internal/api/server/tokenpolicy.go, internal/api/server/sessionpolicy.go, internal/api/server/providers.go, internal/api/server/githost_app.go, internal/api/server/localusers.go, internal/api/server/settings.go, internal/api/server/gate_config.go, internal/api/server/server.go (the `Handler` and `Config` structs): hold a `Guard`; the settings declaration; the `enforced` field.
- internal/api/tfe/freshness.go, internal/api/pulumi/freshness.go, internal/api/profile/freshness.go, internal/api/issuer/freshness.go: hold the same `Guard`.
- internal/api/tfe/tfe.go, internal/api/pulumi/pulumi.go, internal/api/profile/profile.go, internal/api/issuer/issuer.go, internal/api/jobs/jobs.go, internal/issuersvc/issuersvc.go: the struct that holds the `Guard` field.
- internal/store/jobstore/store.go, internal/store/jobstore/apply.go, internal/store/jobstore/record.go: pass the policy to `checkDestroyProof`.
- pkg/accesstoken/service.go, pkg/profile/service.go, pkg/profile/issuer_settings.go, internal/issuersvc/settings.go, pkg/dustpan/auditchain.go: hold a `Guard`.
- pkg/job/service_approve.go, pkg/job/gate.go, internal/store/jobstore/events.go, internal/api/jobs/approve.go: the destroy approval reads the policy.
- pkg/dustpan (wiring): makes the one `Policy` and the `Guard` of each layer.
- internal/api/apidesc (openapi.json), clients/typescript: regenerated.
- docs/guides/approval-rules.md, docs/guides/access-tokens.md, docs/guides/console-sign-in.md: the setting and its effect.
- e2e/signin_token.go (new): the helper `EnableFreshAuth`. e2e/tokens_test.go, e2e/audit_chain_test.go, e2e/sessionpolicy_test.go, e2e/decision_record_retention_setting_test.go, e2e/restore_access_setting_test.go, e2e/issuer_test.go, e2e/profiles_test.go, e2e/e2e_test.go, e2e/signin_test.go, e2e/approval_rules_test.go, e2e/gate_routes_test.go, e2e/proxy_rules_test.go, e2e/namespace_principals_test.go: call it, because the default is off and these steps expect a fresh step for an access token.
- e2e/settings_test.go: the step that changes `freshAuthEnabled` and reads its effective value. No e2e step approves a `destroy` with an access token: the jobs API cannot start a `destroy` job (`job.unsupported`), so `TestDestroyApprovalByTokenFollowsSetting` proves that case.

## Check

- Test the setting default is false and shows its source: TestFreshAuthEnabledDefault fails on main
- Test each gated call site passes with the setting off and refuses with it on: one table test for each layer (task 2.x) fails on main for the "off passes" row
- Test an access token approves a `destroy` with the setting off and is refused with it on: TestDestroyApprovalByTokenFollowsSetting fails on main
- Test the on-to-off change needs a fresh step and the off-to-on change needs none: TestFreshAuthEnabledChangeNeedsStep fails on main
- Test the rule and namespace API show `"enforced": false` with the setting off: TestStoredRulesShowNotEnforced fails on main
- Test no call site asks for a step without the policy: TestEveryFreshCallSiteUsesPolicy fails on main
- Docs page states the setting and its effect: make docs-lint
- E2E step: PATCH the setting, then approve a `destroy` with an access token, with the setting off and on: make e2e

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `requireFresh` runs the freshness check of a change on a request with a console session (`internal/api/server/freshness.go:112`)
- `checkTokenFreshness` refuses a token change from a stale session (`internal/api/server/freshness.go:146`)
- `Service.requireFresh` of the access token service runs the freshness port (`pkg/accesstoken/service.go:805`)
- `newDestroyFresh` accepts only a person's console session (`pkg/job/service_approve.go:325`)
- `freshAuth` reports a valid authentication at most `ConfirmFreshness` old (`pkg/job/gate.go:287`)
- `ErrFreshStepRequired` is the refusal of a weaker setting without a step (`pkg/setting/setting.go:109`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chains come from `docs/adr/INDEX.md`.

**ADR 385: A fresh step is opt-in** (owner-tagged: decisions 1, 2, 3, 5, 8).

- Decisions that apply: 1, 2, 3, 4, 5, 6, 7, 8.
- Changed by: none yet. It changes ADR 212 decision 3, ADR 225 decision 5, ADR 227 decision 3 and extends ADR 227 decision 2, ADR 56 decision 5 and ADR 301 decision 12.
- Quote: "The setting `freshAuthEnabled` is a boolean of the installation settings. It covers only the normal dangerous actions that use 2FA step-up (owner, 2026-11-15). Its effective default is `false`."
- Quote: "When the setting is off, the server asks a person for no fresh authentication."
- Quote: "The handler, the service and the store each read the same effective value. None of them keeps a rule of its own."
- Quote: "A change of `freshAuthEnabled` from on to off reduces security. It needs a fresh step while the setting is on (ADR 212 decision 3). A change from off to on needs no step."
- Quote: "An approval rule with `freshAuthSeconds` keeps its stored value while the setting is off. A namespace with `freshAuthForApprovals` on keeps it too. The settings API and the rule API show `"enforced": false` for each of them."
- Quote: "ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has."
- Effect: this change implements decisions 1 to 7 for the installation level. Decision 8 (three levels) is out of scope as above.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decisions 2, 3).

- Decisions that apply: 2 (extended by ADR 385), 3 (changed by ADR 385), 4, 5, 6.
- Changed by: ADR 301, ADR 320, ADR 339, ADR 354, ADR 385.
- Current rule: ADR 227 with ADR 301, 320, 339, 354, 385.
- Quote: "A window of 0 asks for a fresh authentication at each step. No value turns the check off."
- Effect: no window value turns the check off. Only the setting does (ADR 385 changes the sentence).
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals** (decision 3).

- Quote: "With built-in MFA off and no SSO provider, ADR 56 decision 5 applies, and the change needs no step."
- Changed by: ADR 385 changes decision 3.
- Conflict: none.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 5).

- Quote: "An approval of a change that holds a `destroy` needs a fresh authentication, always."
- Changed by: ADR 385 changes decision 5: "always" holds only when `freshAuthEnabled` is on.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step** (decision 12) and **ADR 56** (decision 5): no step when the setting is off, for every sign-in method (ADR 385).

## Open questions

none

Questions considered and answered:

- Does a lower level (federation, namespace) turn the step off? No. ADR 385 decision 8: "A lower level can turn a requirement on. It cannot turn it off." This change adds the installation level, and the read function is the one place a later level joins.
- Is the first-administrator code, the factor change or reset, the enrollment code or the break-glass proof gated? No. ADR 385 decision 5 keeps them. The enrollment code is the reading of the PM, and the owner has not decided it, so the change keeps it as it is.
- Is a new silent rule added? No. Each default shows its effective value and source in the API, and each change writes an audit entry (decisions 1 and 4).
- Does the setting give an agent a new action? No. ADR 385 decision 7 and ADR 354.
