# 121. The model port is provider-neutral and has no stream

Status: accepted · 2026-10-17 · decisions 1, 2, 4, 8 and 9, and one item of "Not decided here", superseded in part by ADR 129

Changed by:

- ADR 129 changes decisions 1, 2, 4, 8 and 9: the model port, adapters,
  connection and the prompt caching item.
- ADR 357 changes decision 6: the port gets a second method that streams.

## Context

ADR 120 records the built-in agent, Dusty. The project owner decided that
the agent calls its model through a port, with an adapter for the Anthropic
Messages API and an adapter for OpenAI-compatible APIs later. The default
model and who pays on the hosted service are not decided. So the port must
name no provider, no model and no price, and each call must report its
tokens.

A turn of the agent is a durable step (ADR 120 decision 9). A step records
its result when it ends, so a part of an answer has no use to the loop.

## Decision

1. **One port, `agent.Model`.** It has two methods:
   - `Complete(ctx, ModelRequest) (ModelResponse, error)`: one request and
     one whole answer.
   - `Info() ModelInfo`: the names of the adapter and the model, for the
     status route and the audit log. It holds no credential.
2. **The request.** A request holds a system prompt, the messages of the
   run, the tool catalog (ADR 122) and a limit of output tokens. A message
   has a role (`user`, `assistant` or `tool`) and parts. A part is text, a
   tool call (an ID, a tool name and JSON arguments) or a tool result (the
   ID of the call, JSON content and an error flag). An adapter makes the
   JSON Schema of each tool from its parameters, and maps the roles and
   parts to the form of its provider.
3. **The answer.** An answer holds one assistant message, a stop kind
   (`end`, `tool-use` or `max-tokens`), the token use and the model name
   that the provider reported. The token use has four counts: input,
   output, cache read and cache write. A provider that reports no cache
   gives 0 for the cache counts. A refusal is an error, not a stop kind
   (decision 4).
4. **Errors.** An adapter translates each failure of its provider into one
   sentinel of the package:

   | Error | When | The loop |
   | --- | --- | --- |
   | `ErrModelUnavailable` | no answer, a server error, an overload | retries the turn with a backoff |
   | `ErrModelRateLimited` | a rate or quota limit, with the wait in `RateLimitError` | retries the turn after the wait |
   | `ErrModelAuth` | the provider refused the credential | stops the run, `model-error` |
   | `ErrModelRequest` | the provider refused the request, for example an unknown model | stops the run, `model-error` |
   | `ErrContextTooLong` | the request is longer than the context window | stops the run, `model-error` |
   | `ErrModelRefused` | the model refused to answer | stops the run and shows the refusal |
   | `ErrInvalid` | no message, or an output limit of 0 or less | stops the run |

   When the context ends, `Complete` returns the error of the context. An
   adapter never returns the error text of its provider, because a
   provider can repeat a part of the request in its error. A part of the
   request can hold a tool result.
5. **No retry in an adapter.** An adapter makes one call. The loop retries
   the turn in a new durable attempt, so the retry counts against the
   attempt limit of the operation (ADR 72) and the token ledger records it
   (ADR 120 decision 20).
6. **No stream.** The port has no stream. The console shows the progress
   of a run by turn: the tool calls and the answer of each turn when the
   turn records them. A stream can come later as a second method, with no
   change to `Complete`.
7. **No context management in the adapter.** An adapter sends the request
   as it gets it. The loop keeps a request short with the byte limit of a
   tool result (ADR 120 decision 16). A run that grows past the context
   window stops with `ErrContextTooLong`. A summary of older turns can
   come later, in the loop, not in an adapter.
8. **Adapters later, with the standard library.** Two adapters come in a
   later change, each in its own package under `pkg/agent/model/`:
   - `anthropic`: the Anthropic Messages API.
   - `openaicompat`: the chat completions API that OpenAI defined and that
     many providers and self-hosted model servers also serve.

   Each adapter uses `net/http` and `encoding/json` only. This ADR adds no
   provider SDK. An adapter that needs one asks the project owner first.
   Each adapter sends its requests through the egress proxy when one is
   set (RFC 16).
9. **One connection for each installation.** An administrator connects one
   model for the installation (ADR 120 decision 3). The connection holds
   the adapter, the base address, the model name, the credential and an
   optional output token limit for each call. A connection for each
   namespace or account can come later, with no change to the port.
10. **A conformance suite.** `agenttest.RunModel` runs each adapter against
    a fake server of its provider that plays a list of scenarios: a text
    answer, a tool call, a rate limit with a wait of 7 seconds, an
    unavailable provider, a refused credential, a refused request, a
    request that is too long, a refusal and no answer. The suite checks the
    mapping of decisions 3 and 4. It also checks that no error text holds a
    canary string from the request. A test in the suite package runs the
    suite against a fake model, so the cases run on each change.

## Consequences

- The loop, the limits and the cost record work the same for every
  provider. The tokens of a run are comparable across providers only as
  counts, because each provider counts tokens in its own way.
- A provider whose API has a feature that the port does not name, for
  example a cache control, can use it inside its adapter only. A feature
  that the loop must control needs a change of the port and of the suite.
- The fake server of each adapter test must follow the real provider. A
  test against the real provider, with a credential from the environment,
  runs outside `make check`.

## Not decided here

- The default model and its provider on the hosted service.
- Prompt caching across runs, and whether the loop asks for it.
- A model connection for each namespace or account.
