Issue: #1002.

## Why

**Persona: operator.** An Operator installs and configures the server and wants to name an external Cedar service as the gate engine, so policy evaluation runs in one approved service while access limits stay linked and saves still validate during an outage.

The user path this change must make work:

1. **Server start.** Operator sets the external Cedar service address, CA, and credential from docs/reference/ and restarts the server.
2. **API.** Namespace administrator saves a gate through the API; the server validates it and calls the check operation of the service.
3. **CLI.** Developer runs the Pulumi CLI or OpenTofu; the server evaluates gates through the service.
4. **Docs page.** Operator reads the configuration page in docs/reference/ for the service address and fallback rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Gate save with a bad condition or unknown fact | Save refused: gate invalid against fact schema; fix the gate document and retry the save. | `GET /api/dustpan/v1/settings` |
| External service answer fails the echo check | Gate evaluation unavailable: external Cedar service did not authenticate or did not echo both IDs; check the service address, CA, credential, and policy-set version in the installation settings, then read the settings resource again. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The engine port lives in pkg/gate/engine.go. Proof: pkg/gate/engine.go
- The conformance runner lives in pkg/gate/gatetest/run_engine.go. Proof: pkg/gate/gatetest/run_engine.go
- Issue #1002 keeps access limits on the linked adapter and keeps external checks and starter packs out of scope. Proof: Out: External checks (RFC 49) and starter packs.

## What Changes

- Service adapter of gate.Engine with TLS, credential, ID echo, and version check
- Configuration for the external Cedar service address and credential
- Save path check operation with refusal fallback and miss record
- CI job with the Rust validator through the service image
- Docs page in docs/reference/ for the configuration

Out of scope:

- External checks of RFC 49 and starter packs
- Linked cedar-go adapter of #996
- New decision points or new gate fields

Depends on:

- #996
- add-access-limits-per-scope
- `add-access-limits-per-scope` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Operator must set endpoint, CA, and credential from one docs page; a wrong value gives one error that names the fix.
- Service outage still surprises during plan and apply; the record must name the fallback and the miss.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 7).

- Decisions that apply: 7 (extended).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Cedar, linked and as a service (owner). The server links `cedar-go` and can also call an external Cedar service, behind one deep engine port (ADR 224 decision 9)."
- Effect: The change adds the service adapter behind the same port and keeps linked cedar-go as the default engine.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 9, 13).

- Decisions that apply: 8 (current), 9 (extended), 13 (current), 15 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Check at save (PM). The server parses and type-checks each gate at save against the fact schema of its decision point, with the schema validator of `cedar-go`."
- Quote: "One deep engine port, two adapters (owner). The port `gate.Engine` takes the compiled policy set of a decision point, the entities and one request for each unit."
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Quote: "Cache (PM). The server caches each compiled policy set by gate ID and gate version."
- Effect: The adapter is the second adapter of decision 9. Save keeps the parse and type check of decision 8. Protocol faults give ErrUnavailable and fail closed. Enforce-mode diagnostics give ErrRuleFailed; evaluate-mode entries are recorded with no effect. The client sends gate ID and gate version so the cache key of decision 15 stays exact.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The gate engine port stays pluggable (owner, 2026-11-07). The port `gate.Engine` of ADR 224 decision 9 stays the one place where an engine plugs in."
- Effect: The external service is an adapter of gate.Engine and runs the same conformance suite as the linked adapter.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The change maps bad answers to gate.ErrUnavailable, but ADR 224 decision 13 (owner) says 'A diagnostic error of the engine for a gate in enforce mode stops the change with gate.ErrRuleFailed'. The error kind for engine errors in enforce mode is not reconciled. Files: design.md (Decisions 2), specs/gate-external-engine/spec.md (Bad answer is unavailable). Resolution: answered: Split protocol faults to gate.ErrUnavailable from Cedar diagnostics to gate.ErrRuleFailed in enforce mode; specs bad-answer-is-unavailable and cedar-diagnostic-fails-closed and design strict check state the split.
- R1.2 (adr conflict): The save fallback rule is not defined. If the fallback accepts a gate that the service did not check, a gate is stored unverified, which may conflict with ADR 224 decision 13 ('An error fails closed (owner)'). The fallback value and its effect on save are not specified. Files: design.md (Layers, save row), specs/gate-external-engine/spec.md (Save validates then checks the service). Resolution: answered: Defined save fallback as refuse with gate.ErrUnavailable and record the miss; spec save-refuses-on-outage and save layer state it, pending owner open question.
- R1.3 (open question): The proposal says the request and answer format needs an RFC and an owner decision before code (design.md Risks; issue #1002 'Needs RFC, then owner decision'). The change instead records a provisional PM decision for the protocol, the evaluate and check paths, and the CI image, and marks Open questions as none. The spec-ready gate and the ADR rule (write an RFC first) are not met for the protocol. Files: proposal.md (Open questions), design.md (Risks). Resolution: open question: Kept protocol, operations, and CI image as owner open questions; no code lands before the RFC and owner decision.
- R1.4 (wrong claim): The error table says a bad echo gives 'retry the call' and names make e2e as the helpful command. design.md Decision 2 says a bad answer is mapped to gate.ErrUnavailable 'without retry of a bad answer'. The operator is not helped by make e2e, which a developer runs in CI. Files: proposal.md (Why, error table). Resolution: answered: Removed retry wording and e2e command; echo-check error names the settings resource read with GET /api/dustpan/v1/settings.
- R1.5 (missing scenario): The spec has no scenario for an outage during a decision point that has gates (not access-limit-only). It does not say whether such a call fails closed with ErrUnavailable or is decided otherwise. Files: specs/gate-external-engine/spec.md (Outage keeps access-limit calls deciding). Resolution: answered: Added outage-enforce-fails-closed requirement with gated outage and evaluate-only scenarios.
- R1.6 (missing scenario): The design layers list TLS CA mismatch, missing credential, slow service timeout, wrong policy-set version and extra IDs, each with a hostile test. The spec has no requirement or scenario for these. Files: design.md (Layers table), specs/gate-external-engine/spec.md. Resolution: answered: Added transport-guards and protocol-echo requirements with hostile scenarios for CA, credential, timeout, size, version, and extra IDs.
- R1.7 (failure mode): The endpoint allowlist, redirect handling, response size limit and the request timeout value are named in the design but not defined. The timeout has no default and no maximum. A hostile or slow service can hold evaluation open or force a redirect to another host. Files: design.md (Layers, transport row), tasks.md. Resolution: answered: Defined https allowlist, no redirect follow, 5 s timeout, and 1 MB answer limit in design transport layer and transport-guards spec.
- R1.8 (failure mode): Policy-set version changes on the service side are not covered. A cached compiled policy set (ADR 224 decision 15, keyed by gate version) can disagree with the version that the service echoes after a rollout. No requirement says what happens then. Files: specs/gate-external-engine/spec.md, design.md. Resolution: answered: Added version-mismatch requirement that gives ErrUnavailable and never uses a cached set.
- R1.9 (missing scenario): Rotation and storage of the service credential, and a change of the service address or CA while the server runs, have no requirement. The proposal says configuration is set at server start only. Files: proposal.md (Why step 1, What Changes). Resolution: answered: Added credential-change-needs-restart requirement for address, CA, and credential rotation with sealed storage.
- R1.10 (open question): The record format for a fallback miss at save is not named (which fields, which result value). The usability note says 'the record must name the fallback and the miss' but no spec scenario checks it. Files: specs/gate-external-engine/spec.md, proposal.md (Usability). Resolution: answered: Added save-miss-is-recorded requirement with result error and fallback fields.
- R2.1 (open question): The Open questions section reads none, but issue #1002 lists three open questions (protocol, evaluate and check operations, CI image) that need an RFC and an owner decision. design.md Risks says code waits for them, and R1.3 says they stay open. The provisional answers in proposal.md still drive code tasks 2.1 to 2.3, so the spec-ready gate is not met. Resolution: open question: Kept five open questions for protocol, operations, CI image, save fallback, and access-limit split; no code lands until the owner decides.
- R2.2 (adr conflict): specs/gate-external-engine/spec.md 'Outage with gates fails closed' stops the change for any gate. ADR 224 decision 13 (owner) gives an error of a gate in evaluate mode only a record entry, and the quoted text is cut off at 'is in the record and st'. The spec must limit the stop to enforce mode, or quote the full decision line that allows it. Resolution: answered: Limited outage stop to enforce mode; evaluate mode records with no effect in outage-enforce-fails-closed and break-glass-keeps-gates.
- R2.3 (open question): The save fallback (refuse with ErrUnavailable) is chosen in the change, but the issue says the fallback rule for a silent service is open. The proposal says ADR 224 decision 8 requires the fallback, but the quoted decision 8 does not state one. The owner must decide the save behavior during an outage, and no ADR answers it. Resolution: open question: Moved save fallback to an owner open question; spec save-refuses-on-outage states the recommended refusal pending that decision.
- R2.4 (open question): Access limits stay on the linked adapter during an outage, and the proposal calls this a scope item. ADR 222 decision 7 (owner) puts the linked adapter and the external service behind one port and does not split the work by kind of check. No ADR or owner decision in the input supports the split. Resolution: open question: Moved access-limit split to an owner open question; spec outage-keeps-access-limit-calls-deciding states the recommended split pending that decision.
- R2.5 (missing scenario): No scenario covers a break-glass session during a service outage. ADR 224 decision 17 keeps gates for break-glass sessions, so an emergency change would fail closed with no stated path. The spec does not say so. Resolution: answered: Added break-glass-keeps-gates requirement with outage scenario.
- R2.6 (failure mode): The server start path is not covered when the service is unreachable at boot, or when the cache holds compiled sets from before the outage. design.md and the spec define no startup behavior and no health state for the service. Resolution: answered: Added server-starts-with-unhealthy-engine requirement with boot-unreachable and cached-set scenarios.
- R2.7 (failure mode): The design says the endpoint is allowlisted, but no allowlist rule is defined: the hosts, the DNS resolution and the rebinding check are not named. A hostile name could still resolve to an internal address. Resolution: answered: Named https allowlist, no redirect, DNS pinned at dial, and refusal of private, loopback, and link-local addresses in design and endpoint-allowlist requirement.
- R2.8 (wrong claim): The error table tells the operator to run go test ./pkg/gate/..., which is a developer command and does not fix an operator's configuration. The helpful command for an operator is not named. Resolution: answered: Replaced developer test command with the settings-resource read for the operator path.
- R2.9 (wrong claim): The provisional answers cite AWS and Google benchmarks that are not in the input and cannot be checked. They are used as support for owner-review decisions. Resolution: answered: Kept only the benchmark name without unverifiable practice claims; reasons state trade-offs only.
- R2.10 (scope): tasks 2.3 (CI job with the pinned service image) and 3.2 (make e2e with gates on tool paths) depend on open question 3 (the CI image), which is not decided. The tasks can be marked done with a provisional image that the owner has not accepted. Resolution: answered: CI and e2e tasks stay blocked behind the open CI-image and protocol questions under the spec-ready gate.
- M.39 (dependency): The adapter keeps the access-limit calls on the linked adapter, so it needs the access limits. Resolution: answered: Access-limit behavior during outage depends on the per-scope access-limit model; added add-access-limits-per-scope to depends_on.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which HTTP protocol does the external service speak?
   - Options: An existing Cedar service API; A format that OpenDustpan defines for evaluate and check.
   - Provisional decision (owner review 14 Nov): Use an existing Cedar service API when it carries request ID, policy-set version, and matched policy IDs; else define a small OpenDustpan format.
   - Reason: A pinned existing API reuses validators and clients; a defined format fits the per-unit request. Benchmark: AWS
   - Persona impact: Operator sets one address and one credential from the docs page; no extra work per namespace. Escape hatch: Linked cedar-go stays the default engine; the operator clears the service address and returns to linked evaluation.
   - Owner-tagged ADRs involved: ADR 224 (their words do not change).
2. What are the evaluate operation and the check operation?
   - Options: One evaluate path plus one check path; One path with an operation field.
   - Provisional decision (owner review 14 Nov): Two paths: evaluate for decision points and check for save validation.
   - Reason: Two paths keep evaluation and save validation separate and testable. Benchmark: AWS
   - Persona impact: Operator configures one endpoint; the split is invisible in daily use. Escape hatch: Save refuses with ErrUnavailable during an outage of the check operation, so no unverified gate is stored.
   - Owner-tagged ADRs involved: ADR 224 (their words do not change).
3. Which container image runs the Rust validator in CI?
   - Options: Cedar agent image; Cedar validator sidecar image used through the service image.
   - Provisional decision (owner review 14 Nov): Run the Rust validator through the pinned image of the external Cedar service.
   - Reason: A pinned service image tests the same code that installations call. Benchmark: Google
   - Persona impact: No operator impact; CI only. Escape hatch: CI runs the linked adapter suite alone until the image is pinned.
   - Owner-tagged ADRs involved: ADR 224 (their words do not change).
4. What is the save fallback when the service does not answer the check operation?
   - Options: Refuse the save with gate.ErrUnavailable; Allow the save in evaluate mode with a recorded miss.
   - Provisional decision (owner review 14 Nov): Refuse the save with gate.ErrUnavailable and record the miss.
   - Reason: Refusal keeps unverified gates out of storage; evaluate-mode storage keeps edits moving with a visible miss. Benchmark: AWS
   - Persona impact: Operator cannot store new gates during an outage under refusal; work resumes after the service recovers. Escape hatch: Linked cedar-go stays the default; no gate is stored while the check operation is unreachable.
   - Owner-tagged ADRs involved: ADR 224 (their words do not change).
5. Do access limits stay on the linked adapter during a service outage?
   - Options: Access limits stay on the linked adapter during an outage; All checks fail closed with gate.ErrUnavailable during an outage.
   - Provisional decision (owner review 14 Nov): Keep access-limit checks on the linked adapter during an outage; gate calls fail closed per mode.
   - Reason: The split keeps access decisions moving; full fail-closed is simpler and stricter. Benchmark: AWS
   - Persona impact: Operator sees access-limit calls still decide during an outage under the split; all calls stop under full fail-closed. Escape hatch: Linked cedar-go stays the default engine; the operator clears the service address and returns to linked evaluation.
   - Owner-tagged ADRs involved: ADR 222, ADR 224 (their words do not change).

Questions considered and answered:

- Is an external Cedar service allowed as a gate engine? Yes. ADR 224 decision 9 names a second adapter beyond linked cedar-go, ADR 222 decision 7 names an external Cedar service, and ADR 335 decision 7 keeps the port pluggable. The linked adapter of #996 is the baseline; this change adds the service adapter.
- Must a gate save still check the gate when the service is configured? Yes for parse and type check. ADR 224 decision 8 requires parse and type check at save with the cedar-go schema validator. The behavior of the check operation when the service does not answer is open; it is asked in open questions.
- Does every engine fault give the same error? No. A transport or protocol fault gives ErrUnavailable and fails closed. A Cedar diagnostic that the service returns intact gives ErrRuleFailed in enforce mode and a record entry in evaluate mode under ADR 224 decision 13.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
