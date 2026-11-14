## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing mapping tests for principalSubjects.Subject for person, service, workload, identity.KindAgent, and an unknown kind. Check: New subject mapping test fails on main with agent mapped to person, passes after the fix.
- [ ] 1.2 Add failing gate evaluation tests at state.write for an agent gate against each caller kind. Check: New GuardStateWrite test with context.principal.kind == agent passes only for the agent kind.

## 2. Fix

- [ ] 2.1 Map identity.KindAgent to gate.PrincipalAgent in principalSubjects.Subject with fail-closed unknown handling through the NEW function newSubjectKind. Check: rg shows identity.KindAgent defined by person-delegates-dusty and its use in pkg/dustpan/stateguard.go through the NEW function newSubjectKind with a fail-closed unknown branch.
- [ ] 2.2 Apply the same kind mapping at the access-limit schema and the change.propose decision point. Check: Same hostile unknown-kind test passes at the adapter, service and handler layers with the NEW error code gate.unknown-principal-kind, covering the access-limit schema and the change.propose decision point.
- [ ] 2.3 Update docs/guides/state-write-gates.md to state that state.write sets agent for agent principals. Check: docs/guides/state-write-gates.md no longer says the server does not set agent, and its example runs as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run an e2e step with a forbid gate on context.principal.kind == agent through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token. Check: E2E gate scenario through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token denies an agent-gated write for an agent caller with gate.denied.
- [ ] 4.2 Complete the usability check for the namespace administrator path. Check: Usability check: a namespace administrator writes the agent gate from the docs page alone and predicts the outcome for each kind.
- [ ] 4.3 Run make check with zero findings. Check: `make check` reports zero findings.
