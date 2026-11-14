## Context

Slice 1 holds the connection store, the installation app registration and map, and Check. This change adds the hint intake, the read adapter methods, and the scoped token, without changing stored shapes from slice 1. Commit waits for the RFC 89 Q4 spike.

## Decisions

1. **Hint parsing stays in the port.** Parse GitHub events in pkg/forge/github behind the Forge port ParseEvent, with an unexported verifier that checks X-Hub-Signature-256 against the two stored secrets from one registration snapshot with constant-time compare per secret. Either match accepts. An unexported dedupe store holds delivery IDs for 24 hours as a best-effort bound only.
2. **Intake answers 204 and drops on replicas.** Serve POST /webhooks/{hook ID} outside /api/dustpan/v1 with no auth actions, always answer 204 after record of accept or reject, and increment git_host_event_rejected_total on reject. A replica drops an event for a namespace it does not serve. A repeated delivery causes no second effect when the state of the effect shows the hint already applied.
3. **Adapter methods and scoped token.** Implement Head, ReadFile, Compare, FindCommit, OpenReview, ReadReview and CloseReview in the GitHub adapter behind capability checks that return ErrUnsupported when the host or auth kind cannot do the call. Hold Commit behind its capability until the RFC 89 Q4 spike fixes author and signature. Add RepoToken that mints a one-repository contents-read token of at most 1 hour with Scoped set.
4. **Duplicate hints cause no second effect.** Make hints idempotent on the hint key of hook ID plus delivery ID plus event effect key of repository, event type and head sha, so a duplicate that reaches another member or arrives after a restart causes no second effect. The dedupe store is a best-effort bound, and the effect check is the guard that stays correct across members and restarts. When the effect state is unavailable the server drops without effect and counts a reject.

## Structure

Unexported type webhookIntake with constructor newWebhookIntake that takes the Forge port, the registration map, the dedupe store and the meter, and checks non-nil and secret length. The constructor is the only way to make a value, so missing checks cannot compile into the route.

## Risks

- Replayed deliveries start duplicate hints.
- Unscoped token leak to runners.
- Hook hostname tied to one member breaks failover.
- Log or error exposes secret or token.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| signature and dedupe | Boundary: verify X-Hub-Signature-256 with constant-time compare against each of two stored secrets from one snapshot; reject short secrets, unknown hook ID, bad body and replayed effect. | Send forged body, wrong secret, replayed delivery ID and short secret directly to the adapter and check reject and metric increment. |
| hook mapping | Containment: map hook ID to app registration through the installation map only; never take a namespace or connection ID from the request; look up installation-to-connection only from stored data. | Send valid signature with guessed hook ID and with path traversal in hook ID and check 204 with reject and no lookup leak. |
| fail-closed intake | Effect limit: answer 204 for every request with no body echo; bound body size and parse time; drop on replica without fetch. | Send 10 MB body, a bad-signature body above the bound, and 10000 rapid replays and check bound, 204 and stable memory. |
| scoped token use | Point of use: enforce repo-token and git-access capabilities on the runner git path and refuse any token with Scoped not set. | Call the runner git credential path with an unscoped token and check refusal even when the token is otherwise valid. |
