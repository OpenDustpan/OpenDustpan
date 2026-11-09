## ADDED Requirements

### Requirement: A notification row is scoped by the namespace ID
The server SHALL key each notification row by the namespace ID of its
namespace. The rows are the sinks, the sink secrets, the sink references, the
notifications, the sequences, the cursors, the subscriptions and the
deliveries. A row of the installation SHALL keep the scope `@server`. The server
SHALL NOT key a notification row by a namespace name. The namespace name SHALL
stay a label in the API answers, the webhook payload and the chat message.

#### Scenario: A namespace is made again under the same name
- **GIVEN** namespace `shop` with a sink and a subscription
- **WHEN** `shop` is deleted and a new namespace named `shop` is created
- **THEN** the new namespace lists no sink and no subscription, and the old rows keep the old namespace ID and reach no new namespace

#### Scenario: A live namespace keeps its rows
- **GIVEN** namespace `shop` with a sink and a subscription
- **WHEN** a client lists the sinks and the subscriptions of `shop`
- **THEN** both lists hold the rows of `shop`

#### Scenario: The installation scope stays apart
- **GIVEN** a sink of the scope `@server`
- **WHEN** a client lists the sinks of any namespace
- **THEN** the list holds no sink of `@server`

#### Scenario: The answers show the name
- **GIVEN** namespace `shop` with a sink and a subscription
- **WHEN** a client reads the sink or the subscription, and a webhook sink sends a notification
- **THEN** the `namespace` field and the webhook `scope` text are `shop`, not the namespace ID
