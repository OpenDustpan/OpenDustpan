# access-token Specification

## Purpose
An access token is a credential that the server issues to a principal, with an expiry. This capability covers the count of days that remain until the expiry of a token. It does not cover how the server issues, stores or revokes a token.

## Requirements

### Requirement: The days until an expiry never go negative

`accesstoken.DaysUntil` SHALL answer the number of started days until the expiry, SHALL answer a positive count for any expiry in the future, including an expiry that saturates `time.Duration`, and SHALL answer 0 for an expiry that is not in the future.

#### Scenario: A far expiry
- **WHEN** the expiry is `9999-12-31T23:59:59Z` and now is in 2026
- **THEN** the count is positive and above 100000

#### Scenario: An expiry two days in the past
- **WHEN** the expiry is 48 hours before now
- **THEN** the count is 0
