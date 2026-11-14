## Context

Write-back is the executor that commits API config edits to the git host. It runs in the server on the durable runner, uses the Forge port, and the team CI applies the change.

## Decisions

1. **Base-commit write-back with job trailer.** The write-back deliver phase reads each edited file at the base commit, applies only the keys of the edits, creates a missing file, checks the base is still the branch head at commit time, writes one commit with the trailer Dustpan-Job holding the job ID, and refuses edits outside the editable config files of the tool StepPlanner.

## Structure

An unexported writeBackApplier built only by NewWriteBackApplier, which holds the base commit, the checked edits, and the job trailer. No other constructor can make it.

## Risks

- A commit with a wrong base overwrites a team push.
- A forge secret reaches the job log.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service checks each edit with job ValidateEdits and the tool StepPlanner Editable before any forge call. | Send an edit with a path outside the config directory and confirm refusal without a forge call. |
| port adapter | Forge adapter allows only the configured repository and a clean relative path inside the code location. | Request a write to an absolute path and confirm containment refusal. |
| HTTP handler | HTTP handler checks unit write grant on the unit object and writes the denial to the audit log on refusal. | Call the edit route without the grant and confirm 403 and an audit entry. |
