---
rfc: 102
title: A step-up of a person in the CLI for writes that need a fresh authentication
status: discussing
created: 2026-10-21
---

# RFC 102. A step-up of a person in the CLI for writes that need a fresh authentication

Status: discussing. This RFC answers RFC 48 question 6, option B (issue
#1234, part of #967). It holds the owner decisions of 20 October 2026 as
decided, and does not reopen them. It asks six questions, each with options
and a recommendation. The step-up credential is a new credential type, so the
owner accepts it in an ADR before work starts. The package `pkg/stepup` holds
the proposed ports and the names of the conformance cases, with no
implementation.

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
- `requireFresh` in `internal/api/server/freshness.go` passes each request
  that has no console session. The token routes call it, so a token create
  with an access token passes this check. `accesstoken.Service` has a
  `Freshness` port, and a search found no place in `cmd/` that sets it.
  Issue #1159 owns the token-change routes.
- `identity.Principal` has the field `Credential`, with the values
  `session`, `access-token`, `job-token` and `trust-token`, and the field
  `Kind`, with `person`, `service` and `workload`. `ActsAsPerson` fails
  closed: an unknown kind counts as a person.
- An error answer of the tool-neutral API has `message`, `code` (the HTTP
  status), `errorCode` and `errorArgs` (`codedResponse`). The refusal of a
  stale console sign-in also has `reauthenticate`, the path of a new
  sign-in.

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

### How this RFC keeps the rule of decision 4

The step-up credential is not an access token, and an access token never
becomes fresh. The server gives a fresh authentication to a request only when
the request holds two things:

- the access token of a person, which says who acts.
- a step-up credential of that same person, which proves a factor that the
  person used a short time ago for this write.

The access token alone gives nothing fresh. The step-up credential alone
authenticates nothing: the server refuses it without the access token that the
challenge names. This is the shape of AWS MFA-backed sessions, where the long-term
key gets a separate temporary credential after an MFA code, and of GitHub sudo
mode, where a session gets a separate, shorter grant.

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
- **Service principals.** Nothing in this RFC lets a service principal get a
  step-up credential.

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
 |  {method, path, bodyDigest, ifMatch,  |
 |   idempotencyKey}                     |
 |-------------------------------------->|  binds the challenge (Q1)
 |  201 {id, nonce, origin, action,      |
 |   object, summary, expiresAt}         |
 |<--------------------------------------|
 |  shows the summary, checks origin and |
 |  digest, signs or asks for the code   |
 |  POST .../step-up-challenges/{id}/answer
 |  {factor, keyId, signature | code}    |
 |-------------------------------------->|  verifies (Q2, Q3, Q5)
 |  201 {credential, expiresAt, scope}   |
 |<--------------------------------------|
 |  PUT .../approval-rules/security      |
 |  (access token + Dustpan-Step-Up)     |
 |-------------------------------------->|  checks binding, uses it once
 |  200                                  |
 |<--------------------------------------|
```

With no CLI factor, or when the namespace turns CLI step-up off, the answer of
the challenge create holds a console URL. The CLI prints it. The person opens
it, does the fresh authentication of ADR 227 decision 1 in the console, reads
the same summary and accepts it. The CLI polls the challenge, as a device
authorization grant does (RFC 8628), and gets the credential when the person
accepts the summary.

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

- **The challenge.** The CLI asks for a challenge with the method, the path,
  `bodyDigest` (SHA-256 of the exact request body bytes, in lowercase hex),
  the `If-Match` value and the `Idempotency-Key` of the write that it will
  send. The server makes the challenge from these values and from its own
  facts: a nonce of 32 random bytes, the principal ID, the access token ID,
  the action and the object of the route, the origin (`publicURL`), and an
  expiry of 120 seconds. A challenge ID has the prefix `stc-`.
- **The signed message.** The CLI signs one canonical text, in UTF-8, with
  one `key: value` pair on each line, in this fixed order:

  ```
  opendustpan-step-up: 1
  origin: https://dustpan.example.com
  challenge: stc-...
  nonce: <base64url of the 32 bytes>
  principal: <principal ID>
  token: <access token ID>
  action: namespace:manage-gates
  object: namespace:acme
  method: PUT
  path: /api/dustpan/v1/namespaces/acme/approval-rules/security
  body-sha256: <hex>
  if-match: "<etag>"
  idempotency-key: <key>
  expires: 2026-10-21T12:02:00Z
  ```

  The server builds the same text from its own record of the challenge, never
  from the text that the client sends, and verifies the signature over it. An
  SSH signature uses the `ssh-keygen -Y sign` format (`PROTOCOL.sshsig`) with
  the namespace `step-up@opendustpan.org`, so that a signature for git or for
  a file never verifies as a step-up, and a step-up signature never verifies
  as a git commit signature. An OpenPGP signature is a detached signature over
  the same text, with a signature notation `step-up@opendustpan.org` of the
  value `1`.
- **Use.** The write that follows must match each bound value. The server
  checks the method, the path, the digest of the body that it reads, the
  `If-Match` value and the `Idempotency-Key`. A mismatch refuses the write,
  stores nothing, uses up the credential and writes a failure entry to the
  audit log. A credential works one time. A retry with the same
  `Idempotency-Key` and the same body is a replay of ADR 210 decision 6. It
  answers the stored first answer and does not need a new credential.
- **What the CLI shows.** Before the person touches the key or types the
  code, the CLI prints:
  - the server origin, from the challenge, and stops when it is not the
    server URL that the CLI uses. This is the verifier name binding of NIST
    SP 800-63B-4, which WebAuthn gets from the relying party ID. A phishing
    server that relays a challenge of the real server gets an origin that the
    CLI refuses.
  - the summary that the server writes for the action, for example
    `Change approval rule "security" in namespace "acme": count 2 to 1`.
  - the change that the CLI computed itself from the body, as a field list
    with the old and the new values, so that the person does not rely on the
    server text only.
  - the first 12 hex digits of `bodyDigest`, which the CLI computed, and
    stops when the challenge holds another digest.
  - the expiry, and the factor that it will use.
- **The console path** shows the same summary and the same field list on the
  accept page.

Benchmark: WebAuthn signs the client data with the challenge and the origin,
and the relying party checks both. AWS Signature Version 4 signs the method,
the path, the headers and a SHA-256 of the body of each request. An SSH signature
namespace stops a signature for one purpose from passing in another
(`PROTOCOL.sshsig`).

### Q2. How does a person register and revoke step-up keys, and what does the audit entry hold?

| Option | Gives | Costs |
| --- | --- | --- |
| A. Reuse the SSH keys of the forge account | No new registration. | The forge is not the identity of OpenDustpan. A forge admin can add a key to an account. A key for git pushes becomes a key for policy changes. |
| B. A key registry for each person in OpenDustpan, with a fresh authentication to add and to revoke | OpenDustpan decides which key is a factor. The server writes an audit entry for each change. | A person registers each key one time. |
| C. B. with an attestation of each hardware key | The server knows that a key is on hardware. | `ssh-keygen` can write an attestation for a FIDO key, but OpenPGP has none, and the server must hold a list of trusted roots. |

**Recommendation: B, and keep C for a later version.**

- **Routes**, under the caller (ADR 210 decision 3):
  - `GET /api/dustpan/v1/me/step-up-keys` lists the keys of the caller.
  - `POST /api/dustpan/v1/me/step-up-keys` adds a key.
  - `DELETE /api/dustpan/v1/me/step-up-keys/{id}` revokes a key.
  - An administrator uses the MFA reset of ADR 46 decision 9 for another
    person. That reset revokes the keys too.
- **Add.** The request holds the public key, a name, and a signature with the
  new key over a registration challenge, which proves that the person holds
  the private key. The add needs a fresh authentication of its own: a console
  session in the window, or a step-up with a key or a TOTP code that the
  person registered before. The first key of a person comes from the console.
  An access token alone never adds a key, so a stolen token cannot add the
  factor of the thief.
- **Revoke.** A revoke needs a fresh authentication with any factor of the
  person, also with the key that it revokes. So a person who still has the
  key can revoke it from the CLI. A person who lost the key revokes it in the
  console. The revoke takes effect at once: each step-up credential that the
  key made stops working.
- **Allowed key types.** `sk-ssh-ed25519@openssh.com`,
  `sk-ecdsa-sha2-nistp256@openssh.com`, `ssh-ed25519`,
  `ecdsa-sha2-nistp256`, and RSA of 3072 bits or more with `rsa-sha2-256` or
  `rsa-sha2-512`. OpenPGP: Ed25519 and RSA of 3072 bits or more. The server
  refuses each other type. A person has at most 10 keys.
- **The key ID.** The server gives each key an ID with the prefix `stk-`. It
  also stores the fingerprint: `SHA256:<base64>` for an SSH key, as
  `ssh-keygen -l` writes it, and the v4 or v5 fingerprint for an OpenPGP key.
- **The counter.** For an `sk-` key, the server stores the signature counter
  of the last signature. It refuses a signature whose counter is not higher,
  unless the key always sends 0. A cloned key then fails.
- **The audit entry.** Each step-up writes one audit entry, with the
  challenge ID, the factor kind, the key ID and its fingerprint (or the TOTP
  factor ID), the user presence and user verification flags of an `sk-` key,
  the counter, the action, the object, `bodyDigest` and the access token ID.
  The audit entry of the write names the challenge ID. The entry never holds
  the signature, the code or the credential. A key add and a key revoke each
  write an audit entry of the kind `change`.

Benchmark: GitHub keeps signing keys apart from authentication keys on an
account. A FIDO authenticator returns a signature counter, and WebAuthn
Level 2 says that a relying party can use it to detect a cloned
authenticator. NIST SP 800-63B-4 asks for an authentication at the assurance level of the
new authenticator, or higher, before the verifier binds it to an account.

### Q3. Can a namespace require a phishing-resistant factor and refuse TOTP?

NIST SP 800-63B-4 says that "OTP authentication is not phishing-resistant",
and that a verifier "SHALL offer at least one phishing-resistant
authentication option at AAL2". A signature of a challenge that holds the
origin, with a CLI that checks the origin (Q1), is phishing-resistant. A
software key on the disk of a workstation is an exportable key, so it does not
meet AAL3, which needs a non-exportable key.

| Option | Gives | Costs |
| --- | --- | --- |
| A. No setting. Each factor counts | Simple. | The regulated persona cannot refuse a factor that a phishing site can relay. |
| B. A namespace setting with three levels | The regulated persona refuses TOTP, or accepts only hardware keys. A small team keeps TOTP. | One more setting, with an order of strictness. |
| C. Key signing only, for everyone | No phishing risk from codes. | A team with no SSH key culture and no hardware key must use the console for each step. |

**Recommendation: B.** The namespace setting `stepUpFactors` has three
values:

- `any` (the default): an SSH key, an OpenPGP key or a TOTP code.
- `key`: an SSH key or an OpenPGP key. The server refuses TOTP with
  `mfa.factor-not-allowed`.
- `hardware-key`: an `sk-` SSH key only, with the user presence flag set in
  the signature. The server reads the flag from the signature
  (`PROTOCOL.u2f`). It cannot check that an OpenPGP key is on a card, so it
  refuses OpenPGP at this level. A sub-option `requireUserVerification`
  asks for the user verification flag too (a PIN or a biometric, from a key
  made with `ssh-keygen -O verify-required`).

The console path always counts at each level, because ADR 227 decision 1 holds
it. The installation holds the same setting as a floor, and the strictest
level that holds for a write applies, as for the window (ADR 227 decision 4).
A change to a less strict level is a weaker setting, so it needs a fresh
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
  server uses it up in the transaction of the write.
- **A window (B).** `opendustpan step-up --namespace acme --for apply` gets a
  window credential before a `tofu apply` or a `pulumi up` with the providers.
  The challenge holds the namespace, the list of actions, and, when the CLI
  knows it, the digest of the saved plan file, so the summary shows each
  change of the plan. The lifetime is the shortest fresh window that holds for
  the person and the namespace (ADR 227 decisions 2 and 4), at most 900
  seconds. A window of 0 allows no window credential: each write uses A. The
  server checks the window of each write again, so a rule with
  `freshAuthSeconds: 0` still asks for a new step. A window credential works
  for at most 50 writes. The CLI writes it to a file with mode `0600` in the
  user configuration directory, and the provider reads it from there, never
  from an environment variable, so that a pipeline log or a child process
  environment does not hold it.
- **How it differs from an access token.**

  | | Access token | Step-up credential |
  | --- | --- | --- |
  | Proves | which principal acts | that the person used a factor for this write, a short time ago |
  | Alone | authenticates | authenticates nothing. The server refuses it without its access token |
  | Holder | a person or a service principal | a person only |
  | Lifetime | days, up to the maximum of ADR 53 | 120 seconds (A), at most 900 seconds (B) |
  | Uses | any number | one (A), at most 50 (B) |
  | Renewal | rotation | none. A new step |
  | Bound to | the principal | the person, the access token ID, the origin, and the request (A) or the namespace and the actions (B) |
  | Header | `Authorization` | `Dustpan-Step-Up` |
  | Stored by the server | a hash | a hash |
  | Makes another credential | can create a token where its grants allow | never makes a step-up credential. It creates an access token only in mode A, for that one create |

- **It never reaches a pipeline or a job.** Each of these layers stops it
  alone:
  1. The server makes a challenge only for a principal of kind `person`,
     with the credential `access-token` from a personal token, or `session`.
     A service token, a job token, a trust token and an unknown kind are
     refused.
  2. The server reads `Dustpan-Step-Up` only on the tool-neutral API under
     `/api/dustpan/v1`. The runner protocol, the tool-compatible APIs and the
     MCP endpoint refuse a request that holds the header.
  3. The server refuses the credential when the access token of the request
     is not the token that the challenge names.
  4. A job spec, a job token and an issued token (ADR 205) never hold it. The
     server makes no claim from it.
  5. The CLI does a step-up only when stdin and stdout are a terminal. With
     no terminal, the CLI returns `ErrFreshAuthRequired` (RFC 48 question 6,
     option A).

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
| B. One TOTP factor for each person, shared with built-in MFA (ADR 46) | One enrollment. One lockout. | The CLI step-up waits for the TOTP part of ADR 46. |

**Recommendation: B.**

- **Enroll.** `POST /api/dustpan/v1/me/totp` needs a fresh authentication from
  the console or from a step-up key. The server makes a secret of 20 random
  bytes (160 bits, as RFC 4226 recommends) and answers it one time, as a
  base32 string and an `otpauth://` URI. The server completes the enrollment
  only after the person sends one valid code. A replay of the create (ADR 210 decision 6)
  never answers the secret again.
- **Parameters.** RFC 6238: HMAC-SHA-1, 6 digits, a step of 30 seconds. The
  server accepts the current step and one step before it, for clock drift
  and typing time.
- **Storage.** The server seals the secret with envelope encryption, with a
  key of the installation (the `secret` package). A database row never holds
  the plain secret. A backup holds the sealed value only.
- **One use.** The server stores the last time step that it accepted, and
  refuses a code of that step or of an earlier step. NIST SP 800-63B-4 says
  that a verifier "SHALL accept a given OTP only once while it is valid".
- **Rate limits.** A challenge takes at most 5 wrong codes, then it is void.
  10 wrong codes in a row for one person lock the TOTP factor of that person
  for 15 minutes. 100 wrong codes in a row disable the factor until the
  person enrolls again, which is the upper limit of NIST SP 800-63B-4. A good
  code sets the count to 0. A lockout stops only the TOTP factor, not the
  account, so the console path and the keys still work. Each wrong code and
  each lockout write an audit entry.
- **Recovery codes** (ADR 46 decision 4) are for a sign-in when a person lost
  each factor. They never answer a step-up challenge.

Benchmark: RFC 6238 and RFC 4226, NIST SP 800-63B-4 as quoted above. AWS STS
takes a 6-digit code. GitHub sudo mode accepts a TOTP code and refuses an SMS
code.

### Q6. Which writes need a fresh authentication, and what error does the CLI get?

The list comes from ADR 227 decision 2 and the ADRs that it names. This RFC
adds no write to it. It says which writes the CLI step-up can satisfy.

| Write | Source | CLI step-up |
| --- | --- | --- |
| A change of a gate object. The objects are approval rules, approval defaults, access limits, path sets, final gates, and gates and ownership rules in `enforce` mode | ADR 230 decision 2 | yes |
| The signature carry-over setting to `on` | ADR 225 decision 12 | yes |
| A setting that makes the installation or a namespace less secure | ADR 212 decision 3 | yes |
| The creation and the rotation of an access token, also of a service principal | ADR 227 decision 2, ADR 70 decision 2 | yes, one write only (Q4) |
| A grant of `principal:manage-tokens`, and a trust rule that names a principal (an act-as edge) | ADR 70 decisions 1, 3 and 10, owner decision 3 | yes |
| The changes of ADR 70 decisions 16, 17 and 23 | ADR 227 decision 2 | yes |
| The switches of ADR 125 decision 3 | ADR 227 decision 2 | yes |
| A secret reveal, and a state download by a person (a reveal) | ADR 227 decision 2, ADR 240 | yes, one read only |
| The add and the revoke of a step-up key, the enrollment of TOTP | this RFC | yes (Q2, Q5) |
| `cliStepUp` from `off` to `on`, `stepUpFactors` to a less strict level | this RFC, ADR 212 decision 3 | yes |
| The apply of a unit that requires a second factor for an apply | RFC 89 question 8 | yes, with `opendustpan apply`, one write bound to the job and its plan |
| An approval or a signature of a change order | ADR 225 decisions 5 and 7, ADR 227 decision 5 | no: console only |
| A confirmation | ADR 103, ADR 128 decision 5 | no: console only |
| A break-glass review | ADR 142 | no: console only |
| A ChatOps approval | ADR 146 | no: in chat, on its own terms |

**The PR-comment apply (owner decision 5).** A comment never holds a step-up.
When the unit or its approval rules ask for a second factor, the server
refuses the comment with a reply that links to the apply in the console. The
reply also names `opendustpan apply --job <job ID>`, which does the step-up in
the CLI. The step-up of that apply binds to the job ID, the action
`job:apply` and the plan digest (`cho-digest/1` for a change order, else the
plan ID), so that a touch for one plan never applies another plan.

**The error.** A write with no fresh authentication answers `403` with
`errorCode` `mfa.fresh-step-required` and changes nothing (ADR 227 decision 6).
The answer keeps `reauthenticate`, the console sign-in path, and adds
`errorArgs.stepUp`:

```json
{
  "code": 403,
  "errorCode": "mfa.fresh-step-required",
  "message": "this change needs a fresh authentication; run the command again in a terminal, or sign in to the console again",
  "reauthenticate": "/api/dustpan/v1/sign-in/corp-sso?fresh=1",
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
| `stepup.ErrNotPerson` | 403 | `mfa.step-up-person-only` |
| `stepup.ErrInvalid` | 422 | `mfa.step-up-invalid` |
| `stepup.ErrLimit` | 422 | `mfa.step-up-key-limit` |
| `stepup.ErrConflict` | 409 | `mfa.step-up-key-conflict` |

`mfa.cli-step-up-off` and `mfa.factor-not-allowed` name the console URL of
the challenge. The client port of RFC 48 maps `mfa.fresh-step-required` to
`ErrFreshAuthRequired`, as it does today. A client with no terminal stops
there.

## A point for the ADR that accepts this RFC

ADR 227 decision 1 says that a fresh authentication is a built-in MFA step or
a new authentication at the SSO identity provider. ADR 46 decision 2 says that
with an SSO identity provider, OpenDustpan adds no MFA step of its own. The
CLI factors of owner decision 1 are a factor of OpenDustpan. The ADR that
accepts this RFC must say:

- that a CLI step-up with a step-up key or a TOTP code is a third kind of
  fresh authentication in ADR 227 decision 1.
- whether it counts in an installation with an SSO identity provider.

**Recommendation:** it counts in each installation. A namespace that wants
only its identity provider sets `cliStepUp: off`, and the console path then
asks the identity provider for a new authentication (`fresh=1`). The ADR
then amends ADR 46 decision 2 for the step-up factors only.

## Security layers (ADR 250)

| Step | The step-up check |
| --- | --- |
| 1. Check at the boundary | A strict decoder for each body. The server refuses a key type outside the allowlist and a challenge ID without `stc-`. It refuses a code that is not 6 digits, and a digest that is not 64 hex digits. |
| 2. Encode or contain | The server builds the signed text from its own record. The SSH namespace and the OpenPGP notation keep a step-up signature apart from other signatures. |
| 3. Limit the effect | One write, 120 seconds (A). At most 900 seconds and 50 writes (B). Rate limits on codes. |
| 4. Authorization at the point of use | The route checks its action on its object as before. The step-up adds a fact. It never grants an action. |
| 5. Fail closed | An unknown factor, an unknown level, a store error or a missing setting refuses, and writes a failure entry to the audit log. |

Each step gets its own test, as `CLAUDE.md` says. The conformance case names
in `pkg/stepup/stepuptest` hold the store part of them.

## The proposed ports

`pkg/stepup` holds the domain types, the sentinel errors and four ports. Each
port has a conformance suite in `pkg/stepup/stepuptest`. The suite holds the
case names only, and each case skips until an adapter exists.

| Port | Methods | Errors |
| --- | --- | --- |
| `KeyStore` | `AddKey`, `Keys`, `Key`, `RevokeKey`, `AdvanceCounter` | `ErrNotFound`, `ErrInvalid`, `ErrLimit`, `ErrConflict` |
| `ChallengeStore` | `CreateChallenge`, `Challenge`, `ConsumeChallenge`, `RecordFailure` | `ErrNotFound`, `ErrExpired`, `ErrUsed` |
| `CredentialStore` | `SaveCredential`, `UseCredential`, `RevokeCredentialsOfKey` | `ErrNotFound`, `ErrExpired`, `ErrUsed`, `ErrMismatch` |
| `TOTPStore` | `EnrollTOTP`, `TOTP`, `AcceptStep`, `RecordTOTPFailure`, `ResetTOTP` | `ErrNotFound`, `ErrUsed`, `ErrLocked` |
| `Verifier` | `Verify` | `ErrBadAnswer`, `ErrInvalid` |

## Proposed terms

The terms go to `GLOSSARY.md` with the ADR.

- **CLI step-up**: the fresh authentication of a person in the
  `opendustpan` CLI, with a step-up key or a TOTP code, for one write or a
  short window. Avoid "step-up" alone (`GLOSSARY.md`), "sudo".
- **Step-up challenge**: the server record that binds one step-up to the
  person, the access token, the origin and the write.
- **Step-up credential**: the short-lived proof that a challenge was
  answered. It is not an access token. Avoid "step-up token".
- **Step-up key**: an SSH or OpenPGP public key that a person registered as a
  factor. Avoid "signing key", which names the secret of a webhook sink.

## Follow-up work

- The ADR of the owner that accepts the step-up credential and the point
  above.
- The TOTP part of ADR 46, shared with the step-up (Q5).
- The store adapters for both dialects, with the conformance suite.
- The step-up routes, and the `mfa.fresh-step-required` answer with
  `errorArgs.stepUp` on each write of Q6 (with #1240 and #1159).
- The CLI commands `opendustpan step-up`, `opendustpan step-up keys` and the
  retry in the client port (RFC 48).
- The user documentation page (ADR 214).

## Outcome

Empty until the owner accepts or drops this RFC.
