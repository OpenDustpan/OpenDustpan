## Context

Built-in MFA for local and LDAP sign-in, no step for SSO, fresh-step checks for tokens and reveal.

## Decisions

1. **Service-owned MFA gate.** The sign-in service issues a session only after a valid second factor.
2. **Cookie-bound freshness.** The fresh step lives in the sealed access cookie of the sign-in that did it.

## Structure

Unexported pending sign-in type made only by its constructor; a session issues only from a verified pending value.

## Risks

- TOTP clock drift locks a person out.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Reject malformed factor input. | Send a malformed code; it is refused. |
| Service | Accept a TOTP code once. | Reuse a code; no session. |

## Diagrams

After this change:

```mermaid
sequenceDiagram
 participant P as Person
 participant S as Server
 P->>S: password
 S-->>P: pending
 P->>S: second factor
 S-->>P: session
```

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/mfa]
P2[internal/mfasvc]
P3[pkg/mfa/mfatest]
P2 --> P1
P3 --> P1
```

States and transitions after this change:

```mermaid
stateDiagram-v2
 [*] --> Pending: NEW
 Pending --> Session: second factor ok
 Pending --> [*]: refused
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 0 sessions issued from a reused TOTP code | sessions | second-factor check |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph Client[Browser or CLI]
 C[console]
 end
 subgraph Server[Server]
 S[sign-in service]
 end
 subgraph Store[Store]
 M[mfa store]
 end
 C --> S
 S --> M
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| console to sign-in service | S | An attacker replays a TOTP code. | boundary check | Reject a code that is not 6 digits. | Send a malformed code; refused. |
| console to sign-in service | S | An attacker replays a TOTP code. | encode or contain | Compare the code in constant time and store only a hash of used codes. | Read the used-code record; it holds no plain code. |
| console to sign-in service | S | An attacker replays a TOTP code. | limit | Count wrong factors in the factor lock of the person. | Send wrong codes past the count; the person is locked. |
| console to sign-in service | S | An attacker replays a TOTP code. | authorize at point of use | Single-use check in the service for the pending sign-in. | Reuse a code; no session. |
| console to sign-in service | S | An attacker replays a TOTP code. | fail closed | Deny on a store error and write an audit entry. | Fail the store during verify; no session and an audit entry. |

## Contracts

**mfa.PendingSignIn**

- Invariant: A pending sign-in never authenticates a request.
- `VerifySecondFactor`: pre: The pending sign-in is valid.. Post: A valid factor issues a session.; A used code issues no session..

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Second-factor check | TOTP code replayed | Use a new code. | No session issued. |
