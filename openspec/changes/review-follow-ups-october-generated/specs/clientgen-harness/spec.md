## ADDED Requirements

### Requirement: stale-412-checks

When a stale If-Match yields 412, the server SHALL include the documented errorCode with a non-empty message.

#### Scenario: Stale 412 checks code and message
- **WHEN** a 412 answer arrives for a stale If-Match
- **THEN** the case rejects missing errorCode, wrong errorCode, and empty message, and accepts the documented errorCode with a non-empty message.

### Requirement: proxy-strips-nominated

When the conformance proxy forwards a request or an answer, the server SHALL exclude headers nominated by Connection values.

#### Scenario: Proxy strips nominated headers
- **WHEN** a request or answer carries Connection values with mixed case, multiple fields, comma-separated values, close and keep-alive tokens, and empty tokens
- **THEN** nominated and standard hop-by-hop headers are excluded both directions while unrelated end-to-end headers survive.

### Requirement: descendant-cleanup

When an adapter closes, the server SHALL terminate its descendant processes by process-group signal with reuse protection.

#### Scenario: Descendant cleanup on Close
- **WHEN** an adapter with a detached-stdio child closes
- **THEN** Close terminates the child, an already-exited child reports success, a SIGTERM-ignoring child receives SIGKILL after 5 seconds, and a reused process-group ID is not signaled.

### Requirement: harness-death-cleanup

When the harness dies on Linux, the server SHALL clean up the adapter and its child processes.

#### Scenario: Harness death cleans adapter and child
- **WHEN** the harness dies on Linux while an adapter and its child ignore stdin EOF
- **THEN** neither the adapter nor its child survives, and thread lifecycle does not kill an adapter whose harness stays alive.

### Requirement: seed-reuse

When seed setup finds an existing seed profile with matching content, the server SHALL reuse it rather than fail on creation conflict.

#### Scenario: Seed reuse second run
- **WHEN** seed setup runs twice against the same server
- **THEN** the second run reuses the profile and reads it back with a usable ETag, while mismatched content, half-written content, failed ETag re-read, unrelated errors, and a concurrent create race still fail except for one winner.

### Requirement: decode-collision

When TypeScript emission claims decode identifiers, the server SHALL reject collisions with its invalid-name error before rendering.

#### Scenario: Decode collision rejected
- **WHEN** a type named Foo collides with decodeFoo or Values names, including a cross-type decode-name collision
- **THEN** the generator rejects with its invalid-name error before rendering.

### Requirement: toolchain-note

Where the Go generator renders output, the server SHALL document that byte-identical output needs the same Go formatting toolchain.

#### Scenario: Toolchain condition documented
- **WHEN** a reader checks the golang generator docs
- **THEN** the package comment states byte-identical output needs the same Go formatting toolchain because generation uses go/format.
