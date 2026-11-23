---
version: 5
---

Roadmap: `docs/roadmap.md:201` (Signed-request recovery).

## Why

**Persona: operator.** The last site administrator of an installation lost
every factor and every recovery code. The operator has no shell on the host,
or must not stop the server. At first boot the operator chose signed-request
recovery and kept an offline operator key. The operator needs one temporary
administrator, made from a request that only that key can sign. The temporary
administrator then resets the factors of the locked-out person and lets the
person enroll a new factor.

The user path this change must make work:

1. **First boot.** Start `opendustpan serve` with
   `--recovery-mode signed-request` and
   `--recovery-public-key-file <file>`. The server stores the mode and the
   first key of the trust bundle in one transaction. The installation name is
   the setting `--installation` (ADR 248). The operator keeps the name and
   the offline key.
2. **Make the request (offline).** `opendustpan recovery request
   --installation <name> --person <name> --reason <text>` writes the canonical
   request `request.json` and the ephemeral private key of the answer,
   `request.key` (mode 0600).
3. **Sign (offline).** `opendustpan recovery sign --key <file> request.json`
   asks for the passphrase of the key file and writes `request.json.sig`. It
   refuses a key file that no passphrase encrypts.
4. **Check (offline).** `opendustpan recovery verify --public-key <file>
   request.json` checks the shape, the times and the signature, with no
   network.
5. **API and CLI.** `opendustpan recovery submit --server <url> request.json`
   sends the request and the signature to
   `POST /api/dustpan/v1/recovery/signed-requests`. It decrypts the answer
   with `request.key` and prints the person, the token and the expiry, as the
   host command does.
6. **Recover.** With the token, the temporary administrator resets the factors
   of the locked-out person (the admin reset route of the change `mfa`) and
   issues an enrollment code for that person
   (the NEW route `POST /api/dustpan/v1/principals/{id}/enrollment-code` of the change
   `app-level-second-factor-totp`). The person enrolls a new factor. A second
   site administrator records the break-glass review.
7. **Rotate the key.** A site administrator with a fresh authentication adds a
   new key to the trust bundle (`POST /api/dustpan/v1/recovery/keys`), then
   removes the old key (`DELETE /api/dustpan/v1/recovery/keys/{id}`).
8. **Docs page.** `docs/guides/signed-request-recovery.md` gives steps 1 to 7.
   A test runs each command of the page.
9. **E2e.** An e2e step runs steps 2 to 6 against the real binary over TLS:
   one recovery, one replay and one bad signature.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Any refusal of the route: a bad signature, an unknown key, a time out of the window, a used nonce, the rate limit, a wrong mode, another installation, a wrong audience or a live recovery | `403` with code `recovery.refused`, and the same text for each reason: "The server refused the recovery request. The audit log and the server log name the reason." | `opendustpan recovery verify --public-key <file> request.json` |
| `recovery sign` gets a key file with no passphrase | Exit 1: "The key file is not encrypted. Encrypt it with a passphrase, then run the command again." | `ssh-keygen -p -f <file>` |
| `recovery submit` gets an answer that `request.key` cannot decrypt | Exit 1: "The answer does not match request.key. Make a new request." | `opendustpan recovery request --help` |
| A temporary administrator of a recovery calls a step that needs a fresh authentication, other than the two recovery steps | `403` with code `mfa.fresh-step-required` | `opendustpan recovery --help` (it names the two steps) |
| A removal of the last key of the trust bundle | `409` with code `recovery.last-key` | `opendustpan recovery keys list` |

Evidence for the claims above:

- The server has the recovery mode setting with the values `bootstrap`,
  `signed-request` and `none`. Proof: `internal/cli/settings.go:370`
  (`"recovery-mode", "DUSTPAN_RECOVERY_MODE", "bootstrap"`).
- No store keeps the recovery mode or a key. Proof: `rg -n 'recovery_mode'
  internal/store/sqlite/migrations internal/store/postgres/migrations` gives no line.
  `docs/guides/host-recovery.md:25` says "no store keeps the mode yet".
- No ID of the installation exists. Proof: `rg -n 'InstallationID' --type go`
  hits only the installations of the Git host app (`pkg/forge/connection.go`).

## What Changes

- The store keeps the first-boot recovery settings: the mode and the trust
  bundle of offline operator keys (ADR 295 decision 8). The stored mode wins
  over `--recovery-mode` (ADR 245 decision 6).
- The NEW public route `POST /api/dustpan/v1/recovery/signed-requests` accepts a signed recovery request.
  It makes one temporary administrator in one transaction, with the nonce, the sealed answer, the
  review notification and the audit entries (ADR 295, ADR 372).
- In signed-request recovery, `serve` refuses to start without `--public-url`
  (the audience of a request) and without a key file.
- Every refusal gives one answer. The audit entry and the server log hold the
  reason, and a wrong audience has the reason `audience`.
- The server encrypts the answer token with HPKE to the ephemeral key of the
  request and stores the sealed answer with the nonce, so the same request
  before `notAfter` gets the same answer again.
- The break-glass use of the kind `temp-admin` records the recovery mode, the
  source and the `keyId` of the signing key. A signed-request recovery opens
  its review whatever `breakGlassReview.enabled` says, and sends one notice to
  the site administrators.
- A temporary administrator of a recovery (host command or route) can reset
  the factors of a person and issue an enrollment code with no fresh
  authentication (ADR 372 decision 6). Every other fresh step refuses it.
- Routes and CLI commands list, add and remove the keys of the trust bundle.
- The CLI commands `recovery request`, `recovery sign`, `recovery verify` and
  `recovery submit`.

Out of scope:

- A change of the recovery mode after the first boot (ADR 57, "Not decided
  here").
- Hardware keys (`sk-ssh-ed25519@openssh.com`). ADR 372 decision 5 asks for a
  key file that a passphrase encrypts. A later change can add them.
- Bootstrap recovery and the host command, except the fields of the review use
  and the two recovery steps of ADR 372 decision 6.
- The admin reset route (change `mfa`) and the enrollment code route (change
  `app-level-second-factor-totp`). This change adds only the exception of ADR
  372 decision 6 to their fresh-step checks.

Depends on:

- `mfa` (the admin reset route)
- `app-level-second-factor-totp` (the enrollment code route)
- `add-one-generic-settings-api` (order of the milestone review)
- `break-glass-sign-in-typed` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The request binds to the installation name and to the audience, the setting
  `--public-url` (the external URL of the server). Both must match. The
  operator keeps the offline key and both values from the first boot, and the
  docs page puts this in the
  first-boot checklist.
- A refusal never says why (ADR 295 decision 5). `recovery verify` finds the
  errors that the operator can make offline: the shape, the times, the key and
  the signature. The other reasons (a used nonce, the rate limit, a live
  recovery) are in the server log and the audit log only.
- A request lives 10 minutes. The operator signs, checks and submits in one
  sitting. A late request needs a new request and a new signature.
- While a temporary administrator of a recovery is live, a second recovery is
  refused. The operator waits for the expiry, or a site administrator ends the
  temporary administrator first.

## What main already does

Each fact names its file, line and function on main. The change specifies
only the difference.

- `pkg/breakglass` has the kind `temp-admin`, in `@server`, with a time limit (`pkg/breakglass/breakglass.go:82 (KindTempAdmin)`)
- `pkg/breakglass` checks the kind of a use (`pkg/breakglass/breakglass.go:135 (validateKind)`)
- `pkg/breakglass` has a store adapter in SQLite, so the condition of ADR 372 decision 4 holds (`internal/store/sqlite/breakglass.go:33 (BreakGlass)`)
- `pkg/breakglass` has a store adapter in PostgreSQL (`internal/store/postgres/breakglass.go:35 (BreakGlass)`)
- The host issuer makes the person, the grant, the record and the token in one transaction (`pkg/accesstoken/host.go:55 (CreateHostRecovery)`)
- The host command knows the three modes `modeBootstrap`, `modeSignedRequest` and `modeNone` (`cmd/opendustpan/host.go:86 (modeBootstrap)`)
- A verifier of the signature format of `ssh-keygen -Y sign` exists on the standard library (`internal/sshsig/verifier.go:27 (NewVerifier)`)
- The verifier parses an SSH public key (`internal/sshsig/sshsig.go:170 (ParsePublicKey)`)
- The denial limiter keys a stream of denials by `Principal`, `Action`, `Kind` and `Scope` (`internal/denylimit/denylimit.go:45 (Key)`)
- The error code of a missing fresh step exists (`internal/errcode/catalog_mfa.go:15 (MFAFreshStepRequired)`)

## Owner decisions that apply

- **RFC 130, accepted as written by the owner on 14 November 2026.** ADR 372
  decisions 2 to 5 record it.
- **ADR 372 decision 6, accepted by the owner on 14 November 2026**: a
  temporary administrator of a recovery resets the factors of a person and
  issues an enrollment code with no fresh authentication.
- **The names, owner, 14 November 2026.** The owner asked for a new name for
  "mode 2" ("wtf does that even mean"). ADR 372 decision 1: "bootstrap
  recovery" and "signed-request recovery".

## Decisions of the product manager in this change

RFC 130 ("Not decided here") gives these four points to the product manager.
Each one was checked against the ADRs. None touches an owner-tagged decision.

1. **The request format.** A detached SSH signature (the format of
   `ssh-keygen -Y sign`, with the namespace `opendustpan-recovery@<installation
   name>`) over a canonical JSON body. The server refuses a body that is not in
   canonical form. ADR check: ADR 57 "Not decided here" leaves the format
   open. ADR 57 "Consequences": "A new library for this is a new dependency."
   The verifier is `internal/sshsig` on the standard library, so no new module
   is necessary.
2. **The lifetime.** `notAfter` is at most 10 minutes after `notBefore`. The
   server accepts a request from `notBefore` minus 2 minutes to `notAfter`
   plus 2 minutes. ADR check: ADR 295 decision 7 keeps a nonce until "the
   not-after time of its request plus 1 hour", which covers the skew.
3. **Key rotation.** The trust bundle holds 1 to 4 keys. A site administrator
   adds a new key. The old and the new key both work. The removal of the old
   key ends it. ADR check: ADR 295 decision 8 says that the store keeps "the
   first-boot recovery mode and the key". The bundle keeps the first-boot key
   and can hold more. An add and a removal change a security setting: each
   needs `server:manage-settings`, a fresh authentication, a reason, an audit
   entry and a notice to the site administrators (ADR 212 decision 3, ADR 227
   decision 2). A temporary administrator of a recovery cannot add a key (ADR
   372 decision 6).
4. **A second live recovery.** The route refuses a request while a temporary
   administrator from a recovery, by the host command or by the route, is
   live. The refusal is the one answer, and the audit reason is
   `recovery-active`. ADR check: ADR 245 decision 8 allows at most one of two
   concurrent recoveries for one person. This decision is stricter, and it
   applies to the route only.

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged
when its text carries "(owner)", or when the ADR records decisions of the
project owner.

**ADR 372: Signed-request recovery: its review, its authentication and the
names of the recovery modes** (owner-tagged: decisions 1 to 6).

- Decisions that apply: 1 to 6 (current).
- Changed by: none. Current rule: this ADR.
- Quote: "Each temporary administrator, from the host command or from the
  route of signed-request recovery, gets one break-glass use of the kind
  `temp-admin`."
- Quote: "A signed-request recovery opens a review also when
  `breakGlassReview.enabled` is off."
- Quote: "The transaction that makes the temporary administrator also writes
  the notification `break-glass.used` (ADR 142 decision 3)."
- Quote: "The CLI tool that signs offline reads the key only from a file that
  a passphrase encrypts"
- Quote: "A temporary administrator of a recovery, from the host command or
  from the route of signed-request recovery, can do exactly two steps with no
  fresh authentication"
- Effect: the review, the notice, the transaction, the signer and the two
  steps of this change.
- Conflict: none.

**ADR 295: Signed-request recovery over the network** (owner-tagged, all
decisions).

- Decisions that apply: 1 to 10 (current).
- Changed by: ADR 372 extends decisions 8 and 9. Chain to the end: ADR 372.
- Current rule: this ADR, with ADR 372.
- Quote: "The route makes a temporary administrator."
- Quote: "The nonce is a unique-key insert. The server inserts the nonce of a
  request with a unique key. The insert is in the same transaction that makes
  the temporary administrator. A second request with the same nonce fails the
  insert. The transaction fails with it."
- Quote: "The database holds the counter of the rate limit, so all instances
  share it. An IPv6 address counts as its /64 group."
- Quote: "Every refusal gives the same answer: the same status, the same body
  and the same shape."
- Quote: "It uses an ephemeral public key that the CLI sends in the request."
- Quote: "The server keeps a nonce until the not-after time of its request
  plus 1 hour."
- Quote: "The route ships only after the store keeps the first-boot recovery
  mode and the key."
- Effect: a temporary administrator and an encrypted token, not a console
  session. One answer. The nonce kept until `notAfter` plus 1 hour. The rate
  limit counter in the database. The stored mode and key in this change.
- Conflict: none. The draft of this change conflicted with decisions 1, 4, 5
  and 7 (a console session, three refusal codes, nonces kept 7 days, a counter
  in memory). This version follows them. The resend path does not conflict
  with decision 3: a second request with the same nonce still fails the
  insert, and the transaction still fails and creates nothing. The resend is a
  separate read after that failed transaction, and it returns bytes that the
  server already stored.

**ADR 245: Host recovery: database access, recovery modes and break-glass
controls** (decisions of the product manager and fixes of the review).

- Decisions that apply: 3, 4, 6, 7, 8, 10, 12, 13 (current).
- Changed by: ADR 295 changes decision 12. ADR 372 changes decision 3. Chain
  to the end: ADR 295, ADR 372.
- Current rule: this ADR, with ADR 295 and ADR 372.
- Quote: "`--recovery-mode` must match the first-boot choice of ADR 57. When
  that choice is stored, a later change makes the stored choice win over the
  setting."
- Quote: "A temporary administrator cannot start a console session through an
  identity provider."
- Quote: "A record and its token end at the same time: 24 hours after the
  create, or the maximum of the token policy when that is shorter."
- Effect: the route reaches the host issuer through `internal/recovery`. The
  test of decision 12 names it as the one other caller (ADR 295 decision 1).
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business
day** (not owner-tagged).

- Decisions that apply: 1, 2, 3 (changed by ADR 372).
- Changed by: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 372. Current
  rule: this ADR, with ADR 149, 210, 222, 227, 297 and 372.
- Quote: "A durable consumer of the notification opens the review with
  `Store.Open`."
- Effect: the transaction of the recovery writes `break-glass.used`, and the
  consumer opens the review.
- Conflict: none.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not
owner-tagged).

- Decisions that apply: 6 (changed by ADR 372), 9 (current).
- Quote: "What MFA covers. Console sign-in, the creation of access tokens, and
  the reveal of a secret value (ADR 41)."
- Quote: "An administrator can reset the MFA of a user. Each reset writes an
  audit entry (ADR 41)."
- Effect: the route asks for no factor (ADR 372 decision 5). A reset by a
  temporary administrator writes its audit entry.
- Conflict: none.

**ADR 227: One fresh-authentication window** (decisions 2 and 3 owner).

- Decisions that apply: 2, 6 (current).
- Quote: "An access token never gives a fresh authentication."
- Effect: the token of a temporary administrator gives no fresh
  authentication. The two steps of ADR 372 decision 6 do not ask for one from
  it. Every other fresh step refuses it.
- Conflict: none.

**ADR 285 and ADR 301** (ADR 285 decision 17 and ADR 301 decision 14 are
decisions of the product manager. ADR 301 decision 15 is owner-tagged).

- Changed by: ADR 372 changes ADR 285 decision 17 and ADR 301 decision 14.
- Quote (ADR 301 decision 14): "An issue has the checks of an MFA reset (ADR
  285 decision 17): a fresh step of the issuer with their own factor, a
  reason, and a reach that covers the person."
- Quote (ADR 301 decision 15, owner): "A change needs a fresh step with an
  existing factor, or an administrator with the checks of an MFA reset."
- Effect: the reason and the reach stay for a temporary administrator. The
  change of a verified email address of decision 15 keeps the fresh step, so a
  temporary administrator cannot do it.
- Conflict: none.

**ADR 57: Break-glass recovery when the last administrator loses every
factor** (not owner-tagged).

- Decisions that apply: 1, 2, 5, 6.
- Changed by: ADR 70, ADR 245, ADR 372.
- Quote: "Every break-glass use writes an audit entry. This applies to
  bootstrap recovery and signed-request recovery. The entry holds the mode,
  the time and the source of the request."
- Quote: "The break-glass setting and each recovery step have a REST API and
  an action (ADR 17), except for the token that the server reads before it
  starts."
- Effect: an audit entry for each use and each refusal, and a route and an
  action for each step. The use entry holds the mode, the time and the client
  address, with the action `recovery:use-signed-request`.
- Conflict: none.

**ADR 39: Commands never touch the database** (decisions 1 and 4).

- Changed by: ADR 237, ADR 238, ADR 245.
- Quote: "Every other command is a client of the REST API."
- Effect: the `recovery` commands are offline tools or REST clients. None
  opens the database. RFC 130 named `opendustpan host recovery-status` as the
  help of a refusal. That command would have to open the database, so this
  change uses the offline `recovery verify` and the logs instead.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which review kind, scope and opt-in rule? ADR 372 decisions 2 and 3.
- Does the route ask for an MFA step? No, ADR 372 decision 5.
- A console session or a token? A token encrypted to the CLI (ADR 295
  decisions 1 and 6). No console session (ADR 245 decision 4).
- How long does the server keep a nonce? Until `notAfter` plus 1 hour (ADR 295
  decision 7).
- Where is the rate limit counter? In the database, for each source address,
  with an IPv6 address counted as its /64 group (ADR 295 decision 4). The limit
  is 5 requests in 15 minutes for each source, and 30 in 15 minutes for the
  installation. The server counts the per-source attempt and refuses it before
  the signature check. It counts the installation attempt after the signature
  check, so a flood of unsigned or badly signed requests cannot block recovery
  through the installation counter. The per-source counter can still lock out
  an operator who shares a source group with the caller (see the Risks item of
  design.md).
- What are the format, the lifetime, the rotation and the rule for a second
  recovery? The four decisions of the product manager above.
- Why is a copied signed request useless? The request holds the HPKE public
  key of the CLI that made it. A copy without `request.key` gives an answer
  that nobody else can decrypt. Before `notAfter` the same request gets the
  stored sealed answer again and creates nothing new; after `notAfter` the
  server refuses it with `nonce-used`.
- What if the locked-out person is a local user who also lost the password?
  The password reset code of ADR 285 decision 17 keeps its fresh step,
  because ADR 372 decision 6 names only the factor reset and the enrollment
  code. The docs page says to make a new local user in that case.
- Does the review store exist? Yes, in both dialects (see "What main already
  does"), so ADR 372 decision 4 holds.
- Where does the installation name come from? The setting `--installation`
  (ADR 248). It is `local` by default, and the server never makes it at
  random. The request binds to it and to the audience, the setting
  `--public-url` (the external URL of the server).
- Does a key add wait 24 hours, as the proposal of RFC 21 for its question O8
  said? No. RFC 21 is not accepted for O8, and no ADR asks for a wait. The add
  needs a fresh authentication and a reason, and it sends a notice to every
  site administrator, so a second administrator sees it.
- Adversarial second pass: not run on this version. Task 0.2 runs it with a
  model of another provider before the first test. Each answer goes into this
  proposal, answered or as an open question.
