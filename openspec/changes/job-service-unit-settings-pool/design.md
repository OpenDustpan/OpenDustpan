## Context

The job planner reads pool, code location, profile reference and steps from the unit settings with namespace defaults as fallback. A location in the start request is an explicit override. Trigger starts carry trigger ID, last saver and caller for the output-read check. The planner snapshots the resolved inputs at start; each output read uses the grant state at that read.

## Decisions

1. **Settings resolution with explicit override and start snapshot.** Planner resolution order: request override then unit settings then namespace unit defaults then refusal with the docs command. Override of pool needs namespace:manage-runners on the namespace; override of location or steps needs unit:define-steps on the unit. The planner stores the resolved snapshot on the job and a later settings edit changes only later starts and later reads use current grants.
2. **Trigger saver check at each read.** Output-read handler calls trigger.OutputReadCheck.Allow for each job with Trigger set after check 1 passes, with the manual-fire caller from StartedBy resolved through the same SaverResolver. Any resolver or store error denies. Check 1 passes when a sharing rule names the unit, its collection or its namespace of the job. The profile reference stays a stable ID in settings; provider open and secret resolve stay in the profile item.
3. **Stable profile reference and missing pool behavior.** Unit settings hold the profile stable ID, never the profile name. The start snapshot holds the ID and the revision in use. A rename keeps the ID so later starts still resolve. A removed profile makes the next start fail closed with 422; a queued job whose pool is deleted stays queued and a run with no runner stays queued per the runner item.

## Structure

Unexported settingsResolver built only by NewSettingsResolver with store, defaults and profile reference reader; Start and Allow take its value and never read raw request fields directly.

## Risks

- Pulumi default steps stay undefined until the runner recorder step design lands; a Pulumi start with no stored steps is refused with the docs command.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Handler checks authorization for pool override, location override, manual lock, and sharing rule plus saver check at each read. | Start with pool override without namespace:manage-runners gets 403; read after saver loses unit:read-outputs on source gets 403. |
| service | Service resolves settings only from stored unit settings and defaults and refuses a trigger start with no stored location. | Trigger start with empty request location and no stored location is refused; forged Trigger with unknown saver denies reads. |
| port adapter | Store adapter resolves the saver without rebuilding the administrator flag and fails closed on missing, deleted, disabled principal or port error. | Saver deleted or disabled between start and read denies the next read. |
