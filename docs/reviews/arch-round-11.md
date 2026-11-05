# Architecture review 11 (519b3bd..b4e1757)

Scope: the 27 commits of trains 55 to 63 on `main`, from 519b3bd (end of
round 10) to b4e1757. Line numbers are at b4e1757. Each finding was checked
against the code before it was written here. No code was changed.

Method: one direct pass over the sign-in methods and `session.Assertion`,
and two read-only passes in parallel (the GitHub forge `Commit` and
`FindCommit`; the other topics). Repowise gave the structure, and `rg` over
`docs/adr` and `docs/rfc` gave the decisions.

## Summary

| # | Severity | Finding | Issue |
| --- | --- | --- | --- |
| 1 | Major | `FindCommit` accepts the job trailer from any author, so a pushed commit can pose as the delivery of a write-back | see PR |
| 2 | Major | The moved-head check of `Commit` fails open for a symlink, a submodule or a directory at a changed path | see PR |
| 3 | Major | The GitHub adapter decides the commit signing and authorship question that ADR 271 leaves open | see PR |
| 4 | Major | The manual lock service keeps the bare configured authorizer, and `TestNewServerAuthenticatorChain` accepts it | see PR |
| 5 | Major | The audit-sink webhook and the notification webhook use two signature algorithms under the same headers and the same `v1=` tag | see PR |
| 6 | Major | ADR 320 decision 7 has no sign-in service: the order lives in two HTTP handlers, and the two copies already differ | see PR |
| 7 | Major | `RunPasswordMethod` tests `localusersvc.PasswordMethod`, which no route of the server calls | see PR |
| 8 to 27 | Minor, info | See "Minor findings" | see PR |

The issue numbers are in the pull request description.

## Major findings

### 1. `FindCommit` trusts the trailer from any author (83e6a4a)

- `pkg/forge/github/commit.go:806-816` returns the first listed commit whose
  last paragraph holds the trailer line. `hasTrailer` (`:915-924`) reads only
  the message text. It does not read the author, the committer, the
  verification or the content.
- The `since` floor (`:902`) uses `commit.committer.date`, which the pusher
  sets.
- ADR 101 decision 3: a takeover that finds the commit records it and makes
  no second commit.
- Effect: a person with push access who knows a job ID (it shows in the API,
  the console and the `dustpan/<id>` branch names) pushes a commit with the
  trailer. A takeover then records that commit as the delivery and drops
  the real change. An honest cherry-pick of an earlier job commit gives the
  same result. The job record is then false ("No magic").
- Fix: read `author.login`, `committer.login` and
  `commit.verification.verified` from the list answer, and accept only a
  commit that the identity of the connection made (the app bot or the token
  user). Or return the identity in `forge.Commit` and let the caller check
  it. Add a conformance case: `Fixture.Push` of a message with the trailer
  gives `ErrNotFound`. The fake must then record the logins.

### 2. The moved-head check fails open for a non-file object (5aebf64)

- `blobAt` (`commit.go:238-251`) turns each `forge.ErrNotFound` into `""`
  ("no file"). `readContents` (`files.go:184-195`) returns `ErrNotFound` for
  a directory and for each `type` other than `file`: `symlink` and
  `submodule` too.
- Effect: the base has no file at a path, and the moved head has a symlink
  or a submodule there; or both have a symlink with other targets. Then
  `before == after == ""`, and `Commit` goes on the new head and overwrites
  the change of the other author. This is the lost-update check of the
  port, and it fails open.
- Fix: tell "absent" apart from "present and not a regular file", and count
  a non-file object as changed. Or read the object IDs of all paths in one
  GraphQL query (`object(expression: "<sha>:<path>") { oid }`, one alias for
  each path), which gives an ID for each object type and also removes the
  call count of minor finding 12. Add a hostile test that serves a symlink
  and a submodule at the head.

### 3. The adapter decides ADR 271 question 4 in code (83e6a4a)

- `docs/adr/0271-the-forge-port-git-host-connections-and-the-installation-level-github-app.md:24-26`:
  question 4 (how the GitHub adapter signs a commit) waits for a signing
  spike, and the ADR does not decide it.
- RFC 12 question 4 (`docs/rfc/0012-api-edits-and-write-back.md`) is an
  owner decision with a recommendation, and RFC 12 is a draft. The
  "mechanics of Commit on GitHub" section of RFC 89 is marked proposed.
  RFC 12 question 5 (follow a moved head) is in the same state.
- The code fixes `createCommitOnBranch`, the app as author and a
  `Co-authored-by` trailer (`commit.go:45-48`, `:562-568`), and follows a
  moved head up to three times (`:142-145`).
- Why it matters: authorship and signing show to each user of a repository.
  CLAUDE.md: an ADR records a decision only after the owner accepts it, and a
  change against an ADR needs an RFC first.
- Fix: get the owner decision on RFC 12 questions 4 and 5, and record it in
  an ADR with a "Changed by" item in ADR 271. Until then, say in the adapter
  doc that it follows the recommendation that waits for a decision.

### 4. The manual lock service keeps the bare authorizer (pre-existing order, documented and tested as correct in this range)

- `pkg/dustpan/dustpan.go:805-809`: "The services that newServices built
  above keep the configured authorizer." `newManualLockService` passes
  `Authorizer: cfg.authorizer` at `dustpan.go:3025`, before `newAuthPorts`
  wraps it. `pkg/manuallock/service.go:343` authorizes lock, unlock and
  manage with that port: no IP allowlist gate, no namespace-service rule.
- `TestNewServerAuthenticatorChain` (`pkg/dustpan/authchain_internal_test.go:175-187`,
  `assertSomeCall`) passes when one call comes through the wrappers. Its
  comment says that not each call does.
- `docs/agents/dustpan-areas.md` says "one authenticator chain" and does not
  name this exception.
- Why it matters: each layer must stop an attack alone. The service layer of
  manual locks does not apply the IP allowlist or the namespace-service
  rule. An embedder's authorizer can widen a namespace-service principal at
  that layer. Related: #2416 item 1 (the prefix check of the same test).
- Fix: build the auth ports before `newServices` (they need only the token
  service), or pass `ports.authorizer` to the manual lock service. Make the
  test assert that each recorded `Authorize` call comes through the whole
  chain, with a named list of the callers that use the bare port on purpose
  (`access.Service`).

### 5. Two webhook signature algorithms share one header set and one version tag (20c498e makes a third copy)

- `pkg/logsink/webhook/webhook.go:138-147` signs `timestamp "." body` with
  one secret.
- `pkg/notify/sign.go:50-64` signs `id "." timestamp "." body`, with one or
  two secrets joined by commas.
- Both send `Dustpan-Timestamp` and `Dustpan-Signature` with the prefix
  `v1=`. The new e2e receiver (`e2e/sinks.go:513-565`) is a third copy of the
  log-sink form.
- Why it matters: one tag names two algorithms. A receiver that checks one
  kind of webhook refuses the other, and the log sink has no secret
  rotation. After the first tag this is a one-way door: receivers in the
  field depend on the form. `internal/releaserecord/shipped.txt` lists no tag
  yet, so the fix is still cheap.
- Fix: before the first tag, one signer and one verifier package for both
  senders (id, timestamp, body, two secrets for rotation), or a different
  tag for the log sink. The e2e receiver calls that verifier, or accepts a
  list of signatures.

### 6. ADR 320 decision 7 has no sign-in service (581c8a5, b4e1757)

- ADR 320 decision 7 gives one order for each method, in "the sign-in
  service": check the Assertion, check the provider, find the principal,
  the steps of `ErrSubjectDisabled` (decision 9), the group claims, the
  built-in factor, the session with the method kind, the factors and the
  time. Decision 11: the audit entry of a refusal records the real reason,
  the provider ID and the method kind.
- No such service exists. The order is in the HTTP adapter twice:
  `internal/api/server/signin.go:420-520` (`callbackIdentity`,
  `startSession`) for the redirect methods, and
  `internal/api/server/localusers.go:365-440` (`acceptLocalAssertion`) for
  the local password.
- The two copies already differ:
  - The local path checks that the Assertion names the expected principal
    (`localusers.go:427-428`). The redirect path does not check that
    `a.ProviderID` is the provider of the route, or that `a.Method` is the
    kind of the provider. It composes the subject from `provider.ID`
    (`signin.go:487`), while `session.CheckFreshSubject` composes it from
    `a.ProviderID` with `SubjectPrefix`.
  - The principal source comes from `SignInProvider.Source`
    (`signin.go:501`), a second value next to `Method.Kind()` that can
    disagree with it. An empty source silently means `oidc`.
  - Every error of `Finish` writes the audit reason "ID token was refused"
    (`signin.go:464`), also for plain OAuth 2.0, which has no ID token, and
    for `ErrReplay`, `ErrRequestBinding` and `ErrMethodUnavailable`. The real
    reason goes only to the application log. The entry holds no method kind.
  - `ErrSubjectDisabled` carries an Assertion so that the service can
    disable the principal (`pkg/session/method.go:38-43`), but the handler
    drops the Assertion on each error.
- Why it matters: LDAP, SAML, CAS and the trusted header each need a route.
  Without one service, each route copies `startSession`, and the copies
  drift further. Decisions 9 and 11 then depend on each handler.
- Fix: one service in `internal/` (for example `signinsvc.Complete(ctx,
  provider, Assertion, methodErr)`) that does decision 7 in order, with the
  cross-check of the provider ID and the kind, the principal source derived
  from the kind, the audit reason from the error class with the method kind,
  and the branch of `ErrSubjectDisabled`. The HTTP adapters only copy
  fields. Give it a hostile test for each step.

### 7. `RunPasswordMethod` tests an adapter that the server does not run (581c8a5)

- `internal/localusersvc/method_test.go:89` runs `RunPasswordMethod` on
  `localusersvc.PasswordMethod`. No non-test code calls
  `Service.PasswordMethod` (`rg 'PasswordMethod\('`).
- The served route calls `h.localUsers.SignIn` (`localusers.go:365`), which
  is `checkPassword` with a nil `BindGate` (`localusersvc/signin.go:48-50`).
  So the boundary check of `checkCredentials`, the bind gate and the error
  translation of `methodError` that the suite proves are not on the served
  path.
- ADR 320 decision 1 and decision 8: the local password is not an adapter
  of these ports; it makes the same Assertion from its own records. The port
  doc (`pkg/session/method.go:340-347`) names LDAP only. The code now has an
  adapter that the ADR says does not exist.
- The port cannot carry what the local path needs: `SignedIn` also holds the
  session generation and `ChangeRequired`, which the session must use. This
  is why the route does not use the port.
- Why it matters: a green suite reads as proof of the served path, but it
  proves a wrapper. The two paths will drift.
- Fix: either route the password sign-in through `PasswordMethod` (and carry
  the generation and the change flag beside the Assertion), or remove the
  adapter and run the cases that apply on the served path through the
  sign-in service of finding 6. Make the ADR and the code say the same thing.

## The Assertion as the base for LDAP, SAML, CAS and the trusted header

Judgment: the three port shapes and the Assertion fit the four methods. No
method needs a new port method. The gaps below are additive fields or
limits, and none is a one-way door before 1.0, but each is cheaper now than
after the first adapter:

- **No handle of the session at the identity provider.** SAML single logout
  needs the `NameID` and the `SessionIndex`, CAS single sign-out needs the
  service ticket, OIDC back-channel logout needs `sid`. The Assertion has no
  field for them. Add an optional `ProviderSession` value (bounded, no
  control character) when the first logout method lands.
- **No upper bound from the provider.** SAML `SessionNotOnOrAfter` limits
  the session. The Assertion cannot carry it. Add an optional "valid until".
- **The subject limit does not fit the principal subject.** An Assertion
  subject may have 255 bytes (`MaxAssertionSubject`), but the principal
  subject `"<provider ID>:<subject>"` has the same 255-byte limit
  (`internal/api/server/signin.go:73`, `pkg/principal/principal.go:127`).
  A valid OIDC `sub` (up to 255 ASCII characters) or a SAML persistent
  `NameID` (up to 256 characters) passes `CheckAssertion` and is refused
  later. Decide one limit and check it once.
- **The group limit may refuse real LDAP groups.** A group of 257 bytes
  refuses the whole sign-in. Active Directory DNs with deep OU paths can be
  longer. Decide whether LDAP returns a DN or a name before the LDAP
  adapter.
- **Five switches list the method kinds.** `checkAssertionMethod`,
  `checkAssertionFactors`, `checkAssertionAuthTime`, `localMethod` and
  `ProviderMode.MethodKind` each list the kinds. One table of the
  properties of each kind (shape, local, factors, time known) removes the
  chance that a new kind is added to four of five.
- **No error for "password expired".** LDAP password policy can report an
  expired password or a forced change. The port has no error for it, and it
  becomes `ErrMethodRejected`. The local path has the same need
  (`ChangeRequired`, finding 7). Decide before the LDAP adapter.
- **A second factor maps to "rejected".** `localusersvc/method.go:103` maps
  `ErrSecondFactorRequired` to `ErrMethodRejected`. Through the port, a
  person with a factor looks like a wrong password. Decision 7 step 6 puts
  the factor in the service, so the method must not ask for it; see finding
  7.
- The trusted header fits: `RequestFacts` carries the peer, the client
  address, the named headers and the checked client chain. `ProviderMode`
  values `saml`, `cas`, `ldap` and `header` are refused by `Validate` and by
  `storedProviders.build` (`pkg/dustpan/storedproviders.go:212-218`) before a
  secret is opened. That is correct and fails closed.

Tracked elsewhere: a plain OAuth 2.0 sign-in starts a session with the
authentication time "now" (`sessionAuthTime`, `signin.go:564`), so a new
sign-in through the provider restarts the fresh window. ADR 301 replaces
this with a built-in factor; #728 holds the work. The guide
(`docs/guides/console-sign-in.md:148-172`) still says that OpenDustpan has
no second factor of its own; update it when ADR 301 lands.

## Minor findings

All go into the issue "Architecture review 11: minor findings".

### Forge

8. **Minor. The compare call can fail for a reason other than ancestry**
   (9c945d2). `requireAncestor` (`commit.go:167-170`) calls the compare API
   with `per_page=1`, but GitHub still sends up to 300 files with patches;
   over `egress.MaxBodyBytes` (4 MiB) the answer is `ErrConflict`. It fails
   closed, but a busy branch can conflict for no real reason. Fix: GraphQL
   `Ref.compare` or the commit history, which send no file list.
9. **Minor. The second validation layer has no test of its own** (83e6a4a).
   `checkCommit` and `checkChanges` (`commit.go:278-304`) repeat
   `forge.ValidateCommit`, but each test goes through `ValidateCommit` first.
   Fix: an internal test that sends hostile input to `checkCommit`,
   `checkChanges`, `validTrailer` and `hasTrailer` directly.
10. **Minor. Two trailer-block rules** (83e6a4a). `hasTrailer` (`:923`)
    accepts the line in any last paragraph; `addCoAuthor` uses
    `isTrailerBlock` (`:401-413`), which needs each line to be a trailer. The
    port doc says a line of the text is not a trailer. Fix: `hasTrailer`
    calls `isTrailerBlock`.
11. **Minor. The `FindCommit` port doc misses two facts** (83e6a4a).
    `pkg/forge/forge.go:251-258` does not say that more than 500 commits
    since `since` gives `ErrUnavailable`, or which clock `since` uses. A
    server clock ahead of GitHub's misses the caller's own commit. Fix:
    document both, and subtract a skew margin.
12. **Minor. A moved head costs up to about 384 REST calls** (5aebf64).
    `changedSince` (`commit.go:206-234`) reads the base again on each of
    three moves. Fix: cache the base blob IDs, or the query of finding 2.
13. **Minor. The suite does not cover each documented error** (83e6a4a).
    `forgetest/commit_cases.go` has no case for a missing repository
    (`Commit`, `FindCommit`) and no `ErrRefused` case for a missing write
    permission. Fix: add them, so the next adapter must pass them.
14. **Info. The fake GitHub encodes guesses.** `e2e/githubfake/graphql.go:118-125`
    (protected branch `FORBIDDEN`), first-parent-only `listCommits`, and 200
    `diverged` for unrelated histories may differ from GitHub. Record real
    answers in the signing spike.
15. **Info. Smaller items.** `Commit` moves onto a new head and the result
    does not show the real parent (add `Parent` to `forge.Commit`);
    `checkGranted` (`files.go:547`) calls a narrower grant "wider"; write
    tokens share `readTokens` and its cap of 64; `FindCommit` needs
    `CapCommit` but reads with a read token.

### Server assembly and sign-in

16. **Minor. Two session-cookie checkers with the same options.**
    `pkg/dustpan/signin.go:233-249` and `internal/api/server/signin.go:222-226`
    each call `sessionauth.New`. Fix: build one and pass it; add the session
    path to `docs/agents/dustpan-areas.md`.
17. **Info. `area_ipallow.go` does not follow the recipe's settings shape**
    (`pkg/dustpan/area_ipallow.go:15-56`: a lazy pointer, where the recipe
    and `area_proxy.go` use a value with a default function). The moved
    comments keep "ADR 55, decision 8" (lines 15, 29).
18. **Minor. The audit reason of a sign-in refusal of a provider error is
    fixed before `Finish`.** `callbackIdentity` returns on `error=` in the
    query (`signin.go:425`), so `Callback.ProviderError` never reaches a
    method. A method that must tell `access_denied` from a disabled person
    (SAML status, CAS) cannot. Fold into finding 6.

### Round trips, faultlog, release record, launcher, sinks

19. **Minor. The restore round-trip filter drops a repeated swap attempt
    without checking that it failed** (748fba8).
    `withoutSwapRetries` (`internal/servertest/roundtrips_test.go:1166-1182`)
    keeps the trips from the last swap start. `TraceQueryEnd` (`:204`)
    records no error. A regression that makes every restore retry once is
    hidden. Fix: record `data.Err`, drop an attempt only after a 40P01 or a
    rollback, at most once for each run.
20. **Minor. faultlog checks the fs sentinels before the network type**
    (c016379). `internal/api/faultlog/faultlog.go:146-149`: a
    `*net.OpError` that wraps `EPERM` logs `file_permission`, and a dial to
    a missing unix socket logs `file_missing`. A full disk under SQLite
    (`SQLITE_FULL`) or PostgreSQL (53100) logs `unclassified`. It fails safe.
    Fix: test `*net.OpError` and `*net.DNSError` before the fs sentinels,
    with rows for both cases.
21. **Minor. `release-record-check` ignores a VERSION from the environment
    with no message** (0d63665). `Makefile:145-149`: `VERSION=v0.1.0 make
    release-record-check` runs the no-tag tests and passes. No CI job runs
    the target; see #2400. Fix: print the mode, or fail when
    `$(origin VERSION)` is `environment`.
22. **Minor. `pkg/launcher` links internal code through `pkg/job` and
    `pkg/runner`** (a606df9). `go list -deps ./pkg/launcher` holds
    `internal/ident`, `internal/token`, `internal/scopeaudit` and about 20
    domain packages. The depguard rule (`.golangci.yml:121-141`) checks
    direct imports only, and its comment says a move to another module is a
    move of files. `deps_test.go` checks drivers and the forge only. Fix:
    refuse `internal/` and an allowlist of `pkg/` in `TestLinkedDependencies`,
    or cut the dependency on `pkg/job` and `pkg/runner` to value types.
23. **Minor. CLAUDE.md (AGENTS.md:189-191) still lists `launcher` as an `internal/` domain
    package** in "Backend rules". The layout and `.golangci.yml:199` say
    `pkg/launcher`.
24. **Info. The syslog receiver can miss a connection on close**
    (`e2e/sinks.go` `accept` and `close`). Fix: a closed flag under the
    lock in `accept`.
25. **Info. Only `stack rm` has an end-to-end gate refusal test**
    (`internal/servertest/pulumi_gate_test.go`). Import, rename, restore and
    cancel, which use the new memo path of 02bc033, have budgets but no
    refusal assertion.
26. **Info. The ADR 327 and ADR 328 links are in ADR 296 form**, the INDEX
    rows match, and no job-proxy code exists, as ADR 327 says. No finding.
27. **Info. The gate memo (02bc033) keeps the fence.** The memo lives for one
    call, keeps no failed read, and `Records.Put` still runs in
    `WriteRecord` with the fence. No finding.

## Checked and fine

- Shared-core rule: no tool-specific field in `pkg/forge`, `pkg/session` or
  `pkg/launcher`; no adapter calls another adapter.
- Layering: `pkg/launcher` imports no `net/http`, `database/sql` or driver
  directly; the sign-in ports are plain structs with no `net/http`.
- `Commit` ancestry fails closed on each unknown status, missing merge base,
  bad JSON, 404 and oversized body; GraphQL errors fail closed; only port
  sentinels come back; input allowlists make no call on bad input.
- Stored providers of a mode with no method are refused before the secret
  is opened; the provider switch is read at each `Begin` and `Finish` and
  fails closed.
- `setSignInLimiters` refuses to start with tokens and no gate.
- The e2e sink receivers decode the wire bytes on their own (TLS, RFC 6587
  framing, constant-time HMAC, timestamp window) and `goleak` runs.
