## ADDED Requirements

### Requirement: guide-steps

Where the runner operator guide is published, the server docs SHALL describe create pool, join token, start runner, scope, watch, drain, and retire in order.

#### Scenario: expired-token-renew
- **WHEN** the operator joins with an expired or revoked token
- **THEN** the page shows the runner.invalid text and the one revoke and reissue call

#### Scenario: wrong-scope-no-claims
- **WHEN** the pool is scoped to the wrong namespace and gets no claims
- **THEN** the page shows how to list pools and fix the scope ids before more work

#### Scenario: drain-lets-claim-finish
- **WHEN** the operator drains a runner with a running claim
- **THEN** the page shows that drain stops new claims, lets the running claim finish, and retire follows only after the watch shows no claim

#### Scenario: secret-not-in-shell-history
- **WHEN** the operator copies a join token or access token
- **THEN** the page passes the join token in the request body from a file and never on the command line, and lists the grant each step needs

### Requirement: error-help

Where a runner step fails, the server docs SHALL name the error code and the one API call that shows the state.

#### Scenario: token-used-help
- **WHEN** join fails because the token was already used
- **THEN** the page names runner.token-used and the one join-token call that helps

#### Scenario: not-found-help
- **WHEN** pool or runner lookup fails
- **THEN** the page names runner.not-found and the one pool list call that helps

#### Scenario: retired-help
- **WHEN** a claim reaches a retired runner
- **THEN** the page names runner.retired and states that retire is final
