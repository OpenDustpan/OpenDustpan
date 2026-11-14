## Context

OpenTofu `cloud` block drives runs through the TFE-compatible API. ADR 254 ships speculative plans on runners. The apply path stays open in RFC 30 and ADR 254 decision 10, so the roadmap step stays partly open.

## Decisions

1. **Keep slice 1 unchanged.** Keep slice 1 unchanged: configuration version upload as a file set with ADR 199 checks, plan job with source kind cli and fixed arguments only from the allowlist refresh, refresh-only, target-addrs, replace-addrs and is-destroy, secret run variables, signed log URL, poll answers without 404, and refusal of every apply route. The speculative run links to the plan job started by the job service.
2. **Leave the apply for the RFC 30 ADR.** Leave the RFC 30 apply mapping out of the spec: is-confirmable and actions/apply, the stored plan, auto-approve, the saved cloud plan of -out, and the remote backend stay open questions for the RFC 30 ADR. The remote apply of the roadmap step stays uncovered until that ADR. Concurrent plans on one workspace run as separate jobs. A rotation of the log URL signing key ends live URLs and the CLI mints a new URL.

## Structure

Unexported type `remotePlan` with constructor `newRemotePlan` that checks workspace execution mode, the speculative flag, fixed arguments from the allowlist and pool before it makes a job spec, so no caller builds a remote plan without the checks.

## Risks

- A secret run variable leaks into a log, an audit entry or an answer.
- Masking covers only the exact value of a run variable; a value echoed in a plan error or derived from a secret is not masked.
- The CLI polls a route that answers 404 and stops with an unclear error.
- A signed log URL replays for another job, another principal, or after expiry.
- A rotation of the log URL signing key ends live URLs before the CLI mints a new one.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| TFE adapter | Adapter allows only speculative uploads on agent workspaces with fixed plan arguments from the allowlist, checks the archive, and refuses speculative=false and every apply route with a JSON:API error. | Send `speculative=false`, unknown plan arguments, a bad archive, and `actions/apply` directly to the adapter and check each refusal names local execution. |
| job service | Service checks `unit:run-plan` on the workspace unit before it starts the plan job and evaluates the gates of change propose; a refusal ends the run as errored and names the decision record. | Start a plan with a principal without the action and with a gate refusal, and check it fails closed with no job and the log names the decision record. |
| HTTP handler | Handler mints the log URL bound to one job, one phase and one principal with a short expiry, checks `job:read-logs` at mint and at read, and masks exact run variable values in streamed output. | Replay a log URL for another job or another principal and after expiry, and check each read fails; send a secret variable and check the log holds only the mask. |
