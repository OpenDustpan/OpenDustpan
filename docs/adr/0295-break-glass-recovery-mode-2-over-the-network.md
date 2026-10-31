# 295. Break-glass recovery mode 2 over the network

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026. Amends ADR 245 (decision 12 and the item "A stored recovery mode" of "Not decided here")

## Context

Mode 2 of ADR 57 is a recovery request that an offline operator key signs. ADR 245 decision 12 lets only the host command reach the host issuer. That rule stops a route in the server from making a temporary administrator.

The owner decided that mode 2 may run over the network. The server then needs a route that accepts a signed request.

## Decision

1. **The route is allowed (owner).** Mode 2 may run over the network. A route in the server accepts a signed recovery request. The route makes a temporary administrator. This amends ADR 245 decision 12 for this route only. The test of that decision names the route as the one other caller. (owner)
2. **The risk statement (owner).** The documentation and the setting say this: "A theft of the private key alone gives a bounded remote temporary administrator." The bound is the expiry. The limits of a temporary administrator in ADR 245 also apply. (owner)
3. **The nonce is a unique-key insert.** The server inserts the nonce of a request with a unique key. The insert is in the same transaction that makes the temporary administrator. A second request with the same nonce fails the insert. The transaction fails with it. (owner)
4. **A shared rate limit.** The database holds the counter of the rate limit, so all instances share it. An IPv6 address counts as its /64 group. (owner)
5. **One answer for every refusal.** Every refusal gives the same answer: the same status, the same body and the same shape. A caller cannot tell the reason of a refusal. A bad signature, an old time, a used nonce, a rate limit and a wrong mode look the same. The audit entry holds the real reason. (owner)
6. **The answer token is encrypted.** The server encrypts the token in the answer. It uses an ephemeral public key that the CLI sends in the request. Only the CLI that made the request can read the token. (owner)
7. **Nonce retention.** The server keeps a nonce until the not-after time of its request plus 1 hour. (owner)
8. **A condition on shipping.** The route ships only after the store keeps the first-boot recovery mode and the key. This is the item "A stored recovery mode" in ADR 245. Until then the route does not exist. (owner)
9. **The controls of ADR 245 apply.** A temporary administrator from this route keeps these controls of ADR 245. They are the reason, the audit entries and the expiry. They also include the refusal in mode 3 and the review of decision 3.
10. **Fail closed.** On an error or an unknown value, the route refuses and writes an audit entry.

## Consequences

- ADR 245 gets a pointer to this ADR.
- Each step of decision 3 to decision 7 needs its own test that sends hostile input to that step alone.
- The user documentation for recovery says what the risk statement says.
