## ADDED Requirements

### Requirement: single-enforce-path

The server SHALL authorize runner, jobs and drift routes through route.Enforce.

#### Scenario: Runner route through Enforce
- **WHEN** a runner management request arrives on ANY /api/runners/{}/pools
- **THEN** the server authorizes the request through Guard.AuthorizeObject and no caller-local wrapper runs

#### Scenario: Jobs route through Enforce
- **WHEN** a jobs request arrives on its declared jobs route
- **THEN** the server authorizes the request through Guard.AuthorizeObject and no caller-local wrapper runs

#### Scenario: Drift route through Enforce
- **WHEN** a drift request arrives on GET /api/dustpan/v1/namespaces/{}/units/{}/drift
- **THEN** the server authorizes the request through Guard.AuthorizeObject

### Requirement: caller-copies-deleted

When route.Enforce runs the address check, the server SHALL hold no address check in the runner, jobs and drift handlers.

#### Scenario: No caller address copy
- **WHEN** route.Enforce runs the address check
- **THEN** no address check remains in the runner, jobs or drift handlers

### Requirement: fold-authorize-all

When a route passes checks to route.Enforce, the server SHALL evaluate the checks through Guard.AuthorizeObject.

#### Scenario: One authorize implementation
- **WHEN** a route passes checks to route.Enforce
- **THEN** the server evaluates the checks through the Guard.AuthorizeObject path

#### Scenario: Unknown check denied
- **WHEN** route.Enforce receives an unknown check
- **THEN** the server denies the request and writes the denial to the audit log

#### Scenario: Empty checks denied
- **WHEN** route.Enforce receives empty checks
- **THEN** the server denies the request and writes the denial to the audit log

### Requirement: address-check-order

While a namespaced tool-neutral route enforces a request, the server SHALL check the direct peer address between Authenticate and Resolve.

#### Scenario: Denied address before resolve
- **WHEN** a namespaced tool-neutral request arrives from a denied client address
- **THEN** the server answers the NEW error code route.address-forbidden without Resolve or handler work

### Requirement: no-namespace-skip

When a route enforced by route.Enforce carries no namespace identity, the server SHALL skip the address check.

#### Scenario: Route with no namespace
- **WHEN** a route enforced by route.Enforce carries no namespace identity
- **THEN** the server skips the address check and continues to Resolve

### Requirement: fail-closed-deny

When the allowlist read errors or times out or the peer address is unparseable, the server SHALL deny the request.

#### Scenario: Allowlist read error
- **WHEN** the allowlist read errors or times out or the peer address is unparseable
- **THEN** the server denies the request and writes the denial to the audit log

### Requirement: address-forbidden-shape

When a denied client address reaches a namespaced tool-neutral route, the server SHALL answer 403 with the NEW error code route.address-forbidden.

#### Scenario: Denied address answer
- **WHEN** a denied client address reaches a namespaced tool-neutral route
- **THEN** the server answers 403 with the NEW error code route.address-forbidden and a message that names the allowlist read command
