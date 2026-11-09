## ADDED Requirements

### Requirement: An audit sink and a log sink apply the egress rules
The server SHALL send each request of a webhook sink and of an OTLP sink
through the egress client, with the installation rules: the check of the URL,
the check of the resolved address of each connection, and the refused ranges,
including the link-local metadata address. The server SHALL refuse a request to
an address that the rules refuse, before a connection opens. A sink that a
refusal stops SHALL record the refusal in its delivery status.

#### Scenario: A blocked address literal
- **GIVEN** a `--audit-sink webhook:` item, an `otlp:` item, or a `--log-sink` item with a loopback or metadata IP literal
- **WHEN** the operator starts the server
- **THEN** the start fails, the error names the sink item, and the error holds no URL

#### Scenario: A host that resolves to a blocked address
- **GIVEN** a sink URL whose host resolves to a private, loopback or metadata address
- **WHEN** the server ships a batch
- **THEN** the delivery fails with `egress.ErrRefused`, the receiver reads no request, and the sink status shows the refusal

#### Scenario: A public receiver
- **GIVEN** a sink URL with a public address that the rules allow
- **WHEN** the server ships a batch
- **THEN** the receiver holds the batch, and the sink status shows the delivery

#### Scenario: A receiver on a private network
- **GIVEN** a sink URL whose host resolves to a private address
- **WHEN** the server ships a batch
- **THEN** the delivery fails with `egress.ErrRefused`, and the sink status shows the refusal, because the installation has no allowed range for a sink
