## ADDED Requirements

### Requirement: own-list-feeds-the-console-banner

The own token list SHALL answer the tokens that expire within the warning window when the caller sends `status=expiring`, each with `expiresInDays` and `expiryWarning` from the server clock, so that the console can show a banner from that one route.

#### Scenario: expiring tokens
- **WHEN** a person has one token that expires in 10 days and one that expires in 60 days, and reads the own list with `status=expiring`
- **THEN** the answer holds the first token with `expiryWarning` soon and not the second

#### Scenario: no total
- **WHEN** the own list has a next page
- **THEN** the answer holds `nextPageToken` and the console shows "at least" with the count of the page

### Requirement: console-banner-fails-closed

The console SHALL show the banner only from a response that passes its runtime check, and SHALL show the error state when the load fails.

#### Scenario: failed load
- **WHEN** the own list answers 400 or a body that fails the check
- **THEN** the page shows the error state and no banner
