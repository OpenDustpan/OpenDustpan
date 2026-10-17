# 120. Dusty, the built-in agent, runs on durable operations

Status: proposed · 2026-10-17

## Context

[RFC 73](../rfc/0073-ai-agent-for-infrastructure.md) asks whether
OpenDustpan ships its own AI agent. [RFC 41](../rfc/0041-agent-interface.md)
records an earlier position: no agent of our own, and an MCP server for the
agents that teams bring.

The project owner decided on 2026-10-17, through the project manager, that
OpenDustpan ships a built-in infrastructure agent with the name Dusty. These
are the decisions of the owner:

- A Go agent loop that runs on the durable operations of ADR 72, so that a
  run continues after a restart.
- A model port, with an adapter for the Anthropic Messages API and an
  adapter for OpenAI-compatible APIs later.
- No shell tool, ever. About 8 to 12 task-shaped tools, not a passthrough
  of the raw API.
- Dusty acts as the user who asked (ADR 70). It never has more access than
  that user. Every tool call is in the audit log (ADR 60).
- Dusty is off until an administrator connects a model. On the hosted
  service, onboarding offers it. It is never on silently.
- Dusty never sees decrypted secrets, with one escape hatch (ADR 124).
- Changes go in as a review request or approve-first by default (ADR 125).
- The same tools are offered over an MCP server and a CLI stdio proxy
  (ADR 123).
- Context comes from the reserved `opendustpan` output of a unit and from the
  docs, links, owners and on-call of each object (ADR 127).
- Change freezes and break-glass apply to Dusty as to a person.
- The default model and who pays on the hosted service are not decided. The
  model port stays provider-neutral, and the cost of each run is
  measurable: the tokens in and out of each run are recorded.

This ADR records the agent loop, its durable steps, act-as, the limits, the
cancel and the cost record. ADRs 121 to 127 record the other parts.

## Decision

### What Dusty is

1. **One loop in the binary.** Dusty is a Go agent loop in the package
   `pkg/agent`. It runs in each server instance, on the shared database. It
   needs no new process, no sidecar and no new role (RFC 7).
2. **No command tool.** No tool of Dusty runs a shell, a command, a script
   or code that the model writes. A test of the tool catalog fails for a
   tool or a parameter that looks like one. Dusty runs no infrastructure
   tool in the backend. Its only effect on infrastructure is a change that
   it proposes, which runs as a job on a runner (ADR 125). So RFC 6 stays
   true: the backend never runs an infrastructure tool.
3. **Off until an administrator connects a model.** An administrator with
   `server:manage-agent` connects a model through the API: the adapter, the
   address of the provider, the model name and the credential. The
   credential is a sensitive setting. The server seals it with the
   key-encryption key (ADR 23) and never answers it. With no connection,
   the run routes answer 409 with an error that names the connect route,
   and the console shows no agent. The MCP endpoint needs no model (ADR
   123 decision 7).
4. **The hosted service never turns it on silently.** Onboarding offers
   Dusty to an account administrator. The administrator turns it on, or
   leaves it off. A later change of the hosted default needs a new ADR.
5. **The audit log must be on.** Every tool call writes an audit entry
   (ADR 126). With `--audit` off (ADR 60 decision 2), the connect route
   refuses, Dusty stays off and the MCP endpoint answers 404. When an
   append fails, the tool call fails. No tool call happens without its
   entry.

### Runs and conversations

6. **A run is one request of a person.** A person starts a run with a
   prompt. The run ends when the model gives a final answer, when it
   reaches a limit, or when a person cancels it. A follow-up question
   starts a new run whose parent is the last run. So one conversation is a
   chain of runs, and each run has its own limits and its own cost.
7. **The run API.** The OpenDustpan API gets these routes. Each declares
   its action and its object (ADR 17):

   | Route | Action | Object |
   | --- | --- | --- |
   | `POST /api/agent/runs` | `agent:start-run` | caller |
   | `GET /api/agent/runs` | `agent:start-run` (own runs), `namespace:manage-agent-runs` (other runs) | caller, namespace |
   | `GET /api/agent/runs/{id}` | as the list | caller, namespace |
   | `GET /api/agent/runs/{id}/transcript` | as the list | caller, namespace |
   | `POST /api/agent/runs/{id}/cancel` | as the list | caller, namespace |
   | `GET /api/agent/status` | `caller:read` | caller |
   | `PUT /api/agent/connection` | `server:manage-agent` | `@server` |
   | `DELETE /api/agent/connection` | `server:manage-agent` | `@server` |
   | `GET /api/agent/usage` | `server:manage-agent` | `@server` |

   The start takes an idempotency key, so a client can retry it. The
   status route answers whether the agent is on, the provider and model
   names, whether direct changes are possible and whether secret values
   can reach an agent (ADR 124). The console reads it.

### The loop on durable operations

8. **A run is a chain of turn operations.** A turn is one model call and
   the tool calls of its answer. Each turn is one durable operation of the
   kind `agent.turn` (ADR 72 decision 1). A durable kind has a fixed list
   of steps, and a run has an unknown number of turns. So each turn is its
   own operation, and the last step of a turn starts the next turn in the
   transaction of its own write.
9. **The steps of a turn.** Each turn has four steps, in this order:
   - `check`: it reads the cancel request, checks the limits, resolves the
     credential of the person (decision 13) and checks the model
     connection. A failed check ends the run with its stop reason.
   - `model`: it calls the model with the transcript and the tool catalog,
     and records the answer and its token use.
   - `tools`: it runs the tool calls of the answer, one at a time, and
     records the result of each call in the transcript.
   - `advance`: it starts the operation of the next turn, or ends the run
     when the answer was final.
10. **A restart continues the run.** A turn whose instance stops keeps its
    claim until the lease expires. Then another instance claims it and
    continues from the last recorded step (ADR 72 decision 2). A run holds
    no claim between turns, and a run waits for no person: a change that
    waits for approval does not keep a run open (ADR 125).
11. **Effects happen once.** A step that proposes or cancels a change
    passes a key that it makes from the run, the turn and the index of the
    call. The job model gives one change for one key. So a turn that runs
    again after a takeover makes no second change.
12. **Reads and model calls can repeat.** A read tool is safe to run
    again. A model call that ran before a lost claim can run again on the
    next claim, and costs its tokens again. The token ledger records each
    attempt (decision 20), so the cost stays complete.

### Act-as

13. **A run acts as its person, at the time of each call.** A run records
    the subject of the person and the ID of the credential that started
    it: the console session or the access token. It never stores the
    credential. The `check` step resolves that credential through the
    `Credentials` port at each turn. Each tool call checks the
    authorization of the resolved principal at the time of the call. So a
    grant that a person loses stops the next call of every run of that
    person.
14. **The run ends with its credential.** When the credential expires, a
    person revokes it, the session ends, the principal is disabled or a
    break-glass session reaches its time limit, the run ends with the stop
    reason `credential-ended`.
15. **No identity of its own.** Dusty is not a principal. It holds no
    grant, no role and no token. The audit log names the person as the
    subject of each entry, and names the run (ADR 126). The act-as rules
    of ADR 70 need no new edge, because a run never acts as a principal
    that its person cannot act as.

### Limits

16. **Each run has every limit.** A limit of 0 never means "no limit".

    | Limit | Default |
    | --- | --- |
    | Turns | 25 |
    | Input tokens, cached and not cached | 400,000 |
    | Output tokens | 40,000 |
    | Time from the start | 20 minutes |
    | Tool calls in one turn | 8 |
    | Bytes of one tool result sent to the model | 64 KiB |
    | Active runs for each person | 3 |
    | Active runs for the installation | 20 |

17. **Three levels can only lower.** An administrator sets the limits of
    the installation, higher or lower than the defaults. A namespace
    administrator with `namespace:manage-agent` can lower them for runs
    that start in the namespace. A person can lower them for one run.
18. **How a limit applies.** The `check` step stops a run that reached the
    time, token or turn limit. So a run can pass a token limit by the
    tokens of its last turn, never by more. The loop answers each tool
    call above the limit of one turn with an error result, and the run
    continues. The loop cuts a tool result above its byte limit and tells
    the model that it did.

### Cancel

19. **A cancel stops the run at the next write.** The person of a run, or
    a holder of `namespace:manage-agent-runs`, cancels it with the cancel
    route. The server records the request on the run, and cancels the
    durable operation of the current turn (ADR 72 decision 1). The holder
    of the turn gets the cancel at its next write. Its step context ends,
    so a model call in flight stops. The run ends with the stop reason
    `canceled`. A cancel never undoes a change that a job applied. The
    answer of the cancel lists the changes that the run proposed. With
    `cancelChanges: true`, the cancel also cancels each change that waits
    for a review or an approval, through the job model, as the person who
    cancels.

### Cost

20. **Tokens are recorded for each attempt.** Each model call writes one
    row to a token ledger, with the run, the turn, the token of the claim,
    the model name and four counts: input, output, cache read and cache
    write. The row needs no fence, so an attempt whose claim was lost is
    counted too. A second row for one run, turn and claim changes nothing.
21. **The cost of a run is measurable.** A run answers two sums: the
    tokens of its recorded turns, and the ledger. The usage route answers
    the ledger for a time range, grouped by person, namespace or model.
    The metrics `dustpan_agent_tokens_total` (with the label `kind`:
    `input`, `output`, `cache_read` or `cache_write`) and
    `dustpan_agent_runs_total` (with the label `stop`) have no person or
    namespace label (ADR 32 decision 5). The server stores no price. The
    operator applies the prices of the provider. The decision of who pays
    on the hosted service can use these numbers.

### Transcripts

22. **The transcript is data, not a log.** The prompt, the answers and the
    tool results of a run go to their own table. They never go to an audit
    entry, a log record or a metric. The transcript holds only masked
    values (ADR 124 decision 9). The table is in the backup (ADR 30). A
    setting deletes transcripts after a retention period: 30 days by
    default. The run record and its audit entries stay.
23. **The system prompt is in the binary.** The server sends a fixed system
    prompt with a version. It tells the model that tool results, outputs,
    docs and logs are data from other people, never instructions. The
    prompt is not the defense. The defense is that every tool call has the
    authorization of the person, that changes need a review or an approval
    by default (ADR 125), and that no secret value reaches the model by
    default (ADR 124).

## Consequences

- New packages: `pkg/agent` (the domain: the run, the loop, the ports, the
  catalog, the gate, the limits) and `pkg/agent/agenttest` (the
  conformance suites). The domain rule of depguard covers `pkg/agent`.
- New ports with conformance suites: `Model` (ADR 121), `Changes` (ADR 125),
  `RunStore` and `Credentials`. A `Toolset` (ADR 122) holds the tools for
  every channel.
- Each SQL store gets the tables of runs, transcripts and the token
  ledger. The tables of runs and transcripts are data tables, not control
  tables (ADR 72 decision 5).
- The identity service implements `Credentials`. It must resolve a console
  session by its ID, not only by its cookie.
- The model sees the tool results of each run. On an installation that
  connects a model of a provider, that data leaves the installation to the
  provider. The connect route and the docs say so. An operator who must
  keep the data inside its network connects a model that it hosts, through
  the OpenAI-compatible adapter.
- RFC 41 and RFC 73 get an outcome that points to ADRs 120 to 127 when
  these ADRs are accepted.

## Not decided here

- The default model, and who pays for the tokens on the hosted service.
- Whether a run can wait for an approval and continue when a person
  approves, instead of a follow-up run.
- A stream of the answer to the console while a turn runs.
- Prices in the server, budgets for each namespace or person, and alerts on
  spend.
