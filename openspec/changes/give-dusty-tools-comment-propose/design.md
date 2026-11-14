## Context

Dusty acts for one person under the grants of that person. Three tools let Dusty explain and propose, never decide.

## Decisions

1. **Toolset extension.** Add Comment and Propose to the agent.Changes port. The Toolset checks agent:use, then the object action, then writes the agent-tool entry. Agent verdicts fail with ErrAgentDenied.
2. **Draft stays draft.** The propose tool calls the draft route of dusty-proposes-change-order. The draft holds unit references and masked values only. That change owns the draft state and the person confirm. comment_job refuses a job with a change-order origin with job.ErrInvalid.

## Structure

The unexported toolset struct has a constructor that registers only the allowlisted tool names. It refuses a name with shell, exec, command, script, eval, run_code or bash.

## Risks

- Model text in comments can carry injected instructions. Comments are data and give no action.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The Toolset validates arguments, checks agent:use and the object action. | Call comment_job with overlong text, an unknown parameter and no agent:use. Each call fails with no write. |
| service | The service masks secret text, uses idempotency keys and fails closed when the audit write fails. | Replay a propose call with secret text. One draft exists and it holds masked values. |
| HTTP handler | The handler refuses an agent confirm and a call while MCP is off. | Call approve with an agent principal. It is refused and audited. |
