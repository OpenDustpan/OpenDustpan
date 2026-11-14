## ADDED Requirements

### Requirement: Token open

When a namespace administrator names a GitLab connection, the server SHALL open the NEW GitLab Forge with the sealed token of that connection.

#### Scenario: Open with token
- **WHEN** the administrator names a GitLab connection with a token
- **THEN** the server opens a GitLab Forge bound to the stored address

#### Scenario: Refuse plain address
- **WHEN** the address uses the http or file scheme
- **THEN** the server refuses the open before any host call

#### Scenario: Refuse private target
- **WHEN** the stored address resolves to a private IP
- **THEN** the server refuses the open before dial

### Requirement: Check expiry answer

When the GitLab self endpoint answers expires_at, the server SHALL carry that expiry in the Check answer.

#### Scenario: Self reports expiry
- **WHEN** the self endpoint answers expires_at
- **THEN** the Check answer carries that expiry

#### Scenario: No expiry
- **WHEN** the self endpoint answers no expires_at
- **THEN** the Check answer flags a token with no expiry

#### Scenario: Malformed expiry
- **WHEN** the self endpoint answers a malformed expires_at
- **THEN** the Check answer treats it as no expiry

### Requirement: Check failure closed

When GitLab refuses the token or the self endpoint fails, the server SHALL deny the Check closed with GitLab refused the credential.

#### Scenario: Bad token
- **WHEN** GitLab answers 401
- **THEN** the server denies closed

#### Scenario: Rate limit
- **WHEN** GitLab answers 429
- **THEN** the server denies closed with a Retry-After hint and no automatic retry

#### Scenario: Bad retry hint
- **WHEN** GitLab answers a huge or malformed Retry-After
- **THEN** the server clamps the hint to 300 seconds and denies closed

#### Scenario: Redirect
- **WHEN** GitLab answers a redirect
- **THEN** the client refuses it and denies closed

### Requirement: Commits chain

When a caller lists commits of a GitLab repository through ListCommits, the server SHALL answer each commit with its id and its parent link.

#### Scenario: List commits
- **WHEN** a caller lists commits
- **THEN** each commit carries its id, parent links and last_commit_id

#### Scenario: Many parents
- **WHEN** a commit has several parents or none
- **THEN** the server answers all parent links

### Requirement: ParseEvent token

If an event carries a wrong X-Gitlab-Token value, then the server SHALL deny the ParseEvent webhook request with 401 and no state change.

#### Scenario: ParseEvent wrong token
- **WHEN** the ParseEvent token is wrong
- **THEN** the server answers 401 and changes no state

#### Scenario: ParseEvent missing token
- **WHEN** the ParseEvent token is missing or empty
- **THEN** the server answers 401 and changes no state

#### Scenario: ParseEvent unknown hook
- **WHEN** the ParseEvent hook ID is unknown
- **THEN** the server answers 404 and changes no state

### Requirement: Unsupported groups

When a caller calls Reviews, Tokens, or Rotation on the GitLab adapter, the server SHALL answer ErrUnsupported.

#### Scenario: Unsupported group
- **WHEN** a caller calls Reviews
- **THEN** the server answers ErrUnsupported
