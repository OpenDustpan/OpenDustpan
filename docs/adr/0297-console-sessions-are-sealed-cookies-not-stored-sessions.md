# 297. Console sessions are sealed cookies, not stored sessions

Status: accepted · 2026-10-31. The project owner set the direction on 31 October 2026. The design details are decisions of the PM under the owner's delegation, and the owner accepts them with the merge. Two questions are held for the owner: where the short state of a sign-in lives, and an optional cache of proof IDs on each replica. The "Changes" list names the ADRs that this ADR changes

Changes:

- Changes ADR 69 decision 4: the idle limit and the maximum length stay. The server checks them from sealed cookies at each request and at each refresh, not from a stored session.
- Changes ADR 55 decision 9: a SCIM or LDAP delete disables the person. No session is kept for review, because the server keeps no session.
- Changes ADR 71: the audit entry of a fresh authentication names the sign-in ID, not a session ID.
- Changes ADR 120 decisions 13 and 14: a run that a console session starts resolves the sign-in that it records, not a stored session. The run has its own idle deadline.
- Changes ADR 142 decisions 1, 3 and 9: the reference of an `account-session` use is the sign-in ID. The time limit of the use is the absolute deadline of the sign-in. The server fills the kind and the time of the authentication from the access cookie.
- Changes ADR 146 decision 9: the server fills `chatops.Auth` from the access cookie of the request. The CSRF value of the sign-in protects the accept.
- Changes ADR 180 decision 15: the audit entry of a change of preferences names the sign-in ID, not a session ID.
- Extends ADR 240: the credential `session` names a request that an access cookie authenticates.
- Changes ADR 285 decisions 5, 17, 19, 20 and 21: "end every session" becomes a bump of the session generation. A good second factor issues cookies, not a stored session. An enroll-only session is a claim of the cookies.

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
  stored session in one transaction (`mfa.PendingStore.CompletePending`). A
  person with no factor, after the grace, gets a session that can only
  enroll a factor and sign out.
- ADR 120 decision 13: a run of Dusty resolves "the console session" of its
  person by its ID before each step and each tool call.
- ADR 142: the review of a break-glass use opens at the sign-in. The
  reference of a session use is the session ID. ADR 146, ADR 180 and ADR 71
  name a session or a session ID too.
- ADR 217: the key-encryption key wraps the data keys of `@server`. "The
  server never derives one key from another key."
- A design for review requests, in an open pull request, asks where the
  token of a Git host user lives, and proposes a sealed copy in the session
  record. The owner's direction above rules out that option.

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
   - the principal ID and the session generation that the issue read
     (decision 12),
   - the sign-in ID (decision 4),
   - the sign-in time, the authentication time at the identity provider, and
     the provider. The sign-in time never changes for the life of the
     sign-in,
   - the time of the last MFA step, and the time and the kind of the last
     fresh authentication (ADR 227): `passkey`, `mfa` (a password and a
     second factor), `password` (ADR 285 decision 21, a person with no
     factor) or `sso`. Each is zero or empty when none,
   - the restriction of the session: none, or `enroll-only` (ADR 285
     decision 21),
   - the idle deadline and the absolute deadline of the session,
   - the thumbprint of the refresh key (decision 6),
   - the CSRF value (decision 15),
   - the time of issue and the expiry of the cookie.

   The expiry is the time of issue plus 5 minutes, and never later than the
   idle deadline or the absolute deadline. `Max-Age` is the same. The issuer
   computes each deadline and each expiry from its own clock at the issue.
2. **Each request checks the access cookie with no session read.** The check
   opens the seal, checks the kind, the version, the key ID and the expiry,
   and then reads the principal. That read is the read that authorization
   already does: it returns the principal, its kind, its disable time, its
   session generation (decision 12), and the session policy
   (`session.PrincipalStateStore`). The request fails closed with `401`
   when:
   - the principal does not exist, is not a person, or is disabled,
   - the generation in the cookie is not the stored generation,
   - the idle deadline or the absolute deadline under the current policy has
     passed,
   - the subject is a dev subject on a server that is not of the kind `dev`
     (ADR 252 decision 7), or is the namespace service principal.

   A cookie with the restriction `enroll-only` authenticates only the routes
   that enroll a factor, read the session, refresh and sign out. Each other
   route answers `403` with the error code `mfa.enrollment-required`. No
   request other than the refresh extends a deadline.
3. **The refresh cookie.** The server sets the cookie
   `__Secure-dustpan_refresh` with `Secure`, `HttpOnly`, `SameSite=Strict`,
   no `Domain`, and `Path=/api/server/session/`. Its value is sealed. It
   holds the claims of the access cookie, except the time of issue and the
   expiry. `Max-Age` ends at the absolute deadline. Only the refresh route
   and the sign-out route open it (decisions 5 and 16). It can make an
   access cookie and a new refresh cookie, and end the runs of its sign-in,
   and nothing else.

   The prefix `__Host-` needs `Path=/`, so the browser would send the refresh
   cookie with each request. The prefix `__Secure-` with a narrow path sends
   it to the session routes only, so a log or a proxy of each other route
   never sees it. The cost: a host under the same parent domain can set a
   cookie with the same name and path, or fill the cookie store of the
   browser so that it drops the refresh cookie. Such a cookie is bound to a
   key that the browser of the victim does not hold, so the refresh fails
   (decision 7). It can stop a refresh. It cannot take over a session. A
   request with two refresh cookies: the server tries each, in the order of
   the `Cookie` header, and uses the first that opens and matches the proof.
   A request with more than two is refused with the step `cookie`.

### The sign-in ID

4. **A sign-in ID names one sign-in.** At each sign-in, the server makes a
   random ID with the prefix `sin-`. Both cookies carry it. The server does
   not store it as a session. It is in each audit entry of the sign-in, the
   refreshes that fail, the sign-out and each request of the session. It
   takes the place of the session ID wherever an ADR names one:
   - a step-up challenge (`stepup.Holder`) and a WebAuthn ceremony
     (`mfa.Challenge`),
   - a run of Dusty (decision 19),
   - the reference of an `account-session` use of a break-glass review
     (ADR 142),
   - `chatops.Auth` (ADR 146),
   - the audit entries of ADR 71 and of ADR 180 decision 15.

   The sign-in ID cannot authenticate a request.

### Refresh

5. **The refresh rules.** `POST /api/server/session/refresh` takes the
   refresh cookie and a proof (decision 7). It needs no access cookie. In
   this order, it refuses, sets no cookie and writes a refusal entry
   (decision 21) when:
   1. the request does not come from an allowed origin (decision 15),
   2. the cookie is missing, does not open, is of another kind, names a key
      version that the ring does not hold, or comes with more than one other
      cookie of its name,
   3. the proof is missing or not valid, its nonce is missing or old, or its
      key does not have the thumbprint of the cookie,
   4. the principal does not exist, is not a person, or is disabled,
   5. the generation in the cookie is not the stored generation,
   6. the idle deadline has passed,
   7. the absolute deadline, or the sign-in time plus the current maximum
      length, has passed,
   8. the subject is refused on this server (decision 2).

   A refresh with no nonce, or with a nonce that is too old, gets `401` with
   the error `use_dpop_nonce` and a new nonce in the `DPoP-Nonce` header
   (RFC 9449 section 8). This answer is a step of the protocol. It writes no
   refusal entry, and the source budget of `internal/authgate` counts it.

   Otherwise it sets a new access cookie and a new refresh cookie, and sends
   a new nonce. The new idle deadline is now plus the idle limit. The
   absolute deadline never moves. The other claims stay. The earlier refresh
   cookie stays valid until its own idle deadline, because the server keeps
   no list of used cookies.

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

6. **One refresh key for each sign-in, bound at the first step.** Before it
   starts a sign-in, the console makes an ECDSA P-256 key pair with
   WebCrypto, with `extractable` set to false, and keeps it in IndexedDB.
   - **The first request of each sign-in path carries a proof** with a
     server nonce, and the server keeps the thumbprint of its key (RFC 7638,
     SHA-256 over `crv`, `kty`, `x` and `y` only):
     - a sign-in through an identity provider: the start keeps it in the
       sign-in request (`session.Request`) until the callback,
     - a password sign-in: the password request. With MFA, the pending
       sign-in (`mfa.Pending`) keeps it until the second factor,
     - a passkey sign-in: the start of the ceremony keeps it in the WebAuthn
       challenge (`mfa.Challenge`),
     - the demo sign-in (ADR 285 decision 24) and a break-glass sign-in
       (ADR 70): the sign-in request itself.

     The step that issues the first cookies takes the thumbprint from that
     record, never from its own request.
   - **Each route that issues cookies again copies the thumbprint and the
     sign-in ID** from the access cookie or from the record of the sign-in.
     It never reads a key or a thumbprint from the request. These routes
     are the fresh step, a change that keeps the session (decision 12), the
     completion of an enrollment, and the refresh. So a person who holds a
     stolen access cookie and the password cannot bind a key of their own.
   - The audit entry `session:sign-in` records the thumbprint, so that a
     reader can compare the keys of the sign-ins of a person.
7. **Each refresh carries a proof.** The proof has the shape of a DPoP proof
   (RFC 9449): a compact JWS with `typ` `dpop+jwt`, `alg` `ES256`, and the
   public key in the `jwk` header. The claims are `htm` (`POST`), `htu` (the
   public URL of the route, with no query and no fragment), `iat`, `jti` (at
   least 128 random bits) and `nonce`. It goes in the `DPoP` header. The
   server parses it with `github.com/go-jose/go-jose/v4` and checks:
   - the form: a compact JWS only, no JSON serialization, no `crit` header,
     no `b64`, no duplicate key in the header or the claims, at most 2048
     bytes,
   - the type, and the algorithm: only `ES256`, with a raw `r||s` signature
     of 64 bytes,
   - the key: `kty` `EC`, `crv` `P-256`, `x` and `y` of exactly 32 bytes, a
     point on the curve, and no private part. The server ignores `kid`,
     `jku`, `x5u` and `x5c`, and never opens a URL,
   - the signature,
   - that `htm` is `POST`, and that `htu` is the configured public URL of
     the route, compared after the scheme and the host are lower case and
     with no trailing slash added. The `Host` header of the request is never
     used,
   - the nonce (below), and that `iat` is within 24 hours of the time of the
     server,
   - that the thumbprint of the key is the thumbprint of the cookie, in
     constant time.

   **The nonce measures the window, not the browser clock.** A nonce is a
   value that the session key ring seals: the time of the server and 128
   random bits, with the associated data `dpop-nonce`. The server stores no
   nonce. A proof is in its window when the time in its nonce is not more
   than 30 seconds before the time of the server. A browser with a clock
   that is 2 minutes off still refreshes, because the server reads only its
   own time. `iat` must still be within 24 hours, so a proof made on a
   device with a clock far off is refused. The server sends a new nonce with
   each answer of the session routes, and with each `use_dpop_nonce`
   answer.

   The server keeps no list of `jti` values.

   **Accepted risk.** A person who copies a refresh cookie and one proof, for
   example from a log of a proxy that ends TLS, can use them again within
   30 seconds. That gives an access cookie of 5 minutes and a refresh cookie
   that needs the key again. The risk stays inside 5 minutes and 30 seconds.
   A shared list of `jti` values would be state on the server that the owner
   does not want. A cache on each replica is a held question for the owner.

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
   shared state." Section 8 gives the server nonce. Section 5: the refresh
   token of a public client "MUST be bound to the respective public key".
   Section 2: "If the private key is non-extractable (as is possible with
   [W3C.WebCryptoAPI]), DPoP renders exfiltrated tokens alone unusable."
   Section 11.4: an attacker can still make proofs while the client is
   online.
8. **Device-bound session credentials are a later option.** The draft
   Device Bound Session Credentials (DBSC) of the W3C binds a session cookie
   to a key in the hardware of the device, and the browser makes the proof.
   This design does not depend on it. When the browsers that the console
   supports ship it, a later ADR can add it next to the WebCrypto key. The
   status on 31 October 2026 is in "Benchmarks".

### The seal and its keys

9. **The seal.** Each cookie value and each nonce is `v1.<key ID>.<base64url
   of nonce and ciphertext>`. The seal is AES-256-GCM from the Go standard
   library, with a random 96-bit nonce. The associated data holds the format
   version, the name of the cookie (or `dpop-nonce`) and the key ID, and the
   connection ID for a Git host token cookie, so a value of one kind never
   opens as another. The plaintext is JSON that a strict decoder reads, at
   most 2048 bytes. A value of more than 4096 bytes is refused before the
   open.

   NIST SP 800-38D limits a key with random 96-bit nonces to 2^32 seals.
   Each replica counts its seals with each key version in memory. When its
   count passes 2^28, it adds a new version at once (decision 11). So the
   limit holds for up to 16 replicas that each reach the count. The time of
   30 days alone would allow about 1,650 seals each second. An installation
   with more than 16 replicas sets a shorter rotation period.
10. **The session key ring.** A session key version is a random 256-bit key,
    with an ID with the prefix `sk-`. The key-encryption key wraps each
    version (ADR 217), and SQL keeps the wrapped version
    (`session.KeyStore`). So each replica opens each cookie. No key comes
    from another key, as ADR 217 says. Only the sealer opens a version, and
    its keys never leave the memory of the process. A version that does not
    unwrap stops the start of the server.
11. **Rotation in two phases.** Each replica reads the ring again each
    minute (the reload interval). It also reads it once when a cookie names
    a key ID of the right form that it does not hold, at most once each 10
    seconds.
    - **A new version opens first and seals later.** The server adds a new
      version each 30 days, or at once when a replica reaches the seal count
      of decision 9. A new version has a time `SealsFrom` 5 minutes after
      its creation: more than four reload intervals. Until then, each
      replica opens with it and seals with the earlier version. So no
      replica sees a cookie with a key that it did not load.
    - A version stops sealing at the `SealsFrom` of the next version. The
      server retires it after the longest maximum length that the setting
      allows, plus the reload interval.
    - The period is a setting (`session.keyRotationDays`), and the API shows
      its effective value.
    - **Retire all, at once.** An administrator with
      `installation:manage-sign-in`, a reason and a fresh authentication can
      rotate with the option `retireAll`. It adds a version that seals at
      once and retires each earlier version, so each session of each person
      ends. Each replica stops opening the retired versions within one
      reload interval (1 minute). A replica that has not loaded the new
      version reads the ring on the first cookie that names it.
    - Each change writes an audit entry.
    - A backup holds no session key version, and a restore makes a new
      ring. So a restore ends every session, and a cookie of another
      installation never opens.
    - A restore of the database outside the backup feature (a snapshot or a
      restore to a point in time) brings back an earlier ring and earlier
      session generations, so sessions that a bump ended can work again.
      The operator guide says to run `retireAll` after such a restore.

### Revocation without sessions

12. **One session generation for each person.** Each principal has a
    session generation, a count that starts at 1. Each cookie carries the
    generation that the issue read. A cookie authenticates and refreshes
    only while its generation is the stored generation. The server adds 1
    to it, in the same transaction as the audit entry of the cause, when:
    - the person signs out everywhere (`POST /api/server/session/sign-out-everywhere`),
    - an administrator ends the sessions of a person
      (`POST /api/server/principals/{id}/end-sessions`, with
      `principal:manage`, a reason and a fresh authentication),
    - the principal is disabled, a password is set, changed or reset, or a
      factor is added, deleted or reset (ADR 285 decisions 17, 19 and 20),
    - a SCIM or LDAP delete disables the person (ADR 55 decision 9).

    The generation needs no clock, so a replica with a wrong clock cannot
    keep a session alive after a bump. An issue that races a bump reads the
    old generation, and its cookies fail at the next request.

    When a change keeps the session of the change (ADR 285 decision 19), the
    answer to the change sets new cookies with the new generation. The
    sign-in ID, the sign-in time, the deadlines and the thumbprint stay
    (decision 6). Each bump ends each Git host token cookie, including at a
    change that keeps the session (decision 14): the person authorizes the
    Git host again. This is on purpose, because a password or a factor
    change can follow a compromise.
13. **What the design loses.** These are the costs, and the owner accepts
    them with this ADR:
    - No list of the live sessions of a person. The console shows the recent
      sign-ins of the person from the audit log: time, provider, source,
      user agent and key thumbprint. The list cannot show which sign-in is
      still live.
    - No end of one session. A person or an administrator ends all sessions
      of a person, or none.
    - A sign-out ends the session in one browser only (decision 16). A copy
      of its access cookie stays valid until its expiry.
    - The bound on a stolen access cookie: it works until its expiry (at
      most 5 minutes) or until the next bump, which the next request sees.
      It cannot make a refresh, and it cannot bind a new key (decision 6).
    - The bound on a stolen refresh cookie: it does nothing without the
      private key (decision 7).

### The token of a Git host user

14. **The token of a Git host user is a sealed cookie.** When a person
    authorizes the OpenDustpan app on a Git host in a session, the server
    seals the user token into the cookie
    `__Host-dustpan_forge_<connection ID>`, with the attributes of the
    access cookie. The cookie holds the token, the connection ID, the Git
    host user ID, the sign-in ID, the session generation and the expiry.
    The server:
    - never writes the token to the database, a log, an audit entry, a
      backup, a runner or an error. The type of the token redacts it in
      each formatted output, as `session.Secret` does,
    - uses it only inside the request of the action of the person, and only
      when the access cookie of the same request has the same sign-in ID and
      generation and authenticates,
    - when the installation links Git host accounts to principals, checks at
      each use that the Git host user ID of the cookie is the account linked
      to the person. So a person cannot act at the Git host as a shared
      account,
    - sets `Max-Age` to the earlier of the expiry of the token and the
      absolute deadline,
    - discards the refresh token of the Git host at once, and never refreshes
      the user token. When it expires, the person authorizes again,
    - clears a cookie that it refuses, and shows each action that needs the
      token as off, with the reason,
    - clears each forge cookie at the sign-out,
    - accepts at most 4 forge cookies, one for each Git host connection.

### Requests from the browser

15. **CSRF and origin.** The CSRF value is 256 random bits that the sign-in
    makes. Both cookies carry it. `GET /api/server/session` returns it with
    the deadlines, the MFA and fresh-authentication times and kind, the
    restriction and the sign-in ID. A request with a method that can change
    state sends it in `X-Dustpan-Csrf`. The server compares it in constant
    time with the value in the cookie. The checks of `Origin` and
    `Sec-Fetch-Site` of `internal/sessionauth` stay, and the session routes
    use them too. Another site cannot read the value, because the cookies
    are `HttpOnly`, sealed, and `SameSite=Strict`.
16. **Sign-out.** `POST /api/server/session/sign-out` needs the origin check
    and the CSRF value. It accepts an access cookie that opens, also after
    its expiry, or a refresh cookie that opens. It needs no valid
    generation and no deadline, because it only ends things. It:
    - ends each run of Dusty of that sign-in ID (decision 19),
    - expires the access cookie, the refresh cookie and each forge cookie
      with `Max-Age=0` at their own name and path,
    - answers `Clear-Site-Data: "storage"`, so the browser deletes the
      refresh key. It never sends `"cookies"`, because that type clears the
      cookies of other hosts under the same parent domain,
    - writes an audit entry.

    A sign-out does not bump the generation, so the sessions of the person
    on other devices stay. "Sign out everywhere" bumps it.
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
    principal, the sign-in ID, the session generation, the absolute deadline
    and an idle deadline. None of them is a credential. The run record is in
    SQL already, so this adds no session store.
    - A request with an access cookie of the same sign-in ID that reads or
      changes the run (the console reads it while it runs) moves the idle
      deadline of the run to the idle deadline of that cookie, at most once
      a minute.
    - Each resolve (ADR 120 decision 13) checks that the principal is
      enabled and a person, that the generation is the stored generation,
      and that neither deadline has passed.
    - The run ends with `credential-ended` at either deadline, at a bump, or
      at a sign-out of the same sign-in ID (decision 16). So the idle limit
      of ADR 69 also stops the actions of an agent.
20. **Sign-in paths.**
    - **MFA.** A pending sign-in stays a short record in SQL, because it
      must count wrong factors (ADR 285 decision 20). The owner decides this
      point (the held question below). It holds no credential that
      authenticates a request. A good factor deletes it and writes the
      audit entry in one transaction, and the answer sets the cookies with
      the thumbprint of the pending sign-in.
    - **Enroll-only (ADR 285 decision 21).** A person with no factor, after
      the grace, gets cookies with the restriction `enroll-only`. A refresh
      keeps it. The enrollment of a factor bumps the generation (decision 12)
      and sets new cookies with no restriction, with the same sign-in ID and
      thumbprint.
    - **Fresh authentication (ADR 227).** A good fresh step sets a new access
      cookie and a new refresh cookie with the new time and kind. The window
      and the error `mfa.fresh-step-required` do not change. ADR 142 and
      ADR 146 read the kind and the time from the cookie.
    - **The demo person (ADR 285 decision 24).** The demo sign-in sets the
      same cookies. A server that is not of the kind `dev` refuses the
      cookies of a dev subject at each request and at each refresh. Its
      session key ring is not in a backup, so a cookie of a dev installation
      never opens on another installation.
    - **Break-glass (ADR 57, ADR 70 decision 25, ADR 142, ADR 245).** The
      absolute deadline of a break-glass sign-in is the time limit of the
      elevated session. ADR 142 stays: the review record opens at the
      sign-in, its reference is the sign-in ID, and its `Use.Until` is the
      absolute deadline. A temporary administrator (ADR 245) still has no
      console session.

### Audit

21. **The audit entries.** Each entry has the sign-in ID and never a cookie
    value, a proof, a nonce, a token or a key.
    - `session:sign-in`: as today, with the sign-in ID, the provider, the
      factors, the deadlines, the restriction and the key thumbprint.
    - `session:refresh-refused`: the step (one of the steps of decision 5),
      the sign-in ID when the cookie opened, the source and the user agent.
      Each replica writes at most one entry for each source address and step
      in a minute, and counts the others in the next entry. This limit is in
      the memory of each replica. A source that changes its address (for
      example in an IPv6 range) meets the source budget of
      `internal/authgate`, which every instance shares.
    - `session:sign-out`, `session:sign-out-everywhere`, and
      `session:ended-by-administrator`, with the reason.
    - `session:generation-bumped`: the principal, the new generation and the
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
    | Access cookie | length at most 4096, form `v1.<key ID>.<base64url>`, key ID pattern, strict JSON, kind | AEAD with the cookie name and the key ID in the associated data. The value never reaches a log | 5-minute expiry, idle and absolute deadlines, `HttpOnly`, `SameSite=Strict`, `__Host-` | principal read at each request: person, enabled, generation, policy, dev subject, restriction | any error is `401`. A refused dev subject writes a denial entry |
    | Refresh cookie | as the access cookie, kind `refresh`, at most 2 of the name | AEAD. `Path` of the session routes only | opens only at the refresh and the sign-out routes. Makes only cookies | the same principal read as the access cookie, and the proof | each refusal writes `session:refresh-refused` |
    | Proof at a refresh | at most 2048 bytes, compact JWS only, `typ`, `alg` `ES256` only, EC P-256 key on the curve with no private part, no `crit`, no duplicate keys | the thumbprint compared in constant time. `htu` compared with the configured URL, never the `Host` header. The proof never reaches a log | the nonce window of 30 seconds. `iat` within 24 hours. No URL opened | the key thumbprint of the cookie | a bad proof refuses the refresh and writes the entry |
    | Proof and public key at a sign-in | as the proof at a refresh | the thumbprint goes to the short record of the sign-in, never to a log | the nonce window. One key for each sign-in | the step that issues the cookies reads the thumbprint from the record, never from its request | a sign-in with no good proof issues no cookie and writes `sign-in.failed` |
    | Nonce | form and length as a cookie, kind `dpop-nonce` | AEAD with `dpop-nonce` in the associated data | 30 seconds | not applicable: a nonce authorizes nothing | an old nonce gets `use_dpop_nonce`, which the source budget counts |
    | `X-Dustpan-Csrf` header | 43 characters of base64url | compared in constant time with the value in the cookie | one value for each sign-in | the value of the cookie of the same request | a missing or wrong value is `403` |
    | `Origin` and `Sec-Fetch-Site` | allowlist of origins. Exact values of `Sec-Fetch-Site` | never written to a header. Logged with CR and LF taken out | not applicable | a change needs `same-origin` or an allowed origin | a refusal is `403` and a log record |
    | Refresh request | `POST` only, no body | no request value reaches a header or a log with CR or LF | a source budget in `internal/authgate` | the steps of decision 5, in order | as the refresh cookie |
    | Sign-out request | `POST` only, no body, origin and CSRF value | as the refresh request | the source budget | ends only the runs of the sign-in ID of the cookie that opened | an unopened cookie ends nothing and clears the cookies |
    | Body of end-sessions, sign-out-everywhere and rotation | strict JSON, a reason of at most 512 bytes of valid UTF-8, `retireAll` a boolean | the reason goes to the audit entry, never to a header | a fresh authentication. The source budget | `principal:manage` on the principal, or `installation:manage-sign-in`. A person signs out everywhere only for themselves | a refusal writes a denial entry |
    | Session generation and run record read from the database | a generation of at least 1. A run record with a sign-in ID of the right form and deadlines that are not zero | the values are compared, never formatted into SQL | not applicable | the generation of the cookie must equal it. The deadlines of the run must not have passed | an unknown or zero value refuses the request or ends the run |
    | Forge cookie | as the access cookie, kind `forge`, a known connection ID, at most 4 | AEAD with the connection ID in the associated data. The token type redacts itself | expiry of the token or the absolute deadline | the access cookie of the same request has the same sign-in ID and generation. The linked Git host account. Then the Git host checks the person | a refused cookie is cleared and the action is off with the reason |
    | Session key ring | wrapped versions only. Key ID pattern | the key-encryption key wraps each version. The key never leaves the memory of the sealer | two-phase rotation. Retire after the longest maximum length. The seal count | only the sealer opens a version | a version that does not unwrap stops the start of the server |

23. **No session in the database or a backup.** A test signs a person in,
    makes a refresh, authorizes a Git host, and makes a fresh step. It then
    reads each table of the database and the backup archive, and fails
    when it finds a cookie value, a part of a cookie value, a nonce, a token
    of the Git host, an unwrapped session key, or a row with a sign-in ID
    other than an audit entry or a short-lived record of decision 4.

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
  uses a short expiry, a key-bound refresh, and one session generation for
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
  `secret_key_base` will expire currently active sessions". The session
  generation and `retireAll` (decision 11) are the answers here.
- **Django `signed_cookies`.** "The session data is signed but not
  encrypted", and "cookie-based sessions are not invalidated when a user
  logs out". This design encrypts the value, and the session generation ends
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
- Each principal gets a session generation, in a numbered migration.
- `session.Request` and `mfa.Pending` get the thumbprint of the refresh key.
  `mfa.Challenge` gets it for a passkey sign-in.
- The run record of Dusty gets the sign-in ID, the generation and the two
  deadlines.
- `internal/sessionauth` checks the access cookie and reads the principal
  state. The CSRF value comes from the cookie, not from a secret.
- `stepup.Holder`, `mfa.Challenge`, the break-glass review, `chatops.Auth`,
  the audit entries and the runs of Dusty name a sign-in ID, not a session
  ID.
- The page of the sessions of a person becomes the list of recent sign-ins
  from the audit log, with "Sign out everywhere".
- The console must make the refresh key, send the proof with the server
  nonce, and make a refresh only when it sends a request.
- One request costs an AEAD open and one read of the principal, in place of
  one read of the session row and a write at most each minute.
- `go.mod` makes `github.com/go-jose/go-jose/v4` a direct requirement in the
  change that first imports it.

## Decisions of the PM on the open points

The PM decided these points on 31 October 2026, under the owner's
delegation:

1. **A sign-out ends the session in one browser** (decision 16). "Sign out
   everywhere" is a separate action that bumps the session generation.
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

## Open questions for the owner (held)

### 1. Where the short state of a sign-in lives

Two records hold state between
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

### 2. A cache of proof IDs on each replica

**Context.** The server keeps no list of `jti` values (decision 7). A copy
of a refresh cookie and one proof works again inside the window of 30
seconds.

The options:

- **(a) No cache.** The server keeps no state for proofs. The window of 30
  seconds and the key bound a replay to 5 minutes and 30 seconds.
- **(b) A cache in the memory of each replica.** Each replica keeps the
  `jti` values of the last 30 seconds and refuses a second use. It stores no
  session and writes nothing to the database. A replay to another replica
  still passes, so the cache stops most replays and not all of them.

**Recommendation: (a).** It keeps the server free of state for sessions,
which is the direction of the owner. The replay stays inside the window and
still needs the cookie of the person.

**Plain summary:** a captured proof can work for 30 seconds more. A cache
would make that harder, at the cost of state in memory.
