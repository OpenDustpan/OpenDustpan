## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing mapping tests in a NEW file pkg/dustpan/stateguard_kind_internal_test.go for principalSubjects.Subject (pkg/dustpan/stateguard.go:335) for person, service, workload, identity.KindAgent, and an unknown kind. Check: New subject mapping test fails on main with agent mapped to person, passes after the fix.
- [ ] 1.2 Add failing gate evaluation tests in pkg/gate/gate_test.go at state.write (pkg/gate/statewrite.go:65, Service.GuardStateWrite) for an agent gate against each caller kind. Check: New GuardStateWrite test with context.principal.kind == agent passes only for the agent kind.

## 2. Fix

- [ ] 2.1 Map identity.KindAgent to gate.PrincipalAgent in principalSubjects.Subject with fail-closed unknown handling through the NEW function newSubjectKind. This replaces the refusal of the kind agent that agent-shapes-before-first-tag adds there, and `TestSubjectRefusesAgentKind` in pkg/dustpan/stateguard_internal_test.go changes to expect `gate.PrincipalAgent`. Check: rg shows identity.KindAgent defined by agent-shapes-before-first-tag and its use in pkg/dustpan/stateguard.go through the NEW function newSubjectKind with a fail-closed unknown branch.
- [ ] 2.2 Apply the same kind mapping at the access-limit schema and the change.propose decision point in pkg/dustpan/stateguard.go through the NEW function newSubjectKind. Check: Same hostile unknown-kind test passes at the adapter, service and handler layers with the NEW error code gate.unknown-principal-kind, covering the access-limit schema and the change.propose decision point.
- [ ] 2.3 Update docs/guides/state-write-gates.md to state that state.write sets agent for agent principals. Check: docs/guides/state-write-gates.md no longer says the server does not set agent, and its example runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/state_write_gates_context_principal_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Run an e2e step with a forbid gate on context.principal.kind == agent through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token. Check: E2E gate scenario through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token denies an agent-gated write for an agent caller with gate.denied. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run make check with zero findings. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
