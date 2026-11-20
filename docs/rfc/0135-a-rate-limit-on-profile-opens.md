---
rfc: 135
title: A rate limit on profile opens
status: discussing
created: 2026-11-20
---

# RFC 135. A rate limit on profile opens

Status: discussing. The owner decided that a rate limit on opens replaces the cap of 100 open sessions (owner, 2026-11-20). The ADR that records this decision is ADR 392. The owner did not decide the form of the rate limit. This RFC holds that form as an open question and gives a recommendation only. It decides no security semantics. This RFC edits no ADR.

## Problem

[ADR 203](../adr/0203-opening-a-profile-delivery-actions-and-audit.md) decision 3 caps the open sessions. ADR 392 ends the cap (owner, 2026-11-20). The cap guarded against use of resources, not against theft:

- Only the credential that opened a session reads it. A thief who holds the credential can open again, so the cap gives no confidentiality.
- A runaway loop of opens grows the table of sessions.
- Each open calls the provider blocks of the profile. A block can make a credential at a cloud provider or a vault (STS, Vault, OIDC). A loop of opens can use up a provider quota that all users of that provider share.

Without the cap, the server has no bound on the count of opens. The owner wants a rate limit on opens for each principal. The questions are: the limit, the window, the answer, the audit record, the place of the setting, and the key of the count.

## Decision of the owner

Quoted from the owner decision of 20 November 2026, as the lead relayed it:

> remove the cap of 100 live open profile sessions (ADR 203 d3, "A principal has at most 100 sessions of direct opens that did not end", and the same cap for a job). Sessions become unbounded in count. A rate limit on opens for each principal replaces the cap.

The decision gives the rate limit "for each principal". It does not say if a job has its own rate limit. This RFC asks (open question 2).

## What main does today

- An open with the consumer `api`, `console` or `pulumi` stores a session. The other consumers store nothing (ADR 203 decision 3).
- The 101st live session fails with `ErrLimit`. The error code is `profile.limit` (`internal/errcode/catalog_profile.go:11`, meaning "the profile is over a limit"). The `Sessions.Put` port comment states the cap (ADR 200, the `Sessions` interface).
- A refused open writes an audit entry (ADR 203 decision 9: "A refused open writes an entry too").
- The shared denial limiter bounds audit entries. It does not decide to refuse a request (see "Does the denial limiter fit").

## Does the denial limiter fit

[ADR 382](../adr/0382-a-repeatable-domain-refusal-goes-through-the-shared-denial-limiter.md) decision 3 says: "A new domain refusal that a caller can repeat records its audit entry through the shared denial limiter." A refused open is such a refusal.

The limiter fits the audit record of a refusal. It does not fit the decision to refuse:

- The package comment says that it "bounds how many audit entries repeated denials write" (`internal/denylimit/denylimit.go:1`). It counts denials that something else already decided.
- `Allow` and `AllowWindow` return a `Decision` with `Write`, `Suppressed`, `Overflow` and `Flush`. None of them says "refuse this request" (`internal/denylimit/denylimit.go:159` and `:166`).
- The first denial of a key in a window writes the entry. Later denials only add to a count (`internal/denylimit/denylimit.go:5`).
- The default window is one minute and the table has at most 4096 keys, 64 for each principal (`internal/denylimit/denylimit.go:27` to `:34`).

So the rate limit needs its own counter. The refusal then goes through the shared denial limiter for its audit entry, with the key of ADR 382: the principal, the action (`profile:open`), the object kind and the scope.

Precedents for a counter that decides:

- The begin of a passkey ceremony: 10 begins in 5 minutes for each person, in memory, one count for each server process, at most 10,000 people, and it fails closed when the table is full (`internal/api/server/passkeys.go:62` to `:71` and `:547` to `:600`).
- The mTLS exchange guard: `ErrRateLimited` for a key and for all keys together (`internal/trustrulesvc/mtls_exchange.go:77`, `internal/trustrulesvc/mtls_exchange_limit.go:155` to `:195`). Its answer is 429 with the code `mtls.rate-limited` (`internal/api/trustrules/exchange.go:263`).
- The test of a notification destination: 429 with the code `sink.test-rate-limited`, the argument `retryAfterSeconds` and the header `Retry-After` (`internal/api/notifications/notifications.go:204` to `:215`).

## Options

### The counter

1. **A count in memory of each server process (as the passkey begin).** Cost: no write, no schema, no cross-process cost. An installation of N instances on one database allows N times the limit, because each instance keeps its own count (`internal/api/server/passkeys.go:62`, comment: "the instances of one database each keep their own count"). A restart sets the count to zero. This is a rate limit against a runaway loop, and it is not exact.
2. **A count from the store.** The open already writes the session row and the audit entry in one transaction. The count of the sessions that were made in the last window, for the owner, is one query in that transaction. Cost: the count only sees opens that store a session (`api`, `console`, `pulumi`) and only while the sweep has not deleted the row. The sweep deletes a session that ended, and the shortest duration is 1 minute, so a window longer than 1 minute can under-count. Gain: exact for all instances, with no new table.
3. **A count in a new table.** A row for each principal and window. Cost: a new table and a write for each open, also for the opens that store nothing. Gain: exact, and it counts every consumer.

### The window and the limit

The numbers below are proposals. The project has no measurement of real opens. The Pulumi CLI opens once for each command (see "The Pulumi CLI follow-up"). A person in the console opens rarely. A CI system with a shared service principal and many parallel jobs is the busy case. Jobs use a job token, and that is a separate key if option 2 of "The key" holds.

1. **A fixed window of 1 minute.** Simple, and the same window as the denial limiter. A caller may send twice the limit across the end of a window.
2. **A sliding window or a token bucket.** No double burst. Cost: more state for each key.

### The key

1. **The stable ID of the principal for a direct open, and the stable ID of the job for a job open (recommended).** The open of a job has the job as its second key, as the cap had (ADR 203 decision 3). A principal of a CI system with many jobs at once does not share one count with all of them. The key is an ID, not a name (ADR 362 decision 1).
2. **The principal only.** A job token has a principal. All jobs of one CI principal share one count. This may block steady use of a CI system with many jobs. The owner's decision says "for each principal" and may mean this.

## Proposal

This is a recommendation. The owner decides.

1. **The counter.** Option 2 of "The counter" first, because it adds no table and the count is exact for all instances. If the owner wants every consumer counted, option 3.
2. **The limit and the window.** 60 opens for each principal in a fixed window of 1 minute, and 30 opens for each job in a fixed window of 1 minute. Steady use is a few opens each minute. A runaway loop sends hundreds each second. Both numbers are marked "provisional" in the docs until a measurement exists.
3. **The key.** The stable ID of the principal for a direct open. The stable ID of the job for a job open. A job open counts for the job only, and not for the principal that started it.
4. **The answer.** HTTP 429 with a new error code `profile.rate-limited` and the argument `retryAfterSeconds`. The header `Retry-After` names the same wait (as `sink.test-rate-limited`). The code `profile.limit` keeps its meaning for the other limits of a profile. The text follows the voice rules, for example: "too many opens of profiles from this principal: try again in 30 seconds".
5. **The audit record of a refusal.** A refused open writes one entry of the kind `access`, with the action `profile:open` and the outcome `denied`, through the shared denial limiter (ADR 382 decision 3). The first refusal in a window writes the entry. The next entry of the key carries the count of the later refusals. The entry names the principal, the job when it is a job open, the limit and the window. It never names a value.
6. **The setting and its effective value.** Two installation settings, with the defaults above: `profile.openLimit.principalPerMinute` and `profile.openLimit.jobPerMinute` (names are proposals). `GET /api/dustpan/v1/settings` shows the effective value and its source (the rule of "each default shows its effective value" in `CLAUDE.md`). A change goes through the settings API, with its audit entry. The value 0 does not turn the limit off (as ADR 227 decision 3: "No value turns the check off"). An operator who wants no limit sets a high value, and the setting shows it.
7. **Fail closed.** When the table of counts is full, or the store cannot count, the open is refused with 429 or 503 and an audit entry. It is not allowed (ADR 250, "Fail closed and record").
8. **The tests.** One table of hostile cases for each layer: the port adapter (the count in the transaction), the service (the key and the limit), and the HTTP handler (the answer and `Retry-After`). For each guard, a test that fails when the guard goes away (`CLAUDE.md`, "Security code").

## ADR check

ADRs that this RFC touches, with the lines that matter and the "Changed by" chain from `docs/adr/INDEX.md`:

- **ADR 203 decision 3** (the cap). Quoted above. Changed by: ADR 210 (decision 12), ADR 222 (decision 5), ADR 225 (decision 5), ADR 241 (extends decision 1), ADR 286, ADR 300 (decision 5), ADR 353 (extends decision 1), and ADR 392 (changes decision 3, owner, 2026-11-20). Current rule for the cap: ADR 392. The words "Only the credential that opened a session reads it" stay.
- **ADR 203 decision 9** (the audit entry of an open). Verbatim: "A refused open writes an entry too. A read of a session writes no entry, because the open wrote one." Not changed. No ADR changes decision 9.
- **ADR 362 decision 1** (owner, 2026-11-08, stable IDs). Changed by: ADR 363 extends decision 2 only. A stored reference to an entity that a user can rename uses the stable ID. The key of the count follows it.
- **ADR 250 decision 3** (defense in depth, "Fail closed and record"). Verbatim: "Every denial and every use of a privilege goes to the audit log." Changed by: ADR 382 extends decision 3.
- **ADR 382** (the shared denial limiter). Decision 3: a new domain refusal that a caller can repeat records its entry through the limiter. Changed by: none.
- **ADR 227 decision 3** (a window of 0 turns nothing off), as ADR 390 extends it. Used as a pattern for the setting, not as a rule.

No owner-tagged ADR forbids a rate limit on opens. The tag "(owner, 2026-11-20)" of ADR 392 is the only owner decision on this question.

## Security analysis

- **What the limit guards.** The quota of a provider and the size of the table. It does not guard confidentiality (the owner accepted this).
- **A loop with many principals.** A thief with many stolen credentials gets one limit for each credential. The rate limit does not stop that. The audit log and the provider quota are the guard. Open question 5 asks if an installation-wide limit is needed.
- **A caller that rotates the key.** The key is the stable ID that the server derived from the credential. The caller does not send it, so the caller cannot rotate it.
- **A job open.** A job that loops can use up the quota of a provider as a principal can. The limit for each job bounds it. A principal that starts many jobs is bounded by the job limit times the number of jobs. See open question 2.
- **Refusal flood.** A caller that is over the limit can send refused opens for ever. The shared denial limiter bounds the audit entries. The count in memory or in the store bounds the work. A refused open does no provider call.

## The Pulumi CLI follow-up

Idea of the lead, not decided. The Pulumi CLI opens a session with the consumer `pulumi` and `duration=2h`, and it reads the session once. A session that lives for 2 hours after one read has no use. If an open session could end after its first full read, the table would hold fewer sessions, and a stolen session id would be good for one read only.

What this needs:

- An e2e check that drives each supported version of the Pulumi CLI and shows that none of them reads the same session twice in one command. The check must fail when a version reads twice. Without it, an end after the first read would break that version.
- A rule for a read of one path. ADR 203 decision 3 says a read answers "the values, or one path". The end after the first read must say if a read with a path counts. This RFC proposes that only a read with no path ends the session.
- A rule for a failed read, for example a network error after the server answered. The CLI cannot read again and must open again. That costs one open of the rate limit.
- A change of ADR 203 decision 3 (the duration and the end of a session). That needs a new ADR and the owner.

Recommendation: do not decide this in the RFC about the rate limit. Write the e2e check first. It costs a test and no product change. Decide after the result.

## Effects

- The OpenSpec change `open-profile-serve-open-sessions` states the cap in `design.md` (lines 137, 170 and 182), in `specs/profile-open/spec.md` (lines 105 and 112) and in `proposal.md` (lines 25 and 181). Its own lane edits them after the ADR. This RFC edits no change.
- The comment of `Sessions.Put` in ADR 200 states the cap. An accepted ADR does not change (ADR 1). The ADR 203 item in "Changed by" is the mark for readers.
- A new error code needs the catalog, the API description and the client golden files (generated with `make generate`).
- A page of the user documentation names the limit and the setting (ADR 214).

## Open questions

The owner decides each security question. This RFC gives a recommendation only.

1. **The limit and the window.** 60 opens for each principal in 1 minute, and a fixed window? Recommendation: yes, provisional, and measure real opens before the first tag. The owner decides.
2. **A job.** Does a job have its own limit (recommendation: 30 for each job in 1 minute), or does a job open count for its principal only? The owner's words say "for each principal". The first reading may block a CI principal with many jobs. The second reading lets a principal start many jobs, each with its own limit.
3. **The counter.** Memory of the process (approximate with N instances), the store (exact, sees only stored sessions) or a new table (exact, a write for each open)? Recommendation: the store.
4. **Which opens count.** All opens of a principal, or only the opens that store a session? A consumer that stores nothing (`launcher`, `run`, `observe`) still calls the provider blocks. Recommendation: all opens, which needs option 3 of "The counter" or a count in memory for those consumers.
5. **An installation-wide limit.** Is a limit on all opens of all principals needed, as a guard against many stolen credentials? Recommendation: no, until a measurement shows the need.
6. **A setting that an operator can raise without limit.** Does the owner accept that an operator sets a very high value (the value 0 never turns the limit off)? Recommendation: yes. The setting shows the effective value, and the change has an audit entry.
7. **The count of a refusal and the window of its audit entry.** Is the shared denial limiter (one entry for each minute and key, with a count) enough, or does the owner want an entry for each refusal? Recommendation: the shared denial limiter (ADR 382).
8. **The Pulumi CLI follow-up.** Does the owner want the e2e check (read once) first, and a later ADR for an end after the first full read?
9. **Fail mode.** When the table of counts is full or the store cannot count, refuse (recommendation) or allow?

## Plain summary

Before: the server let a principal hold at most 100 open profile sessions. The owner removes that cap. It never stopped a thief, and it blocked honest use. The real risk is a loop that opens a profile again and again. Each open can make a cloud credential, and too many can break a shared quota for everyone.

After (recommended): the server counts opens for each principal in each minute. Over the limit, it answers 429 with a clear code and a wait time, and it writes one audit entry with a count. The limit and its effective value show in the settings API. Normal use never reaches it.

The owner decides the numbers, whether a job has its own limit, and what the server does when it cannot count.

## Checklist

- [x] Tolerates asynchronous replication between installations: the count and the open are writes of one namespace in one installation, under its fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). A count in memory is local to one process and is not replicated.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
