---
version: 12
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
  - `internal/api/server/freshness.go`: `requireFresh` (:112), `checkTokenFreshness` (:180)
  - `internal/api/server/tokens.go` (:478, :592, :797, :849), `internal/api/server/tokenpolicy.go` (:153), `internal/api/server/sessionpolicy.go` (:146), `internal/api/server/providers.go` (:271, :333, :365), `internal/api/server/githost_app.go` (:210, :263, :444, :487), `internal/api/server/localusers.go` (:691), `internal/api/server/settings.go` (:428, :445)
  - `internal/api/tfe/freshness.go` (:54), `internal/api/pulumi/freshness.go` (:66), `internal/api/profile/freshness.go` (:62), `internal/api/issuer/freshness.go` (:62)
  - `pkg/accesstoken/service.go` (:165, :418, :873), `pkg/profile/service.go` (:498), `pkg/profile/issuer_settings.go` (:303), `internal/issuersvc/settings.go` (:142)
  - `pkg/setting/change.go` (:153), `pkg/dustpan/auditchain.go` (:557)
  - `pkg/job/service_approve.go` (:87, `newDestroyFresh`), `pkg/job/gate.go` (:316, `freshAuth`: gated, see task 2.4), `internal/store/jobstore/events.go` (:18, `checkDestroyProof`)
  - `internal/api/server/gate_config.go` (:120, :130, :365, :560, `freshAuthSeconds`)
  - `internal/trustrulesvc/service.go`: `requireFresh` (:586), called by `requireWriter` (`internal/trustrulesvc/service_bundle.go:164`) and by the rule writes at `:263` and `:395`; `internal/api/trustrules/handlers.go` maps `ErrFreshRequired` and `ErrStaleToken` to the refusal (:208-211, :546-551)
- Open work adds one more such check: the session-limit check of #3286. The trust rule check of #3283 is in the list above.

## Fix boundary

- Declare the installation setting `freshAuthEnabled`: boolean, effective default `false`, reduces security when it goes from on to off. Its settings API shows the effective value and its source. A flag, an environment variable or the configuration file may set it.
- One decision point, the NEW `freshstep.Policy` (in the existing package `pkg/freshstep`, extended: main has only the type `Guard`), that every fresh-step call site asks. With the setting off it answers "no step". The handler, the service and the store each read the same effective value. None keeps its own rule.
- Gate each call site that the `rg` of task 1.1 finds, except the sites that ADR 385 decision 5 keeps.
- The refusal of the approval route (`internal/api/jobs/approve.go`, "no session MFA proof yet") is not the answer with the setting off. With the setting on, the fresh fact of #3257 is a separate change that depends on this one.
- A stored `freshAuthSeconds` of an approval rule and `freshAuthForApprovals` of a namespace keep their values with the setting off. The settings API and the rule API show `"enforced": false` for them.
- A change of `freshAuthEnabled` from on to off needs a fresh step while it is on. From off to on needs none. Both write an audit entry with the old and the new value.
- The change or reset of a factor needs a fresh step whatever the setting says (ADR 385 decision 5: "It stays required, whatever the setting says."). This covers the passkey routes (begin, finish, remove) and the reset of a local user. The route handler checks it with a step check that the setting never turns off, and `internal/mfasvc` checks it again: its `Actor` carries the result of the fresh step, and the register and remove methods refuse with no fresh step. Each layer stops the change alone, so the freshstep `Policy` is not used for these sites.
- With the setting off, only a person (`identity.KindPerson`) approves a `destroy` with `auth: none`. The handler, the service and the store each allow that one kind and refuse every other kind (a service, a workload, an agent). Before this change only a person's fresh session approved a `destroy`, so a non-person must not gain the approval (ADR 385 decision 7: "an agent holds no right that a person has"; decision 2 names "a person").
- A guide section at the setting says in one sentence that with the setting off an access token with the right grant may approve a `destroy` and make a setting weaker.

Out of scope:

- The first-administrator code, the issue of an enrollment code, and the proof of a break-glass pass keep their own rules (ADR 385 decision 5). RFC 132 holds the question for the last two. This change does not gate them.
- The levels of the federation and the namespace (ADR 385 decision 8 says "may exist"; RFC 132 holds the place of the federation value). This change adds the installation level only, and its read function takes the strictest value when a lower level exists later.
- The fresh fact of the sealed session cookie for the approval of a `destroy` (the change of issue 3257 that follows this one).
- The window `freshAuthSeconds` and its values (ADR 227 decision 3 keeps them).

## Files

New:

- pkg/freshstep/freshstep.go (existing, extended: main has only the type `Guard`; add the NEW `Policy` and `NewGuard`, and give `Guard` its methods), pkg/freshstep/freshstep_test.go (NEW), pkg/freshstep/leak_test.go (NEW).
- pkg/job/freshstep_policy_test.go, internal/api/server/freshstep_policy_test.go, internal/store/jobstore/events_freshstep_test.go, pkg/profile/profiletest/freshstep_policy_test.go: the table of each layer with the setting off and on.

Edited (each from the `rg` list of "Evidence"; task 1.1 confirms the list on current main):

- pkg/setting/change.go, pkg/setting/setting.go: the on-to-off change is a weaker change.
- internal/mfasvc/service.go, internal/mfasvc/service_test.go, internal/mfasvc/layers_test.go, internal/mfasvc/challengesource_test.go: the `Actor` carries the fresh fact, and the register and remove methods refuse with none (a step that the setting does not turn off). internal/mfasvc/fresh_test.go (new): the table of hostile cases of this layer.
- internal/api/server/passkeys_fresh_test.go (new): the table of hostile cases of the handler layer for a factor change.
- internal/api/server/passkeys.go: the passkey routes use the step check that stays on, and pass the result in the `Actor`.
- internal/api/server/freshness.go, internal/api/server/tokens.go, internal/api/server/tokenpolicy.go, internal/api/server/sessionpolicy.go, internal/api/server/providers.go, internal/api/server/githost_app.go, internal/api/server/localusers.go, internal/api/server/settings.go, internal/api/server/gate_config.go, internal/api/server/server.go (the `Handler` and `Config` structs): hold a `Guard`; the settings declaration; the `enforced` field.
- internal/api/tfe/freshness.go, internal/api/pulumi/freshness.go, internal/api/profile/freshness.go, internal/api/issuer/freshness.go: hold the same `Guard`.
- internal/api/tfe/tfe.go, internal/api/pulumi/pulumi.go, internal/api/profile/profile.go, internal/api/issuer/issuer.go, internal/api/jobs/jobs.go, internal/issuersvc/issuersvc.go: the struct that holds the `Guard` field.
- internal/store/jobstore/store.go, internal/store/jobstore/apply.go, internal/store/jobstore/record.go: pass the policy to `checkDestroyProof`.
- internal/trustrulesvc/service.go, internal/trustrulesvc/service_bundle.go: `requireFresh` takes the `Guard` and `requireWriter` (`internal/trustrulesvc/service_bundle.go:164`) passes it, so the bundle writes use the same policy.
- pkg/accesstoken/service.go, pkg/profile/service.go, pkg/profile/issuer_settings.go, internal/issuersvc/settings.go, pkg/dustpan/auditchain.go: hold a `Guard`.
- pkg/job/service_approve.go, pkg/job/gate.go, internal/store/jobstore/events.go, internal/api/jobs/approve.go: the destroy approval reads the policy.
- pkg/dustpan (wiring): makes the one `Policy` and the `Guard` of each layer.
- internal/api/apidesc (`internal/api/apidesc/openapi.json`), clients/typescript: regenerated.
- docs/guides/approval-rules.md, docs/guides/access-tokens.md, docs/guides/console-sign-in.md: the setting and its effect.
- e2e/signin_token.go (on main): add the NEW helper `EnableFreshAuth`. e2e/tokens_test.go, e2e/audit_chain_test.go, e2e/sessionpolicy_test.go, e2e/decision_record_retention_setting_test.go, e2e/restore_access_setting_test.go, e2e/issuer_test.go, e2e/profiles_test.go, e2e/e2e_test.go, e2e/signin_test.go, e2e/approval_rules_test.go, e2e/gate_routes_test.go, e2e/proxy_rules_test.go, e2e/namespace_principals_test.go, e2e/localusers_test.go, e2e/passkeys_test.go: call it, because the default is off and these steps expect a fresh step for an access token.
- pkg/job/service.go: the new field `ServiceConfig.FreshGuard` (a `freshstep.Guard`). The zero value requires a fresh step, so a missing field fails closed.  e2e/coverage_jobs.go, internal/api/jobs/ipallow_test.go, internal/api/jobs/pool_answer_test.go, internal/api/jobs/routes_test.go, internal/api/server/githost_fixture_test.go, internal/api/server/signin_test.go, pkg/job/service_test.go: set the new field in each `ServiceConfig` literal, because exhaustruct needs every field.
- cmd/opendustpan/host.go, pkg/dustpan/dustpan.go: set `accesstoken.Config.Guard` to the real `Guard` of the one `Policy`. The preflight PR adds `Guard` to `accesstoken.Config` in every existing literal (31 files, 40 literals on main) with the zero value; this change does not touch the other 29 files of those literals. A test that needs a ready `Guard` uses the helpers cmd/opendustpan/freshguard_helper_test.go and internal/store/postgres/freshguard_helper_test.go (new). The zero value of `Guard` requires a fresh step, so a literal that the preflight fills fails closed.
- pkg/job/jobtest/approve.go, pkg/job/jobtest/mem_test.go: the conformance cases of `auth: none` for a person and for a non-person.
- pkg/job/jobtest/jobtest.go, internal/store/sqlite/jobs_test.go, internal/store/postgres/jobs_test.go: the NEW `Domain` method `CreatePrincipal` that the two cases use to make a principal of a given kind.
- e2e/passkeys_test.go, e2e/localusers_test.go: the false comments are corrected (see task 2.7).
- internal/store/jobstore/export_internal_test.go: the test export `CheckDestroyProofStepOn` and `CheckDestroyProofStepOff`, so that the black-box store test can call `checkDestroyProof`.
- internal/api/tfe/freshstep_policy_test.go, internal/api/pulumi/freshstep_policy_test.go, internal/api/profile/freshstep_policy_test.go, internal/api/issuer/freshstep_policy_test.go, internal/issuersvc/freshstep_policy_test.go, internal/trustrulesvc/freshstep_policy_test.go, pkg/accesstoken/freshstep_policy_test.go (new): the table of each layer with the setting off and on.
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
- `checkTokenFreshness` refuses a token change from a stale session (`internal/api/server/freshness.go:180`)
- `Service.requireFresh` of the access token service runs the freshness port (`pkg/accesstoken/service.go:873`)
- `newDestroyFresh` accepts only a person's console session (`pkg/job/service_approve.go:380`)
- `freshAuth` reports a valid authentication at most `ConfirmFreshness` old (`pkg/job/gate.go:316`)
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
- Is the first-administrator code, the enrollment code or the break-glass proof gated? No. ADR 385 decision 5 keeps them. The enrollment code is the reading of the PM, and the owner has not decided it, so the change keeps it as it is.
- Is the change or reset of a factor gated by the setting? No. ADR 385 decision 5: "the change or reset of a factor (ADR 301 decision 8, ADR 285 decision 17). It stays required, whatever the setting says." With the setting off, a passkey change and a local-user reset still need a fresh step. The handler and `internal/mfasvc` each check it.
- Does ADR 385 decision 2 ("an access token that holds the grant may approve a `destroy`") let a service or workload principal approve a destroy with the setting off? No, the PM reads it for a person's access token: the same decision says "asks a person for no fresh authentication", decision 7 says the setting "removes the step for a person" and gives an agent no new action, and before this change only a person's fresh session approved a `destroy`. A non-person gains no approval. If the owner means a service or workload token too, that is a new decision and a new change.
- Is a new silent rule added? No. Each default shows its effective value and source in the API, and each change writes an audit entry (decisions 1 and 4).
- Does the setting give an agent a new action? No. ADR 385 decision 7 and ADR 354.
