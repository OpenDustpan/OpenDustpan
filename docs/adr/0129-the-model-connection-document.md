# 129. The operator sets every field of the model connection

Status: accepted · 2026-10-18 · supersedes parts of ADRs 120 and 121 (see "What this ADR replaces")

## Context

ADR 120 decision 3 lets an administrator connect one model for the
installation: an adapter, a base address, a model name and a credential.
ADR 121 gives the agent a provider-neutral model port, `agent.Model`, and
plans two adapters. ADR 121 also leaves open prompt caching and how a
connection names the details of each model server.

The project owner decided on 2026-10-18, through the project manager:

- No default model yet. No benchmark shows yet that a small model is fit
  for the work of Dusty.
- The product does not document or name a specific model provider. It
  makes configurable each setting that a provider can need. The choice of
  a provider, and the terms of that provider, are the decision of the
  operator.
- For now, the operator sets every field: the base address, the wire
  format, the model name, the authentication, extra headers, and the
  fields for tools, a stream, reasoning, prompt caching and errors.
- The authentication names a header and a secret reference. The
  configuration never holds a secret value.
- Every persona is first class: a homelab with a local model server, a
  startup with a hosted account, and an enterprise with a gateway.
- The worked examples are generic, and the examples of a homelab come
  first.

The project manager also reported a result of the agent benchmark: models
repeated tokens that a person pasted into a prompt.

Model servers, gateways and hosted APIs differ in these mechanisms. The
design makes each one a field of the connection document:

- Most model servers and gateways serve the Chat Completions format. Some
  serve the Messages format, and some serve both.
- The name of the field for the output token limit differs between servers
  of one format.
- Support for `tool_choice` differs. Some servers refuse the field, some
  accept only some values, and some models refuse a forced tool. So Dusty
  never forces a tool.
- Some model servers give tool calls only when their operator turns tool
  calls on. A model can also answer with no tool call at all. So a probe
  at connect time must check tool calls.
- Prompt caching is automatic at some servers and explicit at others,
  with `cache_control` breakpoints. Some servers route a cache by a body
  field or a header that holds a stable identifier for each conversation.
- Some gateways refuse a request with no identifier for each
  conversation. Some ask each client to send its own user agent.
- Some formats define a field that identifies an end user. No server that
  the design must support requires it.
- Some servers answer reasoning parts with a signature, in a field whose
  path differs. The next request must send the part back unchanged, or the
  server refuses it.
- Rate-limit hints have many forms: `retry-after` in seconds or as an HTTP
  date, a header in milliseconds, durations such as `6m0s`, and RFC 3339
  times. The header names differ.
- Some servers report a used-up plan or quota as a 429 or a 402 that is
  not a throttle. Only an error type or code in the body tells the cases
  apart, and its values differ.
- Most hosted APIs accept a static key in a header. The header name and
  the prefix differ. Some APIs accept only signed requests or short-lived
  OAuth tokens.

## Decision

### No default

1. **No default model, no default provider, no preset.** The server
   fills no field of a connection with a value for a provider. The
   defaults are empty values, the `userAgent` of the release (decision
   12) and the `retry-after` header of RFC 9110 (decision 23). The
   console shows no recommended model, and the docs give generic worked
   examples (decision 36), not a default. With no connection, Dusty is
   off (ADR 120 decision 3). A default needs a benchmark result and a new
   ADR.

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
   | `format` | the wire format (decision 5) | none |
   | `baseURL` | the address before the path of the format (decision 4) | none |
   | `model` | the model name that the request sends, as is | none |
   | `auth` | the header and the secret reference (decision 9) | none |
   | `headers` | extra headers (decision 11) | empty |
   | `query` | extra query parameters (decision 11) | empty |
   | `userAgent` | the `User-Agent` header (decision 12) | `OpenDustpan/<release>` |
   | `outputTokens` | the field name and the limit for each call (decision 13) | none |
   | `contextTokens` | the context window of the model, for the status route and the console | empty |
   | `tools` | the tool fields (decision 14) | none |
   | `transport` | `whole` or `stream` (decision 15) | none |
   | `reasoning` | the reasoning parts to keep (decision 16) | empty |
   | `cache` | the caching mode (decision 17) | none |
   | `conversationKey` | where the conversation key goes (decision 18) | empty |
   | `providerFields` | extra fields of the request body (decision 21) | empty |
   | `errors` | the error types or codes for a quota and for a long context (decision 22) | empty |
   | `rateLimit` | the headers that hold a wait (decision 23) | `["retry-after"]` |
   | `requestIDHeaders` | the headers that hold the request ID of the server (decision 22) | empty |

   A path in a field is a dotted path into a JSON object, for example
   `metadata.session`. It has 1 to 8 segments and at most 128 bytes. A
   segment is a decimal index, or a name of letters, digits and `_` that
   does not start with a digit.

   `contextTokens` is optional. The status route answers it, and the
   console shows it. The loop and the adapter do not read it in version 1:
   a request that is too long gets an error from the server, and the
   adapter maps that error to `ErrContextTooLong` (decision 22).
4. **The base address.** `baseURL` is an absolute URL of at most 2048
   bytes. Validation refuses user information, a query string (`query`
   holds query parameters), a fragment, and a path that ends in the path
   of the format. The adapter appends the path of its format to `baseURL`:
   `/chat/completions`, `/messages` or `/responses`. A server that uses
   another path is not supported in version 1. The plain-secret check of
   decision 11 also runs on the path, because some gateways put a key in
   the path. The scheme and the host follow the URL check of decision 26.
   The adapter never puts a URL in an error or a log: it replaces each
   `*url.Error` with its operation and the class of its inner error.

### Wire formats

5. **Three formats, two first.** `format` takes one of these values:

   | Value | The format | Support |
   | --- | --- | --- |
   | `chat-completions` | the Chat Completions format | first adapter change |
   | `messages` | the Messages format | first adapter change |
   | `responses` | the Responses format | a later change |

   Chat Completions comes first because most local model servers and
   gateways serve it. Messages comes first too, because ADR 120 planned
   it and its caching is explicit. A connection with `responses` fails
   validation with `ErrFormatNotSupported` until its adapter ships.
6. **No other format.** The server has no adapter for a format of one
   provider or one model server. An operator who needs such a format puts
   a gateway in front of it that serves one of the formats of decision 5.
7. **No request signing and no token exchange.** The server does not sign
   requests. It does not get or refresh an OAuth access token. A
   credential is one static secret. An operator who must use signed
   requests or a short-lived token runs a gateway that does it.
8. **The adapter of each format.** Each adapter is in its own package
   under `pkg/modeladapter/`, for example `pkg/modeladapter/chatcompletions`
   and `pkg/modeladapter/messages`. An adapter imports `net/http`, so it is
   outside `pkg/agent/`, which the depguard rule for domain packages
   covers. `pkg/agent` keeps the port and imports no transport. Each
   adapter uses `net/http` and `encoding/json` only (ADR 121 decision 8).
   One adapter serves every server of its format. No adapter has a code
   path for the name of a provider or a host. A difference between servers
   is a field of the document.

### Authentication and secrets

9. **`auth` names a header and a secret item.** The form is:

   ```json
   "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." }
   ```

   The adapter sends the header `header` with the value `prefix` and the
   value of the secret item. `prefix` can be empty, for a server that
   takes the key alone in a header such as `x-api-key`. `auth` can also be
   `{"none": true}`, for a model server that takes no key. The document has
   no field for a plain value. Validation refuses a `secret` that is not a
   reference to a secret item of the connection. `header` follows the
   name rules of decision 11.
10. **The secret is a secret item of the scope `@server`.** A secret item
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

    Each reference in the connection or in a pending document (decision
    29) is a row with a foreign key to its secret item. A `DELETE` of an
    item that a reference row holds fails on the foreign key, and the
    route answers 409. A connection write adds its reference rows in its
    own transaction, and fails on the foreign key when the item does not
    exist. So a `DELETE` of an item and a `PUT` of the connection cannot
    both pass, also under the read committed isolation of PostgreSQL.

    No route answers a value. A rotation takes effect at the next model
    call. The value is never in a log, an audit entry, a metric, an error
    or a transcript. The audit log is on for the agent (ADR 120 decision
    5), so each write of a secret item writes an entry (decision 33).
11. **Extra headers and query parameters.** `headers` maps a header name
    to `{"value": "..."}` or `{"secret": "itm-..."}`. `query` maps a
    parameter name to the same two forms. Each holds at most 16 entries,
    and each value at most 1 KiB. A query parameter name holds letters, digits and `-._~` only. These rules apply:
    - A header name is a token of RFC 9110: letters, digits and
      ``!#$%&'*+-.^_`|~``, with no space and no `:`. Validation compares
      names with no regard to case, and refuses two names that differ
      only in case.
    - A value holds visible ASCII, space and tab only.
    - Validation refuses these header names: the headers that the
      transport owns (`Host`, `Content-Length`, `Content-Type`,
      `Transfer-Encoding`, `Connection`, `TE`, `Upgrade`, `Cookie`, and
      each name that starts with `Proxy-`), the header of `auth`,
      `User-Agent`, and each header of `conversationKey`.
    - Validation refuses a plain value with `ErrPlainSecret` when the
      name holds `token`, `key`, `secret`, `auth`, `session`, `cookie`,
      `password` or `signature`, with no regard to case. It also refuses a
      plain value that the secret filter (decision 24) finds secret-like.
      The error names the field and tells the operator to store the value
      as a secret item.
    - The console makes each new header and query parameter a secret
      item by default. The operator changes it to a plain value.

    The plain-secret check also runs on the keys and string values of
    `providerFields` (decision 21). `providerFields` has no secret form in
    version 1.
12. **The user agent.** `userAgent` sets the `User-Agent` header of each
    request. The default is `OpenDustpan/` and the release, for example
    `OpenDustpan/0.9.0`. A value is printable ASCII, at most 200 bytes.
    The value stays the same between the requests of a release, because
    some servers use it to route a cache.

### Fields of the request

13. **The output token limit.** `outputTokens` holds `field` and `max`.
    `field` is the name of the body field of the limit, for example
    `max_tokens`, `max_completion_tokens` or `max_output_tokens`. The
    operator sets the name that the server documents. Validation refuses
    a name that is not a segment of decision 3, and a name that the
    adapter owns (decision 21). `max` is the limit for each call. The
    adapter sends the lower of `max` and the output tokens that remain in
    the run (ADR 120 decision 16). The adapter sends no `temperature`,
    `top_p` or other sampling field of its own, because some reasoning
    models refuse them.
14. **Tools.** `tools` holds `choice` and `parallel`:
    - `choice` is `omit` or `auto`. With `omit`, the request has no
      `tool_choice` field, for a server that refuses it. The adapter never
      forces a tool.
    - `parallel` is `omit`, `on` or `off`. With `omit`, the request has
      no field for parallel tool calls. For `chat-completions`, `on` and
      `off` set `parallel_tool_calls` to `true` and `false`. For
      `messages`, they set `tool_choice.disable_parallel_tool_use` to
      `false` and `true`. Some servers need `on` for more than one call in
      a turn.
    - The Messages format puts that field in `tool_choice`. So for
      `messages`, validation refuses `parallel` `on` or `off` with
      `choice` `omit`.

    The adapter sends the tool catalog (ADR 122) in the order of the
    catalog, with each JSON Schema in canonical JSON. So the bytes of the
    tools stay the same between requests, as a cache needs.
15. **Transport.** With `whole`, the adapter asks for one whole answer.
    With `stream`, it asks for a stream, reads it to the end, and builds
    one whole answer. The port keeps no stream (ADR 121 decision 6). A
    stream helps when a gateway or a server closes an idle connection on a
    long answer. With `stream` on Chat Completions, the adapter sets
    `stream_options.include_usage`, so the last event holds the token use.
    A server that refuses that field fails the `transport` check of the
    probe, and the operator then sets `whole`. The adapter ignores SSE
    comment lines, for example keep-alive lines. An error event in a
    stream with the status 200 is an error of the call, with the mapping
    of decision 22.
16. **Reasoning parts.** `reasoning` holds `keep`, a list of up to 8
    entries. With an empty list, the adapter sends no reasoning part back.
    - For `chat-completions` and `responses`, an entry is a path
      (decision 3) in the assistant message of the answer, for example
      `reasoning_details`.
    - For `messages`, an entry is a type of content block, for example
      `thinking`.

    The adapter keeps the value of each entry, with its signature, as an
    opaque part (decision 37). It sends the part back unchanged in the next
    request of the run, at the same path or as the same block. Validation
    refuses an entry that the adapter owns, for example `content`,
    `tool_calls` or `role`. The fields that turn reasoning on and set its
    effort differ for each server. The operator sets them in
    `providerFields`.

    With `stream`, the adapter builds the value of each entry from the
    deltas, with one rule for each format:
    - For `chat-completions` and `responses`, the adapter merges the value
      at the path of the entry in each delta into the value that it built.
      It appends a string to the string. It merges an object key by key,
      with the same rule. It appends each element of an array. An element
      with an `index` field merges into the element with the same `index`,
      with the same rule. Another value replaces the value that it built.
    - For `messages`, the start event of a block of a type in `keep` gives
      the block. Each delta event of that block appends each string field
      other than `type` to the field of the block with the same name, for
      example `thinking` and `signature`. The stop event ends the block.

    The probe has a `reasoning` check (decision 30) for a `keep` list that
    is not empty.
17. **Caching.** `cache` holds `mode` and `ttl`:
    - `none`: the adapter adds no cache field.
    - `automatic`: the server caches by itself. The adapter adds no cache
      field. It keeps the prefix stable (decision 20).
    - `breakpoints`: the adapter adds `cache_control` blocks, for the
      Messages format and for a Chat Completions gateway that takes them.
      It puts one block at the end of the tools, one at the end of the
      system prompt, and one at the last message. Three blocks stay below
      the limit of 4 that the Messages format sets.
    - `ttl` is empty or a duration such as `5m` or `1h`, at most 8 bytes.
      The adapter sends it as is in each block. It applies only to
      `breakpoints`.

    The adapter acts the same with `none` and with `automatic`. Only the
    `cache` check of the probe differs: it runs for `automatic` and
    `breakpoints`, and not for `none`. The answer reports the cache read
    and cache write tokens of each call, and the token ledger records them
    (ADR 120 decision 20). So an operator can see whether the cache works.

### The conversation key

18. **One key for each conversation, in the fields that the operator
    names.** A conversation is a chain of agent runs (ADR 120 decision
    6). The conversation key is the same for each model call of one
    conversation, and differs for each conversation. `conversationKey`
    holds the places where the adapter sends it:
    - `headers`: up to 4 header names, for example `["x-session-id"]`.
      Each follows the name rules of decision 11.
    - `bodyField`: one path (decision 3) in the request body, for example
      `prompt_cache_key` or `metadata.session`, or empty.
    - `prefix`: text before the key, up to 16 bytes, for example `conv_`.

    With all fields empty, the adapter sends no key.
19. **The key has no identity of a person.** The key is the first 26
    characters, in lower-case base32, of HMAC-SHA256 over the ID of the
    first run of the conversation. 26 characters hold 130 bits. So:
    - the key stays the same for every run, turn and retry of the
      conversation, also after a takeover by another instance,
    - a server cannot get a run ID, a subject, a namespace or a name from
      the key,
    - two conversations of one person have two keys that a server cannot
      link by the key.

    The HMAC key is one random 32-byte value for the installation, sealed
    in the scope `@server` (ADR 23). It is not a secret item of decision
    10: the secrets routes do not list, replace or remove it, and no route
    answers it. The server makes it at start, before it serves a route:
    an insert into a table with one row and a unique key, with an
    on-conflict clause that does nothing, and then a read of the row. So
    two instances that start at the same time make one key, also under the
    read committed isolation of PostgreSQL, and the first probe can send
    the conversation key. A probe has no run, so the probe computes the
    key over its own probe ID. The HMAC key is in the table list of a
    backup (ADR 30), sealed. No route rotates it in version 1. A new key
    changes the conversation key of each running conversation. The only
    cost is one cache miss for each conversation.

    The adapter never sends an end-user field: `user`,
    `safety_identifier`, `metadata.user_id`, or a header with a subject,
    an email address or a name. Validation refuses these names in
    `providerFields` and in `headers`. A server can still link requests by
    the credential and by the address of the server.
20. **A stable prefix.** A cache matches a prefix of exact bytes. So:
    - The system prompt has a version, and has no time, date, name of a
      person, namespace or run ID (ADR 120 decision 23).
    - The catalog has a fixed order, and its JSON is canonical (decision
      14).
    - The loop only appends to the transcript of a run, and a follow-up
      run starts with the transcript of its parent.
    - The user agent and the headers are the same for each call of a
      release (decision 12).

    A revealed value (ADR 124 decision 9) changes the bytes of one tool
    result in one call. The cache misses for the rest of that request
    only.

### Request body fields

21. **`providerFields` is a JSON object of extra fields.** The adapter
    merges it into each request body with a deep merge: when the body and
    `providerFields` both hold an object at a key, the adapter merges the
    two objects key by key, with the same rule. It adds each other key of
    `providerFields` that the body does not hold. `providerFields` holds at
    most 8 KiB and at most 8 levels. Examples are a routing object of a
    gateway, the reasoning fields of decision 16, and a context size option
    of a local model server.

    Validation checks the full path of each key of `providerFields`, at
    each level. It refuses a path that is an owned path or is below an
    owned path, and a value that is not an object at a path above an owned
    path. These are the owned paths:
    - `model`, `messages`, `system`, `input`, `tools`, `tool_choice`,
      `parallel_tool_calls`, `stream`, `stream_options`, and the `field` of
      `outputTokens`,
    - `cache_control` at each level, and the path of
      `conversationKey.bodyField`,
    - the end-user fields of decision 19: `user`, `safety_identifier` and
      `metadata.user_id`.

    So the deep merge never changes a value that the adapter wrote. The
    server does not check the meaning of the other keys. The probe of
    decision 29 shows whether the server accepts them.

### Errors

22. **The error mapping.** Each adapter maps a status and an error body
    to a sentinel of ADR 121 decision 4. It reads the fields `error.type`
    and `error.code` of the body, never its text. `errors` holds two lists
    of these values, each with up to 16 strings of at most 64 bytes:
    `errors.quota` and `errors.contextTooLong`. Both are empty by default.

    | Answer | Sentinel |
    | --- | --- |
    | 401, 403 | `ErrModelAuth` |
    | 402, or a 429 with a type or code in `errors.quota` | `ErrModelQuota` (new, decision 37) |
    | other 429 | `ErrModelRateLimited`, with the wait of decision 23 |
    | 408, 409, 500, 502, 503, 504, 529, a broken stream | `ErrModelUnavailable` |
    | 400 or 413 with a type or code in `errors.contextTooLong` | `ErrContextTooLong` |
    | other 4xx | `ErrModelRequest` |

    `requestIDHeaders` lists up to 4 header names. The ledger row of a call
    keeps the value of the first of these headers that the answer has, for
    support.
23. **The wait of a rate limit.** `rateLimit.waitHeaders` lists up to 8
    header names. The default is `["retry-after"]`. The adapter reads them
    in order, and uses the first value that parses. It parses each value
    in the same way, whatever the header:
    - a decimal number is seconds, or milliseconds when the header name
      ends in `-ms`,
    - a duration such as `6m0s`, `1.5s` or `250ms`,
    - an HTTP date,
    - an RFC 3339 time.

    The adapter computes the wait of a time with its own clock. With no
    hint, the loop uses its backoff. A wait above 5 minutes stops the run
    with the stop reason `model-rate-limited`, because a turn must not hold
    a claim
    that long. `ErrModelQuota` stops the run with `model-quota`.

### The secret filter

24. **The secret filter is always on.** `agent.SecretFilter` removes text
    that looks like a secret. No setting, flag or connection field turns
    it off. It runs on readable text:
    - on the prompt of a person, before the prompt is in the transcript or
      reaches a model. The run answer tells the person how many values the
      filter removed,
    - on each tool result, before the transcript records it and before it
      reaches a model. A revealed value of ADR 124 decision 9 is the one
      exception: the model gets it in one call, and the transcript gets it
      masked,
    - on each answer of the model, before the model step records it: the
      text, the arguments of each tool call, and the readable copy of each
      reasoning part,
    - on each error of the probe (decision 29).

    An opaque reasoning part (decision 37) must go back unchanged, so the
    filter does not change it. The store seals it, no route answers it,
    and the console never shows it. If the format also gives a readable
    copy of the part, the transcript holds that copy, filtered. Each
    record in readable form goes through the filter. The record of an
    opaque part in readable form is its size and its format only.

    The filter removes the exact value of each secret item of the
    connection, and each match of a list of patterns in the binary: known
    key and token prefixes, PEM private key blocks, JSON web tokens, the
    `Authorization` header form, and a value after a name such as
    `password` or `secret`. The list has no rule on entropy alone, because
    resource IDs and hashes of infrastructure match one. A test corpus of
    secrets and of IDs from state checks the list. Each match becomes
    `[removed: secret-like text]`. ADR 124 decision 9 still masks each
    revealed value. The filter is a second defense that does not depend on
    the model.

### Egress

25. **The egress allowlist.** A model call goes through `pkg/egress` and
    `egresshttp`. The allowlist is a setting with the action
    `server:manage-settings` on `@server`, at `GET` and `PUT
    /api/server/egress-allowlist`. With no entry, no model call goes out.
    An entry is one of these:
    - a host name, or a `*.` suffix,
    - a CIDR range with one port, and `"http": true` to allow plain
      `http`.

    Validation refuses a CIDR entry that:
    - is wider than `/24` for IPv4 or `/64` for IPv6, `/0` included,
    - has no port, or more than one port,
    - is in a range that embeds an IPv4 address (`::ffff:0:0/96`, `::/96`,
      `::ffff:0:0:0/96`, `64:ff9b::/96`, `64:ff9b:1::/48`, `2002::/16`, `2001::/32`). The
      operator writes the IPv4 range instead. The range `::/96` also holds
      `::1`, so validation refuses `::1/128`. A model server on the same
      host as the server uses `127.0.0.1`,
    - overlaps the always-denied list.

    The always-denied list is in the binary, and no entry overrides it:
    `0.0.0.0/8`, `169.254.0.0/16`, `255.255.255.255/32`,
    `100.100.100.200/32`, `168.63.129.16/32`, `224.0.0.0/4`, `::/128`,
    `fe80::/10`,
    `fd00:ec2::254/128` and `ff00::/8`. It holds the link-local range, the
    metadata addresses of clouds, the unspecified addresses, broadcast and
    multicast. Each check unmaps an IPv4-mapped IPv6 address with
    `Unmap()` first, and then checks the IPv4 address.

    A public address is an address that `egress.CheckAddr` accepts. So a
    loopback, private (RFC 1918), shared (`100.64.0.0/10`), unique local
    (`fc00::/7`), link-local, multicast or other reserved address is not
    public.

    The server flag `--egress-public-only` makes the server refuse each
    CIDR entry: a `PUT` of the allowlist with one answers 422. With the
    flag, `egress.CheckModelURL` and the dial guard also ignore each CIDR
    entry that the store holds, for example an entry from before the
    restart or from a restored backup. The server logs once at start that
    it ignores these entries, with their count. So a model call reaches
    public addresses only. OpenDustpan Cloud sets this flag. A self-hosted
    operator can set it too, so the hosted service has no path of its own
    (ADR 59). A change of the flag needs a restart.
26. **The URL check of a model connection.** `egress.CheckModelURL` checks
    `baseURL` against the allowlist, at validation and before each call:
    - A host name must match a host entry or a `*.` suffix entry. Its
      scheme must be `https`. Validation refuses plain `http` with a host
      name.
    - An IP literal must be in a CIDR entry, with the port of the URL or
      the default port of the scheme. The check refuses an IP literal that
      no CIDR entry holds, also a public one. The check also refuses a
      host that `egress.CheckURL` reads as an IPv4 address in a short or
      numeric form, for example `127.1`.
    - The scheme can be `http` only for an IP literal in a CIDR entry with
      `"http": true`. A public address always needs `https`.
27. **The dial guard.** The guard checks each resolved address of each
    connection. It refuses an address in the always-denied list first. It
    then allows a public address (decision 25), and an address in a CIDR
    entry on the port of that entry. It refuses each other address. So an
    allowed host name that resolves to an address that is not public
    connects only when a CIDR entry holds that address and port. This
    accepts DNS rebinding of an allowed `https` host name to an address
    that a CIDR entry allows. The design accepts it for these reasons:
    - A homelab often serves a model on an internal host name with an
      internal certificate, on an address that a CIDR entry allows.
    - The operator allowed both the name and the range. A rebinding
      reaches only an address and a port that the operator allowed.
    - TLS must check the certificate for the allowed name, so a rebinding
      to a server with no such certificate fails.

    A change of the allowlist takes effect at the next connection. A call
    in flight ends on its open connection. The client closes its idle
    connections after a change, so each new connection reads the new list.
28. **No proxy, and the limits of a call.**
    - The client follows no redirect.
    - The client uses no proxy. It reads no proxy from the environment.
      The server has no outbound proxy setting for model calls in version
      1, and the egress proxy of jobs (RFC 16) does not apply. With a
      proxy, the dial guard could check only the address of the proxy,
      and the proxy would resolve the target. An operator who must send
      model calls through a proxy runs a gateway behind that proxy, and
      allows the gateway.
    - A model call takes up to 10 minutes, and a stream with no byte for 2
      minutes fails. The body of an answer is at most 4 MiB.

### The probe

29. **The capability probe is a durable operation.** The probe runs as a
    durable operation of the kind `agent.probe` (ADR 72 decision 1), so a
    route never waits on a model:
    - `PUT /api/agent/connection` checks the document, stores it as a
      pending document with the ID of a new probe as its key, starts that
      probe, and answers 202 at once with the probe ID. A probe checks
      only the pending document of its own ID. When the probe passes, its
      last step makes that document the connection, in the transaction of
      its own write, and only when the version of `If-Match` is still the
      version of the connection. Otherwise, and when the probe fails, the
      connection stays as it was, and the probe removes its pending
      document. A probe whose version is no longer current ends with the state `failed`. Its error tells the operator to read the connection and send the `PUT` again.
    - A later `PUT` of the connection, in its own transaction, cancels
      each earlier probe that has a pending document, and removes those
      documents. A canceled probe ends with the state `canceled` at its
      next step. Its last step finds no pending document, and changes
      nothing.
    - `DELETE /api/agent/connection` removes the connection, removes each
      pending document and cancels each probe that has one, in one
      transaction. A check that runs at that time ends at its next step.
      A probe with no store (the next item) holds no pending document, so
      the `DELETE` does not cancel it.
    - `POST /api/agent/connection/probe` starts a probe of a document with
      no store, and answers 202 with the probe ID.
    - `GET /api/agent/connection/probes/{id}` answers the state of the
      probe and of each check: `pending`, `running`, `passed`, `warning`,
      `failed`, `skipped` or `canceled`. For each check it gives the time
      it took, the tokens it used, and for a failure the status class of
      the answer, the sentinel and an error in strict STE.
    - A check has a timeout of 2 minutes, and `reach` 10 seconds. The
      probe has a total timeout of 10 minutes. A timeout fails the check.
30. **The checks.** The probe makes these checks, in order, and stops at
    the first failure:

    | Check | What it does | A failure |
    | --- | --- | --- |
    | `reach` | opens a TCP connection, and a TLS handshake for `https`, with the egress rules. It sends no HTTP request | blocks the connect |
    | `auth` | sends one short text request | blocks the connect |
    | `tools` | offers one probe tool and asks the model to call it, with up to 2 attempts | blocks the connect |
    | `tool-result` | sends the tool result back and asks for a text answer | blocks the connect |
    | `transport` | with `stream`, reads the streams of the earlier checks to their end | blocks the connect |
    | `usage` | checks that the answers report input and output tokens | a warning |
    | `reasoning` | with a `keep` list that is not empty, checks that an answer held a value at each entry. It then sends the values back in one more request, and checks that the server accepts it | a warning |
    | `cache` | with `automatic` or `breakpoints`, sends a fixed prefix of at least 2,048 tokens two times and reads the cache tokens | a warning |

    Dusty needs tool calls. A small local model can answer one attempt
    with no tool call, so the `tools` check makes a second attempt. A
    model that answers both attempts with no tool call fails the check.
    Some servers cache only a prefix above a minimum size, so the `cache`
    warning says that a short prefix can show no cache. A failure gives
    the name of the check and an error in strict STE. The error names the
    field of the document to look at. It never holds the error text of
    the server. Examples:
    - "The model gave no tool call in 2 attempts. Make sure that the
      model server has tool calls on."
    - "The server refused the field max_tokens. Set outputTokens.field to
      the name of the output token field that the server documents."
31. **The probe costs tokens, and the ledger records them.** Each probe call sends the lower of `outputTokens.max` and 1,024 as its output token limit. A full probe makes at most 7 calls, so it uses about 6,000 input tokens and at most 7,168 output tokens. Each
    probe call writes a ledger row with the origin `probe` and no run.
    The key of a probe row is the probe ID, the name of the check and the
    number of the attempt. A second row with the same key changes nothing,
    as a second row for one run, turn and claim (ADR 120 decision 20).
    The installation can start 6 probes in each minute. A `PUT` of the
    connection and a `POST` of the probe route both count. A start above the limit answers 429 with `retry-after`.

### Routes

32. **The connection routes.** Each route declares its action and its
    object (ADR 17). They replace the connection routes of ADR 120
    decision 7:

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/agent/connection` | `server:manage-agent` | `@server` |
    | `PUT /api/agent/connection` | `server:manage-agent` | `@server` |
    | `POST /api/agent/connection/probe` | `server:manage-agent` | `@server` |
    | `GET /api/agent/connection/probes/{id}` | `server:manage-agent` | `@server` |
    | `DELETE /api/agent/connection` | `server:manage-agent` | `@server` |
    | `GET /api/agent/formats` | `server:manage-agent` | `@server` |
    | `POST /api/agent/secrets` | `server:manage-agent` | `@server` |
    | `GET /api/agent/secrets` | `server:manage-agent` | `@server` |
    | `PUT /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` |
    | `DELETE /api/agent/secrets/{ref}` | `server:manage-agent` | `@server` |
    | `GET /api/server/egress-allowlist` | `server:manage-settings` | `@server` |
    | `PUT /api/server/egress-allowlist` | `server:manage-settings` | `@server` |

    The read answers the document with references only, its version, the
    pending document if there is one, and the result of its last probe. A
    `PUT` takes `If-Match` with the version, so two administrators cannot
    overwrite each other. The formats route lists each format, its support
    state and the values of each field, for the console form. Each change
    passes the policy check (ADR 17).

### Audit

33. **The audit entries.** Each of these writes an entry of the kind
    `change` on `@server`, as ADR 126 decision 7 asks for a connect:
    - a connect, a change and a disconnect: `changes` holds the old and
      the new document, with references only, and the probe result,
    - a probe with no store: the result of each check,
    - a create, a rotation and a removal of a secret item: the reference
      only,
    - a change of the egress allowlist: the old and the new list.

    The end entry of a run (ADR 126 decision 6) also holds the count of
    values that the secret filter removed. No entry holds a value, a
    prompt, an answer or an error text of a server.

### OpenDustpan Cloud later

34. **A managed connection can come later, with no break.** OpenDustpan
    Cloud may later offer a connection that the service runs. It is a
    connection document like any other, with a secret item that the
    service owns and an account cannot read. It adds the field `managed`,
    and the account administrator still turns Dusty on (ADR 120 decision
    4). A self-hosted operator can use the same field for the teams of an
    installation, so the hosted service has no feature that the server
    lacks (ADR 59). A managed default model needs the benchmark result
    and a new ADR (decision 1).
35. **Version 1 stays valid.** A new format is a new value of `format`. A
    new field gets a default that keeps the behavior of version 1. A
    connection for each namespace or account (ADR 121 decision 9) is a
    list of documents, each in version 1.

### Worked examples

36. **The docs give these generic examples.** Each is a full document.
    The values `itm-...` are references that the secrets route answered.
    The host names, model names, header names and error codes show the
    form only. They are not a recommendation, and they name no provider.

    A homelab with a local model server on a private address over plain
    `http`, with no key. The egress allowlist has `{"cidr":
    "192.168.1.20/32", "port": 8080, "http": true}`:

    ```json
    {
      "version": 1,
      "format": "chat-completions",
      "baseURL": "http://192.168.1.20:8080/v1",
      "model": "example-model",
      "auth": { "none": true },
      "outputTokens": { "field": "max_tokens", "max": 8192 },
      "contextTokens": 65536,
      "tools": { "choice": "omit", "parallel": "omit" },
      "transport": "stream",
      "cache": { "mode": "automatic" },
      "providerFields": { "options": { "context_size": 65536 } }
    }
    ```

    A homelab with a local model server on the same host as the server,
    with a key. The allowlist has `{"cidr": "127.0.0.1/32", "port": 8081,
    "http": true}`:

    ```json
    {
      "version": 1,
      "format": "chat-completions",
      "baseURL": "http://127.0.0.1:8081/v1",
      "model": "example-model",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "outputTokens": { "field": "max_tokens", "max": 8192 },
      "contextTokens": 65536,
      "tools": { "choice": "auto", "parallel": "on" },
      "transport": "stream",
      "cache": { "mode": "automatic" }
    }
    ```

    A homelab with a hosted gateway that needs a header for each
    conversation, and that reports a used-up plan as a 429. The allowlist
    has `gateway.example.net`:

    ```json
    {
      "version": 1,
      "format": "chat-completions",
      "baseURL": "https://gateway.example.net/v1",
      "model": "example-model",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "outputTokens": { "field": "max_tokens", "max": 16000 },
      "contextTokens": 200000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "stream",
      "reasoning": { "keep": ["reasoning_details"] },
      "cache": { "mode": "automatic" },
      "conversationKey": { "headers": ["x-conversation-id"], "prefix": "conv_" },
      "errors": { "quota": ["plan_exhausted"] },
      "rateLimit": { "waitHeaders": ["retry-after-ms", "retry-after"] }
    }
    ```

    A startup with a hosted API in the Messages format and a bearer key,
    with explicit caching. The allowlist has `api.example.com`:

    ```json
    {
      "version": 1,
      "format": "messages",
      "baseURL": "https://api.example.com/v1",
      "model": "example-large",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "headers": { "x-api-version": { "value": "2026-01-01" } },
      "outputTokens": { "field": "max_tokens", "max": 16000 },
      "contextTokens": 200000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "stream",
      "reasoning": { "keep": ["thinking"] },
      "cache": { "mode": "breakpoints", "ttl": "1h" },
      "errors": { "contextTooLong": ["context_too_long"] },
      "requestIDHeaders": ["request-id"]
    }
    ```

    A team with a hosted API in the Chat Completions format that routes
    its cache by a body field, and a routing object for a gateway. The
    allowlist has `*.example.org`:

    ```json
    {
      "version": 1,
      "format": "chat-completions",
      "baseURL": "https://models.example.org/api/v1",
      "model": "example-large",
      "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-..." },
      "outputTokens": { "field": "max_completion_tokens", "max": 16000 },
      "contextTokens": 400000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "whole",
      "cache": { "mode": "automatic" },
      "conversationKey": { "bodyField": "metadata.conversation" },
      "providerFields": { "routing": { "allowFallbacks": false } },
      "requestIDHeaders": ["x-request-id"]
    }
    ```

    An enterprise with an internal gateway that takes the key in a header
    of its own, and a version as a query parameter. The allowlist has
    `llm.corp.example`:

    ```json
    {
      "version": 1,
      "format": "chat-completions",
      "baseURL": "https://llm.corp.example/v1",
      "model": "example-deployment",
      "auth": { "header": "x-gateway-key", "prefix": "", "secret": "itm-..." },
      "query": { "api-version": { "value": "2026-01-01" } },
      "outputTokens": { "field": "max_completion_tokens", "max": 16000 },
      "contextTokens": 128000,
      "tools": { "choice": "auto", "parallel": "omit" },
      "transport": "whole",
      "cache": { "mode": "none" }
    }
    ```

### Changes to the model port

37. **The port gets one part kind, one error and three stop reasons.**
    - A message part can be `opaque`: bytes of the format of the
      connection, with the name of the format. The loop stores it in the
      transcript, sealed as ADR 124 decision 9 seals a reveal, and gives
      it back to the adapter unchanged. The run, the turn and the part are
      the additional data of the seal. No route answers an opaque part,
      and the console never shows one.
    - A model can repeat a revealed value (ADR 124 decision 9) in its
      reasoning. So from the first model call of a run whose request holds
      a revealed value, each opaque part of the run gets the hard expiry
      of that reveal. After it, the store answers no such part, and the
      reaper removes it. The design uses the hard expiry and not a removal
      at run end, because the reaper removes the part also when the
      durable runner fails the turn for good and no step of the run runs
      again. No later run needs the part, because a follow-up run drops
      the opaque parts of its parent.
    - A run uses the format and the model of the connection at its first
      turn. The `check` step of a later turn ends the run with the stop
      reason `connection-changed` when the format, the model or `baseURL`
      changed. So no tool turn sends an opaque part to another format,
      model or server. A follow-up run starts on the new connection, and
      drops the opaque parts of the transcript of its parent. A change of
      another field takes effect at the next model call of the run.
    - `Info()` answers the format and the model name of the connection,
      not the name of a provider. The status route answers the format and
      the model name.
    - `ErrModelQuota`: the account has no plan, quota or credit left.
      The loop stops the run with the stop reason `model-quota`, and the
      status route says so.
    - A wait above 5 minutes (decision 23) stops the run with the stop
      reason `model-rate-limited`.

    `agenttest.RunModel` gets cases for each rule of decisions 13 to 23:
    each output token field name, `choice: omit`, the conversation key in
    each place and at a dotted path, no end-user field, each cache mode,
    each entry form of `reasoning.keep`, an opaque part that comes back
    unchanged, an error event in a stream with the status 200, each form
    of a wait, a 402, each list of `errors`, and a canary secret in an
    answer that the secret filter removes. It also gets a canary secret in
    an opaque reasoning part: the part comes back unchanged, byte for
    byte, and the canary is in no readable record.

## What this ADR replaces

- **ADR 120, decision 3:** "the adapter, the address of the provider, the
  model name and the credential. The credential is a sensitive setting.
  The server seals it with the key-encryption key (ADR 23)". The
  connection is the document of decision 2, and the credential is a
  secret item of `@server` (decision 10).
- **ADR 120, decision 7:** the rows `PUT /api/agent/connection` and
  `DELETE /api/agent/connection`. Decision 32 gives the routes of the
  connection. Also "the provider and model names" in the answer of the
  status route: the status route answers the format and the model name
  (decision 37).
- **ADR 120, decision 20:** "Each model call writes one row to a token
  ledger, with the run, the turn, the token of the claim". A row also has
  an origin, and a probe row has no run and has the key of decision 31.
- **ADR 120, stop reasons:** the stop reasons of a run get
  `model-quota`, `model-rate-limited` and `connection-changed` (decision
  37).
- **ADR 121, decision 1:** "`Info() ModelInfo`: the names of the adapter
  and the model". `Info()` answers the format and the model name (decision
  37).
- **ADR 121, decision 2:** "A part is text, a tool call ... or a tool
  result". A part can also be opaque (decision 37).
- **ADR 121, decision 4:** the table gets `ErrModelQuota` (decision 37).
  The row `ErrModelRateLimited` changes: it is a rate limit, and not a
  quota (decision 22). The loop retries the turn after the wait, and
  stops the run with `model-rate-limited` when the wait is above 5
  minutes (decision 23).
- **ADR 121, decision 8:** "Two adapters come in a later change, each in
  its own package under `pkg/agent/model/`". Both adapters come in the
  first adapter change (decision 5), in packages under `pkg/modeladapter/`
  (decision 8). The rule on the egress proxy of RFC 16 in that decision
  changes too: decision 28 gives the proxy rule.
- **ADR 121, decision 9:** "The connection holds the adapter, the base
  address, the model name, the credential and an optional output token
  limit". The connection holds the fields of decision 3.
- **ADR 121, "Not decided here":** "Prompt caching across runs, and
  whether the loop asks for it". Decisions 17 to 20 decide it.

## Consequences

- `pkg/agent` gets `ConnectionSpec`, `ConnectionSpec.Validate`,
  `SecretFilter`, `ConversationKey`, `ErrFormatNotSupported`,
  `ErrPlainSecret`, `ErrModelQuota`, the opaque part and the probe kind.
  `agent.Connection` holds the document.
- `pkg/modeladapter/chatcompletions` and `pkg/modeladapter/messages` are
  the first two adapters. The change that adds them adds
  `pkg/modeladapter/` to the Layout section of `CLAUDE.md`. Each adapter
  runs `agenttest.RunModel` against a fake server for each mechanism of
  decisions 13 to 23.
- `pkg/egress` gets the egress allowlist, the always-denied list, the CIDR
  entries, `CheckModelURL`, the `http` rule and the flag
  `--egress-public-only`. The webhook and the iCal feed keep their rules:
  they do not read the allowlist.
- Each SQL store gets the tables of the connection document, the pending
  document, the probes, the secret items of the connection, the HMAC key
  and the egress allowlist. The backup table list gets the same tables.
  The ledger gets the columns `origin` and `providerRequestID`.
- An operator writes more fields than with a preset. The docs and the
  formats route make each field visible, and the probe tells what is
  wrong before Dusty uses the connection.
- The docs name no provider. The operator reads the documentation and the
  terms of the server that they choose.
- A local model in a homelab is as easy to connect as a hosted API: one
  allowlist entry and one document.
- The secret filter can remove text that is not a secret, for example a
  long ID that starts like a key. The person sees the removal in the
  answer. A false positive is safer than a leak.
- `GLOSSARY.md` gets the terms Connection document, Wire format, Secret
  item, Conversation key, Secret filter, Capability probe, Egress
  allowlist and Stop reason. The term Model connection changes.

## Not decided here

- A default model, for self-hosted servers and for OpenDustpan Cloud.
- A hashed end-user identifier for servers that ask for one.
- Request signing and OAuth token refresh in the server.
- A format of one provider or one model server.
- A path of a format other than the fixed path of decision 4.
- A form for a key in the path of `baseURL`. Version 1 has no supported
  form for it, and the plain-secret check refuses one (decision 4).
- A secret form for `providerFields`.
- An outbound proxy for model calls.
- A route that rotates the HMAC key of the conversation key.
- A connection for each namespace or account.
