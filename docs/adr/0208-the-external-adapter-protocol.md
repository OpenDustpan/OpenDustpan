# 208. The external adapter protocol

Status: accepted · 2026-10-18 · part of ADR 200

## Context

The project owner decided on 18 October 2026 (owner):

- A team can write and host its own profile provider, the **external
  adapter**. The server calls it with an HTTPS `POST` and a signed JWT. The
  JWT holds `sub`, `aud`, `exp`, `iat`, `jti`, a hash of the body, and the
  claims of ADR 205.
- The protocol is documented, so that an adapter is easy to write.
- The docs never claim compatibility with another product.

The adapter checks the JWT with the discovery document and the keys of
the issuer of the namespace (ADR 204). So the adapter needs no shared
secret, and a team that rotates nothing still gets short-lived proof of
each call.

## Decision

1. **One adapter for every kind.** The adapter names `login/external`,
   `secrets/external`, `outputs/external` and the rotator `external` use
   one protocol. The block gives the URL and the optional audience:

   ```yaml
   providers:
     cmdb:
       use: secrets/external
       with:
         url: https://cmdb-adapter.internal.example/open
         request: { team: payments }
   ```

   The URL must be `https`, with no user information, no query that holds
   a secret input and no fragment. The outbound rules of ADR 206 decision 6
   apply.
2. **The request.** The server sends one `POST` to the URL:

   ```
   POST /open HTTP/1.1
   Host: cmdb-adapter.internal.example
   Authorization: Bearer <JWT>
   Content-Type: application/json
   Dustpan-Request-Id: <jti of the JWT>
   ```

   The body is one JSON object with camelCase fields:

   ```json
   {
     "schemaVersion": 1,
     "operation": "open",
     "kind": "secrets",
     "block": "cmdb",
     "inputs": { "team": "payments" },
     "context": { "namespace": "acme", "access": "write", "unit": "web/prod" }
   }
   ```

   - `operation` is `open` for a provider, and `rotate` or `retire` for a
     rotator.
   - `inputs` are the resolved inputs of `request` in the block. A secret
     input is in plain text in the body, over TLS. So the server sends a
     secret input only to an adapter URL that the profile author wrote,
     and the docs say so.
   - `context` holds the claims of the JWT, for an adapter that reads JSON
     and does not decode the JWT.
   - For `rotate`, the body also holds `state`, the current state of the
     rotator. For `retire`, it holds the state of the rotation to retire.
3. **The JWT.** The issuer of the namespace signs it (ADR 204), with
   `RS256` and the `kid` of the active key. Its claims are:

   | Claim | Value |
   | --- | --- |
   | `iss` | The issuer of the namespace. |
   | `sub` | The subject of the block (ADR 205 decision 6). |
   | `aud` | The `audience` of the block, else the URL with no path. |
   | `iat`, `nbf` | The time of the call. |
   | `exp` | `iat` plus 60 seconds. |
   | `jti` | A random ID with the prefix `oit-`. |
   | `purpose` | `adapter`. |
   | `body_sha256` | The SHA-256 of the exact bytes of the body, in base64url with no padding. |
   | the claims of ADR 205 decision 3 | As for a login token. |

   `purpose` is `adapter`, so a cloud that trusts login tokens of the
   namespace can refuse such a token. The audience of an adapter is
   different from the audience of a cloud too.
4. **What the adapter checks.** The docs give these steps, in this order,
   and the Go package `pkg/profile/external` gives `Verify`, which does
   them:
   1. Read the issuer URL that the adapter trusts from its own
      configuration, never from the token.
   2. Get the discovery document and the JWKS, and cache them for at most
      1 hour. Get them again for an unknown `kid`, at most once a minute.
   3. Check the signature, `alg` `RS256`, `iss`, `aud`, `exp` and `nbf`,
      with at most 30 seconds of clock skew.
   4. Check that `purpose` is `adapter`.
   5. Hash the body bytes as received, and compare with `body_sha256` in
      constant time.
   6. Refuse a `jti` that it saw in the last 2 minutes.
   7. Apply its own rules to the claims, for example `namespace` and
      `collection`.

   `Verify` uses the standard library only, so the package adds no
   dependency to an adapter in Go. The docs also show the same steps in
   Python with a common JWT library.
5. **The answer.** A success is `200` with one JSON object:

   ```json
   {
     "schemaVersion": 1,
     "values": { "dbHost": "db.internal", "dbPassword": "s3cret" },
     "plain": ["/dbHost"],
     "expires": 1792345678000,
     "identity": "cmdb role payments-read"
   }
   ```

   - `values` is any JSON value. Each leaf is secret, except the leaves that
     `plain` names with JSON pointers. A forgotten mark then hides a value
     and never shows one.
   - `expires` is optional, in Unix milliseconds. An open does not cache a
     result past it (ADR 203).
   - `identity` is optional, for the audit entry. The server cuts it to 200
     characters.
   - For `rotate`, the answer also holds `state`, the new state, which the
     server seals with the data key of the profile (ADR 202 decision 8).
     For `retire`, `204` is a success.
6. **Errors.** A failure is a status of 400 or more with the body
   `{"error": {"code": "<code>", "message": "<text>"}}`:

   | Status | `code` | Error of the port |
   | --- | --- | --- |
   | 400 | `invalid` | `ErrInvalid` |
   | 401, 403 | `denied` | `ErrDenied` |
   | 404 | `not-found` | `ErrNotFound` |
   | 429, 500 to 599 | `unavailable` | `ErrUnavailable` |

   The server keeps at most 200 characters of `message`, and masks each
   secret input in it before it stores or shows the text. A redirect, an
   answer above 1 MiB, an answer that is not JSON and an unknown
   `schemaVersion` are `ErrUnavailable`. The server does not retry an
   open. A rotation retries with the backoff of its durable operation
   (ADR 202 decision 8).
7. **Limits.** The body of a request is at most 256 KiB. The timeout is the
   timeout of the block, 10 seconds by default and at most 30 seconds. The
   server sends at most 16 calls at the same time to one host, and the
   other calls of the open wait.
8. **Versions.** `schemaVersion` names the shape of the body and the
   answer. The server sends version 1. A new optional field is an
   addition. A change of a field needs version 2, and the server then sends
   the highest version that the block names with `protocolVersion`, 1 by
   default. **One-way door:** an adapter that a team hosts holds the shape
   of version 1.
9. **The docs.** The reference page of the protocol gives the request, the
   JWT, the checks, the answer and the errors, with one complete exchange.
   A guide gives an adapter of about 50 lines in Go with `Verify`, and the
   same in Python. The docs say "the OpenDustpan external adapter
   protocol", and never that it works like or with the protocol of another
   product.
10. **Conformance.** `pkg/profile/external` holds the client that the
    adapter names use. It runs `providertest.RunProvider` and
    `providertest.RunRotator` against a test adapter in the test, which
    uses `Verify`. Extra cases: a body that does not match `body_sha256`, a
    second use of one `jti`, a token of another namespace, a token with
    `purpose` `login`, and an answer with no `plain`, whose leaves must all
    be secret.

## Consequences

- A team can connect any internal system to profiles with one HTTPS
  endpoint and no shared secret.
- The server sends secret inputs to the adapter. A profile author who
  points a block at a wrong URL sends them there. The save of a revision
  shows each new host of an external block in the change, so a reviewer of
  the change sees it (ADR 202 decision 5).
- The public SDK of the server (ADR 35) gets `pkg/profile/external`, which
  adapter authors import.
