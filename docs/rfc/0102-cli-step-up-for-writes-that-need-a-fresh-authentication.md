---
rfc: 102
title: A step-up of a person in the CLI for writes that need a fresh authentication
status: discussing
created: 2026-10-21
---

# RFC 102. A step-up of a person in the CLI for writes that need a fresh authentication

Status: discussing. This RFC answers RFC 48 question 6, option B (issue
#1234, part of #967). It holds the owner decisions of 20 October 2026 and the
PM decisions of 21 October 2026 as decided, and does not reopen them. It asks
six questions, each with options and a recommendation, and lists the points
that stay open for the owner. The step-up credential is a new credential type,
so the owner accepts it in an ADR before work starts. The package
`internal/stepup` holds the proposed ports and the names of the conformance cases,
with no implementation.

## Problem

Some writes need a fresh authentication (ADR 227). An access token never gives
a fresh authentication (ADR 227 decision 6). So today a person at a terminal
cannot do such a write from the CLI. The person must open the console, sign in
again and do the write there. RFC 48 question 6 chose option A for now: the
client fails with `ErrFreshAuthRequired`, and a person does the write in the
console (ADR 270 decision 7). Option B, a step-up for a person in the CLI, was
left to this RFC.

Three personas from RFC 48 decide the options:

- **A platform engineer at a regulated company.** The engineer wants each
  change to a control to be attributed to a person, with a factor that a
  phishing site cannot relay. The engineer accepts a hardware key touch for
  each such change.
- **A small self-hosting team.** It has no SSO identity provider and no
  hardware keys. It wants the SSH key that each person already has, or a
  TOTP app, and no extra service.
- **A team that manages OpenDustpan as code** (RFC 48). It runs `tofu apply`
  or `pulumi up` with the providers on a workstation. A provider is a child
  process of the tool and has no terminal of its own.

### Facts on `main` (checked 2026-10-21)

- The server holds no built-in MFA code. No package holds TOTP, WebAuthn or
  recovery codes. ADR 46 is a decision with no implementation yet.
- `internal/api/server/gate_config.go` refuses each write to an approval
  default or an approval rule that has no console session (`fresh()`), with
  `403` and a plain message. The answer has no `mfa.fresh-step-required`
  code. The freshness check of a console session reads the sign-in time of
  the session (`checkTokenFreshness` in `internal/api/server/freshness.go`).
- Until PR #1322 merges, `requireFresh` in `internal/api/server/freshness.go`
  passes each request that has no console session, so a token create with an
  access token passes this check. `accesstoken.Service` has a `Freshness`
  port, and a search found no place in `cmd/` that sets it.
- PR #1322 (open on 2026-10-21) makes `main` refuse an access token with
  `403 mfa.fresh-step-required` on these writes: the token create and rotate
  of the caller, of a service principal and of a principal, `PATCH` of the
  session policy, `POST`, `PATCH` and `DELETE` of the sign-in providers, and
  the gate writes. Its `403` has no `reauthenticate` field. A stale console
  session keeps `401` with `reauthenticate`. Issue #1323 asks the owner which
  status is correct.
- `main` does not check freshness yet on grants and act-as edges, on the
  override switches, on the signature carry-over, on reveals, on the disable
  of the audit chain, or on runner join tokens. Issues #1240 and #1159 own
  that work.
- `identity.Principal` has the field `Credential`, with the values
  `session`, `access-token`, `job-token` and `trust-token`, and the field
  `Kind`, with `person`, `service` and `workload`. `ActsAsPerson` fails
  closed: an unknown kind counts as a person. `accesstoken.Kind` has
  `personal` and `service`. No token field records the flow that issued a
  personal token.
- An error answer of the tool-neutral API has `message`, `code` (the HTTP
  status), `errorCode` and `errorArgs` (`codedResponse`). The refusal of a
  stale console sign-in also has `reauthenticate`, the path of a new
  sign-in.
- The installation API is served under `/api/server/...` and also under
  `/api/dustpan/v1/...` (`mountV1` in `pkg/dustpan/dustpan.go`).
- `go.mod` has `github.com/ProtonMail/go-crypto` v1.5.2 as a direct
  requirement since #1306, for the OpenPGP checks of the registry, and
  `golang.org/x/crypto` v0.56.0 as an indirect requirement only. It has no
  TOTP module.
- `GLOSSARY.md` has **Trusted signing key**: an OpenPGP key that a namespace
  registers for its registry. It is not a factor of a person.

### Owner decisions of 20 October 2026 (not reopened)

1. **CLI-native factors (owner decision).** The step-up runs in the CLI with
   factors that work in a terminal, not only with a web redirect:
   - the signature of a server challenge with an SSH key or an OpenPGP key.
     With a FIDO2 `sk-` SSH key on a hardware key, the person only touches
     the key.
   - a TOTP code that the person types in the CLI.

   A person with no CLI factor completes the step in the console.
2. **On by default, a namespace can turn it off (owner decision).** CLI
   step-up is on by default. A namespace can turn it off (opt-out).
3. **Only people do a second factor (owner decision).** A service principal
   does not do a second factor. Other controls cover service principals: the
   IP allowlists of service principals, and the step-up that a person needs
   to act as a service principal or to issue an access token for one.
4. **The rule stays (owner decision).** An access token never gives a fresh
   authentication.
5. **PR-comment apply (owner decision, RFC 89 question 8).** When a stack
   requires a second factor for an apply, the console turns off the apply
   from a pull request comment, and a message says why. A comment that cannot
   run gets a reply that refuses and links to the apply in the console. The
   step-up must work with this rule.

### PM decisions (2026-10-21)

The PM made D1 to D5 on behalf of the owner after review round 1, and D6
and D7 after review round 2.
They are final for this RFC.

- **D1. Two levels in v1 (review M3).** The step-up factor levels are `any`
  and `key`. v1 has no `hardware-key` level: without an attestation, the
  server cannot prove that a key is on hardware. A `hardware-key` level comes
  back later, with attestation, in a future RFC. The console path counts as
  `key` only after a WebAuthn assertion, never after a sign-in with a
  password only.
- **D2. A window is not bound to a plan (review S1).** A window credential
  keeps the limits of at most 900 seconds and 50 writes. It binds to the
  access token or the session, and the list of the plan is information only.
  The exclusion list of M5 applies. The credential file gets the hardening
  of S1, and this RFC states the risk that stays from a process of the same
  user.
- **D3. TOTP counters and enrollment (review S7).** The step-up TOTP
  failure counter is separate from the console sign-in counter. The lock
  check and the failure count are one atomic step. An enrollment has a
  pending state. A person can enroll TOTP for the CLI when built-in MFA is
  off, or in an SSO installation after a fresh authentication at the
  identity provider. A replayed time step answers "failed".
- **D4. Only the interactive CLI sign-in answers a step-up (review M4).**
  Only a token from the interactive sign-in flow of the `opendustpan` CLI,
  or a console session, can make a challenge. Automation uses job
  identities and approval rules. Each layer of Q4 names a server-side check.
  A check that runs only in the client stops nothing alone.
- **D5. A key change needs the same level (review M1).** An add or a revoke
  of a step-up key needs a factor at the level `key` or stricter. TOTP adds
  no key and revokes no key.
- **Origin (review M2).** The CLI builds the origin line from its own
  configured URL, and the request lines from its own request. The `201`
  answer of the challenge create holds each field that the CLI needs.
- **Exclusions (review M5).** This RFC writes the explicit list of the writes
  that a window credential never covers.
- **D6. The first key (review round 2).** A person with no WebAuthn and no
  SSO cannot add the first step-up key through the CLI. The person adds it
  in the console after a WebAuthn assertion, or an administrator enrolls it
  (Q2, "Admin enrollment of the first key"). This closes the earlier open
  point on the first key.
- **D7. SSO and the level `key` (review round 2).** A fresh authentication
  at the SSO identity provider (`fresh=1`) counts at the level `any` only.
  This closes the earlier open point on SSO and the level `key`. OpenDustpan cannot see which factor the identity provider
  used. So `fresh=1` alone adds no key and revokes no key (D5), and it does
  not pass a write at the level `key`.
- **Dependencies (review S12).** An open question for the owner. Prefer the
  standard library: RFC 6238 over `crypto/hmac`. Use `golang.org/x/crypto/ssh`
  for `sshsig` only if `go.mod` already has it as a direct requirement. On
  `main`, `golang.org/x/crypto` is indirect only, and
  `github.com/ProtonMail/go-crypto` is direct (open point 2).
- **ADR 46 decision 2 amendment.** An open question for the owner, with the
  conditions of the reviewer (see the section on it).
- **#1323 (401 or 403).** Open for the owner. This RFC aligns with it as an
  open point.

### How this RFC keeps the rule of decision 4

The step-up credential is not an access token, and an access token never
becomes fresh. The server gives a fresh authentication to a request only when
the request holds two things:

- the access token of a person, from the CLI sign-in, which says who acts.
- a step-up credential of that same person and that same token, which proves
  a factor that the person used a short time ago for this write.

The access token alone gives nothing fresh. The step-up credential alone
authenticates nothing: the server refuses it without the access token or the
session that the challenge names. This is the shape of AWS MFA-backed
sessions, where the long-term key gets a separate temporary credential after
an MFA code, and of GitHub sudo mode, where a session gets a separate, shorter
grant.

### What this RFC does not change

- **Approvals and signatures stay in the console.** An approver is a person in
  a session (ADR 225 decision 7). A step-up in the CLI does not make an access
  token an approver. A CLI approval is a separate question for a later RFC.
- **Confirmations stay in the console.** A confirmation comes from an
  interactive console session, never from an access token (ADR 128 decision
  5).
- **The tool CLIs get no step-up.** The Pulumi CLI and OpenTofu send only
  their token, and their protocols have no place for a second credential. A
  write through a tool-compatible API that needs a fresh authentication fails
  with the error shape of that tool, and the message names the console and
  the `opendustpan` CLI.
- **Service principals and automation.** Nothing in this RFC lets a service
  principal, a job or a pipeline get a step-up credential. Automation uses job
  identities and approval rules.
- **One route adds a factor for another person, and only the first key.**
  An administrator can reset the factors of another person (ADR 46 decision
  9), and can enroll the first step-up key of a person who has none (D6). No
  other route adds a factor for another person.

## The flow

```
CLI                                   Server
 |  PUT .../approval-rules/security      |
 |  (access token)                       |
 |-------------------------------------->|  needs a fresh authentication
 |  403 mfa.fresh-step-required          |
 |  errorArgs.stepUp: challenge path,    |
 |  factors, window                      |
 |<--------------------------------------|
 |  POST /api/dustpan/v1/me/step-up-challenges
 |  {method, target, contentType,        |
 |   bodySha256, ifMatch, idempotencyKey}|
 |-------------------------------------->|  binds the challenge (Q1)
 |  201 {id, nonce, origin, principal,   |
 |   credentialKind, credentialId,       |
 |   action, object, method, target,     |
 |   contentType, bodySha256, ifMatch,   |
 |   idempotencyKey, summary,            |
 |   summarySha256, scope, factors,      |
 |   consoleUrl, expiresAt}              |
 |<--------------------------------------|
 |  compares the echoed values with its  |
 |  own, builds the text itself, shows   |
 |  the summary, signs or asks for code  |
 |  POST .../step-up-challenges/{id}/answer
 |  {factor, keyId, signature | code}    |
 |-------------------------------------->|  builds the text from its record,
 |                                       |  verifies (Q2, Q3, Q5)
 |  201 {credential, expiresAt, scope}   |
 |<--------------------------------------|
 |  PUT .../approval-rules/security      |
 |  (access token + Dustpan-Step-Up)     |
 |-------------------------------------->|  checks binding, uses it once
 |  200                                  |
 |<--------------------------------------|
```

**The console path.** With no CLI factor, or when the namespace turns CLI
step-up off, the CLI prints `consoleUrl`. The URL holds only the challenge ID,
never a secret. The person opens it and signs in as the same person. The
accept page shows the summary and the field list, and also who made the
challenge: the name of the access token, the client IP address of the
challenge create, and its time. The person does the fresh authentication of
ADR 227 decision 1 and accepts. The challenge then goes to the state
`accepted`. The CLI polls `GET .../step-up-challenges/{id}` at most once each
5 seconds, as a device authorization grant does (RFC 8628). The first poll
after the accept gets the credential, and the challenge goes to `answered`.
A poll must come from the `Holder` of the challenge: the same access token or
session that made it. A poll from another token or session of the same
person gets `404` (`stepup.ErrNotFound`). The service takes the holder of the
new credential from the poll request, never from the challenge record, and
`CompleteChallenge` refuses a credential whose holder differs from the
challenge, so the store refuses a poll from another holder too.

The device grant pattern has a known phishing risk: an attacker with the
access token makes a challenge and sends the console URL to the person. The
accept page shows the token name, the IP address and the time so that the
person can see that the challenge is not theirs. The page also says "Accept
only a request that you started in your terminal now". The credential still
binds to the access token of the attacker, and the write that it allows is the
one that the summary shows.

The states of a challenge: `open`, `accepted` (console path only), `answered`
(a credential exists) and `void` (expired, failure limit, or revoked). Each
change of state is one store transaction.

## Questions

Each question lists options, a recommendation and a benchmark. The benchmarks
come from public documentation, read on 2026-10-21. Check them again at the
time of the decision.

### Q1. How does a challenge bind to one action and its object, and what does the CLI show?

A signature or a code must not work for another write. A person must know what
the touch or the code allows.

| Option | Gives | Costs |
| --- | --- | --- |
| A. A random nonce and a short expiry only | Simple. The answer proves a recent factor. | The credential works for each write in the expiry. A person who signs for one change signs for any change that a malware process on the workstation sends at the same time. |
| B. A. plus the action and the object | The credential works only for one action on one object, for example `namespace:manage-gates` on `acme`. | A different body for the same object passes. A person approves "change rule `security`" and the request sets `count` to 0. |
| C. B. plus a digest of the request, the `If-Match` value, the `Idempotency-Key`, the server origin and the principal, single use | The credential works for one request with one body on one version of the object, on one server, for one person. A second use fails. | The CLI must compute the digest the same way as the server. A body that the CLI changes after the challenge needs a new challenge. |

**Recommendation: C.**

- **The challenge create.** The CLI sends the method, the request target,
  the `Content-Type`, `bodySha256` (SHA-256 of the exact request body bytes,
  in lowercase hex), the `If-Match` value and the `Idempotency-Key` of the
  write that it will send. The server makes the challenge from these values
  and from its own facts: a nonce of 32 random bytes, the principal ID, the
  credential kind and ID of the request (the access token ID, or the session
  ID for the console), the action and the object of the route, the origin
  (`publicURL`), and an expiry of 120 seconds. A challenge ID has the prefix
  `stc-`.
- **The `201` answer.** It holds each value that the signed text holds, so
  that the CLI can build the text: `id`, `nonce`, `origin`, `principal`,
  `credentialKind`, `credentialId`, `action`, `object`, `method`, `target`,
  `contentType`, `bodySha256`, `ifMatch`, `idempotencyKey`, `summary`,
  `summarySha256`, `scope`, `factors`, `consoleUrl` and `expiresAt`.
- **Who writes which line.** The CLI builds the signed text itself:
  - from its own configuration and its own request: `origin` (the server URL
    of the CLI configuration, normalized as below), `method`, `target`,
    `content-type`, `body-sha256`, `if-match` and `idempotency-key`.
  - from the server answer: `challenge`, `nonce`, `principal`, `credential`,
    `action`, `object`, `summary-sha256` and `expires`.

  The CLI refuses the step when a value that the server echoes differs from
  its own value. The server builds the text from its own record of the
  challenge, never from a text that the client sends, and verifies the
  signature over it. A relay of the challenge of the real server through a
  phishing server gives a text with the origin of the phishing server, when
  the CLI is configured with it. The real server then gets a signature over
  another origin, and refuses it. The server check does not need help from
  the client, as in WebAuthn, where the client writes its own origin into
  `clientDataJSON` and the relying party refuses another origin.
- **The signed text.** One canonical text, in UTF-8, with one `key: value`
  pair on each line, in this fixed order, each line ended by one LF, with no
  CR:

  ```
  opendustpan-step-up: 1
  purpose: write
  origin: https://dustpan.example.com
  challenge: stc-...
  nonce: <base64url of the 32 bytes, no padding>
  principal: <principal ID>
  credential: access-token <access token ID>
  action: namespace:manage-gates
  object: namespace:acme
  method: PUT
  target: /api/dustpan/v1/namespaces/acme/approval-rules/security
  content-type: application/json
  body-sha256: <hex>
  if-match: "<etag>"
  idempotency-key: <key>
  summary-sha256: <hex>
  expires: 2026-10-21T12:02:00Z
  ```

  `purpose` is `write` for one write, `window` for a window and `key-add`
  for the proof of possession of a new key (Q2). A window text has the lines
  `namespace` and `actions` in place of the lines from `method` to
  `idempotency-key`.
- **The `key-add` text.** It has these lines, in this order:

  ```
  opendustpan-step-up: 1
  purpose: key-add
  origin: https://dustpan.example.com
  challenge: stc-...
  nonce: <base64url of the 32 bytes, no padding>
  principal: <principal ID of the person who gets the key>
  credential: access-token <access token ID>
  key-type: sk-ssh-ed25519@openssh.com
  key-fingerprint: SHA256:<base64>
  expires: 2026-10-21T12:02:00Z
  ```

  `principal` and `key-fingerprint` bind the proof to one person and one
  key, so that a proof for one person or one key never adds another. The CLI
  computes `key-type` and `key-fingerprint` from the public key that it
  sends. The server computes both again from the public key of the add
  request and refuses a mismatch. `credential` names the holder of the
  request that made the challenge: the access token or the session of the
  person, or the session of the administrator for an admin enrollment (Q2).
- **Canonical values and boundary checks.** The server checks each value at
  the challenge create with an allowlist, and refuses a value that fails
  (ADR 250 step 1). It does not change a value to make it valid.
  - `origin`: the scheme and the host in lowercase, with no default port
    (443 for `https`, 80 for `http`), and the path of `publicURL` with no
    trailing slash. Only `https`, except for a loopback host.
  - `target`: the path and the raw query, exactly as the request sends them.
    It is the canonical form under `/api/dustpan/v1/`. A write that comes on
    the `/api/server/` form of the same route is compared after the route
    table maps it to its `/api/dustpan/v1/` form. Each handler that needs a
    fresh authentication does the check (layer 2 of Q4 is per handler, not
    per prefix). At most 2048 bytes, printable ASCII from 0x21 to 0x7E.
  - `content-type`: the media type in lowercase, with no parameters except
    `charset=utf-8`. The write must send the same value. A PATCH with
    `application/merge-patch+json` and one with `application/json-patch+json`
    can mean different changes for the same bytes.
  - `if-match`: at most 256 bytes, printable ASCII from 0x20 to 0x7E. No CR,
    LF or other control character, so that a value cannot add a line.
  - `idempotency-key`: the allowlist of ADR 210 decision 6.
  - An absent `If-Match` or `Idempotency-Key` gives the line with an empty
    value, for example `if-match:` with no space after the colon. The server
    refuses a header that is present and empty.
  - `body-sha256`: 64 lowercase hex digits. `summary-sha256`: SHA-256 of the
    summary bytes that the server sends, in lowercase hex.
- **The signatures.** An SSH signature uses the `ssh-keygen -Y sign` format
  (`PROTOCOL.sshsig`) with the namespace `step-up@opendustpan.org`, so that a
  signature for git or for a file never verifies as a step-up, and a step-up
  signature never verifies as a git commit signature. The hash algorithm of
  the signature is `sha256` or `sha512`, and the server refuses each other
  value. An SSH signature holds the public key of the signer. The server
  compares it with the registered key and verifies with the registered key
  only, never with the embedded key. An OpenPGP signature must name the
  fingerprint of the registered key or of one of its signing subkeys as its
  issuer, and the server verifies with that registered key only. An OpenPGP signature is
  a detached binary signature (type 0x00) over the same text, by the primary
  key or a signing subkey, with the notation `step-up@opendustpan.org` of the
  value `1` in the hashed area. The server refuses a text-mode signature
  (type 0x01), a notation in the unhashed area, and a key or subkey that is
  expired or revoked at the time of the answer.
- **Use.** The write that follows must match each bound value. The server
  checks the method, the target, the `Content-Type`, the digest of the body
  that it reads, the `If-Match` value and the `Idempotency-Key`. A mismatch
  refuses the write, stores nothing, makes the credential void and writes a
  failure entry to the audit log. A credential works one time. A retry with
  the same `Idempotency-Key` and the same body is a replay of ADR 210
  decision 6. It answers the stored first answer and does not need a new
  credential.
- **What the CLI shows.** Before the person touches the key or types the
  code, the CLI prints:
  - the origin of its own configuration.
  - the summary that the server writes for the action, for example
    `Change approval rule "security" in namespace "acme": count 2 to 1`.
  - the change that the CLI computed itself from the body, as a field list
    with the old and the new values, so that the person does not rely on the
    server text only.
  - the first 12 hex digits of `bodySha256`, which the CLI computed.
  - the expiry, and the factor that it will use.

  The summary and the field values hold names that other people chose, for
  example a rule name. The CLI writes each control character (C0, C1 and
  DEL, which includes ESC and so each ANSI escape sequence) as a visible
  escape, for example `\x1b`, before it prints the text, so that a name
  cannot move the cursor or overwrite a line. `summary-sha256` binds the
  bytes that the server sent, so that the audit entry proves what the person
  saw. The server also refuses control characters in names at their own
  boundary, and the CLI does not rely on that.
- **The console path** shows the same summary and the same field list on the
  accept page, with the same escape of control characters (`html/template`).

Benchmark: WebAuthn signs the client data with the challenge and the origin
that the client writes, and the relying party checks both. AWS Signature
Version 4 signs the method, the path, the query, the headers and a SHA-256 of
the body of each request. An SSH signature namespace stops a signature for one
purpose from passing in another (`PROTOCOL.sshsig`).

### Q2. How does a person register and revoke step-up keys, and what does the audit entry hold?

| Option | Gives | Costs |
| --- | --- | --- |
| A. Reuse the SSH keys of the forge account | No new registration. | The forge is not the identity of OpenDustpan. A forge admin can add a key to an account. A key for git pushes becomes a key for policy changes. |
| B. A key registry for each person in OpenDustpan, with a fresh authentication to add and to revoke | OpenDustpan decides which key is a factor. The server writes an audit entry for each change. | A person registers each key one time. |
| C. B. with an attestation of each hardware key | The server knows that a key is on hardware. | `ssh-keygen` can write an attestation for a FIDO key, but OpenPGP has none, and the server must hold a list of trusted roots. |

**Recommendation: B, and keep C for the future RFC of the `hardware-key`
level (D1).**

- **Routes**, under the caller (ADR 210 decision 3):
  - `GET /api/dustpan/v1/me/step-up-keys` lists the keys of the caller.
  - `POST /api/dustpan/v1/me/step-up-keys` adds a key.
  - `DELETE /api/dustpan/v1/me/step-up-keys/{id}` revokes a key.
- **Add (D5).** The request holds the public key, a name, and a signature with
  the new key over a challenge of the purpose `key-add`, which proves that the
  person holds the private key. The proof uses its own SSH namespace,
  `step-up-key-add@opendustpan.org`, or the OpenPGP notation value `key-add`,
  so that a registration signature is never a step-up signature. The add
  needs a fresh authentication at the level `key` or stricter: a step-up
  with a key that the person registered before, or the console path with a
  WebAuthn assertion. TOTP adds no key. A console sign-in with a password
  only, or with a recovery code, adds no key. A fresh authentication at the
  SSO identity provider (`fresh=1`) counts at the level `any` only (D7), so
  it alone adds no key. In an SSO installation, each key add needs
  `fresh=1` in addition to the factor at the level `key`, if the owner
  accepts the conditions of the ADR 46 amendment. An access token alone
  never adds a key, so a stolen token cannot add the factor of the thief.
- **Two challenges for one add.** A key add uses two challenges:
  1. A challenge of the purpose `key-add`, which the CLI creates with the
     public key. The new key signs its text (Q1). This proves that the
     signer holds the private key.
  2. The add itself, `POST /api/dustpan/v1/me/step-up-keys`, is a write that
     needs a fresh authentication at the level `key`. It gets a `write`
     step-up as in Q1, with a key that the person registered before or the
     console path with WebAuthn. The body holds the public key, the name,
     the `key-add` challenge ID and its signature, so `body-sha256` binds
     the factor of the step-up to this one new key and this one proof.

  The `key-add` challenge makes no credential. A `key-add` challenge that a
  completed add did not use is void at its expiry.
- **The first key (D6).** A person with no step-up key and no WebAuthn has no
  factor at the level `key`, also in an SSO installation (D7). Such a person
  cannot add the first key through the CLI. The person adds it in the
  console after a WebAuthn assertion, or an administrator enrolls it.
  When no person in the installation can reach the level `key`, neither
  path works. The section "First key enrollment from the no-factor state"
  gives the options for that state.
- **Admin enrollment of the first key (D6).**
  - **Route.** `POST /api/dustpan/v1/principals/{principal}/step-up-keys`,
    also under `/api/server/principals/{principal}/step-up-keys`. The action
    is `server:enroll-step-up-key` on the object `principal:<ID>`. It is a
    new action, and no built-in role has it except the installation
    administrator.
  - **Fresh authentication.** The administrator needs a fresh
    authentication at the level `key`: a `write` step-up with a step-up key
    of the administrator, or the console path with a WebAuthn assertion. A
    window credential never covers it (Q4).
  - **The first key only.** The server refuses the enrollment with `409`
    `mfa.step-up-key-conflict` when the person holds a key, also a suspended
    key. The store checks this in the same transaction as the add
    (`KeyStore.EnrollFirstKey`), so that two enrollments at the same time
    give one key. A later key the person adds with D5.
  - **Never the own account.** The server refuses an enrollment whose
    principal is the administrator, with `422` `mfa.step-up-invalid`. The
    route checks it, and the store checks it again.
  - **Only a person.** The principal must be of the kind `person`, and not
    disabled. Else `403` `mfa.step-up-person-only`.
  - **The proof.** The request holds the public key and a `key-add`
    signature over a challenge whose `principal` line names the person and
    whose `credential` line names the session or token of the administrator.
    The person can sign it and give the public key and the signature to the
    administrator, or the administrator can make the key pair and sign it.
  - **What the proof cannot show.** A `key-add` signature only shows that
    the signer holds the private key. It does not show that the person
    holds it. An administrator who made the key pair, or who kept a copy,
    holds a factor of the person at the level `key`. If that administrator
    also holds a CLI sign-in token of the person, the administrator can step
    up as the person. Without that token, layer 3 of Q4 and D4 stop the
    step-up: a challenge binds the access token or the session of the person
    who makes it, and only the interactive CLI sign-in of the person or a
    console session of the person can make one. The user guide tells the
    person to revoke an enrolled key and add their own, when the
    administrator made the key pair.
  - **Audit and email.** The enrollment writes an audit entry of the kind
    `security`, with the administrator, the person, the key ID and the
    fingerprint, in the same transaction as the add. The server sends the
    email of the notifications below to the person. The key row records the
    administrator (`EnrolledBy`), and the key list of the person shows it.
- **Revoke (D5).** A revoke needs a fresh authentication at the level `key`:
  any registered key of the person, also the key that it revokes, or the
  console path with WebAuthn. TOTP revokes no key, so a thief with a TOTP
  seed cannot remove the keys of the person. A person who lost each key and
  has no WebAuthn asks an administrator for the MFA reset. When that person
  holds the only keys of the installation, the last-holder recovery of the
  section "First key enrollment from the no-factor state" applies. The revoke takes
  effect at once: each step-up credential that the key made stops working.
- **Admin reset.** The MFA reset of ADR 46 decision 9 needs a fresh
  authentication of the administrator. It deletes the keys and the TOTP
  factor of the person, and revokes each step-up credential of the person,
  from a key or from TOTP. A window credential never covers it (Q4). The
  section "First key enrollment from the no-factor state" proposes the level
  `key` for the reset of a person who holds a step-up key. The
  only route that adds a factor for another person is the admin enrollment
  of the first key (D6).
- **Notifications.** The server sends an email to the person (the SMTP of
  ADR 212) on each key add, each key revoke, each TOTP enrollment, each TOTP
  reset, each admin reset and each admin enrollment. The email says what changed, when, and from
  which IP address, and how to revoke.
- **Allowed key types.** `sk-ssh-ed25519@openssh.com`,
  `sk-ecdsa-sha2-nistp256@openssh.com`, `ssh-ed25519`,
  `ecdsa-sha2-nistp256`, and RSA of 3072 bits or more with `rsa-sha2-256` or
  `rsa-sha2-512`. OpenPGP: Ed25519 and RSA of 3072 bits or more. The server
  refuses each other type. A person has at most 10 keys.
- **The key ID.** The server gives each key an ID with the prefix `stk-`. It
  also stores the fingerprint: `SHA256:<base64>` for an SSH key, as
  `ssh-keygen -l` writes it, and the v4 or v5 fingerprint for an OpenPGP key.
- **The counter.** For an `sk-` key, the server stores the signature counter
  of the last signature. A signature whose counter is not higher, unless the
  key always sends 0, is a possible clone. The server then refuses the answer
  with `mfa.step-up-failed`, suspends the key, revokes the credentials that
  the key made, writes an audit entry of the kind `security` and sends an
  email to the person. A suspended key answers no challenge. The person
  revokes it and adds a key again.
- **The audit entry.** Each step-up writes one audit entry in the same
  transaction that saves the credential, with the challenge ID, the factor
  kind, the key ID and its fingerprint (or the TOTP factor ID), the user
  presence and user verification flags of an `sk-` key, the counter, the
  action, the object, `bodySha256`, `summarySha256` and the credential kind
  and ID of the request. The audit entry of the write names the challenge ID.
  The entry never holds the signature, the code or the credential. A key add
  and a key revoke each write an audit entry of the kind `change`.

Benchmark: GitHub keeps signing keys apart from authentication keys on an
account. A FIDO authenticator returns a signature counter, and WebAuthn
Level 2 says that a relying party can use it to detect a cloned
authenticator. NIST SP 800-63B-4 asks for an authentication at the assurance
level of the new authenticator, or higher, before the verifier binds it to an
account.

### Q3. Can a namespace require a phishing-resistant factor and refuse TOTP?

NIST SP 800-63B-4 says that "OTP authentication is not phishing-resistant",
and that a verifier "SHALL offer at least one phishing-resistant
authentication option at AAL2". A signature of a challenge where the server
checks an origin that the client wrote (Q1) resists a relay. A software key
on the disk of a workstation is an exportable key, so it does not meet AAL3,
which needs a non-exportable key. An `sk-` key with no attestation can come
from a software FIDO middleware (`SSH_SK_PROVIDER`), which sets the user
presence and user verification flags itself. So without an attestation the
server cannot prove a hardware key.

| Option | Gives | Costs |
| --- | --- | --- |
| A. No setting. Each factor counts | Simple. | The regulated persona cannot refuse a factor that a phishing site can relay. |
| B. A namespace setting with the levels `any` and `key` (D1) | The regulated persona refuses TOTP. A small team keeps TOTP. | One more setting, with an order of strictness. No level proves hardware. |
| C. Key signing only, for everyone | No phishing risk from codes. | A team with no SSH key culture and no hardware key must use the console for each step. |

**Recommendation: B, with the two levels of D1.** The namespace setting
`stepUpFactors` has two values:

- `any` (the default): an SSH key, an OpenPGP key or a TOTP code.
- `key`: an SSH key or an OpenPGP key. The server refuses TOTP with
  `mfa.factor-not-allowed`.

Neither level is AAL3, and neither level proves that a key is on hardware.
The user guide says so. A level `hardware-key` with an attestation and a
configured list of roots is the subject of a future RFC.

**The console path at each level.**

| Console factor of the fresh authentication | `any` | `key` |
| --- | --- | --- |
| WebAuthn assertion (built-in MFA, ADR 46) | yes | yes |
| TOTP code (built-in MFA) | yes | no |
| A password only, with built-in MFA off | yes, when ADR 227 decision 1 counts it | no |
| A recovery code | no | no |
| A fresh authentication at the SSO identity provider (`fresh=1`, D7) | yes | no |

The console path of a CLI challenge records the console method of the fresh
authentication in the audit entry of the step-up, and its credential records
the level that the method met (`Credential.Level`). At each use, the server
compares that level with the level that holds for the write at that time
(`Use.Level`), and refuses a credential below it with
`mfa.factor-not-allowed`. So a window credential from a TOTP code stops at
its next use when the namespace changes to `key`.

**A write done directly in the console.** This RFC does not decide whether
`stepUpFactors` also governs a fresh write that a person does directly in
the console, with no CLI challenge. It is open point 5, next to the ADR 46
amendment, because the answer decides what the level `key` means in an SSO
installation with no built-in WebAuthn.

The installation holds the same setting as a floor, and the strictest level
that holds for a write applies, as for the window (ADR 227 decision 4). A
change to a less strict level is a weaker setting, so it needs a fresh
authentication (ADR 212 decision 3).

The opt-out of owner decision 2 is a separate namespace setting,
`cliStepUp`, `on` by default. With `off`, each step-up of the namespace goes
through the console. A change from `off` to `on` is a weaker setting. The
installation holds `cliStepUp` too, with `on` or `off`, and `off` there turns
it off for each namespace.

Benchmark: NIST SP 800-63B-4 AAL2 and AAL3, quoted above. GitHub can require
two-factor authentication for an organization. AWS IAM conditions read `aws:MultiFactorAuthPresent`
and `aws:MultiFactorAuthAge`. AWS STS `GetSessionToken` takes a 6-digit
`TokenCode` and a device `SerialNumber`, so the CLI path of AWS is an OTP
code, not a security key.

### Q4. What is the step-up credential, how long does it live, and how does it differ from an access token?

| Option | Gives | Costs |
| --- | --- | --- |
| A. One write. The challenge of Q1 binds the credential, and it works one time | The smallest effect. A window of 0 (ADR 227 decision 3) works with no special case. | A provider apply that changes 12 rules asks for 12 touches. |
| B. A short window. The credential works for named actions in one namespace for a short time, like GitHub sudo mode | A provider apply that changes many objects needs one touch. | Malware on the workstation can send other writes in the window. |
| C. Mark the access token as fresh for a time | No new credential. | It breaks owner decision 4. Not an option. |

**Recommendation: A for each write of the `opendustpan` CLI, and B only for a
provider apply, with these limits:**

- **One write (A).** The credential binds to the request of Q1. It expires
  with the challenge, at most 120 seconds after the challenge create. The
  server uses the credential up in its own transaction before the write, not
  in the transaction of the write. A write that then fails (an `If-Match`
  412, a fence error, a store error) needs a new step. The replay of
  ADR 210 decision 6 covers a retry of a write that succeeded.
- **A window (B).** `opendustpan step-up --namespace acme --for apply` gets a
  window credential before a `tofu apply` or a `pulumi up` with the providers.
  The challenge holds the namespace and the list of actions. When the CLI
  knows the saved plan file, the summary also shows the changes of the plan,
  as information only: the server does not limit the writes to that plan
  (D2). The lifetime is the shortest fresh window that holds for the person
  and the namespace (ADR 227 decisions 2 and 4), at most 900 seconds. A
  window of 0 allows no window credential: each write uses A. The server
  checks the window of each write again, so a rule with
  `freshAuthSeconds: 0` still asks for a new step. A window credential works
  for at most 50 writes. It binds to the access token or the session that
  made the challenge.
- **What a window credential never covers (M5).** The server refuses a
  window credential for each of these writes, with `mfa.step-up-mismatch`,
  and asks for a one-write step (A) or the console. The list is each row of
  Q6 that says "one write only" or "one read only", and a change to Q6 changes
  this list in the same change:
  - a step-up key add or revoke, and the admin enrollment of a first key.
  - a TOTP enrollment, confirmation or reset.
  - the admin MFA reset of another person.
  - a change of `cliStepUp` or `stepUpFactors`.
  - an access token create or rotate, of any principal.
  - `PATCH` of the session policy.
  - `POST`, `PATCH` and `DELETE` of a sign-in provider.
  - a grant of `principal:manage-tokens`, and an act-as edge.
  - the create of a runner join token.
  - the apply of a job (`job:apply`), which binds one job and one plan.
  - a reveal (a secret reveal, a state download by a person).

  The server checks this list on its own. It does not rely on the namespace
  check of the window, which refuses an installation write today only
  because a window has one namespace.

  The list is in the server, not in the challenge, so a client cannot ask for
  a window that holds one of these actions. The challenge create refuses such
  an action in the list with `mfa.step-up-invalid`.
- **The credential file (D2).** The CLI writes the window credential to a
  file in the user configuration directory, and the provider reads it from
  there, never from an environment variable, so that a pipeline log or a
  child process environment does not hold it.
  - One file for each origin and access token ID.
  - The CLI creates the file with `O_CREAT|O_EXCL|O_NOFOLLOW` and mode
    `0600`, in a directory with mode `0700`.
  - On each read, the CLI and the provider open the file with `O_NOFOLLOW`
    and refuse a file with another owner, with a group bit or an other bit
    set, or that is not a regular file.
  - The CLI deletes the file at the expiry and when `opendustpan step-up`
    exits. A reader that finds an expired file deletes it.
- **The risk that stays.** Each process of the same user can read the file
  for up to 900 seconds and 50 writes. Such a process can usually read the
  access token too, and the key agent socket. So the window adds little for a
  person with a software key or a TOTP seed on the same workstation. For a
  person with an `sk-` key, one touch becomes up to 50 writes in the
  namespace. A namespace that does not accept this sets its fresh window to
  0, and then each write needs a touch.
- **How it differs from an access token.**

  | | Access token | Step-up credential |
  | --- | --- | --- |
  | Proves | which principal acts | that the person used a factor for this write, a short time ago |
  | Alone | authenticates | authenticates nothing. The server refuses it without its access token or session |
  | Holder | a person or a service principal | a person only, from the CLI sign-in or a console session |
  | Lifetime | days, up to the maximum of ADR 53 | 120 seconds (A), at most 900 seconds (B) |
  | Uses | any number | one (A), at most 50 (B) |
  | Renewal | rotation | none. A new step |
  | Bound to | the principal | the person, the access token or session, the origin, and the request (A) or the namespace and the actions (B) |
  | Header | `Authorization` | `Dustpan-Step-Up` |
  | Stored by the server | a hash | a hash |
  | Makes another credential | can create a token where its grants allow | never makes a step-up credential. It creates an access token only in mode A, for that one create |

- **It never reaches a pipeline or a job (D4).** Each of these layers is a
  server-side check, and each has its own test that sends the hostile request
  to that check alone:
  1. **The challenge create.** The server makes a challenge only for a
     principal of kind `person` with one of two credentials: a console
     session, or a personal access token that the interactive sign-in flow
     of the `opendustpan` CLI issued. The token row records that flow, a
     server-side fact. A personal access token from the token create route,
     a service token, a job token, a trust token and an unknown kind are
     refused with `mfa.step-up-person-only`. Test: a personal token that the
     token route made gets `403`.
  2. **The header.** The server reads `Dustpan-Step-Up` only in the handlers
     of the tool-neutral API that need a fresh authentication. A middleware
     of the runner protocol, of the tool-compatible APIs and of the MCP
     endpoint refuses each request that holds the header. Test: a runner
     protocol request with the header gets `400`.
  3. **The binding.** At each use, the server refuses the credential when
     the credential kind and ID of the request differ from those of the
     challenge, or the person differs. Test: a valid credential with a
     second token of the same person fails with `mfa.step-up-mismatch`.
  4. **The job spec.** The job spec validator refuses a spec that holds the
     header name `Dustpan-Step-Up` or a value with the prefix `stp-`, in an
     environment value, a variable or a file. A job token and an issued token
     (ADR 205) never get a claim from a step-up credential. Test: a job
     spec with an `stp-` value gets `422`.
  5. **The window exclusions.** A window credential never covers the writes
     of the list above. Test: a window credential on a token create gets
     `mfa.step-up-mismatch`.

  **The limit of the literal filter.** The filter of layer 4 refuses a
  literal paste only: the header name `Dustpan-Step-Up` or a value with the
  prefix `stp-`. It does not find a credential that is encoded (for example
  base64) or split into parts. Do not use layer 4 alone to keep a pasted
  step-up credential out of a job. Layer 1 and layer 3 stop it:
  - **Challenge eligibility (layer 1).** The server makes a challenge only
    for a person with a console session or an interactive CLI sign-in token.
    A job token or an issued token cannot get a credential to paste.
  - **Credential binding (layer 3).** At each use, the server compares the
    credential with the access token or session of the challenge. A step-up
    credential in a job, with a job token or an issued token, fails with
    `mfa.step-up-mismatch`. The credential authenticates nothing alone.

  So a pasted step-up credential alone gives no step-up claim and no fresh
  authentication to a job token or an issued token.

  The CLI does a step-up only when stdin and stdout are a terminal. This is
  a usability guard, not a layer: `script`, `expect` or `docker run -t`
  gives a terminal, and an attacker can write their own client. With no
  terminal, the CLI returns `ErrFreshAuthRequired` (RFC 48 question 6,
  option A).

  **What no layer stops.** A person who puts a software key, an OpenPGP key
  or a TOTP seed into a pipeline, and also the access token of a CLI sign-in,
  defeats each server layer, because the server sees the same requests as
  from the person. Only a factor with user presence resists that, and only
  weakly, because an `sk-` key without attestation can be software (Q3).
  Automation uses job identities and approval rules instead.

Benchmark: AWS `GetSessionToken` returns a separate temporary credential after
an MFA code, from 900 seconds to 36 hours for an IAM user. GitHub sudo mode
lasts about two hours, and each sensitive action resets the timer. NIST
SP 800-63B-4 says that at AAL3 the overall reauthentication timeout "SHALL be
no more than 12 hours", and that the inactivity timeout should be no more than
15 minutes. OAuth 2.0 DPoP (RFC 9449) binds a token to a key of the client. A
DPoP key is a later layer for option B, and this RFC does not propose it.

### Q5. How does a person enroll TOTP, how is the secret stored, and what are the limits?

| Option | Gives | Costs |
| --- | --- | --- |
| A. A TOTP factor for the CLI only | The CLI does not depend on ADR 46. | A person enrolls two TOTP apps for one installation. |
| B. One TOTP factor for each person, shared with built-in MFA (ADR 46), with a separate failure counter for each surface (D3) | One enrollment. A lockout of the step-up never blocks a sign-in. | The CLI step-up waits for the TOTP part of ADR 46. |

**Recommendation: B.**

- **Enroll.** `POST /api/dustpan/v1/me/totp` needs a fresh authentication
  from the console or from a step-up key. The server makes a secret of 20
  random bytes (160 bits, as RFC 4226 recommends) and answers it one time,
  as a base32 string and an `otpauth://` URI. The new factor is `pending`.
  A pending factor answers no challenge and does not replace the active
  factor. `POST /api/dustpan/v1/me/totp/confirm` with one valid code of the
  pending factor makes it active, deletes the earlier factor and revokes the
  credentials that the earlier factor made, in one transaction. A pending
  factor that nobody confirms expires after 10 minutes. A replay of the
  create (ADR 210 decision 6) never answers the secret again.
- **When built-in MFA is off (D3).** ADR 46 decision 5 has built-in MFA off
  by default, and owner decision 2 has CLI step-up on by default. A person can
  enroll TOTP for the CLI step-up when built-in MFA is off. The factor then
  counts for a step-up only, and the console sign-in does not ask for it.
- **In an SSO installation (D3).** A person can enroll TOTP after a fresh
  authentication at the identity provider (`fresh=1`), for each enrollment.
- **Parameters.** RFC 6238: HMAC-SHA-1, 6 digits, a step of 30 seconds. The
  server accepts the current step and one step before it, for clock drift
  and typing time.
- **Storage.** The server seals the secret with envelope encryption, with a
  key of the installation (the `secret` package). A database row never holds
  the plain secret. A backup holds the sealed value only. The service seals
  and opens the secret. The store keeps the sealed bytes and gives them back
  unchanged.
- **One use (D3).** The server stores the last time step that it accepted.
  A code of that step or of an earlier step answers `mfa.step-up-failed`, the
  same answer as a wrong code, and counts as a wrong code. So the answer
  does not tell anyone that the code was valid. NIST SP 800-63B-4 says that
  a verifier "SHALL accept a given OTP only once while it is valid".
- **Rate limits (D3).** A challenge takes at most 5 wrong codes, then it is
  void. The step-up surface has its own counter, apart from the counter of
  the console sign-in. 10 wrong codes in a row on the step-up lock the TOTP
  factor for the step-up for 15 minutes (`mfa.factor-locked`). 100 wrong
  codes in a row disable the factor for the step-up until the person enrolls
  again (`mfa.factor-disabled`), which is the upper limit of NIST SP
  800-63B-4. A good code sets the step-up count to 0. A step-up lockout never
  blocks the console sign-in, and the console path and the keys still work.
  Each wrong code and each lockout write an audit entry.
- **Atomic attempts (D3).** The server reserves one attempt before it checks
  the code: one store transaction checks the lock and the disable, and counts
  the attempt as a failure. The lock and the disable come only from earlier
  attempts. The reservation that takes the count to 10 passes and sets the
  lock in the same transaction. The 11th reservation gets
  `mfa.factor-locked`. A good code of a reserved attempt then sets the count
  to 0, ends the lock, and stores the step, in one transaction. A lock that
  its own reservation or a parallel reservation set does not refuse it. So
  the 10th code still counts: 10 wrong codes lock the factor, not 9. The
  disable at 100 works the same way. Parallel challenges never get more than
  10 guesses before the lock, or 100 before the disable.
- **Recovery codes** (ADR 46 decision 4) are for a sign-in when a person lost
  each factor. They never answer a step-up challenge, and a sign-in with a
  recovery code gives no fresh authentication for a factor add.

Benchmark: RFC 6238 and RFC 4226, NIST SP 800-63B-4 as quoted above. AWS STS
takes a 6-digit code. GitHub sudo mode accepts a TOTP code and refuses an SMS
code.

### Q6. Which writes need a fresh authentication, and what error does the CLI get?

The list comes from ADR 227 decision 2 and the ADRs that it names. This RFC
adds no write to it, except the step-up settings and factors. It says which
writes the CLI step-up can satisfy, and which writes `main` checks today.

| Write | Source | Fresh check on `main` | CLI step-up |
| --- | --- | --- | --- |
| A change of a gate object. The objects are approval rules, approval defaults, access limits, path sets, final gates, and gates and ownership rules in `enforce` mode | ADR 230 decision 2 | session only today; `403` for a token with #1322 | yes |
| The creation and the rotation of an access token, of the caller, of a service principal and of a principal | ADR 227 decision 2, ADR 70 decision 2 | with #1322 | yes, one write only (Q4) |
| `PATCH` of the session policy | ADR 227 decision 2 | with #1322 | yes, one write only |
| `POST`, `PATCH` and `DELETE` of a sign-in provider | ADR 227 decision 2 | with #1322 | yes, one write only |
| The signature carry-over setting to `on` | ADR 225 decision 12 | no: #1240 | yes |
| A setting that makes the installation or a namespace less secure, also the disable of the audit chain | ADR 212 decision 3 | no: #1240 | yes |
| A grant of `principal:manage-tokens`, and a trust rule that names a principal (an act-as edge) | ADR 70 decisions 1, 3 and 10, owner decision 3 | no: #1159 | yes, one write only |
| The changes of ADR 70 decisions 16, 17 and 23 | ADR 227 decision 2 | no: #1159 | yes |
| The override switches of ADR 125 decision 3 | ADR 227 decision 2 | no: #1240 | yes |
| A secret reveal, and a state download by a person (a reveal) | ADR 227 decision 2, ADR 240 | no: #1240 | yes, one read only |
| The create of a runner join token | this RFC (recommendation) | no | yes, one write only |
| The add and the revoke of a step-up key, the enrollment and the reset of TOTP | this RFC | not built | yes, one write only, at the level of D5 for a key |
| The admin enrollment of the first step-up key of another person (D6) | this RFC | not built | yes, one write only, at the level `key` for the administrator |
| The admin MFA reset of another person | ADR 46 decision 9, this RFC (Q2) | not built | yes, one write only |
| `cliStepUp` from `off` to `on`, `stepUpFactors` to a less strict level | this RFC, ADR 212 decision 3 | not built | yes, one write only |
| The apply of a unit that requires a second factor for an apply | RFC 89 question 8 | not built | yes, with `opendustpan apply`, one write bound to the job and its plan |
| An approval or a signature of a change order | ADR 225 decisions 5 and 7, ADR 227 decision 5 | session | no: console only |
| A confirmation | ADR 103, ADR 128 decision 5 | session | no: console only |
| A break-glass review | ADR 142 | session | no: console only |
| A ChatOps approval | ADR 146 | in chat | no: in chat, on its own terms |

A new sign-in provider is not plainly a "weaker setting", so it has its own
row. A runner join token lets a machine join a pool and run jobs, so this RFC
recommends a fresh authentication for its create. That is a new entry in the
list of ADR 227 decision 2, and the owner decides it in the ADR.

**The PR-comment apply (owner decision 5).** A comment never holds a step-up.
When the unit or its approval rules ask for a second factor, the server
refuses the comment with a reply that links to the apply in the console. The
reply also names `opendustpan apply --job <job ID>`, which does the step-up in
the CLI. The step-up of that apply binds to the job ID, the action
`job:apply` and the plan digest (`cho-digest/1` for a change order, else the
plan ID), so that a touch for one plan never applies another plan.

**The error.** A write with no fresh authentication changes nothing. The
status is open point 6 (#1323). The recommendation follows the first option of
#1323:

- A credential that can never be fresh, such as an access token, answers `403`
  with `errorCode` `mfa.fresh-step-required` and `errorArgs.stepUp`. This is
  the `403` of #1322.
- A stale console session keeps `401` with `reauthenticate`, if the owner
  amends ADR 227 decision 6 so. A sign-in fixes it, and the released console
  reacts to that `401`.

The `403` of #1322 has no `reauthenticate` field. This RFC does not add one
to the `403`: for the console path, the CLI uses `consoleUrl` from the `201`
answer of the challenge create. A change of the `403` body is only the new
`errorArgs.stepUp`:

```json
{
  "code": 403,
  "errorCode": "mfa.fresh-step-required",
  "message": "this change needs a fresh authentication; run the command again in a terminal, or use the console",
  "errorArgs": {
    "stepUp": {
      "challenges": "/api/dustpan/v1/me/step-up-challenges",
      "factors": ["ssh", "openpgp", "totp", "console"],
      "windowSeconds": 900,
      "scope": "write"
    }
  }
}
```

`factors` holds only the factors that the namespace and the installation
allow for this write and that the person has. `console` is always in the list.
The errors of the step-up itself:

| Error | Status | Code |
| --- | --- | --- |
| `stepup.ErrNotFound` | 404 | `mfa.step-up-not-found` |
| `stepup.ErrExpired` | 403 | `mfa.step-up-expired` |
| `stepup.ErrUsed` | 403 | `mfa.step-up-used` |
| `stepup.ErrMismatch` | 403 | `mfa.step-up-mismatch` |
| `stepup.ErrBadAnswer` | 403 | `mfa.step-up-failed` |
| `stepup.ErrFactorRefused` | 403 | `mfa.factor-not-allowed` |
| `stepup.ErrCLIStepUpOff` | 403 | `mfa.cli-step-up-off` |
| `stepup.ErrLocked` | 429 | `mfa.factor-locked` |
| `stepup.ErrDisabled` | 403 | `mfa.factor-disabled` |
| `stepup.ErrNotPerson` | 403 | `mfa.step-up-person-only` |
| `stepup.ErrInvalid` | 422 | `mfa.step-up-invalid` |
| `stepup.ErrLimit` | 422 | `mfa.step-up-key-limit` |
| `stepup.ErrConflict` | 409 | `mfa.step-up-key-conflict` |
| `stepup.ErrBootstrapClosed` (proposed) | 409 | `mfa.step-up-bootstrap-closed` |
| `stepup.ErrChallengeLimit` | 429 | `mfa.step-up-challenge-limit` |
| `stepup.ErrCounterRegressed` | 403 | `mfa.step-up-failed` |

`stepup.ErrCounterRegressed` answers the same code as a bad answer, and the
audit entry of the kind `security` holds the cause. `mfa.factor-locked` tells
the person to wait 15 minutes. `mfa.factor-disabled` tells the person to
enroll again. `mfa.cli-step-up-off` and `mfa.factor-not-allowed` name the
console URL of the challenge. The client port of RFC 48 maps
`mfa.fresh-step-required` to `ErrFreshAuthRequired`, as it does today. A
client with no terminal stops there.

**Limits on challenges.** A person has at most 10 open challenges. A create
above the limit fails with `stepup.ErrChallengeLimit`, `429`
`mfa.step-up-challenge-limit`. The server deletes a challenge row one day
after its expiry, and a credential row at its expiry.

## The amendment of ADR 46 decision 2 (open point 4)

ADR 227 decision 1 says that a fresh authentication is a built-in MFA step or
a new authentication at the SSO identity provider. ADR 46 decision 2 says that
with an SSO identity provider, OpenDustpan adds no MFA step of its own. The
CLI factors of owner decision 1 are a factor of OpenDustpan. The ADR that
accepts this RFC must say:

- that a CLI step-up with a step-up key or a TOTP code is a third kind of
  fresh authentication in ADR 227 decision 1.
- whether it counts in an installation with an SSO identity provider.

**Recommendation:** it counts in each installation, and ADR 46 decision 2 is
amended for the step-up factors only, with these conditions from review
round 1:

- In an SSO installation, each key add by the person (not only the first)
  and each TOTP enrollment needs a fresh authentication at the identity
  provider (the console path with `fresh=1`), in addition to the factor of
  D5 for a key add. `fresh=1` alone counts at `any` only (D7). Then the identity provider still controls
  enrollment, as GitHub does with SSH keys under SAML SSO.
- In an SSO installation, the installation default of `stepUpFactors` is
  `key`, not `any`. TOTP is often weaker than the policy of the identity
  provider, and it is a new secret outside it.
- Deprovisioning stops the step-up. A person whom the identity provider
  removes, or a disabled principal, gets no challenge and no credential, and
  the server revokes each step-up credential of that person. Today the
  revocation of tokens on deprovision gives this. The ADR states it as a
  layer, with its own test.
- OpenDustpan cannot see an identity provider policy that requires
  phishing-resistant MFA. The user guide tells such an organization to set
  the installation floor of `stepUpFactors` to `key`, or `cliStepUp: off`.

A namespace that wants only its identity provider sets `cliStepUp: off`, and
the console path then asks the identity provider for a new authentication
(`fresh=1`). At the level `key`, that console path fails, because `fresh=1`
counts at `any` only (D7).

**The effect on SSO installations (with open point 5).** With D7 and the
default `key` above, a person in an SSO installation with no built-in
WebAuthn has no console factor at the level `key`. The first key of each
such person is an admin enrollment (D6). If `stepUpFactors` also governs a
direct console write (open point 5, option A), each write of Q6 at the level
`key` in such an installation needs a step-up key in the CLI, also a gate
change that a person starts in the console. The owner decides the amendment
and open point 5 together.

## First key enrollment from the no-factor state

This section proposes options. It does not decide. The owner decides with
the ADR that accepts this RFC.

### The gap

D5 says that each key add needs a factor at the level `key`. D6 says that
the first key of a person comes from a WebAuthn assertion in the console or
from an administrator, and the administrator needs a fresh authentication at
the level `key`. D7 says that `fresh=1` counts at the level `any` only.

Look at an installation with these facts:

- People sign in with OIDC, SAML or CAS. ADR 46 decision 2 gives these
  people no built-in WebAuthn.
- No person holds a step-up key, in any state.
- No person who is not a break-glass account holds a passkey. A self-hosted
  installation can turn break-glass off (ADR 70 decision 23). Then no
  person holds a passkey at all.

This is the **no-factor state**. In it, no person can reach the level `key`.
So no person can add a first key (D5), and no administrator can enroll one
(D6). The rules do not allow a write, and they name no other path. The
level `key` cannot be reached until a key exists, and a key cannot exist
until a person reaches the level `key`.

The same state can come back when the last key of the installation goes
away: a person revokes it, an administrator resets the MFA of its holder, or
an administrator deletes its holder. The section "The way back to the
no-factor state" below treats this path.

Two existing paths do not close the gap:

- **A break-glass account.** When break-glass is on, a break-glass account
  signs in with a passkey, so it reaches the level `key` and can do the
  admin enrollment of D6. But each break-glass sign-in needs a reason,
  raises an alert and opens a review (ADR 70 decisions 25 and 26). A routine
  first setup then becomes an emergency use each time. With break-glass off,
  this path does not exist.
- **The enrollment code of ADR 301 decision 14.** It covers a person of a
  plain OAuth 2.0 provider or a trusted header only. Its issuer needs a
  fresh step with their own factor. ADR 301 decision 15 already widens D6
  for those two methods. It does not cover OIDC, SAML or CAS.

### What each option must give

The issue is the first key of the installation, not the first key of each
person. When one administrator holds a key, D6 covers each other person. So
each option below enrolls one key, for one person who holds the action
`server:enroll-step-up-key` through a stored grant, and only in the
no-factor state. After that, D5 and D6 apply with no change.

**The rules that each option overrides.** Options A, B and D let a person
add their own first key with no fresh authentication at the level `key`.
That overrides two rules:

- D5: a key add needs a factor at the level `key`.
- D6: the first key comes from a WebAuthn assertion in the console or from
  an administrator at the level `key`, and an administrator never enrolls
  their own account.

Option C overrides owner decision 4 instead (see option C). "No magic" in
`CLAUDE.md` asks for no hidden rule. So the catalog of code rules gets one
entry for the chosen option. The entry names each rule that the option
overrides, the no-factor state as its only condition, and the audit entry of
each use.

**The no-factor check is installation-wide, under one lock.** The check
reads these counts:

- the step-up keys of the installation, of each person and in each state,
  suspended keys included.
- the passkeys of each person who is not a break-glass account.

The check passes only when both counts are zero. The service checks it at
the issue of an offer and at the redeem. The store checks it again in the
transaction of the add. A store error or an unknown value counts as "not in
the no-factor state", and the server refuses.

`KeyStore.EnrollFirstKey` (Q2) checks one person only, so its lock is not
enough. Each option uses a new store method that takes the **step-up
installation lock** first: the write lock of the database for SQLite, and
for PostgreSQL READ COMMITTED with the advisory lock of the step-up scope of
the installation, as ADR 245 decision 8 does for the host recovery. These
writes also take the same lock before they change a count:
`AddKey`, `EnrollFirstKey`, `RevokeKey`, the admin MFA reset, the delete of
a principal who holds a key or a passkey, and the passkey add of
`mfa.PasskeyStore`. So two redeems for two persons, a redeem and an admin
enrollment, or a redeem and a passkey add never both see the no-factor
state. Of two such writes, the second reads the rows of the first in a new
statement snapshot.

The ports change in "The proposed ports": `KeyStore` gets
`EnrollBootstrapKey`, and a new port `BootstrapStore` holds the offers
(the codes of option A and the nonces of option D). Their conformance cases:

- "two bootstrap redeems for two persons at the same time: one key".
- "a bootstrap redeem and a first-key enrollment at the same time: one key".
- "a bootstrap redeem and a passkey add at the same time: the redeem
  refuses or the passkey add sees one key".
- "a bootstrap redeem refuses when a person who is not a break-glass
  account holds a passkey".
- "a bootstrap redeem refuses when a suspended key exists".
- "a spent, voided or expired code refuses, and a new code voids the old
  code".
- "a used nonce refuses".
- "a failed audit append stores no key and spends no code".

### The way back to the no-factor state

Q2 does not give the level of the admin MFA reset. If it is `any`, this
path exists:

1. An administrator does a fresh authentication with `fresh=1`, which is
   the level `any` (D7).
2. The administrator resets the MFA of each key holder, or deletes each key
   holder. The installation is in the no-factor state again.
3. The administrator gets an offer (the bootstrap admin token of option A,
   or a signed request of option D) and enrolls their own key.

The administrator then holds a factor at the level `key`, from a fresh
authentication at the level `any`. That is what D7 forbids. The email to
the administrators detects it after the fact. It does not stop it.

**Proposal: the last-key guard.** These writes need a fresh authentication
at the level `key` of the caller:

- the admin MFA reset of a person who holds a step-up key, in any state.
  A reset of a person who holds TOTP only stays at the level of Q2.
- the delete of a principal who holds a step-up key, by an administrator.

The store also refuses, under the step-up installation lock, each write
that takes the count of step-up keys of the installation to zero, unless
the write carries a fresh authentication at the level `key` or the
last-holder recovery below.

**A deprovision.** A deprovision by the identity provider or by SCIM has no
fresh step. It disables the principal and revokes its credentials and
sessions. It does not delete the keys, so the keys still count and the
guard is not involved. A SCIM delete of a principal who holds a step-up key
becomes a disable, and the server writes the audit entry
`step-up.delete-held-by-guard` with the SCIM connection and the principal.
An administrator at the level `key` deletes the principal later, or the
last-holder recovery does.

**The own revoke of the last key.** A revoke needs a factor at the level
`key` of the person (D5), also when it revokes the last key of the
installation. So the own revoke passes the guard. It reopens the no-factor
state on purpose: the holder, with their own key, chose to leave the
installation with no key. After it, option A or D works again.

**What the way back gives a caller.** A caller at the level `key` who holds
`server:enroll-step-up-key` can already enroll a key for each person
through D6. For that caller, the way back gives nothing new. A holder who
does not hold `server:enroll-step-up-key` gives something new when they
revoke the last key: after the revoke, an administrator at the level `any`
with the bootstrap admin token can enroll a key through option A. This
rests on a trust assumption: the holder revokes on purpose. The audit
entry of the revoke and the email to each administrator show it. A thief
must hold the key of that holder to do the revoke, and such a thief can
already do each write at the level `key` as that holder.

**The last-holder recovery.** The guard alone locks out a sole holder. Q2
tells a person who lost each key to ask an administrator for the MFA reset.
When that person holds the only keys of the installation, no other caller
reaches the level `key`, so the guard refuses the reset. The same holds for
the delete of a sole holder after a deprovision. The key still counts, so
the installation never reaches the no-factor state either.

The way out is the host recovery:

1. The operator makes a temporary administrator with a host recovery mode:
   mode 1 with `opendustpan host create-temp-admin` (ADR 245), or mode 2
   with the signed request over the network (ADR 295). The controls of
   ADR 245 apply: the reason, the audit entries, the expiry of 24 hours or
   less, and the review of decision 3.
2. The temporary administrator calls the admin MFA reset, or the delete, of
   the sole holder. The request names the principal and holds a reason.
3. The guard accepts the record of the live temporary administrator in place
   of the level `key` for this write only, and only when the person holds
   every step-up key of the installation. With another holder, the guard
   refuses, because that holder can do the reset at the level `key`.
4. The store writes the reset or the delete, and the audit entry
   `step-up.last-holder-recovery`, of the kind `security`, with the
   temporary administrator, the recovery mode, the reason, the person and
   the fingerprints of the deleted keys. The server records the write as a
   break-glass use, so it is part of the review of the temporary
   administrator.
5. The installation is now in the no-factor state. Option A, or option D in
   mode 2, applies again.

A temporary administrator has only an access token, and an access token
never gives a fresh authentication (ADR 227 decision 6, owner decision 4).
So step 3 is an exception for one write: the recovery record takes the
place of the fresh step of Q2 and of the level `key`. The host shell or the
offline operator key is the proof, as for each other recovery. Question 10
asks the owner to accept it.

**The dead end of mode 3.** In the recovery mode `none`, no temporary
administrator exists, and mode 3 closes options A and D. When the sole holder
loses each key, or the identity provider removes the sole holder, the guard
refuses each reset and each delete of that holder, and the installation
cannot reach the level `key` again until a restore from a backup. A
break-glass account, when break-glass is on, reaches the level `key` with
its passkey and can do the reset, as a break-glass use. Option B would also
be a way out, but it reopens D7. This dead end is the cost of mode 3. The
user guide states it, and tells an operator of mode 3 with break-glass off
to register keys for at least two administrators.

**Tests of the guard.** Each test sends the hostile request to one layer
with the other layers off:

| Layer | The check | Tests |
| --- | --- | --- |
| 1. Check at the boundary | The reset and the delete name the principal with its prefix, and the recovery reason has no Cc or Cf character. | A principal ID with no prefix gets `422`. A recovery reason with U+202E gets `422`. |
| 2. Encode or contain | The service reads the level from the credential record of the server, never from the request. | A reset with a body field `"level": "key"` and a step at `fresh=1` gets `403`. |
| 3. Limit the effect | The recovery exception covers one write, for a sole holder, while the record of the temporary administrator is live. | A recovery reset when a second person holds a key gets `403`. A recovery reset after the expiry of the record gets `403`. A recovery reset in the recovery mode `none` gets `403`. |
| 4. Authorization at the point of use | The service checks the level `key` of the caller, or the recovery record. The store checks the count of keys under the lock, separately for each path. | Service, with the store check off: a reset of a key holder at `fresh=1` gets `403`, and a delete of a key holder at `fresh=1` gets `403`. Store, with the service check off: the store refuses a reset that removes the last key at the level `any`. The store refuses a delete of the last holder at the level `any`. A SCIM delete of the last holder becomes a disable, with one `step-up.delete-held-by-guard` entry and the key still counted. The own revoke of the last key passes with a `key` step, and the store refuses it with a TOTP step or a step at `fresh=1`. |
| 5. Fail closed | A store error, an unknown level, an unknown recovery mode or a failed audit write refuses the write and deletes no key. | A store that returns an unknown error during the count refuses the reset. A failed append of `step-up.last-holder-recovery` deletes no key. |

### Benchmarks

The author read the pages on 2026-11-07. Check them again at the time of
the decision.

| System | How the first strong factor gets enrolled | What it shows |
| --- | --- | --- |
| GitHub | The steps to add a security key or a passkey say that the person must already have two-factor authentication with a TOTP app or SMS. The key is an extra method ([configuring 2FA](https://docs.github.com/en/authentication/securing-your-account-with-two-factor-authentication-2fa/configuring-two-factor-authentication)). | A weaker factor comes first, then the key. |
| Google Workspace | An administrator who enforces 2-Step Verification sets a new-user enrollment period of 1 day to 6 months. In that period, a user signs in with a password only ([deploy 2SV](https://support.google.com/a/answer/9176657)). | A time-bounded weak window. |
| AWS root user | The root user signs in with the email address and the password. Then the root user assigns a passkey or a security key, in the console only, never from the CLI or the API ([root passkey](https://docs.aws.amazon.com/IAM/latest/UserGuide/enable-fido-mfa-for-root.html)). The root user must register MFA within 14 days of the first sign-in attempt ([root MFA](https://docs.aws.amazon.com/IAM/latest/UserGuide/enable-mfa-for-root.html), [MFA for identities](https://docs.aws.amazon.com/IAM/latest/UserGuide/gs-identities-mfa.html)). | A password-only authentication enrolls the first key, in a fixed time, in the console only. |
| Okta Identity Engine | A temporary access code is a code that an administrator generates for a user. It can be optional or disabled, never required ([temporary access code](https://help.okta.com/oie/en-us/content/topics/identity-engine/authenticators/configure-temporary-access-code.htm)). Okta also gives a rule of the account management policy for the first phishing-resistant authenticator. The rule asks for two enrolled authenticators, a trusted network zone and a low risk ([first phishing-resistant enrollment](https://help.okta.com/oie/en-us/content/topics/identity-engine/policies/oamp-configure-first-pr-enrollment.htm)). | An administrator code, or weaker factors with context conditions. |
| Microsoft Entra | The Temporary Access Pass of ADR 301 decision 14: an administrator issues a code with a fixed life time ([Temporary Access Pass](https://learn.microsoft.com/en-us/entra/identity/authentication/howto-authentication-temporary-access-pass)). | An administrator code. |
| Keycloak | `kc.sh bootstrap-admin` makes a temporary administrator from the host (ADR 245 decision 1). | The host is the root of trust. |
| HashiCorp Vault | `vault operator generate-root` makes a root token only with a quorum of the unseal key shares or the recovery key shares. | Several holders of offline secrets, not the host. |

None of these systems asks for a phishing-resistant factor to enroll the
first phishing-resistant factor. Each one starts from a weaker proof, and
limits it in time, in use or by a second party.

### Option A. A one-time bootstrap code from the bootstrap admin token

The bootstrap admin token makes an offer. The person takes the offer with a
new authentication at the identity provider that starts after the offer.
This is the shape of ADR 339 for the first administrator.

**The sequence.**

1. The operator calls `POST /api/server/step-up-bootstrap-codes` with the
   bootstrap admin token (ADR 6). The body holds the principal ID of the
   person and a reason.
2. The server checks the no-factor state and the person. The person must be
   a live, enabled person of the installation, with a stored grant that
   gives `server:enroll-step-up-key` on the installation. The server refuses
   a break-glass account, a temporary administrator, a service principal
   and a `dev:` subject. The reason must have no control (Unicode Cc) or
   format (Unicode Cf) characters.
3. The server makes a code of 32 bytes from `crypto/rand`, stores its
   SHA-256 hash and its issue time, and answers the code one time in the
   `201` body. One code is open at a time. A new code voids the old code.
   The code lives 10 minutes and works one time.
4. The operator gives the code to the person through a channel that the
   operator chooses. The RFC cannot make that channel safe. A person who
   reads the code on the way holds half of the proof. Step 6 makes sure that
   this half alone is not enough. The owner decides whether the RFC names a
   channel (question 6).
5. The person signs in to the console and sends the code to
   `POST /api/dustpan/v1/me/step-up-keys/bootstrap/start`. The server checks
   the code and starts a **bootstrap fresh request**. The request records
   the code ID, the principal ID, the sign-in ID of the session and its own
   start time. The server keeps it for 5 minutes.
6. The server redirects to the identity provider with the fresh request of
   ADR 320 decision 16 (`max_age=0` for OIDC, `ForceAuthn` for SAML,
   `renew=true` for CAS). The fresh step passes only when all of these hold:
   - the authentication time is not before the start of the bootstrap fresh
     request, so it is also after the issue of the code (ADR 320 decision
     16).
   - the assertion names the person of the session, and the sign-in ID is
     the sign-in ID of the request (ADR 320 decision 18).

   A fresh step that the session had before the request does not count, so
   a stolen session in its fresh window cannot redeem the code. The thief
   must also authenticate as the person at the identity provider.
7. The console makes a `key-add` challenge (Q1) for the person. The person
   signs its text with the new key, with `ssh-keygen -Y sign` and the
   key-add namespace, and pastes the public key and the signature. This is
   the console SSH fallback of ADR 301 decision 16.
8. The console sends `POST /api/dustpan/v1/me/step-up-keys/bootstrap` with
   the ID of the bootstrap fresh request, the code, the public key, the
   name, the `key-add` challenge ID and the signature.
9. `KeyStore.EnrollBootstrapKey` takes the step-up installation lock, checks
   the no-factor state again, checks the code and the bootstrap fresh
   request, spends both, adds the key and appends the audit entry, in one
   transaction. `EnrolledBy` holds the ID of the bootstrap admin principal.

**The authorization conditions.** All of these hold at the time of the add:

- the installation is in the no-factor state.
- the code exists, is not spent, voided or expired, and names the principal
  of the session.
- the bootstrap fresh request exists, is not spent or expired, names the
  code, the person and the sign-in ID of the session, and its fresh step
  passed as in step 6.
- the session is a console session of that person.
- the person holds `server:enroll-step-up-key` through a stored grant.
- the `key-add` signature verifies with the public key of the request, and
  the `principal` line names the person.

**Why an unauthorized caller cannot use it.** The caller needs two things
from two roots of trust: the bootstrap admin token, from the configuration
of the host, and a new authentication of the person at the identity
provider after the offer. Each layer stops the attack alone, and each test
sends the hostile input to that layer with the other layers off:

| Layer | The check | Tests |
| --- | --- | --- |
| 1. Check at the boundary | A strict decoder for each body. The code is 43 characters of base64url. The principal ID, the challenge ID and the fresh request ID have their prefixes. The key type is in the allowlist of Q2. The reason has no Cc or Cf character. The server refuses each other value and does not change it. | A code of 42 characters, a key type `ssh-dss`, a reason with U+202E and an unknown field each get `422`. |
| 2. Encode or contain | The code goes in a `POST` body only, never in a URL. The server never logs it, stores its hash only and compares the hashes with `crypto/subtle`. The SQL uses placeholders. The `key-add` text comes from the record of the server, and its signature namespace keeps it apart from each other signature. | A code in the query string gets `400`. The captured log of an issue and of a redeem holds no code. An AST test fails when the compare of the code uses an operator other than `subtle.ConstantTimeCompare`. A reason and a key name with `'`, `;` and `--` store and read back unchanged. |
| 3. Limit the effect | One code at a time, 10 minutes, one use, one key, for one person, and only in the no-factor state. A bootstrap fresh request lives 5 minutes and works one time. A wrong code counts in the factor lock of the person (ADR 301 decision 14). The key adds no grant. | A second redeem of a spent code fails. A redeem after a key exists fails. A code after 10 minutes on a fake clock fails. A new code voids the old code. The tenth wrong code locks the factor, and the eleventh gets `mfa.factor-locked`. |
| 4. Authorization at the point of use | The issue route accepts only the bootstrap admin token. The redeem routes accept only a console session, never an access token. They check the stored grant, the bootstrap fresh request and its sign-in ID, and the principal of the code. The store checks the no-factor state, the code and the fresh request again in the transaction. | Each with the service checks off: a CLI sign-in token gets `403`. A job token gets `403`. A fresh step that started before the code issue gets `403`. A fresh step of another sign-in ID of the same person gets `403`. The code of person X in a session of person Y gets `403`. A person with no stored grant gets `403`. The store refuses a redeem while a key exists. |
| 5. Fail closed | An unknown state, a store error or a failed audit write refuses the request and stores nothing. Each refusal writes an audit entry. All refusals of the redeem give the same answer, `403` `mfa.step-up-failed`, so a caller cannot tell a wrong code from a closed state. The audit entry holds the real reason. | A store that returns an unknown error gives `403` and one refusal entry. A failed audit append gives `403`, stores no key and does not spend the code. The bodies of a wrong code, a spent code and a closed state are byte for byte the same. |

**The audit records.** In the scope `audit.ServerScope`:

- `step-up.bootstrap-code-issued`: the bootstrap admin principal, the
  person, the reason, the code ID, its issue time and its expiry. Never the
  code.
- `step-up.bootstrap-code-voided`: when a new code voids an open code, and
  at the expiry.
- `step-up.bootstrap-fresh-started`: the code ID, the person and the
  sign-in ID.
- `step-up.bootstrap-key-enrolled`, of the kind `security`: the person, the
  sign-in ID, the fresh request ID, the fresh kind and its time, the code
  ID, the key ID and its fingerprint. It is in the same transaction as the
  add.
- `step-up.bootstrap-refused`: each refusal, with the real reason.

The server sends the email of Q2 to the person and to each installation
administrator. The email is a detective control only. With no SMTP sender
(ADR 301 decision 15 allows none), question 9 applies.

**How it fits the level `key`.** The code and the new authentication
together take the place of the factor at the level `key`, for one add only.
Neither one alone adds a key. The code is not a factor, and it never passes
a step-up. After the add, the person has a factor at the level `key`, and D5
and D6 apply to each later add, with no exception.

**What option A takes from ADR 339.** It uses the same offer and redeem
shape, the same bootstrap admin token, the same form of code (32 bytes, a
hash, a constant-time compare, 10 minutes, one use) and the same refusal of
a break-glass person, a temporary administrator, a service and a `dev:`
subject. It has its own code store and does not use the marker of ADR 339.
ADR 339 leaves open two points that option A also depends on:

- whether the person needs a second factor besides the fresh
  authentication. Option A has no second factor: the fresh step and the
  code are the proof. If the owner asks for one in ADR 339, the same answer
  applies here, and option A then needs a factor that the no-factor state
  does not have.
- whether the redeem ends the bootstrap admin token (ADR 53 decision 9).
  Question 8 asks it for option A.

**Costs.**

- After the operator retires the bootstrap admin token (ADR 53 decision 9),
  the operator sets a token again to use this path.
- The hosted service has no host-side token for a tenant. It needs the
  support path of ADR 70 decision 28.
- The trust stays with the host and the identity provider. A person who
  holds the host configuration and the account of an administrator at the
  identity provider can add a key of their own. Such a person can already
  do each write at the level `any`.

**Variants.**

- **A host command.** `opendustpan host` opens the database and makes the
  code, as `host create-temp-admin` does (ADR 245). ADR 245 decision 1
  allows that for one command only, and ADR 39 forbids it for the others.
  So this variant needs an owner change of ADR 39 and ADR 245. The route of
  option A needs no such change, and works in a container with no shell, as
  ADR 339 notes.
- **A signed request.** The offer comes from a request that the offline
  operator key of mode 2 signs (ADR 295), not from the bootstrap admin
  token. This is option D.

### Option B. A fresh SSO authentication with `max_age` and an `acr` claim

The identity provider says, with a claim, that the person used a strong
factor. The server takes that claim at the level `key` for the first key
only.

**The sequence.**

1. An operator sets, on the sign-in provider, a list of `acr` values (OIDC)
   or `AuthnContextClassRef` values (SAML) that the server accepts as the
   level `key` for a first key. It is empty by default. CAS has no such
   claim, so this option does not exist for CAS.
2. The person starts the add in the console. The server checks the
   no-factor state and the stored grant of the person, and starts a fresh
   request bound to the person and the sign-in ID, as in option A.
3. The server redirects to the identity provider with `max_age=0` and
   `acr_values`, and asks for `acr` as an essential claim (OpenID Connect
   Core 1.0, sections 3.1.2.1 and 5.5.1.1). For SAML, it sends
   `ForceAuthn="true"` and a `RequestedAuthnContext` with the comparison
   `exact`.
4. The server checks the answer: `auth_time` is not before the start of the
   fresh request (ADR 320 decision 16), the subject is the person of the
   session (ADR 320 decision 18), and `acr` is in the list. It refuses a
   missing claim.
5. The answer must have an ID: the `jti` of the ID token, or the `ID` of the
   SAML assertion. `jti` is optional in OIDC, and some providers do not send
   it. The server refuses an ID token with no `jti`, so this option does not
   work with such a provider.
6. The person signs the `key-add` challenge and the console sends the add.
   `KeyStore.EnrollBootstrapKey` takes the step-up installation lock, checks
   the no-factor state again, inserts the token ID with a unique key in
   `BootstrapStore`, adds the key and appends the audit entry, in one
   transaction.

**The authorization conditions.** The no-factor state, a non-empty list on
the provider, the stored grant of the person, a fresh request of the person
and the sign-in, a new authentication after its start with an accepted
claim and an ID, and a valid `key-add` proof.

**Why an unauthorized caller cannot use it.**

| Layer | The check | Tests |
| --- | --- | --- |
| 1. Check at the boundary | The server accepts an `acr` value only from the list, compared byte for byte, and refuses an empty list. | A value that differs only in letter case gets `403`. An empty list refuses each answer. |
| 2. Encode or contain | The claim comes only from a signed ID token or a signed assertion that the method port verified. The server never reads it from the request of the browser. | A callback with an `acr` query value and a token with no `acr` gets `403`. A token with a bad signature gets `403`. |
| 3. Limit the effect | One key, one time, only in the no-factor state. The token ID is a unique-key insert in the transaction of the add. | The same `jti` twice: the second gets `403`. An ID token with no `jti` gets `403`. A SAML assertion with no `ID` gets `403`. A redeem while a key exists gets `403`. |
| 4. Authorization at the point of use | The route checks the stored grant, the fresh request, its sign-in ID and the claim. The store checks the state again. | Each with the service checks off: an `auth_time` before the start gets `403`. The subject of another person gets `403`. A person with no stored grant gets `403`. The store refuses while a key exists. |
| 5. Fail closed | A missing claim, an unknown value, a token with no `auth_time`, a store error or a failed audit write refuses, with an audit entry. | A token with no `auth_time` gets `403`. A store error gives `403` and one entry. A failed audit append stores no key and inserts no token ID. |

**The audit records.** `step-up.bootstrap-key-enrolled`, of the kind
`security`, with the provider ID, the `acr` value, the `auth_time`, the
token or assertion ID, the key ID and its fingerprint. A refusal writes
`step-up.bootstrap-refused`.

**How it fits the level `key`.** It reopens D7 for one write. D7 says that
OpenDustpan cannot see which factor the identity provider used. An `acr`
claim does not change that. It is a statement of the identity provider, not
a proof that OpenDustpan can check. Its values are not standard across
providers. The list of accepted values is a setting of the sign-in provider.
In the no-factor state, a change of that setting can only ask for the level
`any`, so a person with an administrator account at the identity provider
and a fresh step can set the list to a value that the provider always sends,
and then add a key. In that case, option B gives no more than "`fresh=1`
adds the first key".

**Costs.** It does not work for CAS, a plain OAuth 2.0 provider, a trusted
header, or an OIDC provider that sends no `jti`. Each provider needs its own
list. The owner reopens D7.

### Option C. A temporary administrator enrolls the first factor

**The sequence.**

1. The operator runs `opendustpan host create-temp-admin` with a named
   person and a reason (ADR 245). The command makes a temporary
   administrator and prints its token.
2. The temporary administrator calls the admin enrollment route of D6 for
   the real administrator, with the public key and the `key-add` proof that
   the real administrator made.

**The authorization conditions.** ADR 245: the off switch, the recovery
mode not `none`, a reason, one transaction, an expiry of 24 hours or less.
D6 adds `server:enroll-step-up-key`, which a site administrator holds.

**What does not fit.** The temporary administrator has only an access
token. ADR 245 decision 4 forbids its sign-in through an identity provider,
and an access token never gives a fresh authentication (ADR 227 decision 6,
owner decision 4). So it cannot reach the level `key` that D6 asks for. To
use it, the owner must make an exception: the host token of a temporary
administrator counts as the level `key` for the first-key enrollment. That
is an exception to owner decision 4.

**Why an unauthorized caller cannot use it.** The layers of ADR 245 apply:
the boundary checks of the reason (decision 17), the one transaction
(decision 7), the expiry (decisions 10 and 13), the store check of the
principal (decisions 8 and 9) and the fail-closed off switch (decision 5).
The admin enrollment adds the layers of Q2: the store refuses a person who
holds a key, and the own account. This RFC does not recommend option C, so
it gives no test table for it.

**The audit records.** The four entries of the create (ADR 245 decision 7),
the entry of the admin enrollment of Q2, and the expiry entry. The review of
ADR 245 decision 3 opens when `pkg/breakglass` has a store.

**Costs.** It is a break-glass use for a routine setup. Each use raises the
signals of a recovery, and its review needs a second administrator who may
not exist yet. It needs a shell on the host. It breaks owner decision 4.
The temporary administrator, not the person, makes the step. So the key
row names a principal that the expiry disables 24 hours later.

### Option D. An offline recovery code

Two forms are possible:

- **D1. The recovery codes of a person** (ADR 46 decision 4). These do not
  fit. A person of OIDC, SAML or CAS has no built-in MFA and so no recovery
  codes. Where a person has them, ADR 301 decision 9 says that a recovery
  code lets the person enroll one replacement factor, one time, and never
  passes a fresh step. A key add needs a fresh authentication at the level
  `key` (D5), and Q5 says that a sign-in with a recovery code gives no
  fresh authentication for a factor add.
- **D2. A request that the offline operator key signs.** This is the key of
  ADR 57 mode 2 and ADR 295. The rest of this option is D2.

**The sequence.**

1. The person signs in to the console and starts a fresh request bound to
   the person and the sign-in ID, as in option A. The fresh step at the
   identity provider must start after the fresh request.
2. The person makes the `key-add` challenge and signs it with the new key.
   The console shows a request text: the origin, the principal ID, the key
   fingerprint, the challenge ID, the fresh request ID, a nonce of 32 bytes
   and a not-after time of at most 10 minutes.
3. The operator signs the request text offline with the operator key.
4. The person sends the request, the operator signature and the `key-add`
   signature to `POST /api/dustpan/v1/me/step-up-keys/bootstrap` in the
   same console session.
5. `KeyStore.EnrollBootstrapKey` takes the step-up installation lock, checks
   the no-factor state, inserts the nonce with a unique key in
   `BootstrapStore` (as ADR 295 decision 3), adds the key and appends the
   audit entry, in one transaction.

**The authorization conditions.** The stored recovery mode is
`signed-request`, the stored operator key verifies the request, the nonce is
new, the time is before the not-after time, the fresh request is of the
person and the sign-in that the request names, and the no-factor state
holds.

**Why an unauthorized caller cannot use it.** The request binds the person,
the key fingerprint, the challenge and the fresh request, so a signature for
one person or one key never adds another. The fresh step of the person is a
second root of trust, as in option A.

| Layer | The check | Tests |
| --- | --- | --- |
| 1. Check at the boundary | A strict decoder. The nonce is 32 bytes, the not-after time is at most 10 minutes after the server time, and each ID has its prefix. | A nonce of 31 bytes gets `422`. A not-after time 11 minutes ahead gets `422`. |
| 2. Encode or contain | The server builds the request text from its own fields, one value on each line. It verifies the operator signature with the stored key only, never with a key in the request. | A field with an LF gets `422`. A request signed by another key, with that key in the request, gets `403`. |
| 3. Limit the effect | One key, one nonce, one use, only in the no-factor state. The rate limit of ADR 295 decision 4 applies. | The same nonce twice: the second gets `403`. A request after its not-after time gets `403`. One request above the rate limit from one /64 gets `403`. |
| 4. Authorization at the point of use | The route checks the stored mode, the person and the sign-in of the session against the request, and the key fingerprint against the `key-add` proof. The store checks the state again. | Each with the service checks off: a session of another person gets `403`. A mode other than `signed-request` gets `403`. A fingerprint that differs from the proof gets `403`. The store refuses while a key exists. |
| 5. Fail closed | Each refusal gives one answer (ADR 295 decision 5) and writes an audit entry. A store error or a failed audit write stores nothing. | The bodies of a bad signature, a used nonce and a wrong mode are byte for byte the same. A failed audit append stores no key and inserts no nonce. |

**The audit records.** As option A, with the fingerprint of the operator
key and the nonce in place of the code ID.

**How it fits the level `key`.** As option A: the signed request and the
fresh step take the place of the factor, for one add only.

**Costs.** ADR 295 decision 8 says that the route of mode 2 ships only after
the store keeps the recovery mode and the key. Mode 1 is the default
(ADR 57 decision 3), so most installations have no operator key. The theft
of the operator key alone adds no key, because the fresh step of the person
is also needed.

### The recovery mode `none`

ADR 57 decision 1 calls mode 3 "no break-glass", with a restore from a
backup. ADR 245 decision 2 refuses the host command in mode 3, because the
operator chose no break-glass and the command must not override that
choice. ADR 57 decision 4 says that the token of mode 1 works for one
recovery. The Consequences of ADR 57 say that mode 3 still needs the
bootstrap admin token on the empty installation that receives the backup.

A first-key offer is not a recovery, and it does not make an administrator.
But it is a path from the host configuration to the level `key`, outside the
factors of the persons. An operator who chose mode 3 chose no such path.

**Proposal.** An explicit mode 3 closes options A and D. The issue route of
option A refuses with `409` `mfa.step-up-bootstrap-closed` and names the
mode. An installation in mode 3 with SSO and with break-glass off then has
no way to the level `key`. The setup and the console say so, and the user
guide tells such an operator to keep a break-glass account or to choose
another mode. A first-key offer does not use up the mode 1 token for a
recovery, so it does not count against ADR 57 decision 4. Question 8 asks
whether it retires the token.

### Comparison

| | A. Bootstrap code | B. SSO `acr` | C. Temporary administrator | D. Signed request |
| --- | --- | --- | --- | --- |
| Roots of trust | host configuration and identity provider | identity provider only | host shell only | offline key and identity provider |
| Works for OIDC, SAML, CAS | yes | OIDC with `jti` and SAML only | yes | yes |
| Works with no shell (container, Helm) | yes | yes | no | yes |
| Overrides | D5 and D6 | D5, D6, and reopens D7 | owner decision 4 | D5 and D6 |
| A break-glass use | no | no | yes | no |
| Works in mode 3 | no (proposal) | yes | no | no |
| Available today | after the route ships | after the claim check ships | the host command exists | after ADR 295 decision 8 |
| Benchmark | Okta code, Entra pass | Okta context rule, AWS password-only first key | Keycloak | Vault quorum, ADR 295 |

### Recommendation

Option A, in the form of the route of ADR 339, with option D as a second
source of the offer when the installation uses mode 2. Both need the
last-key guard and the installation-wide lock above.

- Option A keeps D7, owner decision 4 and the break-glass rules. Its
  override of D5 and D6 is narrow: one key, one person with the stored
  grant, in the no-factor state, with two roots of trust that an attacker
  must both hold. The new authentication must start after the offer, so a
  stolen session and an intercepted code are not enough.
- Option B makes the level `key` depend on a value that an administrator at
  the level `any` can change, so it adds no strength over `fresh=1`. It also
  reopens a PM decision.
- Option C breaks owner decision 4 and turns a routine setup into a
  break-glass use.
- Option D gives the same shape as A with no long-lived secret in the
  configuration, but only in mode 2.

The benchmarks support this: none of them asks for a strong factor to
enroll the first strong factor, and Okta and Entra use a one-time code from
an administrator.

### Questions for the owner

1. **Which option?** A, B, C, D, or A with D for mode 2 (recommended).
2. **A permanent marker, or reopen at zero?** With a marker, as ADR 339
   decision 7, the path works one time in the life of the installation. A
   later loss of each key goes through recovery (ADR 57, ADR 245), and
   recovery has no path to the level `key` either. With no marker, the path
   opens each time the installation is in the no-factor state again.
   Recommendation: no marker, but only with the last-key guard of question
   5. Without the guard, a marker.
3. **Do break-glass passkeys count?** The proposed no-factor state ignores
   the passkeys of break-glass accounts, so that a routine first setup needs
   no break-glass use. The other choice counts them, and then an
   installation with break-glass on uses a break-glass sign-in for its
   first key. Recommendation: ignore them.
4. **Does the recovery mode `none` close options A and D?**
   Recommendation: yes, as in "The recovery mode `none`".
5. **The level of the admin MFA reset.** Recommendation: the level `key` for
   the reset of a person who holds a step-up key, and for the delete of
   such a principal, with the store guard of "The way back to the no-factor
   state". The level of Q2 stays for a person with TOTP only. A sole holder
   who loses each key, or whom the identity provider removes, gets out
   through the last-holder recovery of host recovery modes 1 and 2. In
   the recovery mode `none`, this is a dead end until a restore (question
   10). The own revoke of the last key passes with the level `key` and
   reopens the no-factor state on purpose.
6. **The delivery channel of the code.** The RFC leaves the channel to the
   operator, and the new authentication after the offer makes an
   intercepted code alone useless. Should the RFC also name a channel, for
   example the code in the console of the person after a sign-in, never in
   a message? Recommendation: leave the channel to the operator, and state
   the risk in the user guide.
7. **Should option A wait for a second factor?** ADR 339 leaves open
   whether the first administrator needs a second factor besides the fresh
   authentication. Recommendation: decide both together, and keep option A
   with no second factor, because the no-factor state has none.
8. **Does a redeem retire the bootstrap admin token?** ADR 339 leaves this
   open for its own redeem (ADR 53 decision 9). Recommendation: no. The
   console asks the operator to retire it, as ADR 53 decision 9 says, and
   the audit entry of each issue shows each use.
9. **What happens with no SMTP sender?** The email to the administrators is
   a stated control, and ADR 301 decision 15 allows an installation with no
   SMTP sender. The options: refuse the issue of a code (fail closed), or
   allow it with a console banner for each administrator and an entry in
   the admin access report (ADR 70 decision 29). Recommendation: allow it
   with the banner and the report entry. The email detects only and stops
   nothing, and the audit entry is the record. The answer of the issue
   route says that no email goes out.
10. **The last-holder recovery and the dead end of mode 3.** Two parts:
    - The last-holder recovery lets a temporary administrator of mode 1 or
      mode 2 reset or delete the sole key holder, with no fresh step. That
      is an exception to ADR 227 decision 6 and owner decision 4 for this
      one write. The write is a break-glass use with the controls of ADR
      245.
    - In the recovery mode `none`, with break-glass off, the loss of the
      sole holder leaves no way to the level `key` until a restore.

    Recommendation: accept both. Accept the dead end as the cost of mode
    `none`. The user guide says so, and tells an operator of mode `none`
    with break-glass off to register keys for at least two administrators.

## Security layers (ADR 250)

| Step | The step-up check |
| --- | --- |
| 1. Check at the boundary | A strict decoder for each body. The server refuses a key type outside the allowlist, a challenge ID without `stc-`, a code that is not 6 digits, and a digest that is not 64 hex digits. It refuses a target, an `If-Match` or an `Idempotency-Key` outside its allowlist (Q1). |
| 2. Encode or contain | The server builds the signed text from its own record, one value on each line, with no CR or LF in a value. The CLI builds the origin and the request lines from its own values. The SSH namespaces and the OpenPGP notation keep a step-up signature and a key-add signature apart from other signatures. The CLI escapes control characters in server text. |
| 3. Limit the effect | One write, 120 seconds (A). At most 900 seconds and 50 writes (B), never for the exclusion list. At most 10 open challenges. Rate limits on codes, with an atomic reserve. |
| 4. Authorization at the point of use | The route checks its action on its object as before. The step-up adds a fact. It never grants an action. Each use checks the person, the credential kind and ID, the level, and the job and plan of an apply. The admin enrollment checks `server:enroll-step-up-key` on the person, and the store refuses the own account and a person who holds a key. |
| 5. Fail closed | An unknown factor, an unknown level, a store error or a missing setting refuses, and writes a failure entry to the audit log. |

Each step gets its own test, as `CLAUDE.md` says. The conformance case names
in `internal/stepup/stepuptest` hold the store part and the verifier part of them.

## The proposed ports

`internal/stepup` holds the domain types, the sentinel errors and five ports.
The section "First key enrollment from the no-factor state" proposes a
sixth port, `BootstrapStore`, and the method `KeyStore.EnrollBootstrapKey`.
They are not in the package yet. Each
port has a conformance suite in `internal/stepup/stepuptest`. The suites hold the
case names only, and each case skips until an adapter exists.

| Port | Methods | Errors |
| --- | --- | --- |
| `KeyStore` | `AddKey`, `EnrollFirstKey`, `EnrollBootstrapKey` (proposed), `Keys`, `Key`, `RevokeKey` | `ErrNotFound`, `ErrInvalid`, `ErrLimit`, `ErrConflict`, `ErrUsed`, `ErrExpired`, `ErrBootstrapClosed` (proposed) |
| `ChallengeStore` | `CreateChallenge`, `Challenge`, `AcceptChallenge`, `CompleteChallenge`, `RecordFailure` | `ErrNotFound`, `ErrInvalid`, `ErrExpired`, `ErrUsed`, `ErrChallengeLimit`, `ErrFactorRefused`, `ErrCounterRegressed` |
| `CredentialStore` | `UseCredential`, `RevokeCredentialsOfKey`, `RevokeCredentialsOfTOTP`, `RevokeCredentialsOfPerson` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrMismatch`, `ErrFactorRefused` |
| `TOTPStore` | `EnrollTOTP`, `ConfirmTOTP`, `TOTP`, `BeginTOTPAttempt`, `AcceptStep`, `ResetTOTP` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrLocked`, `ErrDisabled` |
| `ResetStore` | `ResetFactors` | `ErrInvalid` |
| `Verifier` | `Verify` | `ErrBadAnswer`, `ErrInvalid` |
| `BootstrapStore` (proposed) | `IssueCode`, `VoidCode`, `Code`, `StartFresh`, `InsertOfferID` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrBootstrapClosed` |

**Atomicity.** `CompleteChallenge` does the whole answer in one transaction,
in this order: it checks the state of the challenge, then, for a key answer,
it checks the key and advances its counter (`Completion.KeyID` and
`Completion.Counter`), then it marks the challenge `answered`, saves the
credential and appends the audit entry of the step-up. So a credential never
exists without its entry, and two answers of one challenge give one
credential. A second answer of a challenge that is not open answers
`ErrUsed` and never touches the counter, so a retry of the same answer, from
the CLI or a proxy after a lost response, never suspends a good key. A
counter that went back suspends the key, revokes its credentials, makes the
challenge void and appends the `security` entry in the same transaction, and
saves no credential, so a cloned key never gets one. No separate counter
method exists. `RevokeKey`, `ResetTOTP` and `ConfirmTOTP` revoke the
credentials and append their entry in the same transaction.
`AddKey` and `EnrollFirstKey` take the challenge of the key add, check it, mark it
`answered`, store the key and append the entry in one transaction, so one proof
adds one key, once, also after the revoke of that key. `EnrollFirstKey` checks
that the person holds no key in the same transaction as the add. `EnrollBootstrapKey`
takes the step-up installation lock, checks the no-factor state of the whole
installation, spends the code or inserts the offer ID, adds the key and appends
its entry, in one transaction. `ResetFactors`
deletes the keys and both TOTP factors, revokes each credential, voids each
open or accepted challenge and appends one entry in one transaction. `BeginTOTPAttempt` records a reservation, and `AcceptStep` validates it against
the record and consumes it in the same transaction as the step, so a reservation
that the store did not make, or that it consumed, changes nothing. `UseCredential` uses the credential up in its own transaction
before the write (Q4).

## Proposed terms

The terms go to `GLOSSARY.md` with the ADR.

- **CLI step-up**: the fresh authentication of a person in the
  `opendustpan` CLI, with a step-up key or a TOTP code, for one write or a
  short window. Avoid "step-up" alone (`GLOSSARY.md`), "sudo".
- **Step-up challenge**: the server record that binds one step-up to the
  person, the access token or session, the origin and the write.
- **Step-up credential**: the short-lived proof that a challenge was
  answered. It is not an access token. Avoid "step-up token".
- **Step-up key**: an SSH or OpenPGP public key that a person registered as a
  factor. Avoid "signing key", which names the secret of a webhook sink, and
  "trusted signing key", which names an OpenPGP key of a namespace registry.
  A trusted signing key never counts as a step-up key, also when it is the
  same OpenPGP key.
- **Step-up factor level**: `any` or `key`, the weakest factor that a
  namespace or the installation accepts for a step-up.
- **No-factor state**: the state of an installation in which no person holds
  a step-up key, in any state, and no person who is not a break-glass
  account holds a passkey. No person can reach the level `key` in it.
- **Step-up bootstrap code**: a one-time code that the bootstrap admin token
  issues for one person in the no-factor state. With a new authentication of
  the person, it adds one first step-up key. It is not a factor. Avoid
  "enrollment code", which names the code of an administrator for a person
  of a plain OAuth 2.0 provider or a trusted header.
- **Bootstrap fresh request**: the record of a new authentication at the
  identity provider that a person starts to redeem a step-up bootstrap code.
  It binds the code, the person and the sign-in ID. Only an authentication
  after its start counts.
- **Step-up installation lock**: the one lock that each write takes before
  it changes the count of step-up keys or passkeys of the installation, so
  that the check of the no-factor state is serialized.
- **Last-key guard**: the rule that a write that takes the count of step-up
  keys of the installation to zero needs the level `key` of the caller, or
  the last-holder recovery.
- **Last-holder recovery**: the reset or the delete of the sole holder of
  the step-up keys of an installation by a temporary administrator of host
  recovery mode 1 or mode 2. It is a break-glass use.

## Open points for the owner

1. **The ADR** that accepts the step-up credential as a new credential type.
2. **New dependencies (S12).** On `main`, `go.mod` has
   `github.com/ProtonMail/go-crypto` v1.5.2 as a direct requirement since
   #1306, for the registry, and `golang.org/x/crypto` v0.56.0 as an indirect
   requirement only. The options:
   - TOTP: RFC 6238 over `crypto/hmac` and `crypto/sha1` of the standard
     library, with no new module. Recommended.
   - `sshsig`: `golang.org/x/crypto/ssh` parses SSH keys and verifies the
     inner signature, but has no `-Y verify`. The module is in the module
     graph already, but only as indirect, so a use makes it a new direct
     requirement. The
     other option is a small `sshsig` verifier on the standard library
     (`crypto/ed25519`, `crypto/ecdsa`, `crypto/rsa`) with the SSH wire
     format written in the project. The owner decides between the new module
     and the project code.
   - OpenPGP: no standard library option. `github.com/ProtonMail/go-crypto`
     is linked already for the trusted signing keys of the registry, so a
     step-up verifier on it adds no module. Its use for a factor of a person
     is a new use of a security dependency, which the owner accepts or
     refuses. The other option is OpenPGP keys out of v1.
3. **The token kind of D4.** A personal access token from the CLI sign-in
   flow needs a server-side field on the token row (for example
   `issuedBy: cli-sign-in`). That is a change to `accesstoken`, with its own
   migration.
4. **The amendment of ADR 46 decision 2**, with the conditions above and
   the effect on SSO installations that open point 5 states.
5. **Does `stepUpFactors` govern a write done directly in the console?** The
   console factor table of Q3 and D1 make the level apply to the console
   path of a CLI challenge. This RFC does not say whether it also applies to
   a fresh write that a person does directly in the console, with no CLI
   challenge. The fact: in an SSO installation, ADR 46 decision 2 gives no
   built-in WebAuthn, and D7 counts `fresh=1` at `any` only. So at the level
   `key`, no console authentication passes in such an installation, and the
   console has no step-up key factor. The options:
   - A. The level governs each fresh write, in the CLI and in the console.
     `key` then holds for the write, whatever the route. In an SSO
     installation with no built-in WebAuthn, at the level `key`, only a
     person with a step-up key can do the write, and only through the CLI.
     Each first key is an admin enrollment (D6). With the SSO default `key`
     of the amendment conditions, this is each SSO installation by default.
   - B. The level governs only a CLI challenge and its console path. A
     direct console write keeps ADR 227 decision 1, and `fresh=1` passes it.
     No installation is blocked, but `key` then holds only for the route:
     the console path of a challenge refuses `fresh=1` at `key`, and the same
     write done directly in the console accepts it.
   - C. A, with the SSO installation default of `stepUpFactors` at `any`,
     not `key`. An SSO installation is not blocked by default. An
     installation or a namespace that sets `key` accepts the effect of A, and
     the user guide says so. TOTP then counts by default in an SSO
     installation, which the amendment conditions tried to prevent.

   Recommendation: C. A level that a direct console write passes around
   gives no guarantee, so B makes `key` weaker than its name. A with the
   default `key` blocks each gate change in a new SSO installation until an
   administrator enrolls a key for each person who changes gates. C keeps
   the meaning of `key` and moves the strict default to an explicit setting.
   C changes one condition of the ADR 46 amendment, so the owner decides it
   with open point 4.
6. **#1323 (401 or 403)**, which shapes the error section of Q6.
7. **The first key in the no-factor state.** The section "First key
   enrollment from the no-factor state" gives four options, a
   recommendation and five questions.

## Follow-up work

- The ADR of the owner that accepts the step-up credential and the open
  points above.
- The TOTP part of ADR 46, shared with the step-up (Q5), with the separate
  counter of each surface.
- The token field of open point 3.
- The admin enrollment route of the first key (D6), with its action
  `server:enroll-step-up-key`.
- The store adapters for both dialects, with the conformance suite, and the
  `Verifier` adapters with their suite.
- The step-up routes, and the `mfa.fresh-step-required` answer with
  `errorArgs.stepUp` on each write of Q6 (with #1240 and #1159).
- The job spec check of layer 4 and the header middleware of layer 2 (Q4).
- The CLI commands `opendustpan step-up`, `opendustpan step-up keys` and the
  retry in the client port (RFC 48).
- The user documentation page (ADR 214), with the limits of the levels (Q3)
  and the guidance for an identity provider with a phishing-resistant policy.

## Outcome

Empty until the owner accepts or drops this RFC.
