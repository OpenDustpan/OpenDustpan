## ADDED Requirements

### Requirement: subscribe

When a person submits an address, the list SHALL send a confirmation mail before adding the address.

#### Scenario: Subscribe with double opt-in
- **WHEN** a person submits an address on the sign-up page
- **THEN** the address stays pending until the person confirms

### Requirement: unsubscribe

When a person follows the unsubscribe link, the list SHALL remove the address within seven days.

#### Scenario: Unsubscribe from a message
- **WHEN** a person follows the unsubscribe link in a list message
- **THEN** the service removes the address and sends no more list mail

### Requirement: no-tracking

While the list sends mail, the list SHALL carry no tracking pixels and no click tracking.

#### Scenario: No tracking in plain messages
- **WHEN** the list sends a release or security notice
- **THEN** the message carries no tracking pixel and no tracked links

### Requirement: post-releases

When the project ships a release or a security notice, the releaser SHALL post that notice to the list.

#### Scenario: Post each notice
- **WHEN** the project ships a release
- **THEN** the releaser posts the notice and records the post link in the checklist
