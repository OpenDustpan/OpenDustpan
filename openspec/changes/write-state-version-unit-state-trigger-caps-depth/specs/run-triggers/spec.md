## ADDED Requirements

### Requirement: Per-unit trigger cap

When a save adds a trigger above 20 triggers on one unit, the server SHALL refuse the save with error code trigger.invalid.

#### Scenario: Save with 20 triggers
- **WHEN** a save adds a 21st trigger to a unit
- **THEN** the server answers 422 with error code trigger.invalid and the cap number 20

#### Scenario: PUT without new row
- **WHEN** a PUT keeps the same unit and the same source
- **THEN** the save succeeds without counting a new trigger

### Requirement: Per-source follower cap

When a save adds a follower above 50 followers of one source, the server SHALL refuse the save with error code trigger.invalid.

#### Scenario: Save with 50 followers
- **WHEN** a save adds a 51st follower to one source
- **THEN** the server answers 422 with error code trigger.invalid and the cap number 50

#### Scenario: PUT changes source
- **WHEN** a PUT moves a trigger to a new source
- **THEN** the server counts one follower on the new source and drops one from the old source

### Requirement: Caps read at each save

When an administrator changes a cap, the server SHALL use the new value at the next save.

#### Scenario: Raised cap
- **WHEN** an administrator raises a cap
- **THEN** the next save with the same counts succeeds

#### Scenario: Lowered cap keeps rows
- **WHEN** an administrator lowers a cap below the current count
- **THEN** existing triggers stay and only a save that adds above the new cap is refused

### Requirement: Serialized namespace writes

While two trigger saves of one namespace run at the same time, the server SHALL serialize the writes.

#### Scenario: Concurrent closing saves
- **WHEN** two saves of one namespace that together close a loop run at the same time
- **THEN** one save succeeds and the other answers 422 with the loop path

### Requirement: Cycle refused at save

When a trigger save makes a follows cycle, the server SHALL refuse the save with error code trigger.cycle.

#### Scenario: Closing save
- **WHEN** a save closes a follows loop
- **THEN** the server answers 422 with error code trigger.cycle and names the units of the loop in order

#### Scenario: Disabled trigger skipped
- **WHEN** the only closing edge belongs to a disabled trigger
- **THEN** the save succeeds

### Requirement: Deep chain stops

While a trigger chain is deeper than 10, the server SHALL refuse each further trigger start.

#### Scenario: Depth above 10
- **WHEN** a trigger chain goes deeper than 10
- **THEN** the dispatcher refuses the start, writes one audit entry and one log line, and names the loop

#### Scenario: Manual fire resets depth
- **WHEN** a person fires a trigger by hand inside a deep chain
- **THEN** the fired start runs at depth zero

### Requirement: Depth refusal fail-closed

If the audit write for a refused trigger start fails, then the server SHALL keep the start refused.

#### Scenario: Audit write fails
- **WHEN** the audit write for a refused start fails
- **THEN** the start stays refused and the log line is still written
