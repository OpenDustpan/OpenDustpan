## ADDED Requirements

### Requirement: No change on main

While the spike runs, the spike branch SHALL keep every file outside spike/ unchanged.

#### Scenario: main stays clean
- **WHEN** the spike run ends
- **THEN** git diff against main lists only files under spike/

### Requirement: Spike doc records answers

When the scratch run ends on a named site, the spike doc SHALL record the answer of that site to each of the six points.

#### Scenario: answers recorded
- **WHEN** the scratch run opens, reads, notes and closes one ticket on a site
- **THEN** the spike doc lists the refused roles, the rate limit, the webhook expiry and the search-after-create result for that site
