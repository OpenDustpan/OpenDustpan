# 173. The HTTP engine and its execution port

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that an HTTP provider maps
create, read, update and delete to HTTP calls, with authentication,
pagination, retries, and drift detection through read (owner). Secrets pass
only through the provider configuration of the tool, and never go in plans
or state in plain text where that can be avoided (owner).

The HTTP engine implements the core port of ADR 172 from a declaration
(ADR 171). It has two parts: the engine, which builds requests and reads
replies, and a port that sends one request. The port is in
the package `httpexec` of `opendustpan/providerkit`.

## Decision

1. **The port, `httpexec.Caller`.** `Call` sends one request and returns
   one reply. A reply of any status is a reply, not an error. The errors
   are `ErrTooLarge`, the error of the context, the error of the
   authorizer, and network errors. The engine decides retries, pages and
   mapping, so an adapter stays small and its suite stays exact.
2. **Credentials come from an `httpexec.Authorizer`.** The engine builds a
   request with no credential. The caller asks its authorizer to set the
   credential headers on a copy of the headers, just before it sends. A log
   line or a test record of a request then never holds a credential. The
   kinds are `none`, `bearer`, `header`, `basic` and
   `oauth2ClientCredentials`. The last one gets a token from the token
   endpoint and keeps it until it expires.
3. **No redirect.** The caller never follows a redirect. A 3xx comes back as
   a reply, and the engine reports it as `ErrRejected`. A credential then
   never goes to a host that the user did not configure.
4. **HTTPS.** The base URL and the token URL of `oauth2ClientCredentials`
   are `https`, except for a loopback host, which tests and local tools use.
   They hold no user information, no query and no fragment, as the launcher
   requires for `--server` (ADR 134 decision 3). `Validate` checks a URL
   with no references. The engine checks a URL with references, for example
   `{config.endpoint}`, with `declaration.CheckEndpoint` when it is
   configured, and refuses it with `ErrInvalidConfig`. The engine uses the
   TLS roots of the system and the proxy from the standard environment
   variables.
5. **Requests.**
   - The path escapes each reference as one segment
     (`declaration.PathSegment`). A reference fills a whole segment, so a
     value cannot add a segment or a query. A value that is empty, `.` or
     `..` would make a segment empty or move up the path, so it is
     `ErrInvalidConfig`. `Validate` refuses a literal `.` or `..` segment.
   - The query takes literals and references.
   - The body is a JSON object that the engine builds from JSON pointers.
     A string becomes a JSON string, a number a JSON number, a bool a JSON
     bool, and a list or a map a JSON array or object. A null value leaves
     its key out.
   - `Content-Type` and `Accept` are `application/json`.
6. **Replies.** A 2xx reply is success. The engine reads each attribute from
   a JSON pointer in the reply body, and converts the JSON value to the type
   of the attribute. An attribute that the reply does not map keeps its
   planned or prior value. When the reply of `create` does not give every
   attribute that the remote system sets, the engine runs the `read` request
   after the create, so the values of the apply have no unknown value
   (PM, for the project owner). After an `update`, it does the same when the
   reply does not give each such attribute that is not `stable` and not the
   `id`. The `read` request needs only the `id` (ADR 171 decision 11). A status in `notFound` (default 404) is
   `ErrNotFound`. Any other status is `ErrRejected`, with the status and the
   masked start of the body.
7. **Retries.** `httpexec.RetryPolicy` decides. The default is four
   attempts, the statuses 429, 502, 503 and 504, a first wait of one second
   that doubles, and at most 30 seconds.
   - The engine retries a network error. The timeout of one attempt is a
     network error too. The engine does not retry when the context of the
     whole operation ended: it passes `ctx.Err()` of the operation in
     `Attempt.CtxErr`. It does not retry `ErrTooLarge` or `ErrOtherOrigin`.
   - The engine retries a POST or a PATCH only when the declaration marks
     the request `idempotent`, also after a network error. The server can
     have applied the first one.
   - `Retry-After` in seconds sets the wait. A wait above the limit ends the
     retries, so the engine does not ignore what the server asked for. A
     value above one day counts as one day before the conversion to a
     duration, so a large value cannot overflow to a short wait.
   - The engine adds random jitter to a wait that does not come from
     `Retry-After`.
   - When the retries end, the error is `ErrUnavailable`.
8. **Lists and pages.** A request with `list` reads an array of items, page
   by page, and selects the one item whose fields match. No match is
   `ErrNotFound`. More than one match is `ErrRejected`. The page kinds are
   `none`, `linkHeader` (the `rel="next"` link), `cursor` and `offset`.
   `maxPages` (default 100) stops a list that does not end. The engine
   resolves a `rel="next"` link against the URL of the request that returned
   it, and checks it against the origin (decision 13) before it sends it.
9. **Drift.** The read request of a resource gives the current values
   (ADR 172 decision 3). A declaration that maps few attributes in its read
   detects drift on those attributes only.
10. **Limits.** A reply body above 16 MiB is `ErrTooLarge`. One attempt has a
    timeout, 30 seconds by default.
11. **Logs.** The engine logs the method, the path without query values, the
    status and the duration. It never logs headers or bodies. At the debug
    level of the tool it logs bodies with sensitive values masked.
12. **No new dependency.** The engine and its caller use `net/http`. The
    client credentials grant is a small implementation on the standard
    library, not `golang.org/x/oauth2`.
13. **One origin (PM, for the project owner).** The engine builds the caller
    with the `httpexec.Origin` of the base URL: its scheme, host and port,
    with the default port of the scheme filled in. A request to any other
    origin is `httpexec.ErrOtherOrigin`. The caller sends nothing and does
    not ask its authorizer, so a credential never goes to a host that a
    reply named, for example in a link to the next page. The engine does not
    retry it, and reports it as `ErrRejected`. The token URL of
    `oauth2ClientCredentials` has its own origin and its own client, without
    the credential of the authorizer.

## Consequences

- The caller suite (`httpexectest`) checks the port with local servers: the
  request arrives unchanged with the credential, a 500 is a reply, a
  redirect comes back and is not followed, a large reply fails, an
  authorizer error sends nothing, a canceled context ends the call, and the
  caller refuses a request to another origin and gives it no credential.
- The retry rules have a table test of their own.
- An API that needs a credential in the query cannot be declared, by
  decision 9 of ADR 171. Its team writes an SDK provider and takes that
  risk on purpose.

## Open questions

- A CA file for an API with a private certificate authority. It can be an
  optional configuration attribute that every HTTP provider gets.
- gRPC APIs (owner: "later gRPC").
- `golang.org/x/oauth2` (BSD-3-Clause) is in the OpenDustpan module. If the
  project owner prefers it to a small implementation, it is a new dependency
  of `providerkit`.
