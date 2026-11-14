## ADDED Requirements

### Requirement: Sealed Slack app connection

The server SHALL store a Slack app connection with bot token and signing secret sealed through CreateConnection.

#### Scenario: Save connection
- **WHEN** site administrator saves a Slack app connection with CreateConnection.
- **THEN** the server stores bot token and signing secret sealed and never returns plain text.

#### Scenario: Read connection
- **WHEN** site administrator reads the connection.
- **THEN** the server returns sealed references without plain text.

### Requirement: Connection routes and audit

When a site administrator manages a Slack app connection, the server SHALL require the NEW action slack-connection:manage on the installation object and write an audit entry.

#### Scenario: Create and read
- **WHEN** site administrator calls the NEW create and read routes.
- **THEN** the server stores the connection, returns sealed references, and writes an audit entry.

#### Scenario: Rotate with overlap
- **WHEN** site administrator rotates the signing secret through RotateSigningSecret.
- **THEN** the server accepts payloads signed by the old secret for 24 hours and writes an audit entry.

#### Scenario: Overlap end
- **WHEN** a payload signed by the old secret arrives after 24 hours.
- **THEN** the server refuses a payload signed by the old secret with no state change.

#### Scenario: Delete blocked
- **WHEN** site administrator deletes a connection that a sink references through DeleteConnection.
- **THEN** the server refuses the delete with no state change.

### Requirement: Slack user link

The server SHALL map a Slack user ID to one principal subject through LinkSlackUser.

#### Scenario: Link user
- **WHEN** a signed-in person calls the NEW link route with LinkSlackUser.
- **THEN** the server stores the Slack user ID and team ID on the principal.

### Requirement: Post as app

When a notification routes to a namespace chat sink with a connection, the server SHALL send it with chat.postMessage as the app through SendWithConnection and the egresshttp check.

#### Scenario: Notify through app sink
- **WHEN** a notification routes to a chat sink with a Slack app connection.
- **THEN** the server posts the rendered message with chat.postMessage as the app through SendWithConnection.

#### Scenario: Post temporary failure
- **WHEN** chat.postMessage answers 408, 429, or 5xx.
- **THEN** the server retries up to 15 attempts with Retry-After and then records final delivery failure.

#### Scenario: Post permanent failure
- **WHEN** chat.postMessage answers 4xx other than 429 for a bad token or revoked app.
- **THEN** the server records permanent failure with no retry and no token in the error.

### Requirement: Verified actions

When a valid Slack acknowledge or approve action arrives, the server SHALL apply it to the linked object through HandleAction only where the linked principal holds the stored grant for that action.

#### Scenario: Valid approve action
- **WHEN** a valid Slack approve action arrives for a linked object.
- **THEN** the server applies the approve as the linked principal through HandleAction.

#### Scenario: Forged or expired action
- **WHEN** a Slack action arrives with a bad signature or an expired timestamp.
- **THEN** the server refuses with no state change.

#### Scenario: Unknown action
- **WHEN** a Slack action arrives with an unknown action ID.
- **THEN** the server refuses with no state change and writes an audit entry.

#### Scenario: Concurrent replay
- **WHEN** two identical valid actions arrive at the same time with one action ID.
- **THEN** only one action applies and the other refuses as duplicate with no second state change.

#### Scenario: Missing grant
- **WHEN** a validly signed action arrives without a stored grant of the linked principal on the linked object.
- **THEN** the server refuses with no state change.

#### Scenario: Unlinked user
- **WHEN** a validly signed action arrives without a linked principal.
- **THEN** the server refuses with a link-required error and no state change.

#### Scenario: Object mismatch
- **WHEN** a validly signed action names a channel or object outside the connection sink.
- **THEN** the server refuses with no state change.

### Requirement: Buttons only on app sinks

Where a Slack sink has no app connection, the server SHALL render messages with no action buttons.

#### Scenario: Webhook sink render
- **WHEN** a message routes to a webhook-form Slack sink.
- **THEN** the server renders the message with no action buttons.
