## Context

Self-service request path for milestone 6 step 1: publish blueprint, submit request, validate inputs, check quotas, provision unit, start runner job with pulumi up or tofu apply. Terms follow ADR 286: Self-service request and Blueprint.

## Decisions

1. **Tool-neutral core with tool adapters.** Keep domain packages tool-neutral per ADR 12 and ADR 110 decision 1 as renamed by ADR 286. Self-service package holds request, blueprint, choice engine and ports. Tool adapters for render and provision live in internal wiring, one per tool.
2. **JSON Schema input validation.** Store JSON Schema on the blueprint version. Validate answers at the API boundary. Unknown or out of range input fails with an error that names the input key. Revisions pin the blueprint version. Request document holds blueprint name, blueprint version, answers, desired present or absent, and collection; this step ships create and update to present, destroy follows the same revision path.
3. **Quota reservation inside submit.** Reserve quota units keys in the submit transaction through the quota.Tx adapter of add-sql-adapter-quota-counter, bound to the same transaction. Job start uses idempotency key request ID and revision per ADR 110 decision 7; reservation idempotency uses the same key plus the quota key. At most one revision in flight. This change does not define gate evaluation; turn-blueprint-rules-catalog-into owns it.
4. **Idempotent submit and errors that name the input.** Require Idempotency-Key on POST self-service requests. A retry with the same key returns the same revision, unit and job without a second reservation. Bad input fails with 400 and error code that names the input key, for example self-service.bad-input. Quota refusal fails with 409 and names the rule and key.

## Structure

Unexported type selfServiceEngine with constructor newSelfServiceEngine that checks schema, quota port and provisioner presence. Only the constructor makes a valid engine. Remaining checks stay behind golangci-lint depguard and errcheck. Many call site rewrites run with gopls rename plus go fix scripts, no new tool.

## Risks

- Blueprint schema drift between versions changes validation of old revisions; revisions pin the blueprint version.
- Quota count drift between store and inventory; hourly reconcile from inventory wins with log and metric per ADR 115 decision 9.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary input check | JSON Schema validation of inputs at the API boundary against the pinned blueprint version schema. Unknown or out of range input refused. | Submit an answer with an extra input and a type error; expect 400 that names the input, and no unit or job. |
| safe render | Renderer encodes values per tool without shell or template eval. Secrets never enter render output; profiles supply them at run time. | Submit input with shell metacharacters and template markers; render output holds them as plain data and no secret value leaks. |
| authorization at use | Authorization checked at point of use for blueprint publish, request submit, and unit read on the provisioned stack or workspace. | Call submit with a token that lacks the request action; expect deny and an audit entry, even with a valid document. |
