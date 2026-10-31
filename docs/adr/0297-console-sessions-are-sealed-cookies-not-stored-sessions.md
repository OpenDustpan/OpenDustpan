# 297. Console sessions are sealed cookies, not stored sessions

Status: accepted · 2026-10-31. The project owner set the direction on 31 October 2026. The design details are decisions of the PM under the owner's delegation, and the owner accepts them with the merge. One question is held for the owner: where the short state of a sign-in lives. The "Changes" list names the ADRs that this ADR changes. Answers open question 1 of the RFC 114 draft

Changes:

- Changes ADR 69 decision 4: the idle limit and the maximum length stay. The server checks them from sealed cookies at each request and at each refresh, not from a stored session.
- Changes ADR 55 decision 9: a SCIM or LDAP delete disables the person. No session is kept for review, because the server keeps no session.
- Changes ADR 70 decision 26: the server marks a break-glass account "used, not reviewed" at the sign-in. The review reads the audit entries of the sign-in ID.
- Changes ADR 120 decisions 13 and 14: a run that a console session starts resolves the sign-in that it records, not a stored session.
- Changes ADR 285 decisions 5, 17, 19 and 20: "end every session" becomes a bump of the valid-after time. A good second factor issues cookies, not a stored session.

## Context

On 31 October 2026 the project owner wrote:

> I don't want session stored in the backend.

> We don't store user tokens on the backend.

> How most tools get around this is having VERY tight token lifetimes with
> long lived refresh tokens which can't do shit.

OpenDustpan has no users, no installations and no data. This design replaces
the stored sessions with no migration and no compatibility path (ADR 73).

Facts on `main` at e804779:

- `pkg/session` stores each console session as a row: the SHA-256 digest of
  a 256-bit secret, the principal, the provider, the times, the source IP and
  the user agent (`session.Store`). The cookie
  `__Host-dustpan_session` holds the secret. Each request reads the row,
  its owner and the policies in one read (`GetSessionByDigest`), and records
  the last use at most once a minute (`TouchSession`).
- The CSRF value is an HMAC of the session secret (`session.CSRFValue`).
  `internal/sessionauth` checks the `Origin` header or `Sec-Fetch-Site`, and
  the header `X-Dustpan-Csrf` on each method that can change state.
- ADR 69 decision 4: a console session ends after 15 minutes with no
  activity, and after 12 hours in all. PCI DSS v4.0.1 requirement 8.2.8 gives
  the 15 minutes.
- ADR 227: one fresh-authentication window, `freshAuthSeconds`, 900 seconds
  by default.
- ADR 285: a password change ends every other session of the person. A reset
  ends every session. A good second factor turns a pending sign-in into a
  stored session in one transaction (`mfa.PendingStore.CompletePending`).
- ADR 120 decision 13: a run of Dusty resolves "the console session" of its
  person by its ID before each step and each tool call.
- ADR 217: the key-encryption key wraps the data keys of `@server`. "The
  server never derives one key from another key."
- The RFC 114 draft (open pull request) asks where the token of a Git host
  user lives. It recommends a sealed copy in the session record. The owner's
  direction above rules out that option.

The owner's direction has three parts. No session record on the server. No
user token of a Git host on the server. A short-lived credential for each
request, and a long-lived credential that can only make a new short-lived
credential.

## Decision

### The two cookies

1. **The access cookie.** After a sign-in, the server sets the cookie
   `__Host-dustpan_access`. Its attributes are `Secure`, `HttpOnly`,
   `SameSite=Strict` and `Path=/`, with no `Domain`. Its value is sealed
   (decision 9). It holds:
   - the format version and the ID of the sealing key version,
   - the principal ID,
   - the sign-in ID (decision 4),
   - the sign-in time, the authentication time at the identity provider, and
     the provider,
   - the time of the last MFA step and the time of the last fresh
     authentication (ADR 227), each zero when none,
   - the idle deadline and the absolute deadline of the session,
   - the thumbprint of the refresh key (decision 6),
   - the CSRF value (decision 15),
   - the time of issue and the expiry of the cookie.

   The expiry is the time of issue plus 5 minutes, and never later than the
   idle deadline or the absolute deadline. `Max-Age` is the same.
2. **Each request checks the access cookie with no session read.** The check
   opens the seal, checks the kind, the version, the key ID and the expiry,
   and then reads the principal. That read is the read that authorization
   already does: it returns the principal, its kind, its disable time, its
   valid-after time (decision 12), and the session policy
   (`session.PrincipalStateReader`). The request fails closed with `401`
   when:
   - the principal does not exist, is not a person, or is disabled,
   - the sign-in time is not after the valid-after time,
   - the idle deadline or the absolute deadline under the current policy has
     passed,
   - the subject is a dev subject on a server that is not of the kind `dev`
     (ADR 252 decision 7), or is the namespace service principal.

   No request other than the refresh extends a deadline.
3. **The refresh cookie.** The server sets the cookie
   `__Secure-dustpan_refresh` with `Secure`, `HttpOnly`, `SameSite=Strict`,
   no `Domain`, and `Path=/api/server/session/refresh`. Its value is sealed.
   It holds the principal ID, the sign-in ID, the sign-in time, the
   authentication time and provider, the MFA and fresh-authentication times,
   the idle deadline, the absolute deadline, the thumbprint of the refresh
   key, and the CSRF value. `Max-Age` ends at the absolute deadline. The
   refresh route is the only route that opens it. It can make an access
   cookie and a new refresh cookie, and nothing else.

   The prefix `__Host-` needs `Path=/`, so the browser would send the refresh
   cookie with each request. The prefix `__Secure-` with a narrow path sends
   it to the refresh route only, so a log or a proxy of each other route
   never sees it. The cost: a host under the same parent domain can set a
   cookie with the same name. Such a cookie is bound to a key that the
   browser of the victim does not hold, so the refresh fails (decision 7).
   It can stop a refresh. It cannot take over a session.

### The sign-in ID

4. **A sign-in ID names one sign-in.** At each sign-in, the server makes a
   random ID with the prefix `sin-`. Both cookies carry it. The server does
   not store it as a session. It is in each audit entry of the sign-in, the
   refreshes that fail, the sign-out and each request of the session. It
   binds the short-lived records that bind a session today: a step-up
   challenge (`stepup.Holder`), a WebAuthn ceremony (`mfa.Challenge`) and a
   run of Dusty. The sign-in ID cannot authenticate a request.

### Refresh

5. **The refresh rules.** `POST /api/server/session/refresh` takes the
   refresh cookie and a proof (decision 7). It needs no access cookie. In
   this order, it refuses, sets no cookie and writes a refusal entry
   (decision 21) when:
   1. the request does not come from an allowed origin (decision 15),
   2. the cookie is missing, does not open, is of another kind, or names a
      key version that the ring does not hold,
   3. the proof is missing or not valid, or its key does not have the
      thumbprint of the cookie,
   4. the principal does not exist, is not a person, or is disabled,
   5. the sign-in time is not after the valid-after time,
   6. the idle deadline has passed,
   7. the absolute deadline, or the sign-in time plus the current maximum
      length, has passed,
   8. the subject is refused on this server (decision 2).

   Otherwise it sets a new access cookie and a new refresh cookie. The new
   idle deadline is now plus the idle limit. The absolute deadline never
   moves. The other fields stay. The earlier refresh cookie stays valid
   until its own idle deadline, because the server keeps no list of used
   cookies.

   The idle limit stays 15 minutes (ADR 69 decision 4), and the maximum
   length stays 12 hours. A person who works with no pause makes a refresh
   about each 5 minutes. A refresh measures the activity, so the session can
   end between 10 and 15 minutes after the last request. The idle limit is
   never longer than the setting. The console makes a refresh only when it
   must send a request and its access cookie expired or will expire in less
   than 30 seconds. It never makes a refresh on a timer.

   Benchmark: OWASP Session Management Cheat Sheet (see "Benchmarks"):
   idle timeouts of 15 to 30 minutes for most applications, 2 to 5 minutes
   for high-value applications, and an absolute timeout.

### The refresh key and its proof

6. **The console makes a key at the sign-in.** Before it starts a sign-in,
   the console makes an ECDSA P-256 key pair with WebCrypto, with
   `extractable` set to false, and keeps it in IndexedDB. The start of the
   sign-in sends the public key in a proof. The sign-in request
   (`session.Request`) keeps the thumbprint of the public key (RFC 7638,
   SHA-256) until the callback. Both cookies carry the thumbprint.
7. **Each refresh carries a proof.** The proof has the shape of a DPoP proof
   (RFC 9449): a JWS with `typ` `dpop+jwt`, `alg` `ES256`, and the public key
   in the `jwk` header. The claims are `htm` (`POST`), `htu` (the public URL
   of the refresh route, with no query and no fragment), `iat`, and `jti`
   (at least 128 random bits). It goes in the `DPoP` header. The server
   checks:
   - the form, the type, the algorithm (only `ES256`), and that the `jwk`
     header holds a public key and no private part,
   - the signature,
   - that `htm` and `htu` are the method and the public URL of this route,
   - that `iat` is not more than 25 seconds old and not more than 5 seconds
     in the future: a window of 30 seconds,
   - that the thumbprint of the key is the thumbprint of the cookie, in
     constant time.

   The server keeps no list of `jti` values.

   **Accepted risk.** A person who copies a refresh cookie and one proof, for
   example from a log of a proxy that ends TLS, can use them again within
   30 seconds. That gives an access cookie of 5 minutes and a refresh cookie
   that needs the key again. The risk stays inside 5 minutes and 30 seconds.
   A list of `jti` values would need a shared store, which is state on the
   server that the owner does not want.

   **What the key stops, and what it does not stop.** A non-extractable
   key stops a script from reading the private key. A copy of the cookies
   alone cannot make a refresh on another device. The key does not stop a
   script that runs in the console (XSS): that script can sign a proof while
   it runs. It might not stop malware that copies the whole profile of the
   browser from the disk, because a browser can keep the key in its profile
   files. A key in hardware (decision 8) closes that gap.

   Benchmark: RFC 9449 section 11.1 (read): servers "MUST only accept DPoP
   proofs for a limited time after their creation", and "servers can store
   the jti value". A single-use check "may not always be feasible in
   practice, e.g., when multiple servers behind a single endpoint have no
   shared state." Section 5: the refresh token of a public client "MUST be
   bound to the respective public key". Section 2: "If the private key is
   non-extractable (as is possible with [W3C.WebCryptoAPI]), DPoP renders
   exfiltrated tokens alone unusable." Section 11.4: an attacker can still
   make proofs while the client is online.
8. **Device-bound session credentials are a later option.** The draft
   Device Bound Session Credentials (DBSC) of the W3C binds a session cookie
   to a key in the hardware of the device, and the browser makes the proof.
   This design does not depend on it. When the browsers that the console
   supports ship it, a later ADR can add it next to the WebCrypto key. The
   status on 31 October 2026 is in "Benchmarks".

### The seal and its keys

9. **The seal.** Each cookie value is `v1.<key ID>.<base64url of nonce and
   ciphertext>`. The seal is AES-256-GCM from the Go standard library, with
   a random 96-bit nonce. The associated data holds the format version, the
   name of the cookie and the key ID, so a value of one cookie never opens
   as another cookie. The plaintext is JSON that a strict decoder reads, at
   most 2048 bytes. A value of more than 4096 bytes is refused before the
   open.
10. **The session key ring.** A session key version is a random 256-bit key,
    with an ID with the prefix `sk-`. The key-encryption key wraps each
    version (ADR 217), and SQL keeps the wrapped version
    (`session.KeyStore`). So each replica opens each cookie. No key comes
    from another key, as ADR 217 says. The newest version seals. Each
    version that is not retired opens.
11. **Rotation.** The server adds a new version each 30 days. A version
    stops sealing when a newer version exists, and the server retires it
    after the longest maximum length that the setting allows. The period is
    a setting (`session.keyRotationDays`), and the API shows its effective
    value. An administrator with `installation:manage-sign-in` and a fresh
    authentication can rotate at once. The option `retireAll` retires each
    earlier version at the same time, so each session of each person ends.
    Each change writes an audit entry. A backup holds no session key
    version, and a restore makes a new ring. So a restore ends every
    session, and a cookie of another installation never opens.

### Revocation without sessions

12. **One valid-after time for each person.** Each principal has a
    valid-after time, zero by default. A cookie with a sign-in time at or
    before it does not authenticate, and does not refresh. The server sets
    it to now (to the later value, never back) in the same transaction as
    the audit entry of the cause, when:
    - the person signs out everywhere (`POST /api/server/session/sign-out-everywhere`),
    - an administrator ends the sessions of a person
      (`POST /api/server/principals/{id}/end-sessions`, with
      `principal:manage`, a reason and a fresh authentication),
    - the principal is disabled, a password is set, changed or reset, or a
      factor is added, deleted or reset (ADR 285 decisions 17, 19 and 20),
    - a SCIM or LDAP delete disables the person (ADR 55 decision 9).

    When a change keeps the session of the change (ADR 285 decision 19), the
    answer to the change sets new cookies with a sign-in time after the new
    valid-after time and the same sign-in ID. The server never issues a
    sign-in time at or before the stored valid-after time. A replica with a
    clock behind the others uses the valid-after time plus 1 millisecond.
13. **What the design loses.** These are the costs, and the owner accepts
    them with this ADR:
    - No list of the live sessions of a person. The console shows the recent
      sign-ins of the person from the audit log: time, provider, source and
      user agent. The list cannot show which sign-in is still live.
    - No end of one session. A person or an administrator ends all sessions
      of a person, or none.
    - A sign-out ends the session in one browser only (decision 16). A copy
      of its access cookie stays valid until its expiry.
    - The bound on a stolen access cookie: it works until its expiry (at
      most 5 minutes) or until the next bump of the valid-after time, which
      the next request sees. It cannot make a refresh.
    - The bound on a stolen refresh cookie: it does nothing without the
      private key (decision 7).

### The token of a Git host user

14. **The token of a Git host user is a sealed cookie.** When a person
    authorizes the OpenDustpan app on a Git host in a session (the RFC 114
    draft), the server seals the user token into the cookie
    `__Host-dustpan_forge_<connection ID>`, with the attributes of the
    access cookie. The cookie holds the token, the connection ID, the Git
    host user ID, the sign-in ID, the sign-in time and the expiry. The
    server:
    - never writes the token to the database, a log, an audit entry, a
      backup, a runner or an error. The type of the token redacts it in
      each formatted output, as `session.Secret` does,
    - uses it only inside the request of the action of the person, and only
      when the access cookie of the same request has the same sign-in ID and
      authenticates,
    - sets `Max-Age` to the earlier of the expiry of the token and the
      absolute deadline,
    - discards the refresh token of the Git host at once, and never refreshes
      the user token. When it expires, the person authorizes again,
    - refuses a cookie whose sign-in time is at or before the valid-after
      time, and clears each forge cookie at the sign-out,
    - accepts at most 4 forge cookies, one for each Git host connection.

### Requests from the browser

15. **CSRF and origin.** The CSRF value is 256 random bits that the sign-in
    makes. Both cookies carry it. `GET /api/server/session` returns it with
    the deadlines, the MFA and fresh-authentication times, and the sign-in
    ID. A request with a method that can change state sends it in
    `X-Dustpan-Csrf`. The server compares it in constant time with the value
    in the access cookie. The checks of `Origin` and `Sec-Fetch-Site` of
    `internal/sessionauth` stay, and the refresh route uses them too. Another
    site cannot read the value, because the cookies are `HttpOnly`, sealed,
    and `SameSite=Strict`.
16. **Sign-out.** `POST /api/server/session/sign-out` clears the access
    cookie, the refresh cookie and each forge cookie, answers
    `Clear-Site-Data: "cookies", "storage"`, and writes an audit entry. The
    console also deletes the refresh key. The sign-out also ends each run of
    Dusty that the sign-in ID started (decision 19). A sign-out does not
    bump the valid-after time, so the sessions of the person on other
    devices stay. "Sign out everywhere" bumps it.
17. **Many tabs.** The tabs of one browser share the cookies and the key.
    Two tabs that refresh at the same time both succeed. The browser keeps
    the last cookies that it receives, and both are valid. The console can
    use a Web Lock to make one refresh for all tabs. Nothing on the server
    depends on it.

### Other clients and sign-ins

18. **The CLI and API clients.** They keep their access tokens (ADR 53),
    with no change. An access token never gives a fresh authentication
    (ADR 227 decision 6).
19. **Runs of Dusty.** A run that a console request starts records the
    principal, the sign-in ID, the sign-in time and the absolute deadline,
    which are not a credential. Each resolve (ADR 120 decision 13) checks
    that the principal is enabled and a person, that the sign-in time is
    after its valid-after time, and that the absolute deadline has not
    passed. The run ends with `credential-ended` at the absolute deadline,
    at a bump, or at a sign-out of the same sign-in ID. A run does not see
    the idle limit, because no refresh reaches it.
20. **Sign-in paths.**
    - **MFA.** A pending sign-in stays a short record in SQL, because it
      must count wrong factors (ADR 285 decision 20). The owner decides this
      point (the held question below). It holds no credential
      that authenticates a request. A good factor deletes it and writes the
      audit entry in one transaction, and the answer sets the cookies.
    - **Fresh authentication (ADR 227).** A good fresh step sets a new access
      cookie and a new refresh cookie with the new time. The window and the
      error `mfa.fresh-step-required` do not change.
    - **The demo person (ADR 285 decision 24).** The demo sign-in sets the
      same cookies. A server that is not of the kind `dev` refuses the
      cookies of a dev subject at each request and at each refresh. Its
      session key ring is not in a backup, so a cookie of a dev installation
      never opens on another installation.
    - **Break-glass (ADR 57, ADR 70 decisions 25 and 26, ADR 245).** The
      absolute deadline of a break-glass sign-in is the time limit of the
      elevated session. The server marks the account "used, not reviewed"
      at the sign-in. The review reads the audit entries of the sign-in ID.
      A temporary administrator (ADR 245) still has no console session.

### Audit

21. **The audit entries.** Each entry has the sign-in ID and never a cookie
    value, a proof, a token or a key.
    - `session:sign-in`: as today, with the sign-in ID, the provider, the
      factors, and the deadlines.
    - `session:refresh-refused`: the reason (one of the steps of decision 5),
      the sign-in ID when the cookie opened, the source and the user agent.
      The server writes at most one entry for each source address and reason
      in a minute, and counts the others in the next entry.
    - `session:sign-out`, `session:sign-out-everywhere`, and
      `session:ended-by-administrator`, with the reason.
    - `session:valid-after-bumped`: the principal, the new time and the
      cause (the audit entry of the change that bumped it).
    - `session:key-added`, `session:key-retired`, `session:keys-rotated`.
    - A refresh that succeeds writes no entry. It updates the activity time
      of the principal for the inactivity disable (ADR 53 decision 4), at
      most once an hour.

### Defense in depth

22. **The layers for each input.** Each cell is a layer that stops the
    attack alone. Each layer has its own test, with hostile input sent
    straight to it (the skipped cases of `sessiontest`).

    | Input | Check at the boundary | Encode or contain | Limit | Authorize at use | Fail closed and record |
    | --- | --- | --- | --- | --- | --- |
    | Access cookie | length at most 4096, form `v1.<key ID>.<base64url>`, key ID pattern, strict JSON, kind | AEAD with the cookie name and the key ID in the associated data. the value never reaches a log | 5-minute expiry. idle and absolute deadlines. `HttpOnly`, `SameSite=Strict`, `__Host-` | principal read at each request: person, enabled, valid-after, policy, dev subject | any error is `401`. a refused dev subject writes a denial entry |
    | Refresh cookie | as the access cookie, kind `refresh` | AEAD. `Path` of the refresh route only | opens only at the refresh route. makes only cookies | the same principal read as the access cookie, and the proof | each refusal writes `session:refresh-refused` |
    | Proof | at most 2048 bytes, compact JWS, `typ`, `alg` `ES256` only, a public `jwk` with no private part | the thumbprint compared in constant time. the proof never reaches a log | window of 30 seconds. `htm` and `htu` of the route | the key thumbprint of the cookie | a bad proof refuses the refresh and writes the entry |
    | Refresh request | `POST` only, no body, allowed `Origin` or `Sec-Fetch-Site: same-origin` | no request value reaches a header or a log that holds CR or LF | a source budget in `internal/authgate` | the steps of decision 5, in order | as above |
    | Forge cookie | as the access cookie, kind `forge`, a known connection ID | AEAD with the connection ID in the associated data. the token type redacts itself | expiry of the token or the absolute deadline. at most 4 | the access cookie of the same request has the same sign-in ID. then the Git host checks the person | a refused cookie is cleared and the action is off with the reason |
    | Session key ring | wrapped versions only. key ID pattern | the key-encryption key wraps each version. the key never leaves memory | rotation each 30 days. retire after the longest maximum length | only the sign-in service opens a version | a version that does not unwrap stops the start of the server |

23. **No session in the database or a backup.** A test signs a person in,
    makes a refresh, authorizes a Git host, and makes a fresh step. It then
    reads each table of the database and the backup archive, and fails
    when it finds a cookie value, a part of a cookie value, a token of the
    Git host, an unwrapped session key, or a row with a sign-in ID other
    than an audit entry or a short-lived record of decision 4.

### Benchmarks

The research of 31 October 2026 read these sources. The project does not
claim compliance with any of them. The quotes come from the pages as read
that day.

- **OWASP Session Management Cheat Sheet.** "Common idle timeouts ranges
  are 2-5 minutes for high-value applications and 15-30 minutes for low
  risk applications." "All sessions should implement an absolute timeout,
  regardless of session activity." "Session timeout management and
  expiration must be enforced server-side." This design enforces each
  deadline on the server, from the sealed value, at each request and at
  each refresh. It recommends `SameSite=Strict`, and `__Host-` for session
  IDs. It asks for a new session ID "after any privilege level change":
  a fresh step sets new cookies (decision 20).
- **OWASP Cookie Theft Mitigation Cheat Sheet.** Not published. The
  research found only an open proposal for a "Cookie Theft Detection Cheat
  Sheet" in the OWASP repository. Nothing in this ADR depends on it.
- **OWASP JSON Web Token Cheat Sheet.** A deny list of revoked tokens is
  "a workaround for the 'stateless session' invalidation problem", and then
  "user sessions won't be completely stateless anymore". It lists a short
  expiry and sender-constrained tokens, which "limit an attacker's ability
  to use a stolen token without its associated private key". This design
  uses a short expiry, a key-bound refresh, and one valid-after time for
  each person in place of a deny list.
- **NIST SP 800-63B-4** (26 August 2025). At AAL3, the overall timeout
  "SHALL be no more than 12 hours" and the inactivity timeout "SHOULD be no
  more than 15 minutes". Cookies "SHOULD have the "__Host-" prefix and set
  "Path=/"", and cookie expiry "SHALL NOT be relied upon to enforce session
  timeouts". Two points do not match this design, and the owner must know
  them:
  - Cookies "SHOULD contain only an opaque string" and "SHALL NOT contain
    cleartext personal information". The sealed value is opaque to the
    client and holds no cleartext. It is not a random string.
  - Session secrets end at a sign-out (section 5.1). A sign-out
    here clears the cookies in the browser, but a copy of the access cookie
    stays valid for at most 5 minutes (open point 1).
  The refresh cookie uses `__Secure-` and a narrow path, not `__Host-`
  (decision 3).
- **RFC 9449 (DPoP).** See decision 7.
- **Rails `CookieStore`.** Cookies "will be encrypted using your
  application's `secret_key_base`", with a limit of 4096 bytes. The security
  guide warns that a person can put back an old cookie, and that "changing
  `secret_key_base` will expire currently active sessions". The valid-after
  time and `retireAll` (decision 11) are the answers here.
- **Django `signed_cookies`.** "The session data is signed but not
  encrypted", and "cookie-based sessions are not invalidated when a user
  logs out". This design encrypts the value, and the valid-after time ends
  a copy at "sign out everywhere".
- **Device Bound Session Credentials.** A W3C WebAppSec draft (editor's
  draft of 8 September 2026). Chrome shipped it for Windows with keys in a
  TPM (Google blog, 9 April 2026), with macOS announced. The Chrome Status
  entry shows no Android or iOS support. Mozilla's standards position is
  negative (closed 5 August 2026). WebKit has no position. So the console
  cannot depend on it (decision 8).
- **Practice of hosted services, from their documentation.** Microsoft Entra
  issues access tokens of 60 to 90 minutes by default, and with Continuous
  Access Evaluation tokens of up to 28 hours that it revokes "in near real
  time" at a disable or a password change. Auth0 rotates refresh tokens and,
  at a reuse, "immediately invalidates the refresh token family". Its
  default idle lifetime is 30 days. Google Workspace web sessions last 14
  days by default. GitHub sudo mode has "a two-hour session timeout
  period". The access cookie of this design (5 minutes) and its session
  limits (15 minutes idle, 12 hours) are shorter than each of these.

## Consequences

- `pkg/session` loses the session row, `Store.CreateSession`,
  `GetSessionByDigest`, `TouchSession` and `EndSession`, and the tables of
  sessions in each SQL dialect. The sign-in request and the providers stay.
- Each principal gets a valid-after time, in a numbered migration.
- `internal/sessionauth` checks the access cookie and reads the principal
  state. The CSRF value comes from the cookie, not from a secret.
- `stepup.Holder`, `mfa.Challenge` and the runs of Dusty name a sign-in ID,
  not a session ID.
- The page of the sessions of a person becomes the list of recent sign-ins
  from the audit log, with "Sign out everywhere".
- The console must make the refresh key, send the proof, and make a refresh
  only when it sends a request.
- One request costs an AEAD open and one read of the principal, in place of
  one read of the session row and a write at most each minute.

## Decisions of the PM on the open points

The PM decided these points on 31 October 2026, under the owner's
delegation:

1. **A sign-out ends the session in one browser** (decision 16). "Sign out
   everywhere" is a separate action that bumps the valid-after time.
   Benchmark: the sign-out of GitHub and of Google ends the session of one
   browser, and each has a separate action for the other sessions.
2. **A refresh that succeeds writes no audit entry** (decision 21). Each
   refusal writes one. Benchmark: the logs of cloud providers and identity
   providers record sign-ins and failures, not each token refresh.
3. **`github.com/go-jose/go-jose/v4` becomes a direct requirement.** It
   parses and verifies the JWS of the refresh proof (decision 7). The binary
   links it already through `go-oidc`. The project uses no parser of its own
   for a JWS.
4. **A run of Dusty ends at the absolute deadline, at a bump, or at a
   sign-out of its sign-in** (decision 19). It does not follow the idle
   limit, because that needs a record of each refresh.

## Open question for the owner (held)

**Where the short state of a sign-in lives.** Two records hold state between
two requests of one sign-in. Neither can authenticate a request:

- the sign-in request of an identity provider (`session.Request`: the
  state, the nonce, the PKCE verifier and the thumbprint of the refresh
  key), for at most 10 minutes,
- the pending sign-in of MFA (ADR 285 decision 20), which counts the wrong
  factors (5 make it void), for at most 5 minutes.

The options:

- **(a) Short records in SQL, as today.** Each record works once, and a
  job deletes it after its expiry. The count of 5 wrong factors on the
  pending sign-in stays exact. The cost: the database holds a record of a
  sign-in in progress for up to 10 minutes. It is not a session.
- **(b) Sealed cookies.** The sign-in request and the pending sign-in go
  into sealed cookies with the attributes of the access cookie, and the
  server stores nothing for them. The count of wrong factors moves to the
  factor lock of the person that exists already (ADR 285 decision 14: 10
  wrong factors in a row lock the factors of the person for 30 minutes,
  in a count that each instance shares). The costs: a cookie cannot work
  once, so a copy of the sign-in request cookie can start a callback again
  inside its 10 minutes (the identity provider still gives each code once).
  A copy of the pending sign-in cookie can try factors until the factor
  lock of the person stops it, so the limit of 5 for each pending sign-in
  goes away.

**Recommendation: (a).** Neither record is a session or a credential that
authenticates a request. Option (a) keeps "a sign-in request works once"
and the limit of 5 wrong factors for each pending sign-in. Option (b)
loses them both. It keeps the factor count of the person,
which is a record on the server too.

**Plain summary:** the server keeps a few minutes of state while a sign-in
is in progress, and nothing after it.
