## Context

Rule sets and the runtime guard come from earlier parts. This part adds proxy-side credential injection from sealed rule-bound storage, per-request records, phase aggregation and audit, response scan of plain values, and the plan-phase write block.

## Decisions

1. **Proxy-side injection.** Credentials bind to proxy rules and stay sealed. The launcher assignment carries references only. The job proxy resolves each reference at request time and adds the credential header.
2. **Records and audit.** Each allowed or refused proxied request writes one request record with host, path, rule, decision, and bytes. The phase record aggregates counts and the audit log stores one entry per finished phase with proxy facts.

## Structure

Unexported proxyCredentialVault built only by newProxyCredentialVault, which checks rule binding, scope, and sealed storage before it returns a handle. Plan-phase safety uses unexported planEffectiveRules built only by newPlanEffectiveRules, which keeps apply-only rules out so a plan write cannot match them.

## Risks

- Credential value leaks through assignment, logs, or records.
- Unrecorded proxy access breaks audit.
- Plan phase performs a write reserved for apply.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| credential hold | Assignment carries credential references without values; proxy resolves values from the sealed store. | Dump assignment and job environment and find no credential value. |
| request check | Proxy matches request against effective rule set, strips job-set credential headers, re-checks each redirect host before it adds the credential, and refuses requests without an allow match. | Send a request with a forged Authorization header and a redirect to an unlisted host and expect refusal with host and path and no credential send. |
| response scan | Proxy scans response body for plain credential values and redacts matches before delivery to the job; scan covers only plain values up to a stated size limit and fails closed. | Return a response that contains a plain credential value and expect a redacted body plus a scan finding; return an unscannable body and expect refusal. |
| plan-phase block | Plan phase uses plan-effective rules only and refuses writes that only apply rules allow before network contact. | Run a plan-phase write that only an apply rule allows and expect refusal before network contact. |
