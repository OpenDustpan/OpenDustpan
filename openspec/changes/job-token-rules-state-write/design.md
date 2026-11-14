## Context

State-write adapters pass no job claim, so the gate service cannot apply the job-token rule of ADR 224 decision 16. A small Claimer source builds a JobClaim of job, phase, PassedApply, and units from the verified job token, and each state.write call passes it to Evaluate, including the TFE-compatible unlocked-write path under ADR 335 decision 3. A deny gate on cancel or force-unlock under ADR 335 decision 1 keeps its effect over the plan-lock exception.

## Decisions

1. **Resolve JobClaim at the boundary and evaluate with it.** Add a Claimer port that resolves job, phase, PassedApply flag, and units from the verified job token once per write request, and pass the resulting JobClaim into Evaluate at each state.write call for the Pulumi and TFE-compatible paths.

## Structure

Unexported type gate.jobClaimResolver with constructor newJobClaimResolver(claimer) that returns only a validated job.JobClaim; no direct struct literal outside the constructor.

## Risks

- A job token with no resolvable job fails closed and its write is refused until the claim source resolves; the job itself stays and a later write after the phase passes change.apply is evaluated again.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Adapter accepts only a JobClaim built by the server Claimer from a verified job token and resolves it fresh for each write request. | Send a state write with a forged job ID and phase and show the write fails closed with identity.ErrDenied. |
| service | Service refuses any write other than the lock or lease of the plan when PassedApply is false or when the unit is outside the units of the job. | Remove the adapter check and show the service still refuses the out-of-phase and cross-unit writes. |
| HTTP handler | Handler declares state.write authorization and returns the denial with the one log check command. | Call each tool write route with an out-of-phase job token and check the same refusal. |
