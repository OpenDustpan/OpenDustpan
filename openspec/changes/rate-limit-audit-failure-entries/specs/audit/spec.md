## ADDED Requirements

### Requirement: Limit per-principal refusal entries

When a principal exceeds 20 refusal entries per 60 seconds, the server SHALL drop the excess refusal entries and keep the same refusal answer.

#### Scenario: Over-limit preference refusal keeps answer and drops entry
- **WHEN** one principal sends preference refusals above 20 entries per 60 seconds
- **THEN** the server answers with the same refusal, writes no new refusal entry, and counts one dropped entry

#### Scenario: Over-limit token refusal keeps answer and drops entry
- **WHEN** one principal sends token route refusals above 20 entries per 60 seconds
- **THEN** the server answers with the same refusal, writes no new refusal entry, and counts one dropped entry

#### Scenario: Second principal is unaffected
- **WHEN** a first principal is over its limit and a second principal sends a refusal
- **THEN** the server writes the refusal entry for the second principal

#### Scenario: Bucket refills after window
- **WHEN** 60 seconds pass after the per-principal limit was hit
- **THEN** the server writes the refusal entry again

#### Scenario: Concurrent same-principal refusals stay bounded
- **WHEN** one principal sends concurrent refusals above the limit
- **THEN** the server stores at most 20 refusal entries plus at most one summary entry for that minute

#### Scenario: Restart resets per-process bucket
- **WHEN** the server process restarts after a principal hit the limit
- **THEN** the server enforces the same 20-per-60-second limit again from zero

#### Scenario: Full subject table still refuses safely
- **WHEN** the limiter table holds 10000 subjects and a new subject sends a refusal
- **THEN** the server answers with the same refusal and stores at most the global caps with no extra entry

#### Scenario: AllowRefusal decides each refusal entry
- **WHEN** a handler calls AllowRefusal with a verified subject and the entry class refusal after 20 entries in 60 seconds
- **THEN** AllowRefusal returns false and records no entry, and it returns true for the next subject

### Requirement: Keep policy and change entries

Where a refusal entry records a policy-check denial or a real change, the server SHALL write the entry outside the refusal limit.

#### Scenario: Policy denial still writes entry when bucket empty
- **WHEN** a principal over the refusal limit hits a policy-check denial
- **THEN** the server writes the denial entry

### Requirement: Bound empty-subject refusals

If a refusal has no verified principal subject, then the server SHALL apply the empty-subject bucket of 100 entries per 60 seconds.

#### Scenario: Empty-subject flood is bounded
- **WHEN** refusals arrive with no verified principal subject above the empty-subject rate
- **THEN** the server stores at most 100 refusal entries per 60 seconds for empty subjects

### Requirement: Bound total refusal entries

When all principals together exceed 200 refusal entries per 60 seconds in the @server scope, the server SHALL drop the excess refusal entries.

#### Scenario: Many-principal flood is bounded
- **WHEN** many principals together send refusals above the global rate
- **THEN** the server stores at most 200 refusal entries per 60 seconds in the @server scope

### Requirement: Record drops with bounded summary

When the server drops refusal entries for a principal, the server SHALL write at most one summary entry per 60 seconds for that principal.

#### Scenario: Drops leave a bounded record
- **WHEN** refusal entries are dropped for a principal
- **THEN** the server writes at most one summary entry per subject per 60 seconds with only the dropped count
