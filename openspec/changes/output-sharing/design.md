## Context

Cross-tool output reads run on runners through the existing tool APIs. The job token carries the job unit. The producer namespace holds each sharing rule keyed by stable IDs, managed by add-output-sharing-setting-new.

## Decisions

1. **Two checks at each read.** Checker evaluates both ADR 273 decision 5 checks at each read: rule names consumer unit, collection, or namespace, and starter holds unit:read-outputs plus unit:read-secrets for secret outputs with current grants. No cached grant passes the read.
2. **Atomic read and audit.** Read, decrypt, and audit write share one store transaction. If the audit write fails the whole read fails closed with no outputs returned.

## Structure

Unexported outputshare.checker built only by NewChecker with Store, Access, Attachments, and States ports; constructor rejects nil ports so no read path skips either check.

## Risks

- Stale starter grants leak secret outputs when the read trusts job start state.
- Consumer-namespace-wide rule widens read reach beyond intent.
- Tool output route returns more than root outputs of latest state.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | HTTP adapter allowlists producer object kinds and unit references, and declares unit:read-outputs on the producer object. | GET outputs read with principal name as consumer gets 403. |
| service | Service rechecks rule match and starter grants at read time, including unit:read-secrets for secret outputs. | Revoked starter grant or removed rule between job start and read denies the in-flight read with 403. |
| store | Checker matches rules by stable ID only, so a renamed unit still matches and a removed-and-made-again unit does not match. | Consumer removed and made again under the same name is denied until a new rule names the new stable ID. |
