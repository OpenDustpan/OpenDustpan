## ADDED Requirements

### Requirement: Open a static profile

When a caller with profile:open on the namespace opens a static profile with the consumer api, console or pulumi, the server SHALL merge the imports, keep each final value, compute each expression value, store the result as one open session, and answer the session ID and its end time with no value.

#### Scenario: Merge in list order
- **WHEN** a profile imports two profiles and sets a value of its own
- **THEN** the session holds the merge in which a later item wins, a map merges key by key, a list or a scalar replaces, and a null removes the earlier value

#### Scenario: Final value holds
- **WHEN** a later profile replaces or removes a value that an earlier profile marked final
- **THEN** the open fails with profile.ErrFinal that names the path and both profiles, and stores no session

#### Scenario: Marks propagate
- **WHEN** an expression reads a secret value or a never-store value
- **THEN** the computed value has the same mark

#### Scenario: Import cycle
- **WHEN** a later revision of an import makes a cycle
- **THEN** the open fails with profile.ErrImportCycle that names the cycle

#### Scenario: One audit entry
- **WHEN** an open succeeds
- **THEN** the server writes one audit entry of the kind access with the action profile:open, the consumer, each profile and revision that the open read, the paths and no value

### Requirement: Refuse a caller without profile:open

If the caller lacks profile:open on the namespace, then the server SHALL answer 403, store no session and write one audit entry of the kind access with the outcome denied and no value.

#### Scenario: No grant
- **WHEN** a caller with profile:read and no profile:open opens a profile
- **THEN** the server answers 403, stores nothing and writes one denied entry

#### Scenario: Job token
- **WHEN** a caller with a job token opens a profile
- **THEN** the server answers 403, stores nothing and writes one denied audit entry, because the job open is not built (owner, 2026-11-20)

### Requirement: Refuse never-store

If an open of a session consumer asks a path whose value is marked never store, then the server SHALL refuse the open with profile.ErrNeverStore that names the path, and store no session.

#### Scenario: Never-store path asked
- **WHEN** an open of the consumer api asks a path that is marked never store, by default or in the paths list
- **THEN** the server answers 422 with the code profile.never-store and the path, and stores nothing

#### Scenario: Path left out
- **WHEN** the paths list leaves the never-store path out
- **THEN** the open succeeds and the session holds no never-store value

### Requirement: Refuse what the open cannot do yet

If the open reaches a provider block, a rule in checks, a consumer other than api, console and pulumi, or an expression name other than values, then the server SHALL refuse the open, name the cause and store no session.

#### Scenario: Provider block
- **WHEN** a profile that the open reaches holds a provider block
- **THEN** the open fails with profile.ErrProvider that names the block, and the server answers 422 with the code profile.provider

#### Scenario: Rule in checks
- **WHEN** a profile that the open reaches holds a rule in checks
- **THEN** the open fails with profile.ErrInvalid that names the profile, until the gate slice for the decision point profile.open lands (owner, 2026-11-20)

#### Scenario: Other consumer
- **WHEN** the consumer is run, opentofu, launcher or observe
- **THEN** the open fails with profile.ErrInvalid

#### Scenario: Other expression name
- **WHEN** an expression reads providers, imports, profiles or context
- **THEN** the open fails with profile.ErrReference that names the profile and the path

### Requirement: Limit an open

If an open reads more than 64 profile revisions, an import depth above 16, or a result above 4 MiB, or asks a duration outside 1 minute to 12 hours, then the server SHALL refuse it with profile.ErrLimit or profile.ErrInvalid that names the limit.

#### Scenario: Limit
- **WHEN** an open reads 65 profile revisions
- **THEN** the open fails with profile.ErrLimit that names the limit

#### Scenario: Duration
- **WHEN** the duration is 30 seconds or 13 hours
- **THEN** the open fails with profile.ErrInvalid and the default duration of 2 hours applies only when the request gives none

### Requirement: Replay an open

When a caller repeats an open with the same Idempotency-Key and the same body within 24 hours, the server SHALL store no second session and SHALL answer 409 with the code request.idempotency-key-replayed-secret and the session ID.

#### Scenario: Replay key
- **WHEN** a principal repeats an open with the same key and body
- **THEN** the server answers 409 with the session ID and the table holds 1 session

#### Scenario: Replay conflict
- **WHEN** a principal replays a key with another body
- **THEN** the server answers 422 with the code request.idempotency-key-reused and stores nothing new

#### Scenario: Replay after revoke
- **WHEN** a principal repeats a key after it lost profile:open
- **THEN** the server answers 403 and not the stored answer

#### Scenario: Record holds no value
- **WHEN** the open stored a session
- **THEN** the replay record holds the session ID and no value

### Requirement: Store a session

When an open succeeds, the server SHALL store one session that is sealed with the data key of the root profile, and SHALL keep at most 100 live sessions for each principal.

#### Scenario: Put one session
- **WHEN** a principal opens with the consumer api
- **THEN** the server stores one session with an ID that starts with pos- and the store holds only the ciphertext

#### Scenario: Limit
- **WHEN** a principal has 100 live sessions and opens again
- **THEN** the open fails with profile.ErrLimit and stores nothing

#### Scenario: Atomic
- **WHEN** the audit write of an open fails
- **THEN** no session stays
