## Context

The first two parts hold the bundle, the rule, the exchange and the trust token. This part proves that path with the real binary over TLS and the real CLIs, and writes the docs page with the proxy rule.

## Decisions

1. **CLIs use the exchanged token.** The CLIs send no client certificate. The workload presents the client certificate only at the exchange call, gets a stored trust token, and runs the Pulumi CLI and OpenTofu with that token.
2. **Proxy rule for mTLS.** The server reads the client certificate only from the TLS handshake. It ignores forwarded certificate headers, so a client cannot forge them. Behind a proxy of the operator the install must terminate mTLS at the server, else the exchange refuses with no certificate.

## Structure

No new stored type. The proof uses the trust bundle, the trust rule and the stored trust token from the first two parts. The e2e helper builds a test CA, a client certificate with a SPIFFE ID in the URI SAN, and an expired and a foreign-CA certificate for refusals.

## Risks

- A docs command that does not run as written hides a setup fault.
- A proxy that strips the client certificate gives a refusal that names the proxy rule.
