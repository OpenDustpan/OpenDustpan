## Context

An agent step uses the same REST API as a person. The audit log stores one hash chain for each namespace and one for the installation. `audit-entry-has-no-agent` makes the agent block of ADR 380. This change writes the delegation ID into its seventh field and keeps the reason in the existing `Reason` field of `audit.Entry`. The chain covers both without a version.

Four points have no ADR or RFC line that settles them. They are the open questions of the proposal.

## Decisions

1. **The delegation in the block, the reason in `Entry.Reason`** (ADR 380 decision 1). `audit.Entry` gets no new field. An allowed step writes its entry in the same transaction as the step. A denied step writes a denial entry in its own transaction.
2. **No version, no old path** (ADR 380 decision 3). The block keeps the text `agent/1`. The change writes no code to verify an entry of an older shape.
3. **A refused reason writes no entry.** `audit.ValidateReason` refuses it with `audit.reason-invalid` and the answer repeats nothing of the input. The rules for the reason are open question 1.

## Structure

The delegated entry is built only by the constructor of the block. A delegated entry with no delegation ID cannot be built.

## Risks

- Transcript text stays out of the entry. It holds the reason and references only.
- A forged delegation ID may fill the log. Open question 3 asks what a denial entry holds for it.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service (NEW `pkg/dustpan/agent_audit_test.go`) | The service refuses a delegated call with no delegation, an ended delegation or an invalid reason. It writes a denial entry for each refusal except `audit.reason-invalid`. | Send a call with an empty, a forged, a revoked and a replayed delegation ID directly to the service. Each is refused. An invalid reason writes no entry. |
| port adapter (`pkg/audit/agent_block_test.go`: `TestNewOriginRefusesHostileFields` at :228 and :336; `pkg/audit/audittest/chain.go` for the sqlite and postgres stores) | The adapter binds the entry to the resolved delegation, hashes the block and the reason, and takes the sequence number from the database in the same transaction. | Write two entries with the same body and different delegation IDs and check that the hashes differ. Append from two instances at the same time and check for no gap and no duplicate. Strip `delegationID` from a stored entry and check that the chain check fails. |
| HTTP handler (`internal/api/audit/audit_test.go`) | The handler requires authentication, returns only the scopes that the caller may read, and answers 503 with `Retry-After: 5` during a database gap. | Read the entries of another namespace with a namespace token and check `audit.entry-not-found`. Tamper one agent entry and check that `GET /api/audit/check` names its sequence number. Block the database and check for 503. |
