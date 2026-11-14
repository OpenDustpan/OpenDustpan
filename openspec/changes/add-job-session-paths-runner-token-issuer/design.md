## Context

The tool step needs a credential that acts as the starter on one unit and stops with the claim. The issuer sits behind job.Claimer.

## Decisions

1. **Issuer behind the claim.** The issuer stores the token hash, job ID, stable unit ID and expiry in the claim transaction. A duplicate make for one claim returns the same token record.
2. **JobID only on job tokens.** Principal.JobID is set only when the credential is CredentialJobToken. A runner key or session key never carries it.

## Structure

Unexported struct jobToken with fields jobID, unitID and expiry. Only its constructor makes a value, and it refuses an empty unit ID or a past expiry.

## Risks

- A token that outlives its claim lets a step write after takeover.
- A token valid on more than one unit breaks the one-unit limit.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The constructor binds one job ID, one unit ID and the expiry. | Use a token on another unit and get 403. |
| port adapter | The store checks claim liveness and expiry at each use. Unknown, malformed or ended tokens fail closed. | Replay an ended token, a malformed token and an unknown token and get 401 with no read. |
| HTTP handler | A job token is accepted only on the unit path of its job. | Send a runner key on a tool path and get 403. |
