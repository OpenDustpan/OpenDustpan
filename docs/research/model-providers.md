# Model providers, gateways and model servers: request rules

Research for ADR 129, collected on 2026-10-18 from primary documentation
and source code. Each claim is marked as verified, with the URL that it
comes from, or as unverified. A claim from a live request with a fake key
says so. No request with a paid key was made.

The note has four parts:

1. Hosted providers: OpenAI (Chat Completions and Responses), Anthropic,
   Azure OpenAI, Google Vertex AI, AWS Bedrock.
2. Gateways and hosted open-model providers: OpenRouter, DeepInfra,
   Together AI, Groq, Mistral, and the headers that the OpenCode client
   sends.
3. Self-hosted model servers and proxies: Ollama, vLLM, LiteLLM, LM
   Studio, and a short part on llama.cpp.
4. The model provider of OpenCode (OpenCode Go and the pay-as-you-go
   Console), and the llama.cpp server in full.

Legend: **V** or VERIFIED = read in a primary source, with its URL. **U** or
UNVERIFIED = not confirmed from a primary source.

Provider docs change often. A later reader checks the URL before relying on
a claim.

---

# Part 1. Hosted providers


## 1. OpenAI (Chat Completions and Responses)

Sources:
- A: https://developers.openai.com/api/reference/overview
- B: https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create (fetched with curl)
- C: https://developers.openai.com/api/reference/resources/responses/methods/create.md
- D: https://developers.openai.com/api/docs/guides/prompt-caching
- E: https://developers.openai.com/api/docs/guides/rate-limits
- F: https://developers.openai.com/api/docs/guides/error-codes
- G: https://developers.openai.com/api/docs/guides/function-calling
- H: https://developers.openai.com/api/docs/guides/reasoning
- I: https://developers.openai.com/api/docs/guides/latest-model.md
- J: https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events
- K: https://developers.openai.com/api/reference/resources/responses/streaming-events.md
- L: https://developers.openai.com/api/docs/guides/safety-best-practices

**1. Auth, headers, base URL**
- Base URL is `https://api.openai.com/v1`. Endpoints are `/chat/completions` and `/responses`. **V** (A)
- Auth header: `Authorization: Bearer <API key or workload-identity access token>`. **V** (A)
- Optional request headers: `OpenAI-Organization`, `OpenAI-Project`, and `X-Client-Request-Id` (ASCII only, 512 characters or fewer, otherwise 400). **V** (A)
- There is no version header to send. Responses carry `openai-version: 2020-10-01`. **V** (A)

**2. Token limits**
- Chat Completions:
  - `max_completion_tokens` is "an upper bound … including visible output tokens and reasoning tokens". **V** (B)
  - `max_tokens` is "Deprecated … in favor of max_completion_tokens, and is not compatible with o-series models". **V** (B)
  - Neither is required.
- Responses: `max_output_tokens` (optional) includes reasoning tokens. When the cap is hit, the response returns `status: incomplete` with `reason: max_output_tokens`. **V** (C, H)
- The reasoning guide recommends reserving about 25,000 tokens when starting out. **V** (H)
- Reasoning constraints: "When reasoning effort is not `none`, remove `temperature`, `top_p`, and `top_logprobs`." Chat Completions also needs `logprobs` removed. Responses needs `message.output_text.logprobs` removed from `include`. **V** (I)
- GPT-6 Astra and GPT-6.1 Sol need the Responses API for tool calling. In Chat Completions, tools only work with `reasoning_effort: "none"`. **V** (G, H)

**3. Tool calling**
- Chat tool definition: `{"type":"function","function":{"name","description","parameters","strict"}}`. The name allows a-z, A-Z, 0-9, `_` and `-`, up to 64 characters. **V** (B)
- Responses tool definition is flat: `{"type":"function","name","description","parameters","strict"}`. **V** (G)
- `tool_choice` values:
  - `"none"`, `"auto"` and `"required"`. **V** (B, G)
  - A specific function: `{"type":"function","function":{"name":…}}` in Chat, or `{"type":"function","name":…}` in Responses. **V** (B, G)
  - `{"type":"allowed_tools","mode":"auto"|"required","tools":[…]}`. **V** (B, G)
  - Defaults: `none` when no tools are sent, `auto` when tools are present. **V** (B)
- `parallel_tool_calls` is a boolean in both APIs. `false` means at most one call per turn. **V** (B, C, G)
- Sending results back:
  - Chat: a message `{"role":"tool","tool_call_id":…,"content": string | [text parts]}`. **V** (B)
  - Responses: an input item `{"type":"function_call_output","call_id":…,"output":"…"}`. **V** (G)
  - Reasoning models: send reasoning items back together with the function-call outputs. **V** (G, H)
- Strict mode requires `additionalProperties:false` on every object and every property listed in `required`. Optional fields are expressed as a `null` type union. **V** (G)
  - Responses: if `strict` is omitted, it tries strict and falls back to non-strict. **V** (G)
  - Chat: non-strict by default. **V** (G)

**4. Streaming**
- Chat:
  - SSE chunks of type `chat.completion.chunk`, carrying `choices[].delta` with `content`, `role`, `refusal` and `tool_calls[]`.
  - Each `tool_calls[]` entry has `index`, `id`, `type`, and `function.name` / `function.arguments` fragments. Accumulate the fragments by `index`.
  - `finish_reason` is `stop`, `length`, `tool_calls` or `content_filter`.
  - **V** (J)
- Chat usage in a stream:
  - Set `stream_options: {"include_usage": true}`. An extra chunk then arrives "before the data: [DONE] message" with an empty `choices` and full `usage`. Every other chunk has `usage: null`. It "may not" arrive if the stream is interrupted. **V** (B)
  - `stream_options.include_obfuscation` also exists. **V** (B)
- Responses streaming events (typed): `response.created`, `response.in_progress`, `response.output_item.added/done`, `response.content_part.added/done`, `response.output_text.delta/done`, `response.function_call_arguments.delta/done`, `response.reasoning_summary_text.delta/done`, `response.reasoning_text.delta/done`, `response.completed`, `response.failed`, `response.incomplete`, `response.queued`, and `error`. **V** (K)
  - The `error` event carries `code`, `message`, `param` and `sequence_number`. **V** (K)
  - Usage arrives in the final `response.completed` response object. **U**
  - Responses `stream_options` only has `include_obfuscation`. **V** (C)

**5. Reasoning**
- Chat: `reasoning_effort`. Responses: `reasoning.effort`. Values: `none | minimal | low | medium | high | xhigh | max`, with support varying by model. **V** (B, C)
- Responses also accepts:
  - `reasoning.summary` (`auto`, `concise` or `detailed`; `generate_summary` is deprecated).
  - `reasoning.context` (`auto | current_turn | all_turns`; GPT-5.6 defaults to `all_turns`).
  - **V** (C, H)
- Encrypted reasoning:
  - With `store:false` or ZDR, reasoning items include `encrypted_content` by default. `include: ["reasoning.encrypted_content"]` is still accepted. **V** (H, C)
  - Send these items back unchanged, or use `previous_response_id`. **V** (H)
- Usage fields: `usage.output_tokens_details.reasoning_tokens` in Responses and `completion_tokens_details.reasoning_tokens` in Chat. **V** (H, B)

**6. Prompt caching**
- Caching is automatic: "enabled by default for supported models". **V** (D)
- Minimum size:
  - GPT-5.6+: 1,024 visible input tokens.
  - Earlier models vary. GPT-5.5 places implicit breakpoints every 2,048 tokens.
  - **V** (D)
- Only an exact prefix match hits: "Cache reuse requires the entire rendered prefix to match." Put stable content first and append rather than rewrite. **V** (D)
- GPT-5.6+ adds `prompt_cache_options {mode: implicit|explicit, ttl: "30m", prewarm, comparison_response_id}` and per-block `prompt_cache_breakpoint`.
  - A request can write up to 4 breakpoints.
  - Matching considers the latest 80 breakpoints.
  - `ttl` `30m` is the only value and the default.
  - **V** (C, D)
- `prompt_cache_retention` (`in_memory` | `24h`) is deprecated in favor of `prompt_cache_options.ttl`.
  - `in_memory` lasts about 5 to 10 minutes idle, 1 hour at most.
  - `24h` lasts up to 24 hours.
  - Defaults: `24h` for non-ZDR organizations, `in_memory` for ZDR organizations. gpt-5.5 and later support only `24h`.
  - **V** (C, D)
- Pricing:
  - Cached reads are discounted by up to 95%.
  - GPT-5.6+: reads cost 0.1x (0.05x for GPT-6.1 Sol) and writes cost 1.25x.
  - Cached tokens still count toward TPM.
  - **V** (D)
- Routing key: `prompt_cache_key` "replaces the `user` field". It affects routing but gives no guarantee. Traffic above about 15 RPM for the same prefix and key can overflow. On GPT-5.6+ it is optional (used for accounting). **V** (B, C, D)
- Usage fields:
  - Responses: `usage.input_tokens_details.cached_tokens` (plus `cache_write_tokens` on GPT-5.6+). **V** (D)
  - Chat: `usage.prompt_tokens_details.cached_tokens` and `cache_write_tokens`. **V** (B)

**7. Rate limits and retries**
- Headers:
  - `x-ratelimit-limit-requests`, `x-ratelimit-limit-tokens`, `x-ratelimit-remaining-requests`, `x-ratelimit-remaining-tokens`.
  - `x-ratelimit-reset-requests` and `x-ratelimit-reset-tokens`, as duration strings such as `1s` or `6m0s`.
  - Project-scoped `x-ratelimit-{limit,remaining,reset}-project-tokens`.
  - `Retry-After` in integer seconds.
  - **V** (A, E)
- `Retry-After` may be sent on 429 (temporary rate limit) and 503 (overload). Treat it as a minimum wait and add jitter. Do not retry quota or billing errors. **V** (E)
- Status codes:
  - 429 `rate_limit_error`, including code `slow_down`, and also billing codes (`insufficient_quota`, `credit_balance_exhausted`, `*_spend_limit_exceeded`).
  - 503 `service_unavailable_error` with code `server_is_overloaded`.
  - 500 is a server error.
  - 401 is an authentication failure.
  - 403 is an unsupported region.
  - **V** (E, F)
- Mid-stream errors: do not automatically replay a request once output has started. **V** (E)
- `retry-after-ms` is not documented by OpenAI. It is an Azure header (see section 3). **V** (absence in E)

**8. Errors and request ID**
- Error body: `{"error":{"message","type","param","code"}}`. **V** for the fields (F); the full envelope shape is **U**.
- Request ID: the `x-request-id` response header. OpenAI recommends logging it in production. **V** (A)
- Processing time: the `openai-processing-ms` response header. **V** (A)

**9. End-user identifier**
- `safety_identifier`: a string, max 128 characters. "We recommend hashing their username or email address, in order to avoid sending us any identifying information." **V** (B, C, L)
- Use a session ID for logged-out users. **V** (L)
- `user` is "Deprecated. This field is being replaced by safety_identifier and prompt_cache_key." **V** (B)

---

## 2. Anthropic Messages API

Sources:
- M: https://platform.claude.com/docs/en/api/messages
- N: https://platform.claude.com/docs/en/api/errors
- O: https://platform.claude.com/docs/en/api/rate-limits
- P: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- Q: https://platform.claude.com/docs/en/build-with-claude/streaming
- R: https://platform.claude.com/docs/en/build-with-claude/thinking
- S: https://platform.claude.com/docs/en/build-with-claude/extended-thinking
- T: https://platform.claude.com/docs/en/build-with-claude/effort
- W: https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls
- X: https://platform.claude.com/docs/en/agents-and-tools/tool-use/strict-tool-use

**1. Auth, headers, base URL**
- `POST https://api.anthropic.com/v1/messages` with headers `x-api-key: $ANTHROPIC_API_KEY`, `anthropic-version: 2023-06-01` and `content-type: application/json`. **V** (curl example in N and X)
- Beta features use the `anthropic-beta: <name>` header, for example `mid-conversation-output-config-2026-07-01`. **V** (T)
- Optional `anthropic-workspace-id` request header. **V** (M)

**2. Token limits**
- `max_tokens` is required. `0` warms the cache without generating output. **V** (M)
- There is no `max_completion_tokens`.
- Thinking tokens count toward `max_tokens`. **V** (R, S)
- With `thinking.type: "enabled"`, `budget_tokens` must be at least 1,024 and less than `max_tokens`, except with interleaved thinking. **V** (S)
- Assistant prefill returns 400 on Claude 4.6+ models. **V** (N)
- Reasoning-mode constraints:
  - Manual extended thinking allows only `tool_choice` `auto` or `none`. **V** (R)
  - Opus 5.5, Sonnet 5.5, Fable 5.1 and Mythos 5.1 reject `tool_choice` `any` and `tool` with 400. **V** (N)
  - Temperature, `top_k` and `top_p` limits with thinking: not found in the pages read. From memory, temperature and `top_k` are not allowed and `top_p` must be between 0.95 and 1. **U**

**3. Tool calling**
- Tool definition: `{name (^[a-zA-Z0-9_-]{1,128}$), description, input_schema (JSON Schema, type: object), strict?, cache_control?, …}`. **V** (M)
- `tool_choice`: `{"type":"auto"|"any"|"tool"(+name)|"none"}`, with optional `disable_parallel_tool_use` (for `auto`, at most one call; for `any` and `tool`, exactly one). **V** (M)
- The response has `stop_reason: "tool_use"` and `tool_use` blocks `{id, name, input}`. **V** (W)
- Sending results back:
  - Send the next `user` message with `{"type":"tool_result","tool_use_id","content"?: string | blocks,"is_error"?: true}`.
  - The results must immediately follow the `tool_use` turn.
  - `tool_result` blocks must come first in the content array, with any text after them. Otherwise the request fails with 400.
  - All parallel results go in one user message.
  - **V** (W)
- Strict mode: set `strict: true` on the tool. The examples use `additionalProperties:false`. It guarantees a valid tool name and valid input. Schemas are cached up to 24 hours. **V** (X)

**4. Streaming**
- Set `"stream": true` to get SSE events:
  - `message_start` (a Message with empty content and initial `usage`).
  - Per block: `content_block_start`, then `content_block_delta`, then `content_block_stop`.
  - `message_delta`: its `usage` counts are "cumulative".
  - `message_stop`.
  - `ping` events, and `error` events (for example `{"type":"error","error":{"type":"overloaded_error",…}}`).
  - Handle unknown event types gracefully.
  - **V** (Q)
- Delta types:
  - `text_delta` carries `text`.
  - `input_json_delta` carries `partial_json`. Concatenate the strings and parse at `content_block_stop`.
  - `thinking_delta`.
  - `signature_delta`, sent once just before the thinking block's stop.
  - **V** (Q, R)
- Cache usage fields appear in `message_start`. **V** (P)
- The `output_tokens_details.thinking_tokens` breakdown appears only in the final `message_delta`. **V** (S)

**5. Thinking**
- Modes: `thinking: {type: "adaptive" | "enabled"(+budget_tokens) | "disabled" | "between_tools"(Sonnet 5.5 only)}`, with optional `display: "summarized" | "omitted" | "updates"(beta)`. **V** (M, R)
- Defaults:
  - On Claude 5.x models, thinking is on and adaptive by default, and `display` defaults to `omitted`.
  - Opus 5.5 rejects `disabled`.
  - On Claude 4.7+, `enabled` returns 400.
  - **V** (R, N)
- Depth is set with `output_config.effort`: `low | medium | high | xhigh | max`. The default is `high`, or `medium` on Opus 5.5 and Haiku 5.5. **V** (T)
- Signatures:
  - Every thinking block carries `signature`.
  - In tool loops, pass `thinking` and `redacted_thinking` blocks back "complete and unmodified". Edited or reordered blocks return 400.
  - On newer models a replayed block is valid only while the `system`, `tools` and earlier messages are unchanged.
  - **V** (R, N)
- Interleaved thinking:
  - Automatic with adaptive thinking.
  - Manual mode on older models needs the `interleaved-thinking-2025-05-14` beta header.
  - **V** (R, S)

**6. Prompt caching**
- Explicit caching: `cache_control: {"type":"ephemeral"}` on a block. **V** (P)
- Automatic caching: a top-level `cache_control` places and moves the breakpoint for you. It is not supported on legacy Bedrock. **V** (P)
- Breakpoints: at most 4 per request, with a lookback window of 20 blocks. **V** (P)
- Prefix order is `tools`, then `system`, then `messages`. A change invalidates that level and every level after it. **V** (P)
  - `tool_choice`, images, thinking settings and effort invalidate `messages`.
  - Tool definitions invalidate everything.
  - **V** (P)
- TTL:
  - The default is 5 minutes, refreshed for free on each use.
  - `"ttl":"1h"` gives 1 hour.
  - 1h entries must come before 5m entries.
  - **V** (P)
- Prices relative to base input:
  - 5-minute write: 1.25x.
  - 1-hour write: 2x.
  - Read: 0.1x (0.05x for Opus 5.5 and Sonnet 5.5; 0.025x for Fable/Mythos 5.1).
  - **V** (P)
- Minimum cacheable prompt:
  - 512 tokens: Opus 5.5, Opus 5, Sonnet 5.5, Haiku 5.5, Fable/Mythos 5.x.
  - 1,024 tokens: Opus 4.8, Sonnet 5, Sonnet 4.5/4.6.
  - 2,048 tokens: Opus 4.7, Haiku 3.5, Mythos Preview.
  - 4,096 tokens: Opus 4.5/4.6, Haiku 4.5.
  - Shorter prompts are silently not cached.
  - **V** (P)
- Usage fields:
  - `cache_creation_input_tokens`, `cache_read_input_tokens`, and `cache_creation.{ephemeral_5m_input_tokens, ephemeral_1h_input_tokens}`.
  - `input_tokens` counts only tokens after the last breakpoint.
  - Total input = read + creation + input.
  - **V** (P, O)
- There is no cache-routing key.
- Caches are isolated per organization, and per workspace. **V** (P)
- For most models, cache reads do not count toward ITPM. **V** (O)

**7. Rate limits and retries**
- Headers:
  - `retry-after` (seconds).
  - `anthropic-ratelimit-{requests,tokens,input-tokens,output-tokens}-{limit,remaining,reset}`. Reset values are RFC 3339 timestamps.
  - `anthropic-priority-{input,output}-tokens-{limit,remaining,reset}`.
  - **V** (O)
- Rate limiting uses a token bucket. **V** (O)
- A 429 spend-cap response has no `retry-after` and carries `error.details.error_code: "enforced_spend_limit_reached"`. **V** (O)
- Status codes:
  - 400 `invalid_request_error`.
  - 401 `authentication_error`.
  - 402 `billing_error`.
  - 403 `permission_error`.
  - 404 `not_found_error`.
  - 409 `conflict_error`.
  - 413 `request_too_large` (32 MB for Messages).
  - 429 `rate_limit_error`.
  - 500 `api_error`.
  - 504 `timeout_error`.
  - 529 `overloaded_error`.
  - **V** (N)
- The SDKs retry twice by default and honor `retry-after`. **V** (N)

**8. Errors and request ID**
- Error body: `{"type":"error","error":{"type":"…","message":"…"},"request_id":"req_…"}`. **V** (N)
- Request ID: the `request-id` response header. Claude Platform on AWS also sends `x-amzn-requestid`. **V** (N)

**9. End-user identifier**
- `metadata.user_id`, max length 512: "should be a uuid, hash value, or other opaque identifier … Do not include any identifying information such as name, email address, or phone number." **V** (M)

---

## 3. Azure OpenAI (Microsoft Foundry)

Sources:
- AZ1: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/api-version-lifecycle (canonical /azure/foundry/openai/…)
- AZ2: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/reference
- AZ3: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/prompt-caching
- AZ4: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/quota (served the "classic" page)
- AZ5: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/reasoning

**1. Auth, URL and versioning**
- v1 API (GA since August 2025):
  - Base URL `https://YOUR-RESOURCE-NAME.openai.azure.com/openai/v1/`; `…services.ai.azure.com/openai/v1/` is also accepted.
  - Paths `/responses` and `/chat/completions`.
  - "`api-version` is no longer a required parameter".
  - `model` = your deployment name.
  - The plain `OpenAI()` client works.
  - **V** (AZ1)
- Preview features are switched on with feature-specific headers, or by an `alpha` segment in the path. **V** (AZ1)
- Legacy URL: `POST https://{resource}.openai.azure.com/openai/deployments/{deployment-id}/chat/completions?api-version=YYYY-MM-DD`.
  - The legacy data-plane GA version is `2024-10-21`.
  - The data-plane table now lists `v1` (GA) and `v1 preview`.
  - **V** (AZ2)
- Auth:
  - API key: the `api-key: <key>` header.
  - Entra ID: `Authorization: Bearer <token>`.
  - **V** (AZ1, AZ2)
- Entra token scope in the v1 examples: `https://ai.azure.com/.default`. **V** (AZ1)
  - The older scope `https://cognitiveservices.azure.com/.default` is **U** (not seen this session).
- Required role: `Cognitive Services OpenAI User`. **V** (AZ1)

**2. Token limits**
- Reasoning models work only with `max_completion_tokens` in Chat and `max_output_tokens` in Responses. **V** (AZ5)
- Parameters not supported by reasoning models (except GPT-6 Astra): `temperature`, `top_p`, `presence_penalty`, `frequency_penalty`, `logprobs`, `top_logprobs`, `logit_bias`, `max_tokens`. **V** (AZ5)
- `max_completion_tokens` arrived in API version 2024-09-01-preview. **V** (AZ1)
- `max_tokens` counts toward the TPM rate-limit estimate, so keep it small. **V** (AZ4)
- Developer messages behave the same as system messages. **V** (AZ5)

**3. Tool calling**
- Same as OpenAI. `parallel_tool_calls` was added in 2024-09-01-preview. **V** (AZ1)
- Parallel tool calls are not supported at `reasoning_effort` `minimal`. **V** (AZ5)
- With gpt-5.6-sol on Chat Completions, function tools combined with reasoning fail. Use `/v1/responses`, or set `reasoning_effort: "none"`. **V** (AZ5)

**4. Streaming**
- Same as OpenAI. `stream_options` and `include_usage` were added in 2024-09-01-preview. **V** (AZ1)
- Azure content-filter annotations inside stream chunks: **U**.

**5. Reasoning**
- `reasoning_effort` (Chat) and `reasoning.effort` (Responses) accept `none`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max`, varying by model. **V** (AZ5)
  - `max` works only with GPT-6 or GPT-5.6 on Responses.
  - `minimal` works only on the original GPT-5.
  - **V** (AZ5)
- Encrypted reasoning items are supported in Responses (v1 preview changelog). **V** (AZ1)

**6. Prompt caching**
- Automatic; "enabled by default".
  - It needs 1,024 tokens or more, and the first 1,024 tokens must be identical.
  - On GPT-5.5 and earlier, hits come in 128-token increments.
  - **V** (AZ3)
- `prompt_cache_key` works for GPT-5.6+ with no particular API version. Above about 15 RPM per prefix and key, some requests miss. **V** (AZ3)
- GPT-5.6+ breakpoints:
  - `prompt_cache_options.mode` (`implicit`/`explicit`), and `ttl` `30m` (only value).
  - Up to 4 writes per request; reads consider the latest 50 breakpoints.
  - Not supported on PTU-M deployments.
  - Earlier models return 400 if these parameters are sent.
  - **V** (AZ3)
- Retention:
  - In-memory: cleared within 5 to 10 minutes idle, always within 1 hour.
  - Extended (`prompt_cache_retention: "24h"`): up to 24 hours, for gpt-5.5, 5.4, 5.3-codex, 5.2, 5.1*, gpt-5, gpt-5-codex and gpt-4.1.
  - `in_memory` is the default for gpt-5.4 and older. gpt-5.5 has extended retention on by default.
  - **V** (AZ3)
- Pricing:
  - Standard deployments: cache reads are discounted. Provisioned: up to 100% discount.
  - GPT-5.6+ may charge for cache writes.
  - **V** (AZ3)
- What is cached: messages, images (the `detail` setting must match), tool definitions, and the structured-output schema. **V** (AZ3)
- Usage fields: `usage.prompt_tokens_details.cached_tokens`, plus `cache_write_tokens` on GPT-5.6+ Standard. **V** (AZ3)

**7. Rate limits and retries**
- Headers:
  - `x-ratelimit-limit-requests`, `x-ratelimit-limit-tokens`, `x-ratelimit-remaining-requests`, `x-ratelimit-remaining-tokens`.
  - `x-ratelimit-reset-requests` and `x-ratelimit-reset-tokens`. The examples (`10`, `300`) look like seconds.
  - `retry-after-ms` on 429 responses.
  - **V** (AZ4)
- The SDK honors `retry-after`. **V** (AZ4)
- RPM is enforced over 1-second or 10-second windows. **V** (AZ4)
- 429 causes: quota exceeded; system capacity throttling ("temporarily unable to process"); a temporary rate-limit reduction (visible when `x-ratelimit-limit-tokens` is below the configured TPM). **V** (AZ4)

**8. Errors and request ID**
- Error body: OpenAI-style `{"error":{code,message,param,type,inner_error{code:"ResponsibleAIPolicyViolation",content_filter_results}}}`.
  - Verified only for the DALL-E schema (`dalleError` with `param`, `type`, `inner_error`). **V** partial (AZ2)
  - The chat error shape is **U**.
- Request ID headers: `apim-request-id` (Microsoft Q&A threads, not official docs); also `x-ms-region` and `x-request-id`. **U** for official documentation.

**9. End-user identifier**
- The `user` field is listed as "A unique identifier representing your end-user" (image API). **V** (AZ2)
- `user_security_context` (Defender for Cloud integration) was added in 2024-12-01-preview. **V** (AZ1)
- Whether Azure accepts `safety_identifier`: **U**.

---

## 4. Google Vertex AI ("Gemini Enterprise Agent Platform")

Sources:
- G1: https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/inference (redirect from vertex-ai/generative-ai/docs/model-reference/inference)
- G2: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/thinking
- G3: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/thinking/thought-signatures
- G4: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/tools/function-calling
- G5: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/context-cache/context-cache-overview
- G6: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/deploy/error-code-429
- G7: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/migrate/openai/overview (redirect from multimodal/call-vertex-using-openai-library)
- G8: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/start/openai
- G9: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/claude/use-claude
- G10: https://platform.claude.com/docs/en/build-with-claude/claude-on-vertex-ai

### 4a. Gemini native (`generateContent`)

**1. Auth and URL**
- `POST https://${LOCATION}-aiplatform.googleapis.com/v1/projects/${PROJECT_ID}/locations/${LOCATION}/publishers/google/models/${MODEL_ID}:generateContent`, with `streamGenerateContent` for streaming. **V** (G1, G2, G4)
- Auth header: `Authorization: Bearer $(gcloud auth print-access-token)`, an OAuth2 access token. **V** (G2, G4)
- In code: Application Default Credentials with scope `https://www.googleapis.com/auth/cloud-platform`. **V** (G8)
- No version header.
- The global endpoint `https://aiplatform.googleapis.com/…/locations/global/…` is shown for the OpenAI-compatible endpoint (G8) and for Claude (G10). **V**
- Service accounts: use ADC with a service-account key or attached SA, or `gcloud auth print-access-token`. Tokens last about 1 hour. **U**

**2. Token limits**
- `generationConfig.maxOutputTokens` (optional). **V** (G1)
- Gemini 4 can return `finishReason: CONTINUATION` with a `continuationToken`. The single-request output limit is 65,536 tokens streaming and 32,768 non-streaming. **V** (G1)

**3. Tool calling**
- Declare tools as `tools: [{functionDeclarations: [{name, description, parameters}]}]`. **V** (G4)
- Results go back as a `user`-role content with `parts: [{functionResponse: {name, response: {...}}}]`. **V** (G4)
- Parallel calls:
  - The model can return several `functionCall` parts.
  - Send all calls first, then all responses. Interleaving them gives 400 on Gemini 3.
  - **V** (G3, G4)
- Mode: `toolConfig.functionCallingConfig.mode`.
  - `ANY` and `VALIDATED` are confirmed through the OpenAI-compat mapping. **V** (G7)
  - `AUTO`, `NONE` and `allowedFunctionNames` are **U**.
- Streaming tool arguments: `toolConfig.functionCallingConfig.streamFunctionCallArguments: true` produces `functionCall.partialArgs[]` and `willContinue` (Gemini 3+). **V** (G4)

**4. Streaming**
- `streamGenerateContent` returns chunks of the same `GenerateContentResponse` shape. **V** for the endpoint (G1).
- `?alt=sse` for SSE, otherwise a JSON array stream: **U**.
- Usage is in `usageMetadata` (`promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`). **V** (G1)

**5. Thinking**
- `generationConfig.thinkingConfig`:
  - `thinkingLevel`: `MINIMAL` (not on Gemini 4), `LOW`, `MEDIUM`, `HIGH`.
  - `thinkingBudget`: Gemini 2.5 only. Deprecated on Gemini 3 and returns 400 on Gemini 4. `-1` means dynamic, `0` turns thinking off, and the default is dynamic up to 8,192.
  - Setting both returns an error.
  - **V** (G1, G2)
- Usage field: `usageMetadata.thoughtsTokenCount`. **V** (G2)
- Thought summaries are supported on Gemini 2.5+; the REST flag `includeThoughts` is **U**.
- Thought signatures:
  - The `thought_signature` field can appear on any part.
  - Gemini 3 enforces it: send signatures back exactly, or get 400.
  - With parallel calls, only the first `functionCall` carries it.
  - A dummy value `skip_thought_signature_validator` exists.
  - **V** (G3)

**6. Context caching**
- Implicit caching:
  - On by default, 90% discount.
  - Two tiers, in-memory and durable. An entry expires no later than 24 hours after creation.
  - Put common content first.
  - **V** (G5)
- Explicit caching:
  - A `cachedContents` resource (`projects/{p}/locations/{l}/cachedContents/{id}`), referenced by `cachedContent` in the request.
  - Default TTL 60 minutes, minimum 1 minute, no maximum.
  - Discount: 90% (75% on Gemini 2.0).
  - Storage is billed.
  - **V** (G1, G5)
- Minimum tokens:
  - Gemini 3 and 4: 4,096.
  - Some Gemini 3.x Flash and Preview models: 6,144 (implicit only).
  - Gemini 2: 2,048.
  - **V** (G5)
- Usage field: `usageMetadata.cachedContentTokenCount`. **V** (G5)
- Caches are isolated by project number. **V** (G5)
- There is no routing key.

**7. Rate limits and retries**
- 429 messages: "Resource exhausted, please try again later." (pay-as-you-go) and "Too many requests. Exceeded the Provisioned Throughput." **V** (G6)
- Remedies: use the global endpoint, back off, use Provisioned Throughput. **V** (G6)
- No rate-limit headers are documented. Whether `Retry-After` is sent: **U**.

**8. Errors and request ID**
- Google RPC error shape `{"error":{"code":429,"message":"…","status":"RESOURCE_EXHAUSTED"}}`: **U**.
- Request-ID header: none documented. **U**

**9. End-user identifier**
- No end-user field. `labels` exists for billing and metadata. **V** (G1, as a field)
- Cache-isolation guidance: "scope cached prompt data and outputs to each user session". **V** (G5)

### 4b. Vertex OpenAI-compatible endpoint
- Base URL: `https://aiplatform.googleapis.com/v1/projects/{project}/locations/{location}/endpoints/openapi`, or the regional form `https://{location}-aiplatform.googleapis.com/…/endpoints/openapi`. **V** (G8, G1)
- Model names look like `google/gemini-3.5-flash`. Pass the OAuth access token as `api_key`. **V** (G8)
- `max_completion_tokens` is an "Alias for max_tokens". **V** (G7)
- `reasoning_effort` maps to `thinking_level` (`minimal`, `low`, `medium`, `high`); the older table maps low/medium/high to 1024/8192/24576 budgets. Use either `reasoning_effort` or `extra_body.google.thinking_config`, not both. **V** (G7, G8)
- `tool_choice`: `none` or `auto`; `required` maps to ANY; `validated` maps to VALIDATED (Google-specific). Tool parameters use an OpenAPI schema. **V** (G7)
- Unsupported parameters are ignored. Gemini-only features go in `extra_body` (`cached_content`, `thinking_config`). **V** (G7)

### 4c. Claude on Vertex
- URL: `POST https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/publishers/anthropic/models/{MODEL}:rawPredict`, or `:streamRawPredict` for streaming.
  - Global: `https://aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/global/publishers/anthropic/models/{MODEL}:rawPredict`.
  - Multi-region: `https://aiplatform.{us|eu}.rep.googleapis.com/...`.
  - **V** (G9, G10)
- Auth header: `Authorization: Bearer $(gcloud auth print-access-token)`. **V** (G9, G10)
- Request body:
  - `model` is not sent in the body.
  - `"anthropic_version": "vertex-2023-10-16"` goes in the body, not in a header.
  - Everything else follows the Messages API.
  - **V** (G10)
- Pricing: regional and multi-region endpoints cost 10% more than global. **V** (G10)
- Limits and features:
  - Payload limit 30 MB.
  - Prompt caching, thinking and tool use are supported.
  - Not supported: Batches, Files API, code execution and web fetch.
  - **V** (G10)
- Per-message effort beta works on Google Cloud. **V** (T)
- Beta flags passed as `anthropic_beta` in the body: **U** on Vertex. On Bedrock InvokeModel this is **V** (T: "send that value in the `anthropic_beta` array of the request body").
- Rate limits, errors and request ID on Vertex follow Google quotas: **U**.

---

## 5. AWS Bedrock

Sources:
- B1: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html
- B2: https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html
- B3: https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html
- B4: https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-mantle.html (Responses API)
- B5: https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html
- B6: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ToolChoice.html
- B7: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ToolResultBlock.html
- B8: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ToolSpecification.html
- B9: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html
- B10: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ReasoningTextBlock.html
- B11: https://platform.claude.com/docs/en/build-with-claude/claude-on-amazon-bedrock-legacy
- B12: https://platform.claude.com/docs/en/build-with-claude/claude-in-amazon-bedrock

**1. Auth and URLs**
- Converse:
  - `POST https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse`, or `/converse-stream`.
  - `modelId` is a base model ID, an inference profile (`us.` / `global.` prefixes), or an ARN.
  - **V** (B1, B2)
- Newer Claude models need an inference profile. A base model ID returns 400 "on-demand throughput isn't supported". **V** (B11)
- InvokeModel (Anthropic):
  - Body: the Messages shape with `"anthropic_version": "bedrock-2023-05-31"`, no `model` field, and betas in an `anthropic_beta` body array.
  - Path `/model/{modelId}/invoke` (and `invoke-with-response-stream`); the path itself is **U** (not quoted in the pages read).
  - **V** for the body (B11, T)
- Auth:
  - SigV4. **V** (B11, B12)
  - Bedrock API keys (bearer tokens) via `Authorization: Bearer <key>` or the `AWS_BEARER_TOKEN_BEDROCK` environment variable. **V** (B2)
    - Short-term keys last up to 12 hours.
    - Long-term keys belong to an IAM user, work only in their own region, and are meant for exploration only.
    - Permissions are controlled by `bedrock:CallWithBearerToken`.
    - **V** (B2)
- Messages-API endpoint ("Claude in Amazon Bedrock", Opus 4.7+):
  - `https://bedrock-mantle.{region}.api.aws/anthropic/v1/messages`.
  - Standard SSE streaming.
  - Headers `anthropic-version: 2023-06-01` plus SigV4 service `bedrock-mantle` (or a bearer token in `x-api-key`).
  - Model IDs like `anthropic.claude-opus-5-5`.
  - **V** (B12)
- OpenAI-compatible endpoints: yes.
  - Chat Completions: `https://bedrock-runtime.{region}.amazonaws.com/openai/v1/chat/completions` (recommended; SigV4 or API key), or `https://bedrock-mantle.{region}.api.aws/v1/chat/completions`.
  - Responses API: base `…/openai/v1` on bedrock-runtime, or `bedrock-mantle…/v1`.
  - `OPENAI_API_KEY` is set to the Bedrock API key.
  - **V** (B3, B4)

**2. Token limits**
- Converse: `inferenceConfig.maxTokens` (optional), plus `temperature`, `topP` and `stopSequences`. Model-specific parameters go in `additionalModelRequestFields` (for example `top_k`). **V** (B1)
- InvokeModel for Claude: `max_tokens` as in the Messages API. **V** (B11 example)

**3. Tool calling (Converse)**
- Tools: `toolConfig: {tools: [{toolSpec: {name, description, inputSchema: {json: …}, strict?}}], toolChoice}`. **V** for `name`, `description`, `inputSchema` and `strict` (B8); the `{json:…}` wrapper is **U**.
- `toolChoice` is a union: `auto` (default), `any`, or `tool` (`{name}`; "Only supported by Anthropic Claude 3 and Amazon Nova"). There is no `none`. **V** (B6)
- Results go back as user content `{toolResult: {toolUseId, content: [...], status: "success"|"error"}}`. `status` is supported for Claude 3/4 and Nova. **V** (B7)
- `stopReason` values: `end_turn | tool_use | max_tokens | stop_sequence | guardrail_intervened | content_filtered | malformed_model_output | malformed_tool_use | model_context_window_exceeded`. **V** (B1)
- No parallel-tool flag in Converse. For Claude, `disable_parallel_tool_use` would go through `additionalModelRequestFields`. **U**

**4. Streaming**
- ConverseStream uses AWS event-stream (binary), not SSE, with events `messageStart`, `contentBlockStart`, `contentBlockDelta`, `contentBlockStop`, `messageStop`, and `metadata` (carrying `usage` and `metrics`). **V** for the event names (B9).
- Stream exceptions: `internalServerException`, `modelStreamErrorException`, `serviceUnavailableException`, `throttlingException`, `validationException`. **V** (B9)
- Tool-input deltas arrive as `contentBlockDelta.delta.toolUse.input` string fragments. **U**
- The bedrock-mantle Messages endpoint uses standard Anthropic SSE. **V** (B12)

**5. Reasoning**
- Converse returns `reasoningContent.reasoningText {text, signature}` blocks. "If you pass a reasoning block back … include the text and its signature unmodified." **V** (B10)
- Enabling thinking through `additionalModelRequestFields: {"thinking": {...}}`: **U** (memory).
- InvokeModel and mantle: Anthropic `thinking` and `output_config` as in the Messages API. **V** (B11, T)

**6. Prompt caching**
- Two kinds: implicit (best effort; Nova and openai.gpt-5.x) and explicit (cache checkpoints). **V** (B5)
- Converse checkpoint: a `cachePoint` block, with `"ttl":"1h"` for one hour. InvokeModel for Claude uses `cache_control` (with `ttl`). The default TTL is 5 minutes and resets on each hit. **V** (B5)
- Minimum tokens per checkpoint: between 512 and 4,096 depending on model, counted cumulatively over the prefix (tools, then system, then messages).
  - For example, Claude Haiku/Sonnet/Opus 5.5: minimum 512, 4 checkpoints, 5-minute and 1-hour TTL, allowed in `system`, `messages` and `tools`.
  - **V** (B5)
- "Simplified cache management": automatic lookback of about 20 blocks. **V** (B5)
- Automatic top-level `cache_control` is not supported on legacy Bedrock. **V** (P, B11)
- OpenAI GPT-5.6-style models on Bedrock:
  - `prompt_cache_options.mode`, a 30-minute minimum TTL, 1,024 tokens per breakpoint.
  - Writes cost 1.25x.
  - Cached reads do not count toward ITPM.
  - Usage fields `cached_tokens` and `cache_write_tokens`.
  - **V** (B5)
- Converse usage fields: `usage.cacheReadInputTokens`, `cacheWriteInputTokens`, and `cacheDetails[{inputTokens, ttl}]`. **V** (B1)

**7. Rate limits and retries**
- Errors that call for backoff:
  - `ThrottlingException` (429).
  - `ModelNotReadyException` (429; the SDK retries it up to 5 times).
  - `ServiceUnavailableException` (503).
  - `ModelTimeoutException` (408).
  - Other errors: `ValidationException` 400, `AccessDeniedException` 403, `ResourceNotFoundException` 404, `ModelErrorException` 424, `InternalServerException` 500.
  - **V** (B1)
- No rate-limit headers are documented.
- Quotas on the mantle Claude endpoint: 2M input TPM by default; up to 5M input and 500k output without Anthropic approval. **V** (B12)

**8. Errors and request ID**
- Error body: AWS JSON `{"message":"…"}`, with the type in the `x-amzn-ErrorType` header: **U**.
- Request ID: `x-amzn-RequestId`. **U** for Bedrock runtime (seen only as `x-amzn-requestid` for Claude Platform on AWS, N).
- The mantle endpoint returns `request-id`. **V** (B12)

**9. End-user identifier**
- No end-user field in Converse. `requestMetadata` (up to 16 key-value pairs) is for filtering invocation logs. **V** (B1)
- Anthropic `metadata.user_id` passed through on InvokeModel or mantle: **U**.

---

## Cross-provider gotchas (from the verified items above)
- Retry hints differ:
  - OpenAI: `Retry-After` in seconds.
  - Azure: `retry-after-ms` in milliseconds.
  - Anthropic: `retry-after` in seconds; absent on the spend-cap 429.
  - Bedrock and Vertex: no documented header; use exponential backoff.
  - Anthropic also uses 529 for overload, where OpenAI uses 503 `server_is_overloaded`.
- Reset formats differ: OpenAI uses durations (`6m0s`), Anthropic uses RFC 3339 timestamps, and Azure uses numbers.
- Cached-token field names:
  - OpenAI Responses: `input_tokens_details.cached_tokens`.
  - OpenAI Chat and Azure: `prompt_tokens_details.cached_tokens`.
  - Anthropic: `cache_read_input_tokens` / `cache_creation_input_tokens`.
  - Bedrock: `cacheReadInputTokens` / `cacheWriteInputTokens`.
  - Gemini: `cachedContentTokenCount`.
- `input_tokens` means different things: Anthropic's excludes cached tokens, while OpenAI's `cached_tokens` is a subset of input or prompt tokens.
- Tool-choice values:
  - Forced use is `required` in OpenAI and `any` in Anthropic and Bedrock.
  - Bedrock Converse has no `none`.
  - Newest Claude models reject `any` and `tool`.
- Signatures must be sent back unchanged: Anthropic thinking `signature`, Gemini `thought_signature`, Bedrock `reasoningText.signature`, OpenAI `encrypted_content` reasoning items.

---

# Part 2. Gateways, hosted open-model providers and the OpenCode client


Research date: 2026-10-18. Sources are primary docs, read as the raw `.md` or OpenAPI files the doc sites publish, plus OpenCode source at commit `802f2d8e75098ae0b45ad839ce98a31589ea8102` (anomalyco/opencode, main).

Legend:
- **V** = verified, with the URL where I read it.
- **U** = unverified: not found in primary docs, or found only in third-party material.

---

## 1. OpenRouter

### Auth, base URL, API formats

- **V** Auth uses `Authorization: Bearer <OPENROUTER_API_KEY>`. Source: https://openrouter.ai/docs/api_reference/authentication.md
- **V** Base URL is `https://openrouter.ai/api/v1`. The OpenAPI `servers` entry says so. Source: https://openrouter.ai/docs/api/api-reference/chat/create-a-chat-completion.md
- **V** Three API formats are offered, all under `/api/v1`. Source: https://openrouter.ai/docs/_llms/api-reference.txt
  - Chat Completions (OpenAI compatible): `POST /chat/completions`
  - Responses API: `POST /responses`
  - Anthropic Messages: `POST /messages`
- **V** Anthropic-SDK clients (such as Claude Code) use `ANTHROPIC_BASE_URL=https://openrouter.ai/api` with the key sent as a bearer token. OpenRouter says to use `ANTHROPIC_AUTH_TOKEN`, not `x-api-key`. The Messages OpenAPI `securitySchemes` lists only HTTP bearer. Sources:
  - https://openrouter.ai/docs/cookbook/coding-agents/claude-code-integration.md
  - https://openrouter.ai/docs/api/api-reference/anthropic-messages/create-a-message.md
- **V** Attribution headers (source: https://openrouter.ai/docs/app-attribution.md):
  - `HTTP-Referer` is required to create an app page.
  - The display-name header is now **`X-OpenRouter-Title`**. The doc says: "`X-Title` is still supported for backwards compatibility."
  - Also available: `X-OpenRouter-Categories` (comma-separated, at most 2 per request, from a fixed list such as `cli-agent` or `ide-extension`; unknown values are dropped silently) and `X-OpenRouter-App-Visibility: hidden`, which only takes effect when an app is first created.
  - Apps on `localhost` must also send `X-OpenRouter-Title` to be tracked.
- **V** Optional request header `X-OpenRouter-Metadata: enabled` returns `openrouter_metadata` (routing info). The legacy name `X-OpenRouter-Experimental-Metadata` is still accepted. Source: the chat-completion OpenAPI above.
- **V** Anthropic beta features pass through via the `x-anthropic-beta` header, e.g. `interleaved-thinking-2025-05-14`. Source: https://openrouter.ai/docs/guides/routing/provider-selection.md

### max_tokens vs max_completion_tokens

- **V** Both are accepted.
  - `max_tokens` is described as "Maximum tokens (deprecated, use max_completion_tokens). Note: some providers enforce a minimum of 16."
  - `max_completion_tokens` is described as "Maximum tokens in completion."
  - Source: chat-completion OpenAPI.
- **V** Reasoning tokens count against `max_tokens`/`max_completion_tokens`; the two share one budget. Source: https://openrouter.ai/docs/guides/best-practices/reasoning-tokens.md
- **V** Routing effect: if you set `max_tokens`, OpenRouter only routes to providers that support a response of that length. Source: provider-selection.md

### Tool calling

- **V** `ChatToolChoice` accepts `"none"`, `"auto"`, `"required"`, a named function `{"type":"function","function":{"name":...}}`, or a server-tool choice. Source: chat-completion OpenAPI.
  - `"any"` is not in the schema.
- **V** `parallel_tool_calls` is a boolean, "default is true for most models". Source: https://openrouter.ai/docs/guides/features/tool-calling.md
- **V** Function `name` must match a-z, A-Z, 0-9, `_` or `-`, at most 64 characters. Tools also accept `cache_control` and `defer_loading`. Source: OpenAPI `ChatFunctionTool`.
- **V** Requests that include `tools` or `tool_choice` are routed, best effort, to providers that support tools. `tools`, `response_format` and `verbosity` act as soft routing preferences even when `require_parameters` is false. Source: provider-selection.md

### Streaming

- **V** Streaming is SSE (source: https://openrouter.ai/docs/api_reference/streaming.md):
  - Comment keep-alive lines `: OPENROUTER PROCESSING` are sent. The doc warns to skip lines starting with `:` before calling `JSON.parse`.
  - The stream ends with `data: [DONE]`.
- **V** Every Chat Completions stream ends with an extra usage chunk just before `[DONE]` (same source).
  - This deviates from OpenAI on purpose: the chunk carries one choice with an empty `delta` that repeats `finish_reason`, instead of an empty `choices` array.
  - So `finish_reason` appears twice; treat the usage chunk as an accounting frame.
- **V** `stream_options.include_usage` is deprecated: "This field has no effect. Full usage details are always included." Source: OpenAPI `ChatStreamOptions`.
- **V** In the other formats, Responses reports usage in `response.completed` and Messages reports it in `message_delta`. Source: streaming.md
- **V** Stream cancelation by aborting the connection stops billing only on some providers (source: streaming.md):
  - Supported: DeepInfra and Together.
  - Not supported: Groq and Mistral.

### Reasoning

- **V** The unified `reasoning` object has these fields (sources: https://openrouter.ai/docs/guides/best-practices/reasoning-tokens.md and the OpenAPI):
  - `effort`: one of `max`, `xhigh`, `high`, `medium`, `low`, `minimal`, `none`.
  - `max_tokens`: a token budget for reasoning.
  - `exclude`: boolean; the model still reasons but the trace is not returned.
  - `enabled`: boolean; `true` means medium effort.
  - Also `summary`, `context` (`auto` / `all_turns` / `current_turn`, GPT-5.6+ only) and `mode` (`standard` / `pro`).
- **V** Top-level `reasoning_effort` is shorthand for `reasoning.effort`; sending conflicting values is an error.
- **V** `effort` and `max_tokens` are mutually exclusive in the unified parameter. The Messages API section says: "accepts either `effort` or `max_tokens` but not both."
- **V** For Anthropic models, `max_tokens` must be strictly higher than the reasoning budget.
- **V** Effort-to-budget mapping is about 95% of max_tokens for max/xhigh, 80% for high, 50% for medium, 20% for low and 10% for minimal.
- **V** The reasoning trace comes back in `message.reasoning` (string) and `message.reasoning_details` (array). In streams these are under `delta.*`.
  - `reasoning_details` items have type `reasoning.summary`, `reasoning.encrypted` or `reasoning.text`, plus `id`, `format` (e.g. `anthropic-claude-v1`, `openai-responses-v1`, `google-gemini-v1`) and `index`. Text items also carry `signature`.
- **V** To preserve reasoning across turns, pass back either `reasoning` (or its alias `reasoning_content`) or the full `reasoning_details` on the assistant message.
  - `reasoning_details` is required for encrypted or summarized types, and is important for tool-calling continuity.
  - The doc says: "the entire sequence of consecutive reasoning blocks must match the outputs generated by the model during the original request; you cannot rearrange or modify the sequence."
- **V** Per-model capabilities are listed in `GET /api/v1/models` under `reasoning`: `supported_efforts`, `default_effort`, `default_enabled`, `supports_max_tokens` and `mandatory`.
- **V** Changing effort mid-request invalidates the prompt cache. Some models (Claude Fable 5.1, GPT-6 Astra, Claude Opus 5) accept an in-history effort update instead. In Chat Completions that is a content-less system message.

### Prompt caching

All items in this subsection come from https://openrouter.ai/docs/guides/best-practices/prompt-caching.md

**Automatic caching (no request change needed):**
- **V** OpenAI: minimum 1024 tokens. Writes are free before GPT-5.6 and cost 1.25x on GPT-5.6+. Reads cost 0.25x or 0.5x.
  - GPT-5.6+ also supports explicit `prompt_cache_breakpoint` per block and root-level `prompt_cache_options {mode:"explicit", ttl:"30m"}`, with a minimum 30-minute TTL.
- **V** Grok: reads cost 0.25x.
- **V** Moonshot: reads cost 0.25x.
- **V** Groq: reads cost 0.5x; currently Kimi K2 only.
- **V** DeepSeek: writes cost 1x, reads 0.1x.
- **V** Z.AI: reads cost about 0.2x.
- **V** Gemini 2.5+ implicit caching: TTL averages 3–5 minutes. Minimum 1024 tokens for 2.5 Flash and 4096 for 2.5 Pro.

**Explicit `cache_control` caching:**
- **V** Anthropic offers two modes:
  - Top-level `cache_control: {type:"ephemeral"}` gives automatic caching on the Anthropic, Vertex, Azure and Bedrock providers.
  - Per-block breakpoints, at most 4.
- **V** Anthropic TTL is 5 minutes by default, or `"ttl":"1h"`. Writes cost 1.25x (5m) or 2x (1h); reads cost 0.1x.
- **V** Anthropic minimum cacheable length depends on the model: 512, 1024, 2048 or 4096 tokens. The doc gives a per-model list.
- **V** Gemini explicit caching: any number of `cache_control` breakpoints is allowed, but only the last one is used. The write TTL is 5 minutes and does not refresh.
- **V** Alibaba Qwen requires explicit `cache_control` with a 5-minute TTL.
- **V** Marker translation: Anthropic-style `cache_control` and OpenAI `prompt_cache_breakpoint` are translated into each other. TTLs are not translated.

**Usage fields:**
- **V** `usage.prompt_tokens_details.cached_tokens` and `.cache_write_tokens`, plus `cache_discount`. On the Responses API they are under `usage.input_tokens_details`.

**Sticky provider routing:**
- **V** Sticky routing is tracked per account, per model and per conversation.
  - By default the conversation key is a hash of the first system/developer message plus the first non-system message.
  - Stickiness expires after **10 minutes** of inactivity.
  - It only applies when cache reads are cheaper than normal input.
  - It is disabled when `provider.order` is set.
  - If the sticky provider errors, the entry is not updated.

**Session identifier (answer to the specific question):**
- **V** `session_id` exists as a top-level body field, or as the **`x-session-id`** HTTP header. Maximum 256 characters. The body value wins if both are sent.
  - It becomes the sticky-routing key.
  - With `session_id` set, stickiness starts on the first successful request. Without it, stickiness only starts after a cache hit is seen.
  - It also groups requests in the Logs "Sessions" view.
  - Non-chat endpoints (embeddings, images, audio and so on) accept only the header, and use it for grouping only.
- **V** If neither `session_id` nor `x-session-id` is sent, OpenRouter falls back to **`prompt_cache_key`** as the sticky key.
- **V** For Z.AI, OpenRouter sends a session-affinity key upstream, derived from the account plus `session_id`.
- **V** `user` is described as a "Per-end-user identifier for abuse isolation". It is hashed into an upstream identity and never forwarded raw. Nothing in the docs says it affects caching or routing. Source: OpenAPI `ChatRequest.user`.
- **V** `metadata` (at most 16 key/value pairs) is a separate field.

### Provider routing: the `provider` object

All items below come from https://openrouter.ai/docs/guides/routing/provider-selection.md

**Fields:**

| Field | Type / default | Meaning |
| - | - | - |
| `order` | string[] | Provider slugs to try, in order. Disables load balancing. Outranks BYOK priority. |
| `allow_fallbacks` | bool, default `true` | When false, only the listed or top providers are used. |
| `require_parameters` | bool, default `false` | Only route to providers that support every parameter in the request. |
| `data_collection` | `"allow"` (default) or `"deny"` | `"deny"` keeps the request off providers that store or train on data. |
| `zdr` | bool | Zero-data-retention endpoints only. Combined as OR with account and guardrail ZDR settings. |
| `enforce_distillable_text` | bool | Only models that allow text distillation. |
| `only` | string[] | Allowlist of provider slugs. |
| `ignore` | string[] | Denylist of provider slugs. |
| `quantizations` | string[] | Filter by quantization level (values below). |
| `sort` | `"price"`, `"throughput"`, `"latency"`, or `{by, partition: "model" \| "none"}` | Explicit sort; disables load balancing. |
| `preferred_min_throughput` | number, or `{p50,p75,p90,p99}` | Soft preference. |
| `preferred_max_latency` | number, or `{p50,p75,p90,p99}` | Soft preference. |
| `max_price` | `{prompt, completion, request, image}` | In $ per million tokens. |

- **V** Accepted quantization values: `int4`, `int8`, `fp4`, `mxfp4`, `nvfp4`, `fp6`, `fp8`, `mxfp8`, `fp16`, `bf16`, `fp32`, `unknown`.
- **V** Slugs have the form `provider[/variant][/region][/tier]`, e.g. `deepinfra/turbo` or `google-vertex/us-east5`. A base slug matches all of that provider's variants.
- **V** Default strategy: skip providers with outages in the last 30 seconds, then pick by inverse-square price weighting, keeping the rest as fallbacks.
- **V** Shortcuts: `:nitro` sorts by throughput and `:floor` sorts by price.
- **V** `models` is a fallback array of model IDs tried in order. Any error can trigger a fallback (context length, moderation, rate limit, downtime). Billing uses the model actually used, returned in `model`. The Messages API accepts `fallbacks` instead. Source: https://openrouter.ai/docs/guides/routing/model-fallbacks.md
- **V** Status of `transforms`:
  - It is not in the current `ChatRequest` OpenAPI schema.
  - Its replacement is `plugins: [{ "id": "context-compression" }]` (middle-out truncation).
  - Compression is on by default for endpoints with 8k context or less; disable it with `{"id":"context-compression","enabled":false}`.
  - Source: https://openrouter.ai/docs/guides/features/message-transforms.md
  - **U** Whether the legacy `transforms: ["middle-out"]` field is still accepted.

### Rate limits, errors, request IDs

- **V** Error body shape is `{"error": {"code": number, "message": string, "metadata"?: {...}}}`. Source: https://openrouter.ai/docs/api_reference/errors-and-debugging.md
  - Codes: 400, 401, 402 (credits), 403 (moderation, guardrail or budget), 408, 429, 502 (model down or invalid response), 503 (no provider meets the routing requirements).
  - `metadata` can include `error_type`, `provider_name`, `provider_code`, `raw`, `reasons`, `flagged_input`, `limit_source` and `resets_at`.
- **V** Errors after the response is committed (source: errors-and-debugging.md and streaming.md):
  - In a stream, the HTTP status stays 200 and the error arrives as an SSE `data:` event with a top-level `error` field and `choices[0].finish_reason: "error"`. The stream then ends.
  - The error can be the first and only event.
  - Example: `{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_exceeded"}},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`
  - Non-streaming requests can also return 200 with a body that contains only `error` and no `choices`.
- **V** A `Retry-After` header may be sent on 429 and 503, and on 402 when `limit_source` is `openrouter_in_flight_budget`. Sources: errors-and-debugging.md and https://openrouter.ai/docs/api_reference/limits.md
- **V** `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset` appear **only on OpenRouter's own 429 error responses**, never on successful responses. Check remaining quota with `GET /api/v1/key`. Source: limits.md
- **V** Free models (`:free`) allow 20 RPM, and 50 RPD (under $10 purchased) or 1000 RPD.
- **V** The request/generation ID is returned in the **`X-Generation-Id`** response header on all endpoints. Source: streaming.md

---

## 2. DeepInfra

### Auth, base URL, API formats

- **V** Auth uses `Authorization: Bearer $DEEPINFRA_API_KEY`. Scoped JWTs (`jwt:...`) work the same way. Source: https://docs.deepinfra.com/account/authentication.md
- **V** The OpenAI-compatible base URL is `https://api.deepinfra.com/v1/openai`, so chat is at `.../v1/openai/chat/completions`. Source: https://docs.deepinfra.com/chat/overview.md
  - The API reference also documents `POST https://api.deepinfra.com/v1/chat/completions`, which accepts optional `x-api-key`, `x-deepinfra-source` and `x-deepinfra-service-tier` headers. Source: https://docs.deepinfra.com/api-reference/chat-completions/openai-chat-completions.md
- **V** An Anthropic Messages endpoint exists at `POST https://api.deepinfra.com/anthropic/v1/messages`, plus `/count_tokens`. Source: https://docs.deepinfra.com/integrations/anthropic.md
  - It accepts `Authorization: Bearer` or `x-api-key`, plus `anthropic-version` and `anthropic-beta`.
  - The doc notes: "Not all Anthropic-specific features may be supported."
- **U** A Responses API: none was found in the docs index.

### max_tokens vs max_completion_tokens

- **V** `max_tokens` is the documented parameter. `max_completion_tokens` does not appear in the chat OpenAPI.
- **V** Most models have a hard cap of 16384 output tokens.
- Source: chat OpenAPI and overview.md

### Tool calling

- **V** The OpenAPI `tool_choice` enum is `none`, `auto` or `required`, or a specific function tool. Source: chat OpenAPI.
- **V** The feature table lists "Parallel tool calls" as supported, "(quality may vary)", and support in streaming mode. Source: https://docs.deepinfra.com/chat/tool-calling.md
- **U** `parallel_tool_calls` as a request parameter: it is not in the OpenAPI.

### Streaming

- **V** SSE `data:` lines terminated by `data: [DONE]`. Source: https://docs.deepinfra.com/chat/streaming.md
- **V** "The final chunk before `[DONE]` contains usage information." Source: streaming.md
- **V** `stream_options.include_usage` defaults to **true**; `continuous_usage_stats` is also available. Source: chat OpenAPI `StreamOptions`.

### Reasoning

- **V** Parameters (source: https://docs.deepinfra.com/chat/reasoning.md and the OpenAPI):
  - `reasoning_effort`. The docs list `none`, `low`, `medium`, `high`; the OpenAPI enum also includes `minimal`, `xhigh`, `max`.
  - `reasoning: {effort, enabled}`. `enabled:false` is the same as `"none"`.
  - `chat_template_kwargs` is also accepted.
- **U** The response field name for the trace. OpenCode's DeepInfra adapter reads `reasoning_content` (see section 6), but I did not find this in DeepInfra's docs.

### Prompt caching

- **V** Caching is automatic prefix caching. Source: https://docs.deepinfra.com/chat/prompt-caching.md
  - Caches are per model and per account, and entries "expire after a period of inactivity". No TTL or minimum is published.
  - Cached tokens are reported in `usage.prompt_tokens_details.cached_tokens`.
- **V** `prompt_cache_key` is an optional body field. DeepInfra recommends a session-scoped value such as `userid-chatsessionid`. Requests with the same key and model share a KV cache "even if their prompt prefixes aren't byte-for-byte identical". Source: prompt-caching.md
- **V** Explicit retention (source: https://docs.deepinfra.com/chat/prompt-cache-retention.md):
  - Send `prompt_cache_options: {mode:"explicit", ttl:"5m"|"1h"}` together with `prompt_cache_key`, and optionally `prompt_cache_breakpoint: {mode:"explicit"}` on a content part.
  - Pricing: writes cost 1.25x (5m) or 2x (1h); reads are billed at the model's cache-read rate.
  - Cached amounts are rounded to model granularity (8,192 tokens on Nemotron-3-Ultra).
  - `usage.prompt_tokens_details.cache_write_tokens` is reported.
  - Only supported on `nvidia/NVIDIA-Nemotron-3-Ultra-550B-A55B` and `moonshotai/Kimi-K2.7-Code`.
- **V** `user` is an end-user ID for abuse monitoring; hashing is recommended. Source: chat OpenAPI.

### Rate limits and errors

- **V** The default limit is **200 concurrent requests per model**; it is not an RPM limit. Exceeding it returns HTTP 429 "Rate limited". A 429 can also occur when a model is busy. Source: https://docs.deepinfra.com/account/rate-limits.md
  - **U** Rate-limit or `Retry-After` headers are not documented.
- **V** Errors use the OpenAI shape, e.g. `{"error":{"message":"Model busy, retry later","type":"invalid_request_error","param":null,"code":"engine_overloaded"}}`. That example is the 429 for `fail_fast:true`. Source: overview.md
- **U** A request-ID header: not documented.

---

## 3. Together AI

### Auth, base URL, API formats

- **V** Auth is bearer. Base URL is `https://api.together.ai/v1`. Source: https://docs.together.ai/docs/inference/openai-compatibility.md
  - The OpenAPI also lists `https://api-inference.together.ai/v2` as an "Optimized environment for inference". Source: https://docs.together.ai/openapi.yaml
- **V** API formats: OpenAI Chat Completions and legacy Completions are supported. Assistants, Threads, Runs, the OpenAI-shaped batches/files API and moderations are not. Source: openai-compatibility.md
- **U** Responses API or an Anthropic Messages endpoint: neither appears in the compatibility matrix or the docs index.
- **V** The parameters `service_tier`, `store`, `metadata` and `prediction` are accepted but ignored. Source: openai-compatibility.md

### max_tokens vs max_completion_tokens

- **V** `max_tokens` is the documented field. `max_completion_tokens` is not in the OpenAPI. Sources: OpenAPI and https://docs.together.ai/docs/inference/chat/parameters.md
- **V** Input tokens plus `max_tokens` above the context length gives an error; the error-code page lists it under "403 - Bad Request". Source: https://docs.together.ai/docs/error-codes.md

### Tool calling

- **V** `tool_choice` accepts `"auto"` (default), `"none"`, `"required"`, or `{type:"function", function:{name}}`. Source: https://docs.together.ai/docs/inference/function-calling/single-call.md
- **V** Parallel calls are documented as model behavior. Source: https://docs.together.ai/docs/inference/function-calling/parallel.md
- **U** `parallel_tool_calls` as a request parameter: it is not in the OpenAPI.
- **V** For DeepSeek V3.1, function calling only works with reasoning disabled. Source: https://docs.together.ai/docs/inference/chat/reasoning.md

### Streaming

- **V** SSE chunks of `chat.completion.chunk`, each carrying a `usage` field (null in the docs example). Source: https://docs.together.ai/docs/inference/chat/overview.md
- **U** The `data: [DONE]` terminator, usage on the final chunk, and `stream_options` support: none are documented, and `stream_options` is not in the OpenAPI.

### Reasoning

- **V** Controls (source: https://docs.together.ai/docs/inference/chat/reasoning.md):
  - `reasoning: {enabled: true|false}` for hybrid models (DeepSeek V4 Pro, GLM-5.2, Kimi K3, Qwen3.6 Plus, MiniMax M3, ...).
  - `reasoning_effort` `low`/`medium`/`high` (GPT-OSS).
  - `chat_template_kwargs` as an alternative toggle.
- **V** The trace comes back in `reasoning` or `reasoning_content`, depending on the model. DeepSeek-R1 instead uses `<think>` tags inside `content`.
- **V** Preserved thinking: set `chat_template_kwargs` with `clear_thinking: false`, and pass `reasoning_content` back unchanged under the same key. Blocks "must exactly match the original sequence".

### Prompt caching

- **V** Caching is automatic, with no parameter or header needed to turn it on. Source: https://docs.together.ai/docs/inference/chat/prompt-caching.md
  - Entries "typically last about an hour", best effort, and may be evicted sooner under load.
  - Minimum length and granularity vary by model and are not published.
  - There is no charge to write; reads are billed at the "Cached input pricing" rate.
  - Cached tokens still count toward rate limits.
- **V** `prompt_cache_key` is a top-level body field that routes requests with the same key as a group to raise hit odds. On dedicated endpoints it also acts as the sticky routing key. It is not in the OpenAPI schema, but the guide documents it. Source: prompt-caching.md
- **V** Reasoning parameters are rendered into the prefix, so change them only between conversations. Source: prompt-caching.md
- **V** The usage field location varies by model (source: openai-compatibility.md):
  - Reasoning models use `usage.prompt_tokens_details.cached_tokens` and `usage.completion_tokens_details.reasoning_tokens`.
  - Some non-reasoning models put `usage.cached_tokens` flat. Read both locations.

### Rate limits and errors

- **V** Serverless rate limits are dynamic: 429 or 503 under high demand, and the guidance is exponential backoff. Source: https://docs.together.ai/docs/serverless/rate-limits.md
  - **U** Rate-limit or `Retry-After` headers are not documented.
- **V** Errors use the shape `{ "error": { "message", "type", "code" } }`, with values specific to Together. Source: openai-compatibility.md
- **V** Status codes: 400, 401, 402, 403 (context exceeded), 404, 413, 429, 500, 503, 504, 524, 529. Source: error-codes.md
- **U** A request-ID header: not documented.

---

## 4. Groq

### Auth, base URL, API formats

- **V** Auth uses `Authorization: Bearer $GROQ_API_KEY`. Base URL is `https://api.groq.com/openai/v1`. Sources: https://console.groq.com/docs/openai.md and https://console.groq.com/docs/api-reference.md
- **V** OpenAI Chat Completions is supported, and so is a Responses API. Source: https://console.groq.com/docs/responses-api.md
- **U** An Anthropic Messages endpoint: none in the docs index.
- **V** Fields that return 400: `logprobs`, `logit_bias`, `top_logprobs`, `messages[].name`. `n` must be 1.
- **V** `temperature: 0` is converted to 1e-8.
- Source for the last two items: openai.md

### max_tokens vs max_completion_tokens

- **V** `max_completion_tokens` is current. `max_tokens` is marked "Deprecated in favor of `max_completion_tokens`" but is still listed. Source: api-reference.md

### Tool calling

- **V** `tool_choice` accepts `none`, `auto`, `required`, or `{"type":"function","function":{"name":...}}`. The default is `none` without tools and `auto` with tools. Source: api-reference.md
- **V** `parallel_tool_calls` is a boolean, default true. Source: api-reference.md
- **V** At most 128 functions. Source: api-reference.md
- **V** `disable_tool_validation` is a Groq extension. Source: api-reference.md
- **V** With reasoning models, tool use or JSON mode requires `reasoning_format` `parsed` or `hidden`; `raw` returns a 400. Source: https://console.groq.com/docs/reasoning.md

### Streaming

- **V** Data-only SSE "terminated by a `data: [DONE]` message". `stream_options` exists, but the docs do not expand its sub-properties. Source: api-reference.md
- **U** Whether `include_usage` is honored, and whether usage arrives in `x_groq.usage` on the last chunk. Historically it did, but the current reference only says `x_groq` is "Groq-specific metadata" and shows `x_groq.id` (e.g. `req_01jbd...`).

### Reasoning

- **V** Parameters (source: https://console.groq.com/docs/reasoning.md and api-reference.md):
  - `reasoning_format` takes `parsed` (separate `message.reasoning` field), `raw` (`<think>` tags in content) or `hidden`.
  - `include_reasoning` is a boolean that is **mutually exclusive** with `reasoning_format`.
  - The `reasoning_effort` enum is `none, default, minimal, low, medium, high, xhigh, max`, with per-model support:
    - GPT-OSS: `low`/`medium`/`high`, default `medium`.
    - Qwen3.8-27B: `none`, `default`, `low`, `medium`, `high`, default `none`; `high` maps to native `xhigh`.
    - Muse Spark Open: `low` to `xhigh`, default `xhigh`, and `none` is not allowed.
  - Values outside a model's set return 400.

### Prompt caching

- **V** Caching is automatic prefix caching with no code changes (source: https://console.groq.com/docs/prompt-caching.md):
  - Cached input gets a 50% discount.
  - "All cached data automatically expires after 2 hours without use."
  - Minimum prefix is 128 to 1024 tokens depending on the model.
  - Supported models are listed as `openai/gpt-oss-20b`, `openai/gpt-oss-120b` and `openai/gpt-oss-safeguard-20b`. OpenRouter's docs separately mention Kimi K2 on Groq.
  - Cached tokens are reported in `usage.prompt_tokens_details.cached_tokens`.
  - Cached tokens do not count towards rate limits.
- **V** No session-key parameter: `prompt_cache_key` is not in the chat reference. `user` exists, for abuse monitoring only. Source: api-reference.md

### Rate limits and errors

- **V** Rate-limit headers (source: https://console.groq.com/docs/rate-limits.md):
  - `retry-after` (seconds, only on 429).
  - `x-ratelimit-limit-requests`, `x-ratelimit-remaining-requests` and `x-ratelimit-reset-requests` (these refer to **RPD**).
  - `x-ratelimit-limit-tokens`, `x-ratelimit-remaining-tokens` and `x-ratelimit-reset-tokens` (these refer to **TPM**). Reset values look like `2m59.56s`.
  - Limits are per organization.
- **V** Error shape is `{"error":{"message":..., "type":"invalid_request_error"}}`. Source: https://console.groq.com/docs/errors.md
  - Codes include 400, 401, 403, 404, 413, 422, 424 (remote MCP), 429, 498 (flex capacity exceeded), 499 (cancelled), 500, 502, 503.
- **V** The request ID appears in the body as `x_groq.id`. Source: api-reference.md
  - **U** A request-ID response header.

---

## 5. Mistral (La Plateforme)

### Auth, base URL, API formats

- **V** Auth uses `Authorization: Bearer $MISTRAL_API_KEY`. Base URL is `https://api.mistral.ai`, with `/v1/chat/completions`. Sources:
  - https://docs.mistral.ai/studio/conversations/advanced/prompt-caching.md
  - https://docs.mistral.ai/openapi.yaml (`servers` and `ApiKey` bearer)
  - https://docs.mistral.ai/resources/error-glossary.md
- **V** Endpoints: Mistral's own chat format, which is close to OpenAI, plus `/v1/fim/completions`, `/v1/agents/completions` and `/v1/conversations`. Source: openapi.yaml paths.
  - There is no `/v1/responses` and no Anthropic `/v1/messages`.

### max_tokens vs max_completion_tokens

- **V** Only `max_tokens` (plus `min_tokens`). `max_completion_tokens` does not appear anywhere in the OpenAPI. Source: openapi.yaml `ChatCompletionRequest`.

### Tool calling

- **V** `ToolChoiceEnum` is `["auto","none","any","required"]`. The OpenAPI says "`any` or `required` means the model must call one or more tools". A named `{"type":"function","function":{"name":...}}` is also accepted. Source: openapi.yaml
- **V** `parallel_tool_calls` defaults to true. Source: openapi.yaml
- **V** Message ordering: an assistant message with several tool calls is followed by one `tool` message per call, then the assistant reply. Source: https://docs.mistral.ai/studio/conversations/function-calling.md
- **V** `ToolMessage` has `content`, optional `tool_call_id` and `name`. Source: openapi.yaml
- **V** Function names must match `^[a-zA-Z0-9_-]{1,64}$`. Source: openapi.yaml
- **V** At most 128 tools. Calls made in parallel may come back in any order. Source: https://docs.mistral.ai/resources/known-limitations.md
- **V** `AssistantMessage.prefix: true` is supported for prefill. Source: openapi.yaml
- **U, but strongly corroborated** Tool call IDs must be exactly 9 characters from `[a-zA-Z0-9]`. Mistral's OpenAPI and docs do not state this. Corroboration:
  - the Mistral error text quoted in https://github.com/vercel/ai/issues/11802 (third party)
  - vLLM's Mistral tool parser
  - OpenCode's own normalizer `valid = /^[A-Za-z0-9]{9}$/`, which hashes other IDs to 9 characters. Source: https://github.com/anomalyco/opencode/blob/802f2d8e75098ae0b45ad839ce98a31589ea8102/packages/ai/src/protocols/utils/mistral-tool-id.ts

### Streaming

- **V** Data-only SSE "terminated by a data: [DONE] message". Source: openapi.yaml `stream` description.
- **V, but conflicting** `known-limitations.md` says "`stream_options.include_usage` must be explicitly set to receive token usage in stream events", yet `stream_options` is **not** in the chat OpenAPI schema.
- **V** Streams time out after 10 minutes of inactivity.
- **U** Whether usage arrives on the final chunk by default.

### Reasoning

- **V** `reasoning_effort` enum is `none, minimal, low, medium, high, xhigh` in the OpenAPI. Source: https://docs.mistral.ai/studio/conversations/reasoning.md
  - Docs for `mistral-small-latest`, `mistral-medium-3-5` and `mistral-large-4-0` describe only `high` and `none`.
  - `zai-glm-5-3` accepts `low`, `high` and `max`.
- **V** With `"high"`, `message.content` becomes a list of chunks: `{type:"thinking", thinking:[TextChunk...]}` followed by `{type:"text"}`. With `"none"`, content is a plain string. Source: reasoning.md
- **V** Magistral (`magistral-small-latest`, `magistral-medium-latest`) is **deprecated**; use `reasoning_effort` instead. It used `prompt_mode: "reasoning"` (the enum has only `reasoning`; null opts out of the default system prompt). `prompt_mode` remains in the OpenAPI. Source: https://docs.mistral.ai/resources/deprecated/native-reasoning.md

### Prompt caching

- **V** Caching is prefix caching, and `prompt_cache_key` raises hit odds without guaranteeing a hit. Source: https://docs.mistral.ai/studio/conversations/advanced/prompt-caching.md
  - Use a stable conversation, session or workflow ID.
  - Cached tokens cost 10% of the input price.
  - They are reported in `usage.prompt_tokens_details.cached_tokens`.
  - Cache blocks are 64 tokens, so prompts under 64 tokens never hit and `cached_tokens` is a multiple of 64.
  - **U** TTL: not published.

### Rate limits and errors

- **V** Limits are per organization; RPS and TPM are enforced independently. Source: known-limitations.md
  - The docs tip says to check the `X-RateLimit-Remaining` header.
  - The 429 entry says to "Check the `Retry-After` response header". Source: error-glossary.md
  - **U** The full header set.
- **V** Status codes: 400, 401, 402, 403, 404, 422 (field-level validation), 429, 500, 502, 503, 504. Source: error-glossary.md
  - **U** The exact error body shape for non-422 errors.
- **U** A request-ID header.

---

## 6. OpenCode (anomalyco/opencode, formerly sst/opencode)

The repository layout changed: the old `packages/opencode/src/provider/` is gone. Request logic now lives in `packages/core` (sessions and plugins) and `packages/ai`, OpenCode's own AI layer. All links below are pinned to commit `802f2d8e75098ae0b45ad839ce98a31589ea8102` (2026-10-18), with base URL `https://github.com/anomalyco/opencode/blob/802f2d8e75098ae0b45ad839ce98a31589ea8102/`.

### User-Agent

- **V** The format is `opencode/${channel}/${version}/${name}`, with `name` defaulting to `"opencode"`. Source: `packages/core/src/app.ts` L24-26.

### Headers sent on every session model request, to all providers

- **V** Source: `packages/core/src/session/model-request.ts` L278-295. The headers are placed in `LLM.request({ http: { headers } })`.
- **V** The generic HTTP transport merges `request.http.headers` into every provider call, with no per-provider filter. Source: `packages/ai/src/route/transport/http.ts` L64-67.
- The headers:
  - `x-opencode-session-id: <session.id>`
  - `x-opencode-parent-session-id` and `x-parent-session-id`, only for child sessions
  - `x-session-affinity: <affinity>`
  - `X-Session-Id: <affinity>`. OpenRouter documents `x-session-id` (case-insensitive) as its sticky-routing key, so in effect this gives OpenRouter session-sticky routing.
  - `User-Agent: opencode/...`
  - `x-opencode-project`, `x-opencode-session: <affinity>`, `x-opencode-client: <app.name>`
- **V** The affinity value is the parent session ID for child sessions, the source session ID for forks, and otherwise the session's own ID. Source: `packages/core/src/session/affinity.ts`.

### What the session ID is

- **V** Session IDs are `"ses_"` + 12 hex characters (a descending timestamp/counter) + 14 random base62 characters from `crypto.getRandomValues`. So the ID is partly time-based and partly random, created once per session, not per request. Sources: `packages/schema/src/session-id.ts` and `packages/schema/src/identifier.ts`.

### `prompt_cache_key` derivation and gating

- **V** The request also carries `promptCacheKey` set to the affinity. The code is `/^ses_[0-9a-f]{64}$/.test(affinity) ? affinity.slice(4) : affinity`; normal IDs do not match the regex, so the full `ses_...` string is used. Source: model-request.ts L294.
- **V** For a body `prompt_cache_key`, the key is truncated to 64 characters. It is only sent when `request.cache !== "none"` and the model's compatibility does not disable it. Source: `packages/ai/src/protocols/shared.ts` L68-82.
- **V** On the OpenAI Chat protocol, `prompt_cache_key` is **off by default**. It is only sent when `compatibility.supportsPromptCacheKey` is true. The code comment says strict providers return 400 on unknown fields, and header-based affinity is unaffected. Source: `packages/ai/src/protocols/openai-chat.ts` L787-800.

### Per-provider behavior

| Provider | Body `prompt_cache_key` | Other | Source |
| - | - | - | - |
| OpenRouter | Yes (`supportsPromptCacheKey: true`) | Attribution headers `HTTP-Referer: https://opencode.ai/` and **`X-Title: opencode`** (the legacy header, not `X-OpenRouter-Title`) | `packages/ai/src/providers/openrouter.ts` L199; `packages/core/src/plugin/provider/openrouter.ts` L12 |
| DeepInfra | Yes | `maxTokensField: "max_tokens"`, `reasoningField: "reasoning_content"` | `packages/ai/src/providers/deepinfra.ts` L50-56 |
| Groq | No | `maxTokensField: "max_completion_tokens"`, `reasoningField: "reasoning"`; `user` only if configured | `packages/ai/src/providers/groq.ts` L92-98 |
| Together | No | `maxTokensField: "max_tokens"`; base URL `https://api.together.xyz/v1`, not the `api.together.ai` host in current docs | `packages/ai/src/providers/togetherai.ts` L10, L47 |
| Mistral | Yes, by default (`sendsPromptCacheKey` unless set false) | `max_tokens`; maps `tool_choice` `required` to `"any"`; tool IDs normalized to 9 alphanumeric characters | `packages/ai/src/protocols/mistral-chat.ts` L402, L420-422, L29 |

- **V** For OpenRouter, OpenCode does not put `session_id` in the body by default. `session_id` appears only in the experimental evaluation options type. The body defaults to `usage: {include: true}`.
- **V** In Mistral-on-Vertex, `prompt_cache_key` is stripped because Vertex rejects it. Source: `packages/ai/src/providers/google-vertex-mistral.ts`.
- **V** Other providers that send `prompt_cache_key` in the body: OpenAI, Azure, Fireworks, Moonshot, Cerebras, DigitalOcean and Meta (ai package). The `PromptCacheKeyPlugin` also adds a fixed list: abliteration-ai, aihubmix, anyapi, auriko, edenai, jalapeno, llama, morph, snowflake-cortex, tencent-tokenhub, upstage, venice. Source: `packages/core/src/plugin/provider/prompt-cache-key.ts`.
- **V** Other provider-specific affinity headers:
  - Vercel AI Gateway: `x-session-affinity: <promptCacheKey>` (`packages/ai/src/providers/vercel-ai-gateway.ts` L201)
  - xAI: `x-grok-conv-id` (`packages/ai/src/providers/xai.ts` L63)
  - OpenAI/ChatGPT: `session-id` (`packages/core/src/plugin/provider/openai.ts` L320, `chatgpt.ts` L278)
  - GitHub Copilot: `X-Interaction-Id` (`github-copilot.ts` L288)
- **V** Other gateways that get `HTTP-Referer`/`X-Title`: Kilo, NVIDIA, LLMGateway, ZenMux and Vercel.

### Not checked

- **U** Whether the released npm `opencode-ai` binary matches this main-branch commit. Older releases (the `packages/opencode/src/session/llm.ts` era) sent `x-opencode-session` only to the opencode provider. That is from memory and I did not re-verify it here.

---

## Key cross-provider takeaways

- **Session or cache-key field:**
  - OpenRouter: `session_id` body field or `x-session-id` header; falls back to `prompt_cache_key`.
  - DeepInfra, Together, Mistral: `prompt_cache_key`.
  - Groq: none, purely automatic.
- **Output limit field:**
  - `max_tokens` only: DeepInfra, Together, Mistral.
  - `max_completion_tokens` preferred, `max_tokens` deprecated but accepted: Groq, OpenRouter.
- **Forcing a tool call:**
  - `"required"`: OpenRouter, DeepInfra, Together, Groq.
  - `"any"` or `"required"`: Mistral.
- **Usage in streams:**
  - Always on: OpenRouter (`include_usage` has no effect).
  - On by default: DeepInfra (`include_usage` default true).
  - Must be requested: Mistral (docs say set `include_usage`, though the schema omits `stream_options`).
  - Undocumented: Groq and Together.
- **Mid-stream errors** as a 200 SSE event with an `error` field and `finish_reason:"error"`: documented only by OpenRouter.

Third-party sources used only for the Mistral 9-character tool-ID rule: https://github.com/vercel/ai/issues/11802, https://docs.vllm.ai/en/stable/api/vllm/tokenizers/mistral/, https://repos.supermodeltools.com/langchain/fn-chat-models-py-is-valid-mistral-tool-call-id

---

# Part 3. Self-hosted model servers and proxies


Researched 2026-10-18. Sources are the official docs, plus source code on GitHub `main` where the docs say nothing. "VERIFIED" means I read it at the URL given. "UNVERIFIED" means it comes from memory or inference, or the docs don't cover it.

---

## Ollama

**1. API formats**
- `/v1/chat/completions`, `/v1/completions`, `/v1/models`, `/v1/embeddings`. VERIFIED https://docs.ollama.com/api/openai-compatibility
- `/v1/responses` was added in v0.13.3. It is stateless only: `previous_response_id`, `conversation` and `truncation` are not supported. VERIFIED (same URL)
- `/v1/messages` is Anthropic-compatible, but covers only a subset of the API. VERIFIED https://docs.ollama.com/api/anthropic-compatibility
- Native API: `/api/chat`, `/api/generate`, `/api/show` and others. VERIFIED https://raw.githubusercontent.com/ollama/ollama/main/docs/api.md

**2. Auth**
- The local server needs no auth. "The client requires an API key value, but Ollama ignores it." VERIFIED (openai-compatibility URL)
- By default it binds to 127.0.0.1:11434. `OLLAMA_HOST` changes the bind address. VERIFIED https://docs.ollama.com/faq
- The cloud service (ollama.com/api and /v1) requires `Authorization: Bearer $OLLAMA_API_KEY`. Its `/v1/messages` "does not accept `x-api-key` alone". VERIFIED https://docs.ollama.com/api/authentication
- There is no built-in auth for a self-hosted server. You need a reverse proxy. UNVERIFIED; the docs don't mention local auth at all.

**3. max_tokens vs max_completion_tokens**
- `/v1/chat/completions` lists `max_tokens`. `max_completion_tokens` is not listed. VERIFIED (openai-compatibility URL)
- Whether `max_completion_tokens` is ignored or honored is UNVERIFIED. Send `max_tokens` to be safe.
- `/v1/responses` takes `max_output_tokens`. In the native API the equivalent is `options.num_predict` (UNVERIFIED, from memory).

**4. Tool calling**
- `tools` is supported on `/v1/chat/completions`. `tool_choice` is explicitly listed as unsupported, along with `logit_bias`, `user`, `n` and logprobs. VERIFIED (openai-compatibility URL)
- On `/v1/messages`, forcing a specific tool or disabling tools is unsupported. VERIFIED (anthropic-compatibility URL)
- Native `/api/chat`: tools are "for the model to use if supported". A tool call has the shape `{"type":"function","function":{"index":0,"name":...,"arguments":{...}}}`, where `arguments` is an object, not a string. Tool results go back as `{"role":"tool","tool_name":...,"content":...}`. VERIFIED https://docs.ollama.com/capabilities/tool-calling and api.md
- Streaming: "gather every chunk of `thinking`, `content`, and `tool_calls`". VERIFIED (tool-calling URL)
- Tool support depends on the model's template. Check `/api/show` capabilities (see section 10). The dependency itself is UNVERIFIED as a doc statement; the capability array is VERIFIED.

**5. Streaming**
- The native API streams by default as NDJSON (`application/x-ndjson`). The final object carries `"done": true` and `done_reason`. VERIFIED https://docs.ollama.com/api/streaming
- `/v1/*` streams SSE as OpenAI does, and `stream_options.include_usage` is supported. VERIFIED (openai-compatibility URL lists `stream_options` with `include_usage`)
- `/v1/messages` streams Anthropic SSE events such as `content_block_delta`. Error events during streaming are not supported. VERIFIED (anthropic-compatibility URL)

**6. Reasoning**
- Native `think` accepts `true`, `false`, `null`, or a level string (for example "low", "medium" or "high" for gpt-oss). Valid levels come from `/api/show` `thinking.values`. The output lands in `message.thinking`. VERIFIED https://docs.ollama.com/capabilities/thinking
- OpenAI compat: "`reasoning_effort` and `reasoning.effort` control model thinking". VERIFIED (openai-compatibility URL)
- The OpenAI-compat response field name (`reasoning` vs `reasoning_content`) is UNVERIFIED.
- Anthropic compat: `thinking.type` set to enabled or disabled works as an on/off switch. `budget_tokens` is "accepted but not enforced". VERIFIED (anthropic-compatibility URL)

**7. Prompt caching / KV**
- There is no `cache_control` support. Prompt caching is listed as unsupported on `/v1/messages`. VERIFIED (anthropic-compatibility URL)
- `keep_alive` defaults to 5m. `OLLAMA_KEEP_ALIVE` sets it globally, and a per-request `keep_alive` overrides it. VERIFIED faq and api.md
- Reuse of the KV cache for a loaded model is implicit and not documented as a feature. UNVERIFIED.
- The native generate response documents `prompt_eval_cached_count`, the "number of prompt tokens read from the cache". VERIFIED api.md. Whether `/v1` usage reports `prompt_tokens_details.cached_tokens` is UNVERIFIED.
- **Context-length pitfall.** The docs conflict:
  - The FAQ says "By default, Ollama uses a context window size of 4096 tokens".
  - https://docs.ollama.com/context-length says the default depends on VRAM: 4k under 24 GiB, 32k for 24–48 GiB, 256k at 48 GiB or more. It recommends at least 64k for agents and coding tools.
  - It is set with `OLLAMA_CONTEXT_LENGTH` or the `num_ctx` option.
  - Both statements are VERIFIED at those URLs. The docs don't describe what happens to overflowing input. That silent truncation drops the oldest content is UNVERIFIED, from memory.
- `OLLAMA_NUM_PARALLEL` "default 1". VERIFIED faq.

**8. Errors**
- Errors come back as `{"error": "<message>"}`. Status codes are 400, 404, 429, 500 and 502.
- A mid-stream error arrives as a final NDJSON line `{"error": ...}`, and the HTTP status stays 200.
- VERIFIED https://docs.ollama.com/api/errors
- The `/v1` error shape (OpenAI-style `{"error":{message,type,...}}`) is UNVERIFIED.

**10. Capability probing**
- `POST /api/show` returns `capabilities`, for example `["completion","vision"]`. Other values such as "tools", "thinking", "insert" and "embedding" exist. VERIFIED that the array exists and contains completion and vision (api.md). The other values are UNVERIFIED.
- `/api/show` also returns `thinking.values` for reasoning levels. VERIFIED (thinking URL)

---

## vLLM

**1. API formats** (all VERIFIED https://docs.vllm.ai/en/latest/serving/online_serving/)
- `/v1/chat/completions` and `/v1/completions`.
- `/v1/responses`, plus `/v1/responses/{id}` and `/v1/responses/{id}/cancel`.
- Anthropic `/v1/messages` and `/v1/messages/count_tokens`. Confirmed in source at `vllm/entrypoints/anthropic/api_router.py` (main, https://github.com/vllm-project/vllm).
- `/v1/models`.

**2. Auth**
- There is no auth by default. Set `--api-key` or the `VLLM_API_KEY` env var. The key "only authenticates requests to endpoints under the `/v1`, `/v2`, and `/inference` path prefixes". Other paths such as `/invocations` and `/health` are open. VERIFIED https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/
- In source, the guarded prefixes are `/v1`, `/v2`, `/inference` and `/cohere`. You can pass several keys as a list. VERIFIED `vllm/entrypoints/serve/middleware/authenticate.py`
- The header must be `Authorization: Bearer <key>`. The middleware checks only that header, so **`x-api-key` is not accepted, even on `/v1/messages`**. Anthropic SDK clients must send Bearer, for example with `auth_token` or `ANTHROPIC_AUTH_TOKEN`. VERIFIED (same source file)
- A failed check returns a 401 with body `{"error":"Unauthorized"}`. This is not the OpenAI error shape. VERIFIED (same source file)

**3. max_tokens vs max_completion_tokens**
- Both are accepted. `max_tokens` is marked `deprecated="max_tokens is deprecated in favor of the max_completion_tokens field"`, and `max_completion_tokens` wins when both are set. VERIFIED `vllm/entrypoints/openai/chat_completion/protocol.py` (main)
- `reasoning_effort` accepts "none", "minimal", "low", "medium", "high", "xhigh" and "max". When set, it switches on the chat-template `enable_thinking`. VERIFIED (same file)

**4. Tool calling**
- `auto` tool choice requires `--enable-auto-tool-choice` (default False) together with `--tool-call-parser <name>`. Parsers include hermes, llama3_json, llama4_pythonic, mistral, pythonic, deepseek_v3, deepseek_v31, openai, kimi_k2, qwen3_xml, glm45 and granite. `--chat-template` is optional. VERIFIED https://docs.vllm.ai/en/latest/features/tool_calling.html and https://docs.vllm.ai/en/latest/cli/serve.html
- `tool_choice` accepts "none", "auto", "required" (vLLM 0.8.3 and later) and a named function. `required` and named choices use structured outputs, which are on by default. The docs warn: "guaranteed a validly-parsable function call - not a high-quality one". VERIFIED (tool_calling URL)
- The default is `"none"`, which becomes `"auto"` when `tools` is given. VERIFIED (protocol.py)
- With `none`, the tool definitions still go into the prompt unless you set `--exclude-tools-when-tool-choice-none`. VERIFIED (tool_calling URL)
- `parallel_tool_calls=false` limits the response to at most one call. VERIFIED (openai_compatible_server URL)
- Streaming tool-call deltas are supported, but the behavior depends on the parser. The `hf` parser emits each call whole once it has parsed. VERIFIED (tool_calling URL)
- Anthropic `tool_choice` accepts `auto`, `any`, `tool` and `none`. VERIFIED `vllm/entrypoints/anthropic/protocol.py`

**5. Streaming**
- Streaming uses SSE. `stream_options` has `include_usage` (default False) and `continuous_usage_stats`. VERIFIED `vllm/entrypoints/generate/base/protocol.py`
- `stream_options` without `stream=true` fails validation. VERIFIED (chat protocol.py)
- `--enable-force-include-usage` includes usage on every request. VERIFIED (serve CLI URL)

**6. Reasoning**
- `--reasoning-parser <name>` (for example deepseek_r1, qwen3, glm45 or gemma4) splits the output into a `reasoning` field, and the field also appears in stream deltas.
- **"`reasoning` used to be called `reasoning_content`"**. Clients that read `reasoning_content` may get an empty value.
- Tool calls are parsed only from `content`, not from reasoning.
- All VERIFIED https://docs.vllm.ai/en/latest/features/reasoning_outputs.html

**7. Prompt caching**
- Automatic prefix caching defaults to on: `enable_prefix_caching: bool = True`. VERIFIED `vllm/config/cache.py` (main). The docs pages don't state the default.
- Turn it off with `--no-enable-prefix-caching`. The flag pair `--enable-prefix-caching`/`--disable-prefix-caching` is shown in https://docs.vllm.ai/en/latest/cli/serve.html, so the exact negation spelling for your version is UNVERIFIED.
- There is no `cache_control` field in the Anthropic request model, so it is presumably ignored. UNVERIFIED inference.
- `cache_salt` isolates caching per request. VERIFIED (openai_compatible_server URL)
- Usage reports cached tokens only with `--enable-prompt-tokens-details` (default False). It then fills `usage.prompt_tokens_details.cached_tokens`. VERIFIED (serve CLI URL, and `PromptTokenUsageInfo` in `vllm/entrypoints/serve/engine/protocol.py`)
- The Anthropic endpoint maps `cached_tokens` to `cache_read_input_tokens` and subtracts it from `input_tokens`. VERIFIED `vllm/entrypoints/anthropic/serving.py`

**8. Errors**
- The shape is `{"error":{"message","type","param","code":<int>}}`. Note that `code` is an integer. VERIFIED `vllm/entrypoints/serve/engine/protocol.py`
- vLLM has no rate-limit headers. UNVERIFIED (none documented).

**9. Extra params**
- Non-OpenAI params go in `extra_body` or are merged into the JSON body. Examples include `top_k`, `min_p`, `repetition_penalty`, `chat_template_kwargs`, `structured_outputs`, `priority` and `cache_salt`. The `user` param is ignored. VERIFIED (openai_compatible_server URL)

**10. Capability probing**
- `/v1/models` returns a ModelCard with `id`, `root`, `parent`, `max_model_len` and `permission`. There is **no tool or reasoning capability flag**. Whether tools work depends on the server flags, which the API doesn't expose. VERIFIED `vllm/entrypoints/serve/engine/protocol.py`

---

## LiteLLM Proxy

**1. API formats**
- OpenAI `/chat/completions` (also `/v1/...`) is the core API. VERIFIED (implicit across docs)
- `/v1/responses` is native for openai, azure and azure_ai. For other providers (Anthropic, Bedrock, Gemini, Vertex) it is bridged to chat completions with only a subset of params. The proxy routes `previous_response_id` follow-ups to the same deployment, and other users' response IDs return 403. VERIFIED https://docs.litellm.ai/docs/response_api
- Anthropic `/v1/messages` works with "All LiteLLM supported providers", including openai, bedrock, vertex_ai, gemini and azure. Usage reports `cache_creation_input_tokens` and `cache_read_input_tokens`. `thinking` is supported. VERIFIED https://docs.litellm.ai/docs/anthropic_unified

**2. Auth**
- `general_settings.master_key` (or `LITELLM_MASTER_KEY`) must start with `sk-`. It is optional: without it, the proxy is open. VERIFIED https://docs.litellm.ai/docs/proxy/virtual_keys and https://docs.litellm.ai/docs/proxy/configs
- Virtual keys come from `/key/generate` and need Postgres (`DATABASE_URL`). VERIFIED (virtual_keys URL)
- Headers accepted as a LiteLLM key, from the `SpecialHeaders` enum in source:
  - `Authorization` (Bearer)
  - `API-Key` (Azure style)
  - `x-api-key` (Anthropic style)
  - `x-goog-api-key`
  - `Ocp-Apim-Subscription-Key`
  - `x-litellm-api-key`
  - a custom name set with `general_settings.litellm_key_header_name`
  - VERIFIED https://raw.githubusercontent.com/BerriAI/litellm/main/litellm/types/proxy/auth/special_headers.py. The docs themselves only show Bearer and the custom header.
- JWT/OIDC auth requires an Enterprise license. VERIFIED https://docs.litellm.ai/docs/proxy/token_auth

**3. max_tokens vs max_completion_tokens**
- Both are accepted, and `max_completion_tokens` "counts both visible output and reasoning tokens". Per-provider support comes from `get_supported_openai_params()`. VERIFIED https://docs.litellm.ai/docs/completion/input
- How each is translated per provider (for example to Anthropic `max_tokens`) is UNVERIFIED in detail.

**4. Tool calling**
- `tools`, `tool_choice` (none, auto, or a dict naming a tool) and `parallel_tool_calls` are supported. VERIFIED (input URL)
- The SDK has `litellm.supports_function_calling(model)` and `supports_parallel_function_calling`. `litellm.add_function_to_prompt = True` covers models without native tools. VERIFIED https://docs.litellm.ai/docs/completion/function_call
- `tool_choice: "required"` per provider is UNVERIFIED.

**5. Streaming**
- `stream_options.include_usage` adds a final usage chunk. VERIFIED (input URL)
- Streaming uses OpenAI SSE, or Anthropic SSE on `/v1/messages`. VERIFIED that streaming is supported (anthropic_unified URL)

**6. Reasoning**
- The response carries a standardized `reasoning_content` for all providers. `thinking_blocks` appear only for Anthropic and must be sent back on tool-use turns, or the request fails with 400.
- `reasoning_effort` is mapped per provider: for older Claude models, low becomes a 1024-token budget and high a 4096-token budget; Claude 4.6 and later get adaptive thinking.
- The native `thinking` param is also accepted.
- `supports_reasoning` is exposed by `/model_group/info`.
- All VERIFIED https://docs.litellm.ai/docs/reasoning_content

**7. Prompt caching**
- `cache_control: {"type":"ephemeral"}` passes through to Anthropic. For Bedrock it is translated to `cachePoint` (invoke and converse). For Vertex/Gemini it goes to context caching. OpenAI and DeepSeek cache automatically.
- Usage returns `prompt_tokens_details.cached_tokens`, and `cache_creation_input_tokens` for Anthropic.
- `/v1/model/info` shows `supports_prompt_caching`.
- Caching is skipped silently below each provider's minimum token count.
- All VERIFIED https://docs.litellm.ai/docs/completion/prompt_caching
- The proxy also has its own response cache, separate from provider prompt caching. Backends are redis (the default when on), s3, gcs, local, disk and semantic variants. It is off until `litellm_settings.cache: True`. VERIFIED https://docs.litellm.ai/docs/proxy/caching

**8. Errors and rate limits**
- Errors are mapped to OpenAI types: 400 BadRequest (subtypes UnsupportedParams, ContextWindowExceeded, ContentPolicyViolation), 401, 402, 403, 404, 408 Timeout, 422, 429 RateLimitError, 500, 503. VERIFIED https://docs.litellm.ai/docs/exception_mapping
- The body is `{"error":{"message","type","param","code":"400",...,"provider_specific_fields"}}`. Note that `code` is a **string**. VERIFIED (same URL, one example)
- Response headers include:
  - `x-ratelimit-remaining-requests`, `-remaining-tokens`, `-limit-requests`, `-limit-tokens`, `-reset-requests` and `-reset-tokens`
  - `x-litellm-call-id`, `x-litellm-model-id`, `x-litellm-response-cost`, `x-litellm-attempted-retries` and others
  - upstream provider headers, re-prefixed `llm_provider-...`
  - VERIFIED https://docs.litellm.ai/docs/proxy/response_headers
- A `retry-after` header is not documented. UNVERIFIED.
- Per-model key limits add `x-litellm-key-remaining-requests-{model}` and `x-litellm-key-remaining-tokens-{model}`. Exceeding a limit returns 429.
- Budget errors use `type: "budget_exceeded"`, but the docs show inconsistent status codes: 400, 429 and 401.
- A Redis outage with fail-closed enforcement returns 503.
- VERIFIED https://docs.litellm.ai/docs/proxy/users

**9. Providers and config** (each fact verified at the URL in its bullet)
- `model_list` entries have this shape: `model_name` (the alias clients use), then `litellm_params` with `model`, `api_base`, `api_key` (as `os.environ/VAR`), and optional `rpm`/`tpm`. An optional `model_info` block can follow. Repeating one alias across several deployments load-balances between them. VERIFIED https://docs.litellm.ai/docs/proxy/configs
- **Bedrock:**
  - "LiteLLM uses boto3 to handle authentication", so LiteLLM does the SigV4 signing.
  - Credentials can be `aws_access_key_id`/`aws_secret_access_key`/`aws_region_name`, `aws_profile_name`, `aws_role_name` + `aws_session_name` (STS AssumeRole), or a Bedrock API key via `AWS_BEARER_TOKEN_BEDROCK` or `api_key`.
  - Model names: `bedrock/<id>`, `bedrock/converse/<id>`, `bedrock/invoke/<id>`. The default is invoke, with converse for models that support it.
  - VERIFIED https://docs.litellm.ai/docs/providers/bedrock
- **Vertex:**
  - Auth via ADC (`gcloud auth application-default login`), `GOOGLE_APPLICATION_CREDENTIALS`, or `vertex_credentials` (a JSON string or file path).
  - Set `vertex_project` and `vertex_location`, or the `VERTEXAI_PROJECT`/`VERTEXAI_LOCATION` env vars.
  - Model names: `vertex_ai/<model>`. Claude on Vertex has its own docs page.
  - VERIFIED https://docs.litellm.ai/docs/providers/vertex
- **Azure:**
  - Model names: `azure/<deployment>`, plus `api_base` and `api_version`.
  - Auth via `api_key`, `azure_ad_token`, a service principal (`tenant_id`/`client_id`/`client_secret`), or `enable_azure_ad_token_refresh` (DefaultAzureCredential).
  - Claude on Azure Foundry uses `azure_ai/`, not `azure/`.
  - VERIFIED https://docs.litellm.ai/docs/providers/azure/
- Provider-specific params: any non-OpenAI param "passes it to the provider in the request body, as a kwarg". On the proxy you can set them per model in `litellm_params` or send them in the request body, so `extra_body` from the OpenAI SDK works. VERIFIED https://docs.litellm.ai/docs/completion/provider_specific_params
- **drop_params:**
  - By default LiteLLM raises an error (`UnsupportedParamsError`) on unsupported params. VERIFIED
  - `litellm_settings.drop_params: true` drops them silently. VERIFIED
  - `additional_drop_params` drops named params, including nested ones such as `tools[*].input_examples`. VERIFIED
  - `allowed_openai_params` forces a param through. VERIFIED
  - All at https://docs.litellm.ai/docs/completion/drop_params

**10. Capability probing**
- `GET /model/info` (also `/v1/model/info`) returns the model list with keys masked and the merged cost-map `model_info`. The docs confirm `supports_prompt_caching` appears there (prompt_caching URL). VERIFIED partially.
- That `supports_function_calling`, `supports_vision`, `max_input_tokens` and similar appear in the same `model_info` is UNVERIFIED (very likely, since they come from the same cost map).
- `/model_group/info` exposes `supports_reasoning`. VERIFIED (reasoning_content URL)
- `/v1/models` lists the models a key is allowed to use. VERIFIED https://docs.litellm.ai/docs/proxy/model_access
- For custom or self-hosted backends, set flags such as `model_info.supports_reasoning: True` (or `supports_function_calling`) yourself, because the cost map won't know the model. VERIFIED for supports_reasoning; the rest is UNVERIFIED.

---

## LM Studio

**1. API formats**
- OpenAI compat: `GET /v1/models`, `POST /v1/responses`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings` and `/v1/decisions`. The default port is 1234. VERIFIED https://lmstudio.ai/docs/developer/openai-compat
- Anthropic compat: `POST /v1/messages`. VERIFIED https://lmstudio.ai/docs/developer/anthropic-compat
- Native REST: `/api/v1/*`, with legacy `/api/v0/*`. VERIFIED https://lmstudio.ai/docs/developer/rest/endpoints
- `/v1/responses` supports `reasoning.effort` and stateful `previous_response_id`. Remote MCP is opt-in. It streams SSE events such as `response.created`, `response.output_text.delta` and `response.completed`. VERIFIED https://lmstudio.ai/docs/developer/openai-compat/responses

**2. Auth**
- Auth is off by default. Turn on "Require Authentication" under Developer → Server Settings, then create a token under Manage Tokens. Send it as `Authorization: Bearer $LM_API_TOKEN`. VERIFIED https://lmstudio.ai/docs/developer/core/authentication
- `/v1/messages` "accepts both `x-api-key` and the standard `Authorization: Bearer <token>`". VERIFIED (anthropic-compat URL)

**3. max_tokens**
- The documented chat params are: model, top_p, top_k, messages, temperature, max_tokens, stream, stop, presence_penalty, frequency_penalty, logit_bias, repeat_penalty and seed. `max_completion_tokens` is not listed. VERIFIED https://lmstudio.ai/docs/developer/openai-compat/chat-completions
- How LM Studio handles `max_completion_tokens` is UNVERIFIED.

**4. Tool calling**
- Models get either "native" support (a tool-aware chat template plus a format LM Studio supports, for example Qwen2.5, Llama 3.1/3.2 and Ministral) or "default" support. Default mode injects a system prompt and converts tool messages to the user role, so it is less reliable.
- Streaming tool calls arrive in fragments and must be accumulated.
- `tool_choice` is not documented for chat completions.
- VERIFIED https://lmstudio.ai/docs/developer/openai-compat/tools
- `/v1/messages` examples use `tool_choice: {"type":"any"}`. VERIFIED https://lmstudio.ai/docs/developer/anthropic-compat/messages

**5. Streaming**
- Streaming uses SSE on all compat endpoints. VERIFIED for messages and responses.
- `stream_options.include_usage` is UNVERIFIED.

**6. Reasoning**
- v0.4.8 added `reasoning_effort` and `reasoning_tokens` to `/v1/chat/completions` and a `reasoning` field to `/api/v1/models`. VERIFIED https://lmstudio.ai/changelog/lmstudio/lmstudio-v0.4.8
- `/v1/responses` supports `reasoning.effort`. VERIFIED
- A Developer setting splits `reasoning_content` from `content` ("When applicable, separate reasoning_content and content in API responses", since 0.3.9). UNVERIFIED: third-party source only.

**7. Prompt caching**
- Nothing is documented. `cache_control` support on `/v1/messages` is unknown, and so is cached-token usage. UNVERIFIED.

**8. Errors**
- Error shape and status codes are not documented. UNVERIFIED.

**10. Capability probing**
- `GET /api/v1/models` returns `capabilities` with `vision` (bool), `trained_for_tool_use` (bool) and `reasoning`. `reasoning` holds `{allowed_options: off/on/low/medium/high, default}`. The object is absent for embedding models.
- The same response gives `max_context_length` and `loaded_instances[].config.context_length`. The example shows a loaded instance at 4096 while the model's max is 262144, the same context pitfall as Ollama.
- VERIFIED https://lmstudio.ai/docs/developer/rest/list
- Legacy `/api/v0/models` gives `type` (llm, vlm or embeddings), `arch`, `state`, `max_context_length`, `quantization` and `compatibility_type`. VERIFIED https://lmstudio.ai/docs/developer/rest/endpoints

---

## llama.cpp server (quick pass)

All VERIFIED at https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md unless marked otherwise.

- **Endpoints:**
  - `/v1/chat/completions`
  - `/v1/responses` ("converting Responses request into Chat Completions request")
  - `/v1/messages` (Anthropic)
  - `/v1/models`, which always returns exactly one model
  - `/v1/embeddings`, `/apply-template` and `/props`
- **Auth:** `--api-key` accepts a comma-separated list. The examples use `Authorization: Bearer` for OpenAI routes and `x-api-key` for `/v1/messages`. The README doesn't spell out exactly which headers are accepted.
- **Tool calling:** requires `--jinja`, including on `/v1/messages`. `tool_choice` is documented only for `/v1/messages` (auto, any, or a named tool).
- **Reasoning:** returned in `reasoning_content` (DeepSeek-style). `reasoning_format: none` gives raw text.
- **Caching:** `cache_prompt` defaults to true (reuses the KV cache). Usage includes `prompt_tokens_details.cached_tokens`, and `timings` reports `cache_n`, `prompt_n` and `predicted_n`.
- **Context:** `-c/--ctx-size` defaults to 0, meaning it is taken from the model.
- **Errors:** the only example is `{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`. No general schema is documented.

---

## Cross-cutting takeaways for a client

1. **Auth headers differ:**
   - vLLM accepts only `Authorization: Bearer`, even on `/v1/messages`.
   - LM Studio and LiteLLM accept both `x-api-key` and Bearer.
   - Ollama local ignores keys, and Ollama cloud requires Bearer.
   - Always send Bearer, and add `x-api-key` for Anthropic-format routes.
2. **max_completion_tokens is not safe everywhere.** vLLM and LiteLLM accept it. Ollama and LM Studio document only `max_tokens`. Prefer `max_tokens` for local servers, or detect the server.
3. **tool_choice:**
   - Unsupported on Ollama, both `/v1/chat/completions` and `/v1/messages`.
   - Full support on vLLM (required and named use structured outputs).
   - Undocumented on LM Studio chat completions.
   - vLLM also needs server flags or tools silently won't parse.
4. **Reasoning field names vary:**
   - vLLM: `reasoning` (renamed from `reasoning_content`)
   - LiteLLM and llama.cpp: `reasoning_content`
   - Ollama native: `message.thinking`
   - Read both `reasoning` and `reasoning_content`.
5. **Error `code` type varies:** vLLM uses an int, LiteLLM a string, and Ollama native uses a plain string `error`. The vLLM 401 body is `{"error":"Unauthorized"}`. Parse defensively.
6. **Cached-token reporting:**
   - LiteLLM and llama.cpp report it.
   - vLLM reports it only with `--enable-prompt-tokens-details`.
   - Ollama has it only natively (`prompt_eval_cached_count`).
   - LM Studio: unknown.
7. **Capability probes:**
   - Ollama: `/api/show` → `capabilities`
   - LM Studio: `/api/v1/models` → `capabilities.trained_for_tool_use`
   - LiteLLM: `/model/info` (cost map; custom models need `model_info` flags)
   - vLLM: none; `/v1/models` gives only `max_model_len`
8. **Context-length pitfall:** Ollama (4k by default on small-VRAM machines) and LM Studio (loaded instance context can be far below the model's max) need explicit context settings for agent workloads.

---

# Part 4. The model provider of OpenCode, and the llama.cpp server


Researched 2026-10-18. Sources:
- Live docs: `https://opencode.ai/docs/go` and `https://opencode.ai/docs/zen`. Both return a 302 redirect, to `https://opencode.ai/v2/docs/console/go/` and `https://opencode.ai/v2/docs/console/models/`. "Zen" is now called "Console" / "OpenCode pay-as-you-go" in the docs.
- Terms: `https://opencode.ai/legal/terms-of-service` (Effective date: Aug 15, 2026).
- OpenCode source: `github.com/anomalyco/opencode`, main branch at commit `802f2d8e75098ae0b45ad839ce98a31589ea8102` (2026-10-18). The file layout has changed: there is no `packages/opencode/`. Request code is in `packages/core` and `packages/ai`.
- Live probes with curl against `opencode.ai` (no valid key; bogus keys only).
- llama.cpp: `tools/server/README.md` and source at master commit `5aeac37a35b44fde4bdffaec9159f2fcb253b622`.

Abbreviations used below:
- GO = https://opencode.ai/v2/docs/console/go/
- ZEN = https://opencode.ai/v2/docs/console/models/
- OC = https://github.com/anomalyco/opencode/blob/802f2d8e75098ae0b45ad839ce98a31589ea8102
- LC = https://github.com/ggml-org/llama.cpp/blob/5aeac37a35b44fde4bdffaec9159f2fcb253b622

---

## Part 1: OpenCode Go / Console (Zen)

### 1. Plans, prices, models, limits

- **VERIFIED (GO).** There are two Go plans: "Go | $10/month" and "Go Plus | $40/month". You subscribe in the OpenCode Console and copy an API key. Go is limited to single-member workspaces ("A workspace with a Go subscription cannot invite members").
- **VERIFIED (GO).** Limits are monthly dollar amounts for each model: "5-hour — 20% of the monthly limit; weekly — 50%; and monthly — 100%." Token pricing is the same on Go and Go Plus.
- **VERIFIED (GO).** Example monthly limits (Go / Go Plus):

  | Model | Go | Go Plus |
  |---|---|---|
  | GLM-5.3 | $15 | $120 |
  | GLM-5.3-Flash | $60 | $180 |
  | Kimi K3 | $15 | $60 |
  | DeepSeek V4.1 Flash | $80 | $160 |
  | Claude Haiku 5.5 | $15 | $60 |
  | GPT 6 Luna | $15 | $60 |
  | MiniMax M3 | $60 | $180 |
  | Qwen3.8 Max | $15 | $60 |

  The page also gives an "Estimated requests" table per 5 hours / week / month. Examples on Go:
  - GLM-5.3: 220 / 540 / 1,080
  - Kimi K3: 110 / 250 / 490
  - DeepSeek V4.1 Flash: 34,670 / 86,670 / 173,330
  - Claude Haiku 5.5: 3,850 / 9,620 / 19,230

  These estimates assume about 50–90K cached tokens per request.
- **VERIFIED (GO).** The current Go model list: Grok 4.7, GLM-5.3-Flash, GLM-5.3, GLM-5.2, GPT 6 Luna, GPT 5.6 Luna, Claude Haiku 5.5, Gemini 4 Argon Contributor, Kimi K3, Kimi K2.7 Code, LongCat-2.0, LongCat 2.5 Preview Free, Step 5 Preview, Beam Free, MiMo-V2.6-Flash/Pro, MiMo-V2.5/Pro, MiniMax M3, Muse Spark 1.3/1.2 Contributor, Qwen3.8 Max, Qwen3.8 Flash, Qwen3.7 Plus, DeepSeek V4.1 Flash, DeepSeek V4 Pro, Hy4 preview, Hy3, Space Bunny. The page says "The list of models may change".
- **VERIFIED (GO).** Overage: with "Use balance" enabled, "Go will fall back to your pay-as-you-go balance after you've reached your usage limits instead of blocking requests."
- **VERIFIED (ZEN).** Console/Zen is pay-as-you-go credit with per-1M-token prices and auto-reload ($20 when the balance drops below $5). It has optional monthly workspace and per-member limits. Card fees are 4.4% + $0.30.
- **VERIFIED (live).** `GET https://opencode.ai/inference/go/v1/models` is public and returns OpenAI-style `{"object":"list","data":[{id,object,created,owned_by:"opencode"}]}`. It also lists IDs not in the docs, for example `glm-5.1`, `kimi-k2.6`, `minimax-m2.5/2.7`, `qwen3.6-plus`, `qwen3.7-max` and `omen-alpha`.

### 2. Endpoints and API format per model

**Current base URLs (VERIFIED, GO + ZEN).** Each format has its own path prefix:

- Go:
  - `https://opencode.ai/inference/go/openai/v1/chat/completions`
  - `https://opencode.ai/inference/go/openai/v1/responses`
  - `https://opencode.ai/inference/go/anthropic/v1/messages`
  - `https://opencode.ai/inference/go/google/v1beta/models/<id>`
  - Models list: `https://opencode.ai/inference/go/v1/models`
- Console/Zen: the same layout without `/go`, for example `https://opencode.ai/inference/openai/v1/responses` and `https://opencode.ai/inference/anthropic/v1/messages`. Models list: `https://opencode.ai/inference/v1/models`.

**Legacy URLs still work (VERIFIED, source + live).** `OC/packages/console/app/src/lib/inference-proxy.ts` maps the old paths to the new ones:

| Old path | New path |
|---|---|
| `/zen/v1/chat/completions` | `/openai/v1/chat/completions` |
| `/zen/v1/responses` | `/openai/v1/responses` |
| `/zen/v1/messages` | `/anthropic/v1/messages` |
| `/zen/go/v1/chat/completions` | `/go/openai/v1/chat/completions` |
| `/zen/go/v1/responses` | `/go/openai/v1/responses` |
| `/zen/go/v1/messages` | `/go/anthropic/v1/messages` |
| `/zen/go/v1/models` | `/go/v1/models` |
| `/zen/go/v1/usage` | `/go/v1/usage` |
| `/zen/v1/models/<id>:generateContent` | `/google/v1beta/models/<id>:...` |

`https://opencode.ai/zen/go/v1/models` and `/zen/v1/models` both returned 200 live.

**Go per-model format (VERIFIED, GO table).**

| Format | Models |
|---|---|
| Responses (`@ai-sdk/openai`) | grok-4.7, gpt-6-luna, gpt-5.6-luna, muse-spark-1.3-contributor, muse-spark-1.2-contributor |
| Anthropic Messages (`@ai-sdk/anthropic`) | claude-haiku-5-5, minimax-m3, qwen3.8-max, qwen3.8-flash, qwen3.7-plus |
| Google (`@ai-sdk/google`) | gemini-4-argon-contributor |
| Chat Completions (`@ai-sdk/openai-compatible`) | glm-5.3-flash, glm-5.3, glm-5.2, kimi-k3, kimi-k2.7-code, longcat-2.0, deepseek-v4.1-flash, deepseek-v4-pro, mimo-v2.6-flash/pro, mimo-v2.5/pro, hy4-preview, hy3, space-bunny, longcat-2.5-preview-free, step-5-preview, beam-free |

**Formats differ between Go and Zen (VERIFIED, ZEN vs GO tables).**
- qwen3.8-max uses Chat Completions on Zen but Messages on Go.
- minimax-m3 uses Chat Completions on Zen but Messages on Go.

On Zen:
- GPT/Grok/Muse models use Responses.
- Claude and Qwen 3.5–3.8 Flash/Plus models use Messages.
- Gemini models use Google format.
- GLM/Kimi/DeepSeek/MiniMax/Mistral and the free models use Chat Completions.
- Jev uses `/inference/systemone/v1/systemone`.

**The models.dev catalog agrees (VERIFIED, https://models.dev/api.json).** Provider `opencode-go` has `api: https://opencode.ai/inference/go/openai/v1` and env `OPENCODE_API_KEY`. Individual models override `npm`/`api`, for example `minimax-m3` → `@ai-sdk/anthropic` at `https://opencode.ai/inference/go/anthropic/v1`. Provider `opencode` has `api: https://opencode.ai/inference/openai/v1`.

### 3. Authentication

- **VERIFIED (ZEN Jev example).** `-H "Authorization: Bearer $OPENCODE_API_KEY"`. The key comes from the OpenCode Console, after you sign in and add billing or subscribe to Go (GO, "How it works").
- **VERIFIED (source, `inference-proxy.ts`).** On the legacy proxy, the key header depends on the endpoint:
  - `/messages` reads `x-api-key`.
  - Google paths read `x-goog-api-key`.
  - Everything else reads `authorization` (the token after the space).

  The proxy then forwards `authorization: Bearer <key>` to the new inference backend.
- **VERIFIED (live).** `x-api-key` on `/inference/go/anthropic/v1/messages` is accepted as a credential header: a bogus key gave `401 Invalid credential`. **UNVERIFIED** whether `Authorization: Bearer` also works on the new `/inference/.../anthropic` path.
- **VERIFIED (live).** The CORS allow-list advertises `Content-Type, Authorization, X-Api-Key, X-Opencode-Org-Id`.
- **VERIFIED (live).** Go error shapes:
  - No key: 401 `{"error":{"type":"server_error","message":"Upstream request failed: Go requires a credential"}}`
  - Bad key: 401 `"... Invalid credential"`
  - Bad key on the Anthropic path: `{"type":"error","error":{"type":"api_error",...}}`

### 4. Request requirements: session header, other headers, User-Agent

**`x-opencode-session` is required on Go.**

- **VERIFIED (live).** Any Go POST without it is rejected before auth, with 400:
  ```
  {"type":"error","error":{"type":"MissingSessionID","message":"Request is missing x-opencode-session and cannot be routed efficiently. Please see https://opencode.ai/docs/go/#where-can-i-use-it"}}
  ```
  This happened on chat/completions, responses, messages and Google paths.
- **VERIFIED (live).** With the header present, the request moves on to the credential check. Any opaque value worked; `ses_test123` reached the auth stage.
- **VERIFIED (GO, "Where can I use it?").** The docs say the client should:
  - "Send typical coding agent traffic"
  - "Identify itself with its own user agent, such as my-coding-agent/1.0, rather than a generic SDK or HTTP-library name."
  - "Send a stable session ID in x-opencode-session for each conversation so we can optimize routing and prompt caching."
- **VERIFIED (GO).** The purpose is routing plus prompt caching, per the quote above. Go also "recognizes [the] native session header" of Claude Code, Codex and ZCode. The page lists validated clients (Hermes, Claude Code, Codex, ZCode, Pi, jcode, Kilo Code CLI) and problem clients (DeepSeek Harness, GitHub Copilot Chat, Kimi Code, MiMo Code).
- **UNVERIFIED.** That the header is used for billing or rate limiting per session. Nothing says so.
- **Console/Zen pay-as-you-go:** a request with a bogus key and no session header got 401 (auth error), not MissingSessionID. So the hard requirement seems to apply to Go only, or Zen checks auth first. **UNVERIFIED** with a valid key.

**User-Agent:**
- Go docs ask for a non-generic agent-specific UA. **UNVERIFIED** that it is enforced: `python-requests/2.31`, `curl` and `my-agent/1.0` all reached the same 401 credential stage.
- No rule requiring the UA to start with `opencode/` was found.

**Free tier is OpenCode-only (VERIFIED, live).** On Zen, free model `big-pickle` with no key, from a non-OpenCode UA, returned 403:
```
{"type":"error","error":{"type":"FreeTierError","message":"OpenCode's free tier can only be used from within OpenCode. Download it at https://opencode.ai and keep building."}}
```
This happened with or without the session header.

**What the OpenCode client sends (VERIFIED, `OC/packages/core/src/session/model-request.ts` ~L278-296).** These headers are set on every model request, with no provider check at this layer:
```ts
"x-opencode-session-id": session.id,
...(session.parentID ? { "x-opencode-parent-session-id": session.parentID } : {}),
"x-session-affinity": affinity,
"X-Session-Id": affinity,
...(session.parentID ? { "x-parent-session-id": session.parentID } : {}),
"User-Agent": App.useragent(app),
"x-opencode-project": session.projectID,
"x-opencode-session": affinity,
"x-opencode-client": app.name,
...
promptCacheKey: /^ses_[0-9a-f]{64}$/.test(affinity) ? affinity.slice(4) : affinity,
```
- **Affinity (VERIFIED, `OC/packages/core/src/session/affinity.ts`).** It is `session.parentID ?? session.fork?.sessionID ?? session.id`. Subagent children share the parent's value, and forks share the source's, "for provider cache routing".
- **Session ID format (VERIFIED, `OC/packages/schema/src/session-id.ts`).** `"ses_" + descending()`.
- **User-Agent (VERIFIED, `OC/packages/core/src/app.ts`).** `opencode/${channel}/${version}/${name}`, with name defaulting to `"opencode"`.
- **Stateless calls (VERIFIED, `OC/packages/core/src/generate.ts` L71).** One-off calls also send a fresh ID. The comment reads: `// Gateways require session attribution even for a stateless call` → `headers: { "x-opencode-session": SessionID.create() }`.
- **Request ID (VERIFIED, `inference-proxy.ts`).** `x-opencode-request-id`, or legacy `x-opencode-request`, is forwarded if present. The core client sending it is **UNVERIFIED**: no hit in core.
- **Body `prompt_cache_key` (VERIFIED):**
  - `OC/packages/ai/src/protocols/open-responses.ts` ~L776-792 sends it on Responses.
  - `OC/packages/ai/src/protocols/openai-chat.ts` ~L789-800 sends it on Chat Completions only when `model.compatibility.supportsPromptCacheKey`. The comment says strict providers 400 on unknown fields.
  - `OC/packages/ai/src/protocols/shared.ts` ~L70-80 caps it at OpenAI's 64-char limit.
  - Other providers get it in their own headers: xAI as `x-grok-conv-id` (`providers/xai.ts` L63), Vercel AI Gateway as `x-session-affinity` (`providers/vercel-ai-gateway.ts` L201).

### 5. Terms on third-party clients

- **VERIFIED (GO).** "OpenCode Go is designed for OpenCode and other coding agents that produce similar types of requests. Traffic is monitored for abuse that degrades the experience for other users."
  - Third-party coding agents are explicitly allowed, and some are "validated".
  - "Although we do not guarantee that they will continue to work in the future."
- **VERIFIED (ZEN, Goals).** "Have no lock-in: use Console with any coding agent, or use another provider with OpenCode." The intro also says: "get an API key to use them with OpenCode or another coding agent."
- **VERIFIED (ToS).** The ToS has no clause limiting paid use to the OpenCode client. Relevant clauses:
  - "You will only use the Services for your own internal use, and not on behalf of or for the benefit of any third party".
  - Prohibitions include uses that "creates, maintains, or uses multiple accounts to circumvent usage limits", "automatically or programmatically extracts data or Output", and "runs ... any processes that run or are activated while you are not logged into the Services, or that otherwise interfere with the proper working of the Services (including by placing an unreasonable load on the Services' infrastructure)".
- **VERIFIED (live).** The free tier, as distinct from Go and paid Console, is restricted to the OpenCode client (the FreeTierError above).

### 6. Rate-limit / quota errors

- **VERIFIED (`OC/packages/ai/src/provider-error.ts` ~L119-127).** The comment reads: "OpenCode Zen reports account caps as typed 429/402 errors that are not throttles". Quota codes are `insufficient_quota`, `usage_not_included`, `billing_error`, `gousagelimiterror`, `freeusagelimiterror`, `creditlimitexceeded`. These are classed QuotaExceeded and not retried.
  - The error `type` names are probably `GoUsageLimitError`, `FreeUsageLimitError` and `CreditLimitExceeded`, matching the live `MissingSessionID` / `FreeTierError` style. Those exact names are an inference: **UNVERIFIED**.
- **VERIFIED (same file, ~L161-163).** "OpenCode Zen replaces upstream codes outside its allow-list but keeps the original as a `[code]` label at the start of the rewritten message" (regex `^[^:\n]+: \[([A-Za-z0-9_.-]+)\]`).
- **VERIFIED (same file, `isRetryable`).** RateLimit errors are retryable. `x-should-retry: true|false` overrides the decision.
- **VERIFIED (generic retry hints).** `OC/packages/ai/src/route/executor.ts` ~L24-54 reads `retry-after-ms`, `retry-after` and `x-ratelimit-{limit,remaining,reset}-*`. `OC/packages/core/src/model-retry.ts` caps the retry-after delay at 15 minutes.
- **UNVERIFIED.** That Go/Zen actually send `retry-after` on 429. This needs a real key.
- **VERIFIED (UI).** `OC/packages/app/src/session/usage-exceeded-dialogs.tsx` shows Go-upsell dialogs for providers `opencode` / `opencode-go` on retry reasons `free_tier_limit` and `account_rate_limit`.
- **Usage endpoint.** `GET /zen/go/v1/usage` → `/go/v1/usage` exists (proxy table). Live `https://opencode.ai/inference/go/v1/usage` with a bogus key returned 401 `{"type":"error","error":{"type":"AuthError","message":"Unauthorized"}}`. Response shape **UNVERIFIED**.
- **Other errors.**
  - The legacy proxy returns 503 `{"error":{"type":"api_error","message":"Inference routing is unavailable. Please retry later."}}` when the backend is unreachable (VERIFIED, source).
  - 404s from the backend are rewritten to 400 (VERIFIED, source).

### 7. Prompt caching

- **VERIFIED (GO/ZEN pricing tables).** Prices include "Cached Read" for every paid model and "Cached Write" for Anthropic/Qwen/GPT. For example, Claude Haiku 5.5 is $0.01 cached read and $0.125 cached write per 1M tokens. The Go request estimates assume tens of thousands of cached tokens per request, so caching is central to the quota math.
- **VERIFIED (GO).** `x-opencode-session` is used "to optimize routing and prompt caching" (sticky routing).
- **UNVERIFIED.** The exact usage fields returned (for example `prompt_tokens_details.cached_tokens` on chat, `cache_read_input_tokens` / `cache_creation_input_tokens` on Messages, `input_tokens_details.cached_tokens` on Responses). This needs a paid key. They presumably pass through upstream-native usage.
- **UNVERIFIED.** Whether Anthropic-format models need explicit `cache_control` breakpoints through this gateway.

---

## Part 2: llama.cpp `llama-server`

All README references are to `LC/tools/server/README.md`.

### Endpoints

- **VERIFIED (README + `LC/tools/server/server.cpp` L266-303).** OpenAI-compatible:
  - `GET /v1/models` (and `/models`)
  - `POST /v1/completions`
  - `POST /v1/chat/completions` (+ `/v1/chat/completions/control`, `/v1/chat/completions/input_tokens`)
  - `POST /v1/responses` (+ `/v1/responses/input_tokens`)
  - `POST /v1/embeddings`
- **VERIFIED.** `/v1/responses` "works by converting Responses request into Chat Completions request."
- **VERIFIED.** Native endpoints: `/completion`, `/tokenize`, `/detokenize`, `/apply-template`, `/props`, `/slots`, `/metrics`, `/health`, and router-mode `/models/load|unload`.

### Anthropic Messages

- **VERIFIED (README "Anthropic-compatible API Endpoints").** Supports `POST /v1/messages` with SSE streaming and `POST /v1/messages/count_tokens` (returns `{"input_tokens": N}`). "Tool use requires `--jinja` flag."
- **VERIFIED.** Documented fields: `model`, `messages`, `max_tokens` (default 4096), `system`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `stream`, `tools`, `tool_choice` (`auto` / `any` / `tool`).
- **VERIFIED (`LC/tools/server/server-chat.cpp` ~L567-575).** The server converts the request to OpenAI form internally (`server_chat_convert_anthropic_to_oai`). There, `tool_choice` `{"type":"any"}` and `{"type":"tool",...}` both map to `"required"`. A named tool is **not** enforced.
- **VERIFIED (~L600-605).** `thinking: {type:"enabled", budget_tokens}` maps to `thinking_budget_tokens`, with a default of 10000.

### Auth (`--api-key`)

- **VERIFIED (README L213-214).** The flags are `--api-key KEY`, a comma-separated list (env `LLAMA_API_KEY`), and `--api-key-file FNAME`.
- **VERIFIED (`LC/tools/server/server-http.cpp` ~L251-300).**
  - The server reads `Authorization` first, then falls back to `X-Api-Key` (comment: "retry with anthropic header").
  - It strips an optional `Bearer ` prefix.
  - `/health`, `/v1/health` and the UI assets are public.
  - A missing or wrong key gives 401 `{"error":{"message":"Invalid API Key","type":"authentication_error","code":401}}`.
- **VERIFIED.** If no key is configured, there is no auth at all.
- **VERIFIED (README "API errors").** Errors use the OpenAI error format.

### Tool calling

- **VERIFIED (README L1422).** "OpenAI-style function calling is supported with the `--jinja` flag (and may require a `--chat-template-file` override...)."
- **VERIFIED (README L232).** `--jinja` is **enabled by default** in the current version: `--jinja, --no-jinja ... (default: enabled)`.
- **VERIFIED (`LC/tools/server/server-common.cpp` L1284-1291).** Without jinja, the server throws "tools param requires --jinja flag" or "tool_choice param requires --jinja flag".
- **VERIFIED (`LC/common/chat.cpp` ~L314-324).** `tool_choice` accepts only the strings `"auto"`, `"none"` and `"required"`. Any other string throws "Invalid tool_choice".
- **VERIFIED, with caveat (`server-common.h` `json_value`, ~L43-55).** An object such as `{"type":"function","function":{"name":...}}` fails the `std::string` type conversion. It is logged as a warning and treated as the default `"auto"`.
- **VERIFIED (`LC/docs/function-calling.md` L25).** Parallel tool calls are "disabled by default, enable it by passing `"parallel_tool_calls": true`".

### Streaming

- **VERIFIED (README).** Streaming is supported on chat completions and SSE on `/v1/messages`.
- **VERIFIED, partly (`LC/tools/server/server-task.cpp` ~L494).** When `include_usage` is set, the final chunk has empty `choices` plus usage, following the OpenAI spec. The mapping from `stream_options.include_usage` to that flag was not located: **UNVERIFIED**.

### Reasoning

- **VERIFIED (README L233).** `--reasoning-format` takes:
  - `none`: thoughts stay unparsed in `message.content`
  - `deepseek`: thoughts go in `message.reasoning_content`
  - `deepseek-legacy`: keeps `<think>` tags in `content` and also fills `reasoning_content`

  Default is `auto`.
- **VERIFIED.** Related flags: `--reasoning-budget N` (default -1, unlimited) and `--reasoning-budget-message`.
- **VERIFIED.** Per-request options: `reasoning_format`, `reasoning_effort` (`none` disables thinking; other values pass to the template), `chat_template_kwargs` (for example `{"enable_thinking": false}`) and `reasoning_control`.
- **VERIFIED.** "The server supports parsing and returning reasoning via the `reasoning_content` field, similar to Deepseek API." Some templates accept reasoning back in history (PR #18994).

### Prompt caching

- **VERIFIED (README L221).** `--cache-prompt` / `--no-cache-prompt` (default: enabled). Per request, `cache_prompt` defaults to `true`; it reuses the common prefix and may cause nondeterminism.
- **VERIFIED (README L222, L539).** `--cache-reuse N` is the minimum chunk size to reuse via KV shifting, default 0 (disabled). The per-request equivalent is `n_cache_reuse`.
- **VERIFIED (README L168).** `--cache-ram N` sets the host-RAM prompt cache size: default 8192 MiB, -1 for no limit, 0 to disable.
- **VERIFIED (README L166).** `--ctx-checkpoints N`, default 32 per slot.
- **VERIFIED (README L177, L243, L592).** Slots:
  - `-np/--parallel N` (default -1 = auto)
  - `-sps/--slot-prompt-similarity` (default 0.10), which routes a request to the slot whose prompt matches best
  - Per-request `id_slot` (default -1 = any idle slot)
- **VERIFIED.** `/slots/{id}?action=save|restore|erase` exists, and needs `--slot-save-path` for save/restore.

### Cached-token reporting

- **VERIFIED (README "Timings and context usage").** The response `timings` object includes `cache_n` (prompt tokens reused from cache), `prompt_n`, `predicted_n` and rates. "The total number of tokens in context is equal to `prompt_n + cache_n + predicted_n`".
- **VERIFIED (README).** Chat completions also return `usage.prompt_tokens_details.cached_tokens`.
- **VERIFIED (`server-task.cpp` L362, L584/695/717, L780).**
  - Chat sets `prompt_tokens_details.cached_tokens`.
  - Responses sets `input_tokens_details.cached_tokens`.
  - Anthropic sets `usage.cache_read_input_tokens = n_cache` and `input_tokens = n_prompt - n_cache`.
  - Anthropic does not report `cache_creation_input_tokens`.

### Capability / info endpoints

- **VERIFIED (README `GET /props`).** Returns `default_generation_settings` (including `n_ctx`), `total_slots`, `model_path`, `chat_template`, `chat_template_caps`, `modalities` (`{"vision": bool}`), `build_info` and `is_sleeping`. Read-only unless the server runs with `--props`.
- **VERIFIED (README `GET /v1/models`).** Always returns one element. It carries:
  - `architecture.input_modalities` / `output_modalities`
  - `meta` with `n_ctx_train`, `n_vocab`, `n_params` and `size`. `meta` may be null while loading.

  The `id` is the `-m` path unless `--alias` is set.

### Defaults

- **VERIFIED (README L50).** `-c, --ctx-size N`: "size of the prompt context (default: 0, 0 = loaded from model)". In other words, the default is the model's trained context.
- **VERIFIED (README L165).** `--kv-unified-per-slot N` sizes the pool to n_parallel*N when `-c` is unset.
- **VERIFIED (README L193-194).** Default host is `127.0.0.1` and default port is **9931** (`--port PORT ... (default: 9931)`). The Docker examples in the README still use 8080 explicitly.
