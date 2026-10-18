# 129. The operator sets every field of the model connection

Status: accepted · 2026-10-18 · supersedes parts of ADRs 120 and 121 (see "What this ADR replaces")

## Context

ADR 120 decision 3 lets an administrator connect one model for the
installation: an adapter, a base address, a model name and a credential.
ADR 121 gives the agent a provider-neutral model port, `agent.Model`, and
plans two adapters. ADR 121 also leaves open prompt caching and how a
connection names the details of each provider.

The project owner decided on 2026-10-18, through the project manager:

- No default model yet. No benchmark shows yet that a small model is fit
  for the work of Dusty.
- For now, the operator sets every field: the base address, the wire
  format, the model name, the authentication, extra headers, and the
  fields of each provider for tools, a stream and prompt caching.
- The authentication names a header and a secret reference. The
  configuration never holds a secret value.
- Every persona is first class: a homelab with a local model server or a
  subscription plan, a startup with a provider account, and an enterprise
  with a gateway.
- The connection supports the model provider of OpenCode fully: a
  header with an identifier that stays the same for one conversation, and a
  user agent that the operator sets, with no identity of a person.

The project manager also reported a result of the agent benchmark: models
repeated tokens that a person pasted into a prompt.

The research note
[`docs/research/model-providers.md`](../research/model-providers.md)
records the request rules of 16 providers, gateways and model servers. It
marks each claim as verified, with its source, or as unverified. These
rules from the note shaped the design:

- Most model servers and gateways serve the OpenAI Chat Completions
  format. Ollama, vLLM, llama.cpp, LM Studio, LiteLLM, OpenRouter, Groq,
  Together, DeepInfra, Mistral, Azure OpenAI (v1 path), Vertex AI and
  Bedrock all have a Chat Completions endpoint.
- Anthropic serves the Anthropic Messages format. OpenCode Go, Bedrock,
  vLLM, llama.cpp, LM Studio and LiteLLM also serve it.
- The output token field differs. OpenAI deprecates `max_tokens` for
  `max_completion_tokens`. Ollama and LM Studio document only
  `max_tokens`. Anthropic requires `max_tokens`.
- `tool_choice` support differs. Ollama does not support it. llama.cpp
  accepts only three strings. The newest Anthropic models refuse a forced
  tool. So Dusty never forces a tool.
- vLLM gives tool calls only when its operator starts it with
  `--enable-auto-tool-choice` and a tool call parser. A model can also
  answer with no tool call at all. So a probe at connect time must check
  tool calls.
- Prompt caching is automatic at OpenAI, Gemini, vLLM and llama.cpp, and
  explicit (`cache_control`) at Anthropic and for Anthropic models through
  OpenRouter and Bedrock. OpenAI, DeepInfra, Together and Mistral route
  the cache with `prompt_cache_key`. OpenRouter routes requests of one
  conversation to one provider by `session_id` or the header
  `x-session-id`.
- OpenCode Go answers 400 to a request with no `x-opencode-session`
  header. Its docs ask for a stable identifier for each conversation, for
  routing and prompt caching. They also ask a client to send its own user
  agent. The OpenCode client sends the same identifier also in
  `prompt_cache_key` and in `x-session-affinity`.
- OpenAI, Anthropic and OpenCode Go each name a field that identifies an
  end user (`safety_identifier`, `metadata.user_id`). None requires it.
- Thinking signatures (Anthropic) and thought signatures (Gemini) must go
  back unchanged in the next request, or the provider refuses it.
- Rate-limit hints have four forms: `retry-after` in seconds or as an
  HTTP date, `retry-after-ms`, durations such as `6m0s` (OpenAI), and
  RFC 3339 times (Anthropic). OpenCode reports a used-up plan as a 429 or
  402 that is not a throttle.
- Bedrock now accepts API keys as a bearer token, and has a Chat
  Completions endpoint. Vertex AI accepts only OAuth access tokens, which
  expire after 1 hour. Azure OpenAI accepts an `api-key` header.

## Decision

### No default

1. **No default model, no default provider, no preset.** The server
   fills no field of a connection. The console shows no recommended model,
   and the docs give worked examples (decision 30), not a default. With
   no connection, Dusty is off (ADR 120 decision 3). A default needs a
   benchmark result and a new ADR.

### The connection document

2. **One JSON document, `agent.ConnectionSpec`.** The connect route takes
   it, the read route answers it, and the store keeps it. It has the field
   `version: 1`. It refuses an unknown field, a document above 32 KiB, and
   a string with a control character. A later version adds fields. It
   never changes what a field of version 1 means.
3. **The fields.** Each field that has no default in this table is
   required:

   | Field | What it holds | Default |
   | --- | --- | --- |
   | `format` | the wire format (decision 4) | none |
   | `baseURL` | the address before the path of the format | none |
   | `model` | the model name that the request sends, as is | none |
   | `auth` | the header and the secret reference (decision 8) | none |
   | `headers` | extra headers (decision 10) | empty |
   | `query` | extra query parameters, for example `api-version` | empty |
   | `userAgent` | the `User-Agent` header (decision 11) | `OpenDustpan/<release>` |
   | `outputTokens` | the field name and the limit for each call (decision 12) | none |
   | `contextTokens` | the context window of the model | none |
   | `tools` | the tool fields (decision 13) | none |
   | `transport` | `whole` or `stream` (decision 14) | none |
   | `reasoning` | how the adapter keeps reasoning parts (decision 15) | none |
   | `cache` | the caching mode (decision 16) | none |
   | `conversationKey` | where the conversation key goes (decision 17) | empty |
   | `providerFields` | extra fields of the request body (decision 18) | empty |

   The adapter appends the path of its format to `baseURL`:
   `/chat/completions`, `/messages` or `/responses`. A server that uses
   another path is not supported in version 1.

### Wire formats

4. **Three formats, two first.** `format` takes one of these values:

   | Value | The format | Support |
   | --- | --- | --- |
   | `openai-chat-completions` | the Chat Completions API that OpenAI defined | first adapter change |
   | `anthropic-messages` | the Anthropic Messages API | first adapter change |
   | `openai-responses` | the Responses API that OpenAI defined | a later change |

   Chat Completions comes first because every local model server and
   every gateway of the note serves it. Messages comes first too, because
   ADR 120 planned it and its caching is explicit. A connection with
   `openai-responses` fails validation with `ErrFormatNotSupported` until
   its adapter ships. The newest OpenAI models call tools only through
   Responses.
5. **No native format of a cloud.** The server has no adapter for the
   Gemini `generateContent` format, the Bedrock Converse format or the
   Ollama `/api/chat` format. Each of these providers also serves Chat
   Completions. An operator who needs a native format puts a gateway such
   as LiteLLM in front of it.
6. **No request signing and no token exchange.** The server does not
   sign requests with AWS SigV4. It does not get or refresh an OAuth
   access token, for example for Vertex AI or for Azure with Entra ID. A
   credential is one static secret. An operator who must use SigV4 or a
   short-lived token runs a gateway that does it. Bedrock API keys and
   Azure `api-key` keys are static, so they work with no gateway.
7. **The adapter of each format.** Each adapter is in its own package
   under `pkg/agent/model/`, with `net/http` and `encoding/json` only
   (ADR 121 decision 8). One adapter serves every provider of its format.
   No adapter has a code path for the name of a provider. A difference
   between providers is a field of the document, never a check of the host
   name.

### Authentication and secrets

8. **`auth` names a header and a secret item.** The form is:

   ```json
   "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." }
   ```

   The adapter sends the header `header` with the value `prefix` and the
   value of the secret item. `prefix` can be empty, for example for
   `x-api-key` or `api-key`. `auth` can also be `{"none": true}`, for a
   model server that takes no key. The document has no field for a plain
   value. Validation refuses a `secret` that is not a reference to a
   secret item of the connection.
9. **The secret is a secret item of the scope `@server`.** A secret item
   is a value that the server seals in a scope with its own data key
   (ADR 23), as the secrets of a sink (ADR 145 decision 13).
   `secret.InstallationSecrets` seals it in the installation scope. These
   routes manage the secret items of the connection, with the action
   `server:manage-agent` on `@server`:

   | Route | What it does |
   | --- | --- |
   | `POST /api/agent/secrets` | stores a value and answers its reference `itm-...` |
   | `PUT /api/agent/secrets/{ref}` | replaces the value of the item, for a rotation |
   | `GET /api/agent/secrets` | lists the references, with the times of create, change and last use |
   | `DELETE /api/agent/secrets/{ref}` | removes the item, or answers 409 while the connection uses it |

   No route answers a value. A rotation takes effect at the next model
   call. The value is never in a log, an audit entry, a metric, an error
   or a transcript. The audit log is on for the agent (ADR 120 decision
   5), so each write of a secret item writes an entry (decision 27).
10. **Extra headers.** `headers` maps a header name to `{"value": "..."}`
    or `{"secret": "itm-..."}`. It holds at most 16 headers, and each
    value at most 1 KiB. Examples are `anthropic-version`,
    `anthropic-beta`, and the attribution headers of OpenRouter. Validation
    refuses these names:
    - the headers that the transport owns: `Host`, `Content-Length`,
      `Content-Type`, `Transfer-Encoding`, `Connection`, `TE`, `Upgrade`,
      `Cookie`, and each name that starts with `Proxy-`,
    - the header of `auth`, `User-Agent`, and each header of
      `conversationKey`, because their own fields set them.

    A plain value that the reply filter (decision 22) finds secret-like
    fails validation with `ErrPlainSecret`. The error names the field and
    tells the operator to store the value as a secret item. The same rule
    applies to `query` and to `providerFields`.
11. **The user agent.** `userAgent` sets the `User-Agent` header of each
    request. The default is `OpenDustpan/` and the release, for example
    `OpenDustpan/0.9.0`. A value is printable ASCII, at most 200 bytes. The
    value stays the same between the requests of a release, because some
    providers use it to route a cache.

### Fields of the request

12. **The output token limit.** `outputTokens` holds `field` and `max`.
    For `openai-chat-completions`, `field` is `max_tokens` or
    `max_completion_tokens`, and the operator chooses it: OpenAI and Azure
    reasoning models need the second, and Ollama and LM Studio document
    only the first. For `anthropic-messages` it is `max_tokens`, and for
    `openai-responses` it is `max_output_tokens`. `max` is the limit for
    each call. The adapter sends the lower of `max` and the output tokens
    that remain in the run (ADR 120 decision 16). The adapter sends no
    `temperature`, `top_p` or other sampling field of its own, because
    reasoning models refuse them.
13. **Tools.** `tools` holds `choice` and `parallel`:
    - `choice` is `omit` or `auto`. With `omit`, the request has no
      `tool_choice` field. Ollama and some gateways need it. The adapter
      never forces a tool.
    - `parallel` is `omit`, `true` or `false`. It sets
      `parallel_tool_calls` or `disable_parallel_tool_use`. llama.cpp
      needs `true` for more than one call in a turn.

    The adapter sends the tool catalog (ADR 122) in the order of the
    catalog, with each JSON Schema in canonical JSON. So the bytes of the
    tools stay the same between requests, as a cache needs.
14. **Transport.** With `whole`, the adapter asks for one whole answer.
    With `stream`, it asks for a stream, reads it to the end, and builds
    one whole answer. The port keeps no stream (ADR 121 decision 6). A
    stream helps when a gateway or a provider closes an idle connection
    on a long answer. With `stream` on Chat Completions, the adapter sets
    `stream_options.include_usage`, so the last event holds the token use.
    A provider that refuses that field fails the `transport` check of the
    probe, and the operator then sets `whole`.
    The adapter ignores SSE comment lines, for example the keep-alive
    lines of OpenRouter. An error event in a stream with the status 200 is
    an error of the call, with the mapping of decision 21.
15. **Reasoning parts.** `reasoning` is `drop` or `keep`:
    - With `keep`, the adapter keeps each reasoning part of an answer as
      an opaque part (decision 31), with its signature, and sends it back
      unchanged in the next request of the run. Anthropic needs it for
      thinking with tools, and Gemini needs its thought signatures.
    - With `drop`, the adapter sends no reasoning part back.

    The fields that turn reasoning on and set its effort differ for each
    provider: `reasoning_effort`, `reasoning`, `thinking`,
    `output_config`, `think` or `chat_template_kwargs`. The operator sets
    them in `providerFields`.
16. **Caching.** `cache` holds `mode` and `ttl`:
    - `none`: the adapter adds no cache field.
    - `automatic`: the provider caches by itself, for example OpenAI,
      Gemini, vLLM and llama.cpp. The adapter adds no cache field. It
      keeps the prefix stable (decision 19).
    - `breakpoints`: the adapter adds `cache_control` blocks, for the
      Messages format and for Anthropic models through a Chat Completions
      gateway such as OpenRouter. It puts one block at the end of the
      tools, one at the end of the system prompt, and one at the last
      message. That is 3 of the 4 blocks that Anthropic allows.
    - `ttl` is `5m` or `1h`, and applies only to `breakpoints`.

    The answer reports the cache read and cache write tokens of each call,
    and the token ledger records them (ADR 120 decision 20). So an
    operator can see whether the cache works.

### The conversation key

17. **One key for each conversation, in the fields that the operator
    names.** A conversation is a chain of agent runs (ADR 120 decision
    6). The conversation key is the same for each model call of one
    conversation, and differs for each conversation. `conversationKey`
    holds the places where the adapter sends it:
    - `headers`: up to 4 header names, for example
      `["x-opencode-session", "x-session-affinity"]`.
    - `bodyField`: one top-level field of the request body, for example
      `prompt_cache_key` for OpenAI, or empty.
    - `prefix`: text before the key, up to 16 bytes, for example `ses_`.

    With all fields empty, the adapter sends no key.
18. **The key has no identity of a person.** The key is the first 26
    characters, in lower-case base32, of HMAC-SHA256 over the ID of the
    first run of the conversation. The HMAC key is a random 32-byte secret
    item of `@server` that the server makes at the first connect. So:
    - the key stays the same for every run, turn and retry of the
      conversation, also after a takeover by another instance,
    - a provider cannot get a run ID, a subject, a namespace or a name
      from the key,
    - two conversations of one person have two keys that a provider cannot
      link by the key.

    The adapter never sends an end-user field: `user`,
    `safety_identifier`, `metadata.user_id`, or a header with a subject,
    an email address or a name. Validation refuses these names in
    `providerFields` and in `headers`. A provider can still link requests
    by the credential and by the address of the server.
19. **A stable prefix.** A cache matches a prefix of exact bytes. So:
    - The system prompt has a version, and has no time, date, name of a
      person, namespace or run ID (ADR 120 decision 23).
    - The catalog has a fixed order, and its JSON is canonical (decision
      13).
    - The loop only appends to the transcript of a run, and a follow-up
      run starts with the transcript of its parent.
    - The user agent and the headers are the same for each call of a
      release (decision 11).

    A revealed value (ADR 124 decision 9) changes the bytes of one tool
    result in one call. The cache misses for the rest of that request
    only.

### Request body fields of a provider

20. **`providerFields` is a JSON object of extra fields.** The adapter
    merges it into the top level of each request body. It holds at most
    8 KiB. Examples are the `provider` routing object of OpenRouter, the
    reasoning fields of decision 15, and `options.num_ctx` of Ollama.
    Validation refuses each key that the adapter owns:
    - `model`, `messages`, `system`, `input`, `tools`, `tool_choice`,
      `parallel_tool_calls`, `stream`, `stream_options`, and each output
      token field,
    - `cache_control` and the `bodyField` of `conversationKey`,
    - the end-user fields of decision 18.

    The server does not check the meaning of the other keys. The probe of
    decision 24 shows whether the provider accepts them.

### Errors, limits and the network

21. **The error mapping.** Each adapter maps a status and an error body
    to a sentinel of ADR 121 decision 4. It reads the error type or code of
    the body, never its text, to choose a sentinel:

    | Answer | Sentinel |
    | --- | --- |
    | 401, 403 | `ErrModelAuth` |
    | 402, or a 429 that names a used-up plan or quota | `ErrModelQuota` (new, decision 31) |
    | 429 | `ErrModelRateLimited`, with the wait |
    | 408, 409, 500, 502, 503, 504, 529, a broken stream | `ErrModelUnavailable` |
    | 400 or 413 with a context-length type or code | `ErrContextTooLong` |
    | other 4xx | `ErrModelRequest` |

    The wait comes from `retry-after-ms`, `retry-after` (seconds or an
    HTTP date), the OpenAI reset durations, or the Anthropic reset times,
    in this order. With no hint, the loop uses its backoff. A wait above
    5 minutes stops the run with `model-quota`, because a turn must not
    hold a claim that long. The ledger row of a call keeps the request ID
    of the provider (`x-request-id` or `request-id`), for support.
22. **The reply filter is always on.** `agent.ReplyFilter` removes text
    that looks like a secret. No setting, flag or connection field turns
    it off. It runs:
    - on the prompt of a person, before the prompt is in the transcript or
      reaches a model. The run answer tells the person how many values the
      filter removed.
    - on each answer of the model, before the model step records it: the
      text, the arguments of each tool call, and each reasoning part.
    - on each error of the probe (decision 24).

    It removes the exact value of each secret item of the connection, and
    each match of a list of patterns in the binary: known key and token
    prefixes, PEM private key blocks, JSON web tokens, the
    `Authorization` header form, and a value after a name such as
    `password` or `secret`. The list has no rule on entropy alone, because
    resource IDs and hashes of infrastructure match one. A test corpus of
    secrets and of IDs from state checks the list. Each match becomes
    `[removed: secret-like text]`. ADR 124 decision 9 still masks each
    revealed value. The filter is a second defense that does not depend
    on the model.
23. **Egress.** A model call goes through `pkg/egress` and
    `egresshttp`, with these changes for model connections:
    - The host of `baseURL` must match an entry of the egress allowlist
      of the installation. With no entry, no model call goes out.
    - The allowlist is a setting with the action `server:manage-settings`
      on `@server`, at `GET` and `PUT /api/server/egress-allowlist`. An
      entry is a host name, a `*.` suffix, or a CIDR range with a port.
    - The dial guard refuses a private, loopback or link-local address
      unless a CIDR entry holds it. So a homelab can allow
      `192.168.1.20/32` port 11434 for Ollama.
    - The client uses `http` only to an address in a CIDR entry with
      `"http": true`. A public address always needs `https`.
    - The client follows no redirect and uses no proxy from the
      environment. It uses the outbound proxy of the installation when the
      operator sets one, and the dial guard then checks the proxy address.
    - A model call takes up to 10 minutes, and a stream with no byte for 2
      minutes fails. The body of an answer is at most 4 MiB.

    On OpenDustpan Cloud, the operator of the service refuses CIDR
    entries, so an account can reach public addresses only.

### The probe

24. **A capability probe at connect time.** `PUT /api/agent/connection`
    runs the probe before it stores the connection. `POST
    /api/agent/connection/probe` runs it on a document without a store.
    The probe makes these calls, in order, and stops at the first failure:

    | Check | What it does | A failure |
    | --- | --- | --- |
    | `reach` | connects to the host with the egress rules | blocks the connect |
    | `auth` | sends one short text request | blocks the connect |
    | `tools` | offers one probe tool and asks the model to call it | blocks the connect |
    | `tool-result` | sends the tool result back and asks for a text answer | blocks the connect |
    | `transport` | with `stream`, reads a stream to its end | blocks the connect |
    | `usage` | checks that the answers report input and output tokens | a warning |
    | `cache` | with a cache mode, repeats a request and reads the cache tokens | a warning |

    Dusty needs tool calls, so a model that answers the `tools` check
    with no tool call fails it. A failure answers 422 with the name of the
    check and an error in strict STE. The error names the field of the
    document to look at. It never holds the error text of the provider.
    Examples:
    - "The model gave no tool call. Make sure that the model server has
      tool calls on. For vLLM, start it with --enable-auto-tool-choice."
    - "The provider refused the field max_tokens. Set outputTokens.field
      to max_completion_tokens."
25. **The probe costs tokens, and the ledger records them.** Each probe
    call writes a ledger row with the origin `probe` and no run. The
    probe route allows 6 probes in each minute for the installation.

### Routes

26. **The connection routes.** Each route declares its action and its
    object (ADR 17). They replace the connection routes of ADR 120
    decision 7:

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/agent/connection` | `server:manage-agent` | `@server` |
    | `PUT /api/agent/connection` | `server:manage-agent` | `@server` |
    | `POST /api/agent/connection/probe` | `server:manage-agent` | `@server` |
    | `DELETE /api/agent/connection` | `server:manage-agent` | `@server` |
    | `GET /api/agent/formats` | `server:manage-agent` | `@server` |
    | `POST /api/agent/secrets` | `server:manage-agent` | `@server` |
    | `GET /api/agent/secrets` | `server:manage-agent` | `@server` |
    | `PUT /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` |
    | `DELETE /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` |
    | `GET /api/server/egress-allowlist` | `server:manage-settings` | `@server` |
    | `PUT /api/server/egress-allowlist` | `server:manage-settings` | `@server` |

    The read answers the document with references only, its version, and
    the result of its last probe. A `PUT` takes `If-Match` with the
    version, so two administrators cannot overwrite each other. The
    formats route lists each format, its support state and the values of
    each field, for the console form. Each change passes the policy check
    (ADR 17).

### Audit

27. **The audit entries.** Each of these writes an entry of the kind
    `change` on `@server`, as ADR 126 decision 7 asks for a connect:
    - a connect, a change and a disconnect: `changes` holds the old and
      the new document, with references only, and the probe result,
    - a probe with no store: the result of each check,
    - a create, a rotation and a removal of a secret item: the reference
      only,
    - a change of the egress allowlist: the old and the new list.

    The end entry of a run (ADR 126 decision 6) also holds the count of
    values that the reply filter removed. No entry holds a value, a
    prompt, an answer or an error text of a provider.

### OpenDustpan Cloud later

28. **A managed connection can come later, with no break.** OpenDustpan
    Cloud may later offer a connection that the service runs. It is a
    connection document like any other, with a secret item that the
    service owns and an account cannot read. It adds the field `managed`,
    and the account administrator still turns Dusty on (ADR 120 decision
    4). A self-hosted operator can use the same field for the teams of an
    installation, so the hosted service has no feature that the server
    lacks (ADR 59). A managed default model needs the benchmark result
    and a new ADR (decision 1).
29. **Version 1 stays valid.** A new format is a new value of `format`. A
    new field gets a default that keeps the behavior of version 1. A
    connection for each namespace or account (ADR 121 decision 9) is a
    list of documents, each in version 1.

### Worked examples

30. **The docs give these examples.** Each is a full document. The
    values `itm-...` are references that the secrets route answered.

    A homelab with Ollama on the local network. The egress allowlist has
    `{"cidr": "192.168.1.20/32", "port": 11434, "http": true}`:

    ```json
    {
      "version": 1,
      "format": "openai-chat-completions",
      "baseURL": "http://192.168.1.20:11434/v1",
      "model": "qwen3-coder:30b",
      "auth": { "none": true },
      "outputTokens": { "field": "max_tokens", "max": 8192 },
      "contextTokens": 65536,
      "tools": { "choice": "omit", "parallel": "omit" },
      "transport": "stream",
      "reasoning": "drop",
      "cache": { "mode": "automatic" },
      "providerFields": { "options": { "num_ctx": 65536 } }
    }
    ```

    A homelab with llama.cpp on the same host as the server, with an API
    key. The allowlist has `{"cidr": "127.0.0.1/32", "port": 9931,
    "http": true}`:

    ```json
    {
      "version": 1,
      "format": "openai-chat-completions",
      "baseURL": "http://127.0.0.1:9931/v1",
      "model": "local",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "outputTokens": { "field": "max_tokens", "max": 8192 },
      "contextTokens": 65536,
      "tools": { "choice": "auto", "parallel": true },
      "transport": "stream",
      "reasoning": "drop",
      "cache": { "mode": "automatic" }
    }
    ```

    A homelab on the OpenCode Go plan. The allowlist has `opencode.ai`:

    ```json
    {
      "version": 1,
      "format": "openai-chat-completions",
      "baseURL": "https://opencode.ai/inference/go/openai/v1",
      "model": "kimi-k3",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "userAgent": "OpenDustpan/0.9.0",
      "outputTokens": { "field": "max_tokens", "max": 16000 },
      "contextTokens": 200000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "stream",
      "reasoning": "keep",
      "cache": { "mode": "automatic" },
      "conversationKey": { "headers": ["x-opencode-session"], "prefix": "ses_" }
    }
    ```

    A team on OpenRouter with an Anthropic model, only providers with zero
    data retention, and no fallback:

    ```json
    {
      "version": 1,
      "format": "openai-chat-completions",
      "baseURL": "https://openrouter.ai/api/v1",
      "model": "anthropic/claude-sonnet-5.5",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "headers": {
        "HTTP-Referer": { "value": "https://opendustpan.example.com/" },
        "X-OpenRouter-Title": { "value": "OpenDustpan" }
      },
      "outputTokens": { "field": "max_completion_tokens", "max": 16000 },
      "contextTokens": 200000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "stream",
      "reasoning": "keep",
      "cache": { "mode": "breakpoints", "ttl": "5m" },
      "conversationKey": { "headers": ["x-session-id"] },
      "providerFields": {
        "provider": { "only": ["anthropic"], "allow_fallbacks": false, "zdr": true }
      }
    }
    ```

    A company with the Anthropic API, and with Azure OpenAI on the v1 path:

    ```json
    {
      "version": 1,
      "format": "anthropic-messages",
      "baseURL": "https://api.anthropic.com/v1",
      "model": "claude-sonnet-5-5",
      "auth": { "header": "x-api-key", "prefix": "", "secret": "itm-..." },
      "headers": { "anthropic-version": { "value": "2023-06-01" } },
      "outputTokens": { "field": "max_tokens", "max": 16000 },
      "contextTokens": 200000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "stream",
      "reasoning": "keep",
      "cache": { "mode": "breakpoints", "ttl": "1h" },
      "providerFields": { "thinking": { "type": "adaptive" } }
    }
    ```

    ```json
    {
      "version": 1,
      "format": "openai-chat-completions",
      "baseURL": "https://example-resource.openai.azure.com/openai/v1",
      "model": "my-deployment",
      "auth": { "header": "api-key", "prefix": "", "secret": "itm-..." },
      "outputTokens": { "field": "max_completion_tokens", "max": 16000 },
      "contextTokens": 400000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "whole",
      "reasoning": "drop",
      "cache": { "mode": "automatic" },
      "conversationKey": { "bodyField": "prompt_cache_key" }
    }
    ```

    The model names of the examples show the form only. The docs say that
    they are not a recommendation.

### Changes to the model port

31. **The port gets one part kind and one error.**
    - A message part can be `opaque`: bytes of the format of the
      connection, with the name of the format. The loop stores it in the
      transcript and gives it back to the adapter unchanged. An adapter
      sends an opaque part only to a connection of the same format and
      model. After a change of the connection, the loop drops the opaque
      parts of older turns. The console never shows an opaque part.
    - `ErrModelQuota`: the account has no plan or credit left.
      The loop stops the run with the stop reason `model-quota`, and the
      status route says so.

    `agenttest.RunModel` gets cases for each rule of decisions 12 to 21:
    each output token field, `choice: omit`, the conversation key in each
    place, no end-user field, each cache mode, an opaque part that comes
    back unchanged, an error event in a stream with the status 200, each
    form of a wait, a 402, and a canary secret in an answer that the reply
    filter removes.

## What this ADR replaces

- **ADR 120, decision 3:** "the adapter, the address of the provider, the
  model name and the credential. The credential is a sensitive setting.
  The server seals it with the key-encryption key (ADR 23)". The
  connection is the document of decision 2, and the credential is a
  secret item of `@server` (decision 9).
- **ADR 120, decision 7:** the rows `PUT /api/agent/connection` and
  `DELETE /api/agent/connection`. Decision 26 gives the routes of the
  connection.
- **ADR 121, decision 2:** "A part is text, a tool call ... or a tool
  result". A part can also be opaque (decision 31).
- **ADR 121, decision 4:** the table gets `ErrModelQuota` (decision 31).
- **ADR 121, decision 8:** "an adapter for OpenAI-compatible APIs later".
  Both adapters come in the first adapter change (decision 4). The rule on
  the egress proxy of RFC 16 in that decision changes too: decision 23
  gives the egress rules.
- **ADR 121, decision 9:** "The connection holds the adapter, the base
  address, the model name, the credential and an optional output token
  limit". The connection holds the fields of decision 3.
- **ADR 121, "Not decided here":** "Prompt caching across runs, and
  whether the loop asks for it". Decisions 16 to 19 decide it.

## Consequences

- `pkg/agent` gets `ConnectionSpec`, `ConnectionSpec.Validate`,
  `ReplyFilter`, `ConversationKey`, `ErrFormatNotSupported`,
  `ErrPlainSecret`, `ErrModelQuota` and the opaque part. `agent.Connection`
  holds the document.
- `pkg/agent/model/openaichat` and `pkg/agent/model/anthropic` are the
  first two adapters. Each runs `agenttest.RunModel` against a fake server
  that follows the note.
- `pkg/egress` gets the egress allowlist, the CIDR entries and the
  `http` rule. The webhook and the iCal feed keep their rules: they do
  not read the allowlist.
- Each SQL store gets the tables of the connection document, the secret
  items of the connection and the egress allowlist. The ledger gets the
  columns `origin` and `providerRequestID`.
- An operator writes more fields than with a preset. The docs and the
  formats route make each field visible, and the probe tells what is
  wrong before Dusty uses the connection.
- OpenCode Go says that it is for coding agents, and that it monitors
  traffic for abuse. The traffic of Dusty is not the traffic of a coding
  agent, so the plan may limit it. The free models of OpenCode answer 403
  to every client that is not OpenCode. The docs say both.
- A local model in a homelab is as easy to connect as a hosted provider:
  one allowlist entry and one document.
- The reply filter can remove text that is not a secret, for example a
  long ID that starts like a key. The person sees the removal in the
  answer. A false positive is safer than a leak.
- `GLOSSARY.md` gets the terms Connection document, Wire format, Secret
  item, Conversation key, Reply filter, Capability probe and Egress
  allowlist. The term Model connection changes.

## Not decided here

- A default model, for self-hosted servers and for OpenDustpan Cloud.
- A hashed end-user identifier for providers that ask for one, for
  example the `safety_identifier` of OpenAI.
- AWS SigV4 signing and OAuth token refresh in the server.
- The native formats of Gemini, Bedrock and Ollama.
- A path of a format other than the fixed path of decision 3.
- A connection for each namespace or account.
