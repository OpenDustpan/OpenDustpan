## ADDED Requirements

### Requirement: Whole chain compare

While the server is built with NewServer, the server SHALL expose the whole wrapper chain including the token gate inside identity.Chain to the chain test.

#### Scenario: ChainShowsWholeChain
- **WHEN** the chain test runs
- **THEN** it compares the whole NewServer chain including the token gate inside identity.Chain, and the expected chain is updated with the wrapper change

### Requirement: Lone dev guard test

While the dev guard is tested alone, the server SHALL fail the lone test of that guard when the guard is removed.

#### Scenario: GuardAloneNamesGuard
- **WHEN** the dev guard is removed
- **THEN** the failing test names the dev guard alone
