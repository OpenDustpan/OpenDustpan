## Context

An installation names an external Cedar service as its gate engine. The adapter translates gate.Engine calls to service calls. Protocol faults give gate.ErrUnavailable and fail closed. Cedar diagnostics passed through give gate.ErrRuleFailed in enforce mode and a record entry in evaluate mode. Request and answer format, save fallback during outage, access-limit split, and CI image wait for an RFC and an owner decision; no code lands before those decisions.

## Decisions

1. **Service adapter behind gate.Engine.** Add an unexported externalEngine in pkg/gate with a constructor that takes endpoint, TLS CA, credential, policy-set version, and timeout, and exposes only gate.Engine methods.
2. **Strict answer check and fail closed.** Verify TLS with configured CA, send credential, follow no redirect, bound request timeout and answer size, echo request ID and policy-set version, reject unknown or extra policy IDs, pin DNS at dial and refuse private and link-local addresses, and map each protocol fault to gate.ErrUnavailable with no retry of a bad answer.
3. **Startup and outage behavior.** Start even when the service is unreachable at boot and mark the engine unhealthy; calls that need a gate fail closed per mode. Keep the proposed save refusal and access-limit handling behind the open questions until the owner decides.

## Structure

Unexported type externalEngine in pkg/gate with constructor NewExternalEngine that checks endpoint, CA, credential, and timeout; only the constructor makes a value.

## Risks

- Request and answer format is still open and needs an RFC and an owner decision before code.
- Save fallback during an outage is still open and needs an owner decision before code.
- Access-limit split during an outage is still open and needs an owner decision before code.
- CI image for the Rust validator is still open.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| transport | Allowlist endpoint URL as https with no redirect follow, TLS with configured CA, credential header, 5 s timeout, 1 MB answer limit, DNS pinned at dial with refusal of private, loopback, and link-local addresses. | Send wrong CA, missing credential, redirect to a second host, oversize answer, slow service, and DNS that rebinds to an internal address; each call gives gate.ErrUnavailable. |
| protocol | Echo request ID and policy-set version with gate ID and gate version, reject unknown and extra policy IDs, pass Cedar diagnostics intact, enforce-mode stops and evaluate-mode records. | Return wrong request ID, wrong version, unknown policy ID, extra policy ID, and a Cedar diagnostic; faults give gate.ErrUnavailable and the diagnostic gives gate.ErrRuleFailed in enforce mode and a record entry in evaluate mode. |
| save | Save path runs local parse and type check first, then service check operation; the refusal and record behavior follows the owner decision on the save-fallback open question. | Stop the test service at save; the save follows the decided fallback and the record names the miss. |
