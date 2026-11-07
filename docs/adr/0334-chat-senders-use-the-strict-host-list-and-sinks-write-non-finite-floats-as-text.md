# 334. Chat senders use the strict host list, and log sinks write non-finite floats as text

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 145 decisions 13 and 20: a chat sink has no legacy host path. The strict host list applies at save time. It applies at send time too.
- Extends ADR 60 decision 12: the OTLP sink and the webhook sink write a float that is not finite as text. The OTLP sink takes a `ca-file` option.

## Context

ADR 145 decision 13 says that the `Target` of a chat sink holds only the host of the URL. It also says that the chat sender sends only to a URL with that host. Decision 20 adds the outbound rules for the chat and webhook senders.

`notify.Sink.ValidateWrite` applies a strict host list when a person creates or replaces a sink. The list accepts an ASCII DNS name, or an IP literal, with an optional numeric port. A sink that the server stored before the strict list can pass `Validate` and fail the strict list. The code keeps this legacy path. Such a sink still sends. Only a replace of it needs a host that passes the list.

The OTLP sink writes a record as OTLP/HTTP JSON. JSON has no text for NaN or infinity. So the OTLP encoder fails the record when a float is not finite. The OTLP sink also has no option for a private certificate authority.

On 7 November 2026 the project owner decided these points.

## Decision

Decided by the project owner on 7 November 2026.

1. **Delete the legacy chat host path (owner, 2026-11-07).** A chat sink has one host rule. Code does not keep a second, weaker path for a sink with a host that the strict list refuses.
2. **The strict host list applies twice (owner, 2026-11-07).** The server checks the host against the strict list when a person saves the sink. The sender checks it again at send time. The two checks are separate layers. Neither check relies on the other.
3. **Write a float that is not finite as text (owner, 2026-11-07).** The OTLP sink and the webhook sink write NaN as `"NaN"`. They write positive infinity as `"Infinity"` and negative infinity as `"-Infinity"`. This is the proto3 JSON mapping. A record with such a float is no longer a failed record.
4. **The OTLP sink takes `?ca-file=` (owner, 2026-11-07).** The URL of an OTLP sink can have the query option `ca-file`. Its value is the path of a PEM file. The file holds the certificate authorities that the sink trusts for its endpoint. The meaning that the option adds to the system roots, and does not turn off the check of the certificate, is the proposed meaning. The owner did not decide it.

## Consequences

- This ADR does not decide the host rule of the webhook sink. The decision about the legacy host path is for the chat sender only.
- A sink with a host that the strict list refuses stops sending. The delivery fails with the error class `refused`. The operator replaces the sink with a host that passes the list.
- The method `HostNeedsUpdate` and the test fixtures for a legacy sink go away with the legacy path.
- The tests of the host list stay. Each layer keeps its own test with hostile input.
- The page `docs/reference/notification-destinations.md` or the guide for notification destinations may describe the legacy behavior. The author of the code change checks that page.
- The pages for log sinks need a line for the `ca-file` option. They need a line for the text of floats that are not finite.
- This ADR does not decide how the server finds the file that `ca-file` names, for example a relative path or a missing file. The code change follows the rules of ADR 250 for file paths.
