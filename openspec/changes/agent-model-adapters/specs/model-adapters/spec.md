## ADDED Requirements

### Requirement: One adapter serves every server of a format
The server SHALL have one adapter for each of the formats `chat-completions` and `messages`. Each adapter SHALL use `net/http` and `encoding/json` only. No adapter SHALL have a code path for the name of a provider or a host.

#### Scenario: A server with its own field name
- **WHEN** a document sets `outputTokens.field` to `max_completion_tokens`
- **THEN** the request body holds that field and not `max_tokens`

#### Scenario: A format with no adapter
- **WHEN** a connection has the format `responses`
- **THEN** `agent.NewModel` fails with `ErrFormatNotSupported`

### Requirement: The adapter never writes over its own fields
The adapter SHALL merge `providerFields` into the request body with a deep merge. The merge SHALL NOT change a value that the adapter wrote. Validation SHALL refuse an owned path, a path below an owned path, and a value that is not an object above an owned path.

#### Scenario: providerFields holds an owned key
- **WHEN** `providerFields` has the key `messages`
- **THEN** the document fails validation

### Requirement: The conversation key holds no identity
The adapter SHALL send a conversation key only in the places that the document names. The key SHALL be the first 26 characters, in lower-case base32, of HMAC-SHA256 over the ID of the first run, with the HMAC key of the installation. The adapter SHALL NOT send an end-user field.

#### Scenario: A follow-up run
- **WHEN** a second run continues a conversation
- **THEN** its key equals the key of the first run

#### Scenario: A providerFields entry names user
- **WHEN** `providerFields` has the key `user`
- **THEN** the document fails validation

### Requirement: Errors map by type and code
The adapter SHALL map an answer to a sentinel by status and by the fields `error.type` and `error.code`, and SHALL NOT read the text of the error. A wait above 5 minutes SHALL stop the run with `model-rate-limited`.

#### Scenario: A 429 with a quota code
- **WHEN** the answer is 429 and its `error.code` is in `errors.quota`
- **THEN** the call fails with `ErrModelQuota` and the run stops with `model-quota`

### Requirement: The connection is guarded where it opens
The client SHALL check each resolved address when the connection opens. It SHALL refuse an always-denied address first. It SHALL NOT follow a redirect and SHALL NOT use a proxy. A call SHALL end after 10 minutes, and a stream with no byte for 2 minutes SHALL fail.

#### Scenario: A host name that resolves to a metadata address
- **WHEN** an allowed host name resolves to a link-local address
- **THEN** the connection fails and the audit log has an entry

### Requirement: An opaque part is kept and not read
The store SHALL seal an opaque reasoning part with the run, turn and part as additional data and SHALL give it back byte for byte. The readable record of the part SHALL hold its size and its format only.

#### Scenario: A canary in an opaque part
- **WHEN** an opaque reasoning part holds a canary
- **THEN** the part comes back byte for byte and no readable record holds the canary
