## ADDED Requirements

### Requirement: unlisted-identical-answer

When the server receives an unlisted path from the fixed allowlist, the server SHALL answer with identical non-2xx status and code through the gated handler and the ungated handler.

#### Scenario: unlisted identical answer
- **WHEN** an unlisted path from the fixed allowlist is requested with no credential through Handler and UngatedHandler
- **THEN** both answers carry identical non-2xx status and identical error code

#### Scenario: positive control catches middleware path
- **WHEN** a middleware serves one unlisted path in front of the root mux
- **THEN** the NEW TestUnlistedPathsMatchGatedAndUngated fails

### Requirement: exact-unauthenticated-answer

The server SHALL answer each listed route without a credential with its exact unauthenticated answer from the per-family table.

#### Scenario: exact unauthenticated answer
- **WHEN** a listed route answers a 400 or 404 that is not its table value
- **THEN** the probe fails and reports the path with expected and actual status and code

#### Scenario: probe coverage statement
- **WHEN** the probe runs on a started server with all components enabled
- **THEN** the probe doc comment names the covered chain and the skipped misuse reporter, request cache, request log, and component gate
