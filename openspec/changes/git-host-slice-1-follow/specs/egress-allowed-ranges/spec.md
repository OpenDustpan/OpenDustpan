## ADDED Requirements

### Requirement: Refuse public ranges

When an allowed range list holds a globally routable range, the server SHALL have ParseAllowedRanges refuse the list.

#### Scenario: Refuses a public range
- **WHEN** a list holds 0.0.0.0/0 or 8.8.8.8/32
- **THEN** parse fails with an error that names the range

#### Scenario: Refuses a straddling range
- **WHEN** a list holds 10.0.0.0/7
- **THEN** parse fails and returns no list

#### Scenario: Accepts private-only lists
- **WHEN** a list holds only ranges inside 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or fc00::/7
- **THEN** parse returns the list

### Requirement: Rules still ignore non-private ranges

Where a non-private range reaches the egress rules, the server SHALL deny it.

#### Scenario: Rule denies a public range past the parser
- **WHEN** a test injects a public prefix past ParseAllowedRanges
- **THEN** the egress rule denies the address
