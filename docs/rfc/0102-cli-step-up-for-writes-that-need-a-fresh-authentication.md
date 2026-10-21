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
`pkg/stepup` holds the proposed ports and the names of the conformance cases,
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
- `go.mod` has no `golang.org/x/crypto` and no OpenPGP or TOTP module.

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

The PM made these decisions on behalf of the owner after review round 1.
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
- **Dependencies (review S12).** An open question for the owner. Prefer the
  standard library: RFC 6238 over `crypto/hmac`. Use `golang.org/x/crypto/ssh`
  for `sshsig` only if `go.mod` already has it. It does not.
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
- **No route adds a factor for another person.** An administrator can only
  reset the factors of another person (ADR 46 decision 9).

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
  signature never verifies as a git commit signature. An OpenPGP signature is
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
  only, or with a recovery code, adds no key. In an SSO installation, each
  key add also needs a fresh authentication at the identity provider
  (`fresh=1`). An access token alone never adds a key, so a stolen token
  cannot add the factor of the thief.
- **The first key.** A person with no step-up key and no WebAuthn has no
  factor at the level `key`. D5 then allows the first key only through the
  console path with WebAuthn, or, in an SSO installation, with a fresh
  authentication at the identity provider. This is open point 5 for the
  owner.
- **Revoke (D5).** A revoke needs a fresh authentication at the level `key`:
  any registered key of the person, also the key that it revokes, or the
  console path with WebAuthn. TOTP revokes no key, so a thief with a TOTP
  seed cannot remove the keys of the person. A person who lost each key and
  has no WebAuthn asks an administrator for the MFA reset. The revoke takes
  effect at once: each step-up credential that the key made stops working.
- **Admin reset.** The MFA reset of ADR 46 decision 9 needs a fresh
  authentication of the administrator. It deletes the keys and the TOTP
  factor of the person, and revokes each step-up credential of the person,
  from a key or from TOTP. No route adds a factor for another person.
- **Notifications.** The server sends an email to the person (the SMTP of
  ADR 212) on each key add, each key revoke, each TOTP enrollment, each TOTP
  reset and each admin reset. The email says what changed, when, and from
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
| A fresh authentication at the SSO identity provider | yes | open point 6 |

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
  and asks for a one-write step (A) or the console:
  - a step-up key add or revoke.
  - a TOTP enrollment, confirmation or reset.
  - a change of `cliStepUp` or `stepUpFactors`.
  - an access token create or rotate, of any principal.
  - a grant of `principal:manage-tokens`, and an act-as edge.
  - a reveal (a secret reveal, a state download by a person).

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
  the attempt as a failure. A good code then sets the count to 0 and stores
  the step, in one transaction that refuses a locked or a disabled factor.
  So parallel challenges never get more than 10 guesses before the lock, or
  100 before the disable.
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
status is open point 7 (#1323). The recommendation follows the first option of
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

- In an SSO installation, each key add (not only the first) and each TOTP
  enrollment needs a fresh authentication at the identity provider (the
  console path with `fresh=1`). Then the identity provider still controls
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
(`fresh=1`).

## Security layers (ADR 250)

| Step | The step-up check |
| --- | --- |
| 1. Check at the boundary | A strict decoder for each body. The server refuses a key type outside the allowlist, a challenge ID without `stc-`, a code that is not 6 digits, and a digest that is not 64 hex digits. It refuses a target, an `If-Match` or an `Idempotency-Key` outside its allowlist (Q1). |
| 2. Encode or contain | The server builds the signed text from its own record, one value on each line, with no CR or LF in a value. The CLI builds the origin and the request lines from its own values. The SSH namespaces and the OpenPGP notation keep a step-up signature and a key-add signature apart from other signatures. The CLI escapes control characters in server text. |
| 3. Limit the effect | One write, 120 seconds (A). At most 900 seconds and 50 writes (B), never for the exclusion list. At most 10 open challenges. Rate limits on codes, with an atomic reserve. |
| 4. Authorization at the point of use | The route checks its action on its object as before. The step-up adds a fact. It never grants an action. Each use checks the person and the credential kind and ID. |
| 5. Fail closed | An unknown factor, an unknown level, a store error or a missing setting refuses, and writes a failure entry to the audit log. |

Each step gets its own test, as `CLAUDE.md` says. The conformance case names
in `pkg/stepup/stepuptest` hold the store part and the verifier part of them.

## The proposed ports

`pkg/stepup` holds the domain types, the sentinel errors and five ports. Each
port has a conformance suite in `pkg/stepup/stepuptest`. The suites hold the
case names only, and each case skips until an adapter exists.

| Port | Methods | Errors |
| --- | --- | --- |
| `KeyStore` | `AddKey`, `Keys`, `Key`, `RevokeKey`, `AdvanceCounter` | `ErrNotFound`, `ErrInvalid`, `ErrLimit`, `ErrConflict`, `ErrCounterRegressed` |
| `ChallengeStore` | `CreateChallenge`, `Challenge`, `AcceptChallenge`, `CompleteChallenge`, `RecordFailure` | `ErrNotFound`, `ErrInvalid`, `ErrExpired`, `ErrUsed`, `ErrChallengeLimit` |
| `CredentialStore` | `UseCredential`, `RevokeCredentialsOfKey`, `RevokeCredentialsOfTOTP`, `RevokeCredentialsOfPerson` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrMismatch` |
| `TOTPStore` | `EnrollTOTP`, `ConfirmTOTP`, `TOTP`, `BeginTOTPAttempt`, `AcceptStep`, `ResetTOTP` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrLocked`, `ErrDisabled` |
| `Verifier` | `Verify` | `ErrBadAnswer`, `ErrInvalid` |

**Atomicity.** `CompleteChallenge` marks the challenge `answered`, saves the
credential and appends the audit entry of the step-up in one transaction, so
that a credential never exists without its entry, and two answers of one
challenge give one credential. `RevokeKey`, `ResetTOTP`, `ConfirmTOTP` and
`AdvanceCounter` (on a regression) revoke the credentials and append their
entry in the same transaction. `UseCredential` uses the credential up in its
own transaction before the write (Q4).

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
  factor. Avoid "signing key", which names the secret of a webhook sink.
- **Step-up factor level**: `any` or `key`, the weakest factor that a
  namespace or the installation accepts for a step-up.

## Open points for the owner

1. **The ADR** that accepts the step-up credential as a new credential type.
2. **New dependencies (S12).** `go.mod` has no `golang.org/x/crypto`. The
   options:
   - TOTP: RFC 6238 over `crypto/hmac` and `crypto/sha1` of the standard
     library, with no new module. Recommended.
   - `sshsig`: `golang.org/x/crypto/ssh` parses SSH keys and verifies the
     inner signature, but has no `-Y verify`, and it is a new module. The
     other option is a small `sshsig` verifier on the standard library
     (`crypto/ed25519`, `crypto/ecdsa`, `crypto/rsa`) with the SSH wire
     format written in the project. The owner decides between the new module
     and the project code.
   - OpenPGP: no standard library option. A new module, or OpenPGP keys out
     of v1.
3. **The token kind of D4.** A personal access token from the CLI sign-in
   flow needs a server-side field on the token row (for example
   `issuedBy: cli-sign-in`). That is a change to `accesstoken`, with its own
   migration.
4. **The amendment of ADR 46 decision 2**, with the conditions above.
5. **The first key (D5).** With D5, a person with no WebAuthn and no SSO can
   add no step-up key, and uses TOTP or the console only. The options:
   - A. Keep it so. The small team uses TOTP, or enrolls WebAuthn first.
     Recommended, because it keeps D5 with no exception.
   - B. Let a console fresh authentication at the level `any` add the first
     key only, with an email to the person and a wait of 24 hours before the
     key answers a challenge. An exception to D5 that the owner must accept.
6. **SSO and the level `key`.** OpenDustpan cannot see which factor the
   identity provider used for `fresh=1`. D1 counts the console path as `key`
   only after a WebAuthn assertion. Recommendation: a fresh authentication at
   the identity provider counts at `any` only. In an SSO installation with the
   default `key`, a person then uses a step-up key, or the console path
   fails at that level.
7. **#1323 (401 or 403)**, which shapes the error section of Q6.

## Follow-up work

- The ADR of the owner that accepts the step-up credential and the open
  points above.
- The TOTP part of ADR 46, shared with the step-up (Q5), with the separate
  counter of each surface.
- The token field of open point 3.
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
