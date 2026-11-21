## Context

An operator installs and configures the server, and must meet local key control rules and prove which key seals stacks and workspaces. The design waits for RFC 33, so the user path, errors, and key outage behavior stay open.

This change records that customer-managed keys are wanted and wait on secrets work. It keeps the current single installation key-encryption key until RFC 33 is accepted. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The KMS connector, per-namespace keys, rotation automation, and backup and restore rules for customer keys all wait for RFC 33. No key plumbing lands before the design is accepted.
2. **One installation key-encryption key stays.** Main seals with a single installation key from the secret domain package (`pkg/secret`) with envelope encryption. That behavior is unchanged until the RFC lands.
3. **A missing key keeps its error shape.** The catalog already names the missing key-encryption key error. Outage behavior refines with the RFC instead of growing a second error here.
4. **Key identity is shown, never key material.** The refinement proves which key seals each scope by identifier and audit record, never by value.

## Structure

No new structure now. The bug class is a second key path beside the installation key; one envelope-encryption path with a key identifier on each sealed value is the structure that removes it. When RFC 33 is accepted, the key reference is an unexported value built only through its constructor, which refuses unknown key identifiers instead of falling back to the installation key.

No custom analyzer and no new tool.

## Risks

- Operators cannot meet key control rules until RFC 33 lands. The docs page says the installation key is the only key and names the wait.
- A key outage reads as data loss. The refinement names outage behavior (fail closed with the named error), so an outage refuses instead of corrupting.
- A backup restores under the wrong key. Backup and restore rules for customer keys ship with the RFC, not after it.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| domain | Seal through envelope encryption only; record the key identifier with each sealed value, never the material. | Read a sealed row; it names the key and shows no key bytes. |
| service | Fail closed with the named missing-key error when the key is absent; change nothing. | Start the server without the key; each seal and unseal refuses with the named code. |
| handler | Refuse key management calls without the grant; never echo key material into errors or logs. | Ask for the key value through the API; the answer is a refusal with no material. |
