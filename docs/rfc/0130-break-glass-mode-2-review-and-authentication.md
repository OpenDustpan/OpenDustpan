---
rfc: 130
title: Break-glass mode 2 over the network, its review and its authentication
status: accepted
adr: 372
created: 2026-11-14
---

# RFC 130. Break-glass mode 2 over the network: its review and its authentication

Status: accepted ([ADR 372](../adr/0372-signed-request-recovery-its-review-its-authentication-and-the-names-of-the-recovery-modes.md)). The owner accepted the four points as
recommended on 14 November 2026. This RFC holds the open points of the OpenSpec change
`break-glass-mode-2`, now `signed-request-recovery`. That change was not spec-ready: "the change conflicts
with ADR 142: an RFC comes first; the change conflicts with ADR 46: an RFC
comes first." It does not edit an accepted ADR. An owner decision on it
gives a new ADR.

## Problem

### What depends on this RFC

- The OpenSpec change `signed-request-recovery`, before `break-glass-mode-2` (roadmap, milestone 4, step 11:
  one administrator on a signed request, a refusal on a wrong signature or a
  reuse, and a break-glass review).
- Its open question 1: "Which review scope and use kind does mode 2 use, and
  does the review open when the opt-in is off?"
- Its open question 2: "Does the recovery session need an MFA step at
  issue?"

### What is already decided, and stays

ADR 295 (owner, 2026-10-31) decides mode 2 over the network. This RFC does
not reopen it:

> 1. **The route is allowed (owner).** Mode 2 may run over the network. A
>    route in the server accepts a signed recovery request. The route makes
>    a temporary administrator.
> 5. **One answer for every refusal.** Every refusal gives the same answer
>    [...] The audit entry holds the real reason. (owner)
> 6. **The answer token is encrypted.** [...] Only the CLI that made the
>    request can read the token. (owner)
> 7. **Nonce retention.** The server keeps a nonce until the not-after time
>    of its request plus 1 hour. (owner)
> 8. **A condition on shipping.** The route ships only after the store
>    keeps the first-boot recovery mode and the key. (owner)
> 9. **The controls of ADR 245 apply.** A temporary administrator from this
>    route keeps these controls of ADR 245. They are the reason, the audit
>    entries and the expiry. They also include the refusal in mode 3 and
>    the review of decision 3.
> 10. **Fail closed.** On an error or an unknown value, the route refuses
>     and writes an audit entry.

ADR 57 decision 5: "Every break-glass use writes an audit entry. This
applies to mode 1 and mode 2. The entry holds the mode, the time and the
source of the request."

ADR 245 decision 3 (product manager): "The second-person review of its
audit entries (`pkg/breakglass`, ADR 142, with the kind `temp-admin`) opens
only when `pkg/breakglass` has a store adapter." Decision 4: "A temporary
administrator cannot start a console session through an identity provider.
[...] It uses only the token that the command printed." Decision 13: the
expiry ends all access of the principal.

ADR 306 decision 1 (owner): the break-glass session limit is 1 hour, as a
setting.

### What the code does on main (14 November 2026)

- `pkg/breakglass/breakglass.go:75` to `:82` has three kinds of use:
  `freeze-break`, `account-session` and `temp-admin`. The kind `temp-admin`
  says: "a temporary administrator that the host command made for recovery.
  Ref is the principal ID. The review covers each action of the principal
  from its create to its end."
- `pkg/breakglass` has no store adapter in either dialect, so no review
  opens for the host command today (ADR 245, "Not decided here").

### The two conflicts

1. **ADR 142 decision 1** names two kinds of use: "`freeze-break`" and
   "`account-session`". ADR 245 decision 3 adds `temp-admin`, but ADR 142
   has no "Changed by" item for it. **ADR 142 decision 2**: "The
   installation setting `breakGlassReview.enabled` is off by default. [...]
   When it is off: a job through a freeze gets no review, a session of a
   break-glass account still gets a review, as ADR 70 requires, with no
   deadline and no notices." No decision says what a temporary
   administrator gets when the setting is off.
2. **ADR 46 decision 6**: "What MFA covers. Console sign-in, the creation of
   access tokens, and the reveal of a secret value (ADR 41)." Mode 2 exists
   for the case where the last administrator lost every factor (ADR 57). No
   factor exists to ask for when the route issues its token.

This is security code (ADR 250): authentication, tokens, a network route
and an untrusted signed body. Each point below keeps defense in depth, fails
closed, and writes an audit entry for every use and every refusal.

## Decision points

Each point has the context, the precedent, the options, a recommendation and
a short explanation. The points depend on each other in this order: 1, 2,
3, 4.

### 1. The kind and the scope of the review (ADR 142 decision 1)

**Context.** ADR 295 decision 1 makes mode 2 a temporary administrator, as
the host command does. ADR 295 decision 9 applies "the review of decision 3"
of ADR 245, and that review has the kind `temp-admin`. The draft change
asked for a new kind `recovery-session` with a console session. A console
session does not exist for a temporary administrator (ADR 245 decision 4).
ADR 371 (owner, 14 November 2026) gives each way to prove identity in the
token exchange its own token kind, so that a list, a revoke and an audit can
tell them apart. The same argument applies to a review list.

**Precedent.**

- HashiCorp Vault: `vault operator generate-root` makes a root token from a
  quorum of unseal or recovery keys. The token is a normal root token; the
  audit device records its use, and Vault advises to revoke it after the
  task (<https://developer.hashicorp.com/vault/docs/commands/operator/generate-root>,
  <https://developer.hashicorp.com/vault/docs/concepts/tokens#root-tokens>).
  The way the token was made does not change what it is.
- Kubernetes (kubeadm 1.29 and later): the break-glass credential
  `super-admin.conf` (group `system:masters`) is separate from the daily
  `admin.conf` (group `kubeadm:cluster-admins`), so that the two can be told
  apart and the daily one can be revoked
  (<https://www.kubernetes.dev/resources/keps/4214/>,
  <https://v1-30.docs.kubernetes.io/docs/setup/best-practices/certificates/>).
- Microsoft Entra ID: emergency access accounts are separate accounts, and
  each sign-in of one raises an alert to the administrators
  (<https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/security-emergency-access>).

**Options.**

- **A. The kind `temp-admin`, with the mode on the use.** One kind for each
  temporary administrator, from the host command or from mode 2. The use
  records the recovery mode (`bootstrap` or `signed-request`) and the source
  of the request (ADR 57 decision 5), and the review list filters on it.
  Scope `@server`, reference the principal ID, `Until` the end of the
  temporary administrator.
  - Gives: one review path for one kind of object (a temporary
    administrator). The code and ADR 245 already have it.
  - Costs: the review list needs a second field to tell the two paths apart.
- **B. A new kind `signed-recovery`.** Same scope and reference as A.
  - Gives: the kind alone tells the network path from the host path, in the
    spirit of ADR 371.
  - Costs: two kinds for one object. Each rule of ADR 142 decision 9 and of
    the store needs both kinds, and a new kind can miss one of them.
- **C. The kind `account-session`.** Refused: that kind is a session of a
  break-glass account of ADR 70, with a session ID. A temporary
  administrator is not a break-glass account (ADR 245 decision 3).

**Recommendation: A.** The object under review is the temporary
administrator, and ADR 295 decision 9 already sends mode 2 to the review of
ADR 245 decision 3. The mode on the use gives the list and the audit the
same separation that option B gives, with one code path. The new ADR also
adds the "Changed by" item that ADR 142 decision 1 lacks today: three kinds,
not two. If the owner prefers the strict reading of ADR 371 for reviews too,
option B is safe; it costs one more kind in each rule.

**ELI5.** A spare key opens the door. Whether the spare key came from the
drawer at home or by courier, the visitor is the same kind of guest, and
the guest book writes down how the key came.

### 2. Does the review open when the opt-in is off? (ADR 142 decision 2)

**Context.** ADR 142 decision 2 makes the deadline and the notices an
opt-in, off by default. With the setting off, a freeze break gets no review
and a break-glass account session still gets one, with no deadline and no
notices. Mode 2 is the one break-glass path that a caller on the network can
reach. A theft of the private key alone gives "a bounded remote temporary
administrator" (ADR 295 decision 2). ADR 347 decision 1 (owner) says to
warn, not block, for choices that the operator owns. Its decision 4 (owner)
says: "Security controls keep their own rules".

**Precedent.**

- PCI DSS v4.0.1 requirement 8.2.2: emergency access must be an exception,
  documented, and each action attributable to a person (the research note of
  ADR 57).
- NIST SP 800-53 Rev. 5 AC-2(2) removes or disables an emergency account
  after a set time
  (<https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final>).
- Microsoft Entra ID: "monitor sign-in and audit logs" of emergency
  accounts, with an alert on each use, always on
  (<https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/security-emergency-access>).
- AWS: a recovery of the root user with a lost MFA device needs the email
  address and the phone number of the account, and AWS mails the account on
  the change (<https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_mfa_lost-or-broken.html>).
- Google Workspace: when a user asks support to be promoted to super
  administrator, Google first contacts the existing administrators
  (<https://knowledge.workspace.google.com/admin/users/recovering-administrator-access-to-your-account>).

**Options.**

- **A. Always a review, with the deadline and the notices of the opt-in.**
  A mode-2 use opens a review whatever the setting. With the setting off,
  the review has no deadline and no reminder steps, as an account session.
  Each mode-2 use also sends one notification `break-glass.used` to the
  administrators at the use, whatever the setting.
  - Gives: no remote recovery passes without a record and a person who
    sees it. The operator keeps the choice of deadlines and reminders.
  - Costs: one notice that the operator cannot turn off, for each mode-2
    use. A mode-2 use is rare.
- **B. Always a review, with a deadline and all the notices.** The opt-in
  does not apply to mode 2.
  - Gives: the strictest follow-up.
  - Costs: a deadline that the operator turned off comes back for one path.
    ADR 347 decision 4 allows it for a security control, but option A meets
    the same need and keeps the setting of the operator.
- **C. Follow the opt-in fully.** With the setting off, no review.
  - Gives: one rule for all kinds.
  - Costs: a remote temporary administrator with no review. Against ADR 295
    decision 9, which applies the review of ADR 245 decision 3 without a
    condition.

**Recommendation: A.** ADR 295 decision 9 already asks for the review, so
the review is not optional. The deadline and the reminders stay the choice
of the operator, as for a break-glass account session. The one notice at the
use is the alert that Microsoft, AWS and Google all send when a recovery
path is used. The notice holds the time, the source address and the
principal of the temporary administrator, and no token.

**ELI5.** The spare key always rings a bell in the office when it is used.
Whether someone then must sign the incident form by Monday is the choice of
the office manager.

### 3. No review store, no route (ADR 142 decision 3, ADR 295 decision 8)

**Context.** ADR 142 decision 3: the use writes the notification
`break-glass.used` in its transaction, and a durable consumer opens the
review with `Store.Open`. ADR 245 let the host command run before a review
store exists, with a warning on standard error. A route on the network has
no operator at the terminal to read a warning.

**Precedent.**

- ADR 295 decision 8 (owner) sets the same kind of condition for the stored
  recovery mode: "Until then the route does not exist."
- Fail closed (ADR 250 step 5): on an unknown state, deny and audit.

**Options.**

- **A. The route needs the review store.** The route does not exist until
  `pkg/breakglass` has a store adapter in each dialect. At run time, the
  transaction that makes the temporary administrator also writes the
  notification `break-glass.used`; when that write fails, the transaction
  fails, and no temporary administrator exists.
  - Gives: no mode-2 use without its review. The same pattern as ADR 295
    decision 8.
  - Costs: mode 2 waits for the review store.
- **B. Ship the route first, with a warning in the log.**
  - Gives: mode 2 earlier.
  - Costs: a remote temporary administrator whose review never opens.

**Recommendation: A.** It is the structure that makes "a use with no review"
impossible: the use and the record of its review commit together or not at
all.

**ELI5.** The spare key works only when the bell is connected. If the bell
is broken, the key does not turn.

### 4. Authentication at the issue, with no factor (ADR 46 decision 6)

**Context.** ADR 46 decision 6 covers console sign-in and the creation of
access tokens with MFA. ADR 57 extends ADR 46 for the case where the last
administrator lost every factor and every recovery code. ADR 295 decision 6
gives the answer as a token encrypted to the CLI that asked. ADR 245
decision 4 gives a temporary administrator no console session through an
identity provider. ADR 46 decision 8: access tokens stay bearer tokens. So
mode 2 makes a token, not a console session, and asks for no MFA step. The
question is whether this is an exception to ADR 46 decision 6, and what
stands in place of the factor.

**Precedent.**

- HashiCorp Vault: `generate-root` asks for no MFA. The quorum of recovery
  keys is the authentication, and the new token comes back encrypted with a
  one-time pad or a PGP key of the operator
  (<https://developer.hashicorp.com/vault/docs/commands/operator/generate-root>).
- AWS: the recovery of a root user with a lost MFA device replaces the
  factor with proof of the email address and of the phone number of the
  account (<https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_mfa_lost-or-broken.html>).
- Google Workspace: a super administrator who lost access proves the
  ownership of the domain with a DNS record in place of the factor
  (<https://knowledge.workspace.google.com/admin/users/recovering-administrator-access-to-your-account>).
- Kubernetes: the break-glass `super-admin.conf` is a client certificate on
  the first control-plane host, with no second factor; its protection is the
  file permission and its custody (<https://www.kubernetes.dev/resources/keps/4214/>).
- NIST SP 800-63B: a cryptographic key that the operator holds is a
  "something you have" authenticator; a key that a passphrase unlocks is
  multi-factor (<https://pages.nist.gov/800-63-4/sp800-63b.html>).

**Options.**

- **A. The signed request is the authentication, and no console session.**
  The new ADR states the exception to ADR 46 decision 6 for this route
  only: the signature with the offline key stands in place of the factor.
  The CLI tool that signs offline reads the key only from a file that a
  passphrase encrypts, so the proof is something the operator has and
  something the operator knows. The answer is the encrypted token of ADR
  295 decision 6. No console session, as ADR 245 decision 4. The token
  lives no longer than the temporary administrator (ADR 245 decision 10),
  and the expiry ends all access (ADR 245 decision 13).
  - Gives: recovery works when no factor exists, which is the purpose of
    mode 2. The exception is narrow and written down.
  - Costs: the security of mode 2 rests on the custody of the offline key
    and its passphrase. ADR 295 decision 2 already states this risk.
- **B. A factor at the issue.** Refused: no factor exists in the case that
  mode 2 serves. It would make mode 2 useless.
- **C. A console session after the signed request.** The draft change
  proposed it. Against ADR 245 decision 4 and ADR 295 decision 6, and a
  console session with no factor is a wider exception to ADR 46 than a
  token.

**Recommendation: A.** It is what Vault, AWS and Google do: a different
proof stands in place of the lost factor, the result is short-lived, and the
use is audited. The layers stay independent. The handler checks the size,
the shape, the time and the mode. The service checks the signature, the
audience and the installation ID again. The store inserts the nonce with a
unique key in the transaction of the temporary administrator (ADR 295
decision 3). The answer is encrypted to the caller. The authorizer checks
each later request against the stored grant of the temporary administrator.
Each refusal writes an audit entry with the real reason (ADR 295 decision
5). Each step gets a test that sends hostile input to that step alone.

**ELI5.** You lost your phone, so the bank cannot send you a code. Instead
you bring the sealed letter from the safe and say the password that opens
it. The bank gives you a temporary card that stops working tonight, and it
writes your visit in the ledger.

## Not decided here

- What a temporary administrator may do with a bearer token where a route
  asks for a fresh authentication (ADR 212 decision 3, ADR 227). This holds
  for the host command too. If the reset of the factors of a person needs a
  fresh step, a temporary administrator cannot finish a recovery. A check of
  the routes and a separate decision follow.
- The format of the signed request, the maximum lifetime and clock skew of
  a request, the rotation of the pinned key and a second live recovery
  (open questions 3, 5, 6 and 7 of the change). The product manager decides
  them in the change; they touch no owner-tagged decision.

## What changes in the blocked change

The change `signed-request-recovery` must follow the owner decisions of ADR 295
that it does not follow today, whatever the outcome of this RFC:

- The route makes a temporary administrator and returns a token encrypted to
  the CLI (ADR 295 decisions 1 and 6), not a console session. The console
  step of its user path and the session rows of its threat model change.
- Every refusal gives the same answer (ADR 295 decision 5). The three
  distinct codes `break-glass.bad-signature`, `break-glass.reused-request`
  and `break-glass.wrong-mode` go; the audit entry holds the reason, and the
  help points to `opendustpan host recovery-status`.
- A nonce lives until the not-after time of its request plus 1 hour (ADR 295
  decision 7), not "expiry plus 7 days". Open question 4 is answered.
- The rate limit counter is in the database and an IPv6 address counts as
  its /64 group (ADR 295 decision 4), not a ring in memory.
- With this RFC accepted as recommended: the review has the kind
  `temp-admin` with the mode on the use (point 1), it opens whatever the
  opt-in, with one notice at each use (point 2), the route needs the review
  store (point 3), and the signed request stands in place of the factor
  (point 4). Open questions 1 and 2 are answered.

## Open questions

The four decision points above, for the owner. ADR 142 is not owner-tagged,
but ADR 295 is, and point 4 states an exception to ADR 46 on a security
path, so the owner decides all four.

## Checklist

- [x] Tolerates asynchronous replication between installations: a recovery
  is an installation action in `@server`, written under the server audit
  scope, and no write waits for another installation (ADR 248 decisions 3,
  6 and 9).

## Outcome

Points 1 to 4 accepted by the owner on 14 November 2026, as recommended:
[ADR 372](../adr/0372-signed-request-recovery-its-review-its-authentication-and-the-names-of-the-recovery-modes.md). ADR 372 also renames mode 1 to "bootstrap recovery" and mode 2 to
"signed-request recovery", and decides the point of "Not decided here" on a
fresh authentication.
