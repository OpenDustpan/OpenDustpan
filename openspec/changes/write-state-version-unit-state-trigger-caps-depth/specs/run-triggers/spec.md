## ADDED Requirements

### Requirement: Per-unit trigger cap

When a save adds a trigger above 20 triggers on one unit, the server SHALL refuse the save with error code trigger.invalid.

#### Scenario: Save with 20 triggers
- **WHEN** a save adds a 21st trigger to a unit
- **THEN** the server answers 422 with error code trigger.invalid and the cap number 20

#### Scenario: Both caps bind
- **WHEN** one save exceeds the unit cap and the source cap at once
- **THEN** the server answers 422 with error code trigger.invalid and names both cap numbers 20 and 50

#### Scenario: PUT without new row
- **WHEN** a PUT keeps the same unit and the same source
- **THEN** the save succeeds without counting a new trigger

#### Scenario: Delete drops count
- **WHEN** a trigger of the unit is deleted
- **THEN** a later save with the same counts succeeds

### Requirement: Per-source follower cap

When a save adds a follower above 50 followers of one source, the server SHALL refuse the save with error code trigger.invalid.

#### Scenario: Save with 50 followers
- **WHEN** a save adds a 51st follower to one source
- **THEN** the server answers 422 with error code trigger.invalid and the cap number 50

#### Scenario: PUT changes source
- **WHEN** a PUT moves an existing trigger to a new source
- **THEN** the server counts one follower on the new source and drops one from the old source

#### Scenario: Rename keeps stable ID
- **WHEN** a unit or its source is renamed
- **THEN** the follower counts follow the stable unit ID and need no recount

### Requirement: Caps settings

Where caps are configured, the server SHALL hold the per-unit cap and the per-source cap in the installation settings with effective values shown on read.

#### Scenario: Defaults shown
- **WHEN** no administrator changed the caps
- **THEN** the settings read shows 20 for the per-unit cap and 50 for the per-source cap

#### Scenario: Admin change
- **WHEN** an installation administrator changes a cap
- **THEN** the next save uses the new value

#### Scenario: Non-admin change refused
- **WHEN** a principal without installation administration writes a cap
- **THEN** the server refuses the settings write with 403

#### Scenario: Invalid cap refused
- **WHEN** a cap value is zero or negative
- **THEN** the server refuses the settings write with 422

### Requirement: Caps read at each save

When an administrator changes a cap, the server SHALL use the new value at the next save.

#### Scenario: Raised cap
- **WHEN** an administrator raises a cap and the same save runs again
- **THEN** the next save with the same counts succeeds

#### Scenario: Lowered cap keeps rows
- **WHEN** an administrator lowers a cap below the current count
- **THEN** existing triggers stay and only a save that adds above the new cap is refused

### Requirement: Serialized namespace writes

While two trigger saves of one namespace run at the same time, the server SHALL serialize the writes so each save checks caps and cycle on current rows.

#### Scenario: Concurrent closing saves
- **WHEN** two saves of one namespace that together close a loop run at the same time
- **THEN** one save succeeds and the other answers 422 with the loop path

#### Scenario: Store outage during save
- **WHEN** the store fails inside the serialized write
- **THEN** the save fails and writes no trigger row

### Requirement: Cycle refused at save

When a trigger save makes a follows cycle, the server SHALL refuse the save with error code trigger.cycle.

#### Scenario: Closing save
- **WHEN** a save closes a follows loop
- **THEN** the server answers 422 with error code trigger.cycle and names the units of the loop in order

#### Scenario: Self follow
- **WHEN** a unit follows itself
- **THEN** the server answers 422 with error code trigger.cycle and names the unit twice

#### Scenario: Cross-tool loop
- **WHEN** a loop runs through a stack and a workspace
- **THEN** the server answers 422 with error code trigger.cycle and names each unit with its tool

#### Scenario: Disabled trigger skipped
- **WHEN** the only closing edge belongs to a disabled trigger
- **THEN** the save succeeds

### Requirement: Deep chain stops

While a trigger chain is deeper than 10, the server SHALL refuse each further trigger start.

#### Scenario: Depth above 10
- **WHEN** a trigger chain goes deeper than 10
- **THEN** the dispatcher refuses the start, writes one audit entry, writes one log line, and names the loop

#### Scenario: Restore-made loop
- **WHEN** a loop from a restore or another version row runs with a missing origin that starts at zero
- **THEN** the chain still stops at depth 10 with one audit entry that names the units seen from the source chain

#### Scenario: Manual fire resets depth
- **WHEN** a person fires a trigger by hand inside a deep chain
- **THEN** the fired start runs at depth zero

### Requirement: Depth refusal fail-closed

If the audit write for a refused trigger start fails, then the server SHALL keep the start refused.

#### Scenario: Audit write fails
- **WHEN** the audit write for a refused trigger start fails
- **THEN** the start stays refused, the log line is still written, and the server reports the store error

#### Scenario: Repeat apply
- **WHEN** the source applies again after a refused deep start, up to 5 times in a row for the same unit
- **THEN** each repeat writes at most 1 audit entry per refused start and starts 0 plans
