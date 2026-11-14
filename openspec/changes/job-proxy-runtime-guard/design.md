## Context

Untrusted job code runs on runners. The sandbox must have no path around the job proxy. Failure of the proxy must stop the job, not open direct access.

## Decisions

1. **Proxy per claim as only exit.** One proxy process per claim, started by the coordinator before any step starts. Steps get proxy address by environment only, with no direct route. Loss of proxy or failed start fails the phase closed.
2. **Bound dial with outbound guard.** All dials use a bound dialer that resolves, checks each address with pkg/egress, and connects only to a checked address. The check repeats after upstream proxy selection. Tunnel to server host is built in.

## Structure

Unexported proxyServer built only by newProxyServer with bound dialer, egress checker, and tunnel host. No caller can make a dial path that skips the checker because dialer field is unexported and constructor rejects nil checker.

## Risks

- Sandbox escape by direct route left after proxy restart.
- DNS time-of-check to time-of-use gap between resolve and connect.
- Upstream proxy that does not apply its own rules.
- Log line that leaks header, body, query value, or credential.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| placement and network | Coordinator starts proxy as own user outside step sandbox, sets sandbox route to proxy address only, drops other packets including DNS. Start failure stops claim before steps start. | Remove route drop and run job that opens direct TCP to outside host; test fails when connection succeeds. |
| request check | Proxy checks CONNECT host against proxy rules before TLS, refuses non-canonical path, checks SNI and Host header match, resolves tunnel host and checks each address with pkg/egress, dials only checked address. | Send CONNECT to unnamed host, mismatched SNI, non-canonical path, and DNS that resolves to denied address; each gets refusal without outbound connection. |
| fail closed | Proxy loss, upstream failure, or guard denial fails the request closed, writes blocked line to job log, and marks phase failed. No fallback to direct dial exists in step environment. | Kill proxy mid-phase and attempt direct fetch from step; fetch fails and phase ends failed with blocked line present. |
