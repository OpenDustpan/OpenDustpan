## Context

The first part stores the bit. This part reads it through the existing service method Service.StateClientEncrypted (pkg/workspace/state_encrypted.go:14) and shows it in the API.

## Decisions

1. **Read the stored bit.** The handler calls the service method that reads the stored bit. It does not call IsClientEncrypted (pkg/workspace/tofu_state.go:143) on the state again.

## Structure

The handler gets the value from the service and has no own rule for it, so the API and the store cannot disagree.

## Risks

- CI runs the e2e step and the agent does not, so a failure shows first in CI.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Read latency of the state version list | 0 | extra store queries per state version | GET /api/v2/workspaces/{}/state-versions; the bit comes from the stored column |
