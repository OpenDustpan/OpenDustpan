# 386. The server makes the job token when the launcher asks for step credentials

Status: accepted · 2026-11-17. Decided by the project owner (owner, 2026-11-17). The owner chose the timing of ADR 133 decision 12 over the timing of ADR 100 decision 20

Changes:

- Changes ADR 100 decision 20: the server makes the job token at the launcher request, not at the claim. The principal, the unit limit and the job-ID claim stay.
- Extends ADR 133 decisions 12 and 13: the server answers the job token of a step at the launcher request. The claim answer holds no job token.

## Context

ADR 100 decision 20 says that the server makes a job token at the claim of a runner phase. ADR 133 decision 12 says that the launcher asks for the credentials of each step through its session. ADR 133 decision 13 says that the runner never holds a credential of a step.

These timings cannot both hold. A token that the server makes at the claim must travel in the claim answer to the runner. That gives the runner a credential of a step. Decision 13 forbids this.

The project owner decided on 17 November 2026. The server makes the job token when the launcher asks for the credentials of a step through its job session (ADR 133 decision 12). The expiry is the earlier of the end of the claim and the end of the step timeout plus grace. This supersedes the timing of ADR 100 decision 20. The owner asked for an ADR.

## Decision

1. **The server makes the job token at the launcher request (owner, 2026-11-17).** The launcher asks for step credentials through its job session. The server makes the job token at that request. The claim of a runner phase makes no job token. The claim answer holds none.
2. **The expiry is the earlier of two times (owner, 2026-11-17).** The first time is the end of the claim. The second time is the end of the step timeout plus the grace period. The token expires at the earlier time.
3. **The runner never holds a credential of a step.** ADR 133 decision 13 stays. The claim answer, the handoff token and the runner state hold no job token.
4. **The rest of ADR 100 decision 20 stays.** The token belongs to the principal that started the job. It acts only on the unit of the job. It carries the job-ID claim. It works only while the claim is live. ADR 213 and ADR 304 change the principal and the reach of the token. They do not change the time of its making.
5. **Each request leaves a record.** The server writes one audit entry for each launcher request. The entry names the job, the step and the expiry.

## Consequences

- The change `add-job-session-paths-runner-token-issuer` names an issuer that runs in the claim. That plan no longer holds. The issue of the token moves to the credentials route of the job session. The existing `jobtoken.Service.Issue` stays the one way to make a token.
- A leaked job token stops working after the step or the claim, as ADR 133 decision 12 says.
- ADR 100 and ADR 133 get the reverse items.
