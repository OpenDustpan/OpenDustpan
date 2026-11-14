## Context

One settings registry serves both scopes. Each key holds type, default, validation, sensitive mark, weaker direction and action.

## Decisions

1. **One resource per scope.** GET returns value, source, default, locked flag and one revision. PATCH uses JSON Merge Patch with an expected revision and applies all or none.
2. **Fresh step.** One weaker key makes the whole PATCH need a fresh step. A longer session limit is weaker. An access token never gives the step.
3. **Idempotency and audit.** A PATCH needs an Idempotency-Key. A replay with the same body answers the stored result; the same key with another body is refused. Audit entries are written in the same transaction and mask sensitive values.
4. **Temporary administrator session limit.** Owner decision, 13 Nov 2026 (queue P12, provisional). The key tempadmin.session-limit holds a duration from 1 minute to 24 hours, default 1 hour, for the installation scope. A new record ends at create time plus the shorter of the limit and the token policy maximum. The create writes an audit entry that names the limit. A stored value outside the range fails closed with an audit entry. An existing record keeps its end time.
5. **Host reset.** The reset option deletes the stored limit. It refuses in recovery mode 3, needs a reason, obeys the off switch and writes an audit entry with the reason.

## Structure

The unexported settingsRegistry has only NewSettingsRegistry as constructor. The unexported tempAdminLimit has only ParseTempAdminLimit, which checks the range of 1 minute to 24 hours.

## Risks

- A wrong weaker mark skips the fresh step.
- A bad stored duration blocks creation; the reset option helps.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Checks the action, the Idempotency-Key, keys, locks and the fresh step. Defense in depth: the service checks too. | PATCH with no key header, an unknown key, a locked key and a weaker value with a token: each is refused. |
| service | Applies all or none, checks the revision and writes audit entries. | A PATCH with an invalid second key and a PATCH with a failed audit write change nothing. |
| port adapter | Keeps declared keys only. The creator reads the limit through ParseTempAdminLimit. | An out-of-range limit row inserted directly makes the creator refuse. |

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/tempadmin]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| settings read latency | 200 | ms at p95 | GET of one scope with 100 settings |
| settings patch latency | 500 | ms at p95 | PATCH of 10 keys on one scope |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph Client
 A[Admin session or token]
 end
 subgraph Server
 B[Settings handler]
 C[Settings service]
 end
 subgraph Store
 D[(Database)]
 E[(Audit log)]
 end
 A --> B --> C --> D
 C --> E
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Client to Server | E | A caller lowers security without a fresh step, or with an access token. | boundary check | The handler allowlists declared keys and rejects a malformed merge patch before the service runs. | TestSettingsPatchRefusals sends an undeclared key and a non-object body. |
| Client to Server | E | A caller lowers security without a fresh step, or with an access token. | encode or contain | Values are typed by the registry and stored through placeholders only. | A string with SQL text in a duration key is refused as value-invalid. |
| Client to Server | E | A caller lowers security without a fresh step, or with an access token. | limit | The body size is capped and a patch holds at most 100 keys. | A patch with 101 keys is refused and changes nothing. |
| Client to Server | E | A caller lowers security without a fresh step, or with an access token. | authorize at point of use | The service requires a fresh session step for each weaker key and refuses tokens. | TestSettingsPatchFreshStep sends a weaker patch with a token and with a stale session. |
| Client to Server | E | A caller lowers security without a fresh step, or with an access token. | fail closed | An unknown weaker mark denies the patch. | A key with no mark is refused in TestSettingsPatchRefusals. |
| Server to Store | T | A stored limit is changed to a bad value outside the API. | boundary check | ParseTempAdminLimit checks the range at use. | TestTempAdminSessionLimit inserts a row of 48 hours and the creator refuses. |
| Server to Store | T | A stored limit is changed to a bad value outside the API. | encode or contain | The creator reads the value as a typed duration through a placeholder query, never as text in SQL. | A row with a non-numeric value makes the creator refuse. |
| Server to Store | T | A stored limit is changed to a bad value outside the API. | limit | The record end is capped by the token policy maximum even if the stored limit is valid. | A 24 hour limit with a 30 minute policy maximum ends at 30 minutes. |
| Server to Store | T | A stored limit is changed to a bad value outside the API. | authorize at point of use | The creator runs only through the host command, which checks mode, reason and off switch. | The command in mode 3 refuses with no read of the limit. |
| Server to Store | T | A stored limit is changed to a bad value outside the API. | fail closed | A bad stored value denies the create and writes an audit entry. | The creator with an out-of-range row creates no record and an audit entry exists. |
| Server to Store | I | An audit entry shows a sensitive value. | boundary check | The registry marks each key sensitive or not; a key with no mark is refused. | A declared key with no mark fails registry construction. |
| Server to Store | I | An audit entry shows a sensitive value. | encode or contain | The audit writer masks sensitive keys. | TestSettingsPatchRefusals checks the mask in the entry. |
| Server to Store | I | An audit entry shows a sensitive value. | limit | An audit value longer than 1024 bytes is cut and marked. | A 5000 byte value gives a cut entry. |
| Server to Store | I | An audit entry shows a sensitive value. | authorize at point of use | The audit read needs the audit action on the scope. | A reader without the action gets 403 on the audit export. |
| Server to Store | I | An audit entry shows a sensitive value. | fail closed | A sensitive key that is static-only cannot be patched, so no value reaches the entry. | A patch of the SMTP password gives 409 and no entry holds the value. |
| Client to Server | R | A change leaves no record. | boundary check | The service refuses a write with no principal on the context. | A service call with an empty principal is refused. |
| Client to Server | R | A change leaves no record. | encode or contain | The audit entry is written in the same transaction as the value. | TestSettingsPatchAtomic fails the audit write and sees no change. |
| Client to Server | R | A change leaves no record. | limit | One entry for each changed key, so a large patch cannot hide a key. | A patch of 3 keys writes 3 entries. |
| Client to Server | R | A change leaves no record. | authorize at point of use | The action check names the principal in the entry. | An entry exists with the principal of the token that made the patch. |
| Client to Server | R | A change leaves no record. | fail closed | A failed audit write fails the patch. | TestSettingsPatchAtomic with a failing audit store returns 500 and no change. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| PATCH write | The audit write or a second key fails. | The client repeats the patch with the same Idempotency-Key. | No key changed and the revision did not move. |
| Create temporary administrator | The stored limit is outside the range. | The operator runs the reset option or a null patch, then creates again. | No record exists; an audit entry names the refusal. |
| Host reset | The mode is 3 or no reason is given. | The operator gives a reason in a mode that allows recovery. | The stored limit is unchanged. |
