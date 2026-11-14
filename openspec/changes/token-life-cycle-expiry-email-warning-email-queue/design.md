## Context

A background sender reads due token warnings from a durable queue and sends through installation SMTP settings. Enforcement and display are in the other two parts.

## Decisions

1. **Durable queue with one retry and fixed times.** The queue stores one row per token and warning time with the stable token ID, the warning time, the due time, a state of queued, sending, retry, sent or failed, a try count and a reason. Poll interval is 60 seconds. One sender holds a lease of 300 seconds per row. A failed send moves the row to retry with a 15 minute backoff, then to failed after one retry.
2. **Stable token ID with send-time recheck.** The token ID is the stable reference. The sender reads the owner email at send time and rechecks that the token still exists, is enabled and still expires after the warning time. A missing address, a revoked or disabled token, or a changed expiry marks the row failed or dropped with the reason, without a send.
3. **No plain-text fallback.** SMTP dial uses the resolved installation settings and the TLS rule of ADR 292. A refused plain connection marks the row failed with the reason refused. No fallback changes the connection to plain text.

## Structure

Unexported type warningQueue with constructor newWarningQueue that checks poll interval 60 seconds, lease 300 seconds and retry limit 1; only the constructor makes a sender, and rows move only through queued, sending, retry, sent and failed.

## Risks

- Duplicate email on sender restart.
- Password leak into queue row or log.
- Plain SMTP to a non-loopback host.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| SMTP settings adapter | Port adapter checks relay host, port range and sender address against an allowlist and refuses empty values and CRLF. | Send a row with an empty relay and a sender with CRLF; the adapter refuses without a network call. |
| Credential handling service | Service holds the SMTP password only in memory from env or file, never writes it to the queue row, log or audit entry. | Force a send failure and read the row, log and audit entry; none holds the password. |
| TLS dial guard | Dialer enforces TLS to non-loopback hosts and fails closed on missing or not valid opt-in. | Point relay at a plain non-loopback listener with opt-in off; the dial refuses. |
