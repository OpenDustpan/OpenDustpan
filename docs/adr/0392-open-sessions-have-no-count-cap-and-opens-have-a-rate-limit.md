# 392. Open sessions have no cap on their count, and a rate limit on opens replaces it

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The form of the rate limit is an open question in RFC 135

Changes:

- Changes ADR 203 decision 3: the cap of 100 sessions that did not end no longer applies. This holds for a principal and for a job. The number of open sessions has no cap. A rate limit on opens for each principal replaces the cap. The other items of decision 3 stay.

## Context

ADR 203 decision 3 says:

> [...] A principal has at most 100
> sessions of direct opens that did not end. A job has at most 100
> sessions that did not end, so a principal of a CI system with many
> jobs at the same time does not reach the limit.

A count cap on live sessions gives no confidentiality. Only the credential
that opened a session reads it (ADR 203 decision 3). A thief who holds that
credential can open again, so the cap does not stop the thief. The cap guards
against use of resources:

- A runaway loop of opens grows the table of sessions.
- Each open calls the provider blocks of the profile. A provider block can
  make a credential at a cloud provider or a vault (for example an STS token, a
  Vault lease or an OIDC token). A loop of opens can use up the quota of that
  provider, and then the quota breaks for every user of it.

The owner decided on 20 November 2026, in these words:

> remove the cap of 100 live open profile sessions (ADR 203 d3, "A principal
> has at most 100 sessions of direct opens that did not end", and the same cap
> for a job). Sessions become unbounded in count. A rate limit on opens for
> each principal replaces the cap.

The owner accepted this reasoning: a rate limit on opens guards the provider
quota better than a count cap, and it never blocks steady use. A principal
with 100 live sessions that each need a long duration is blocked by the cap
but is not a risk. A principal that opens 1000 times in one minute is a risk
whatever the count of live sessions.

This ADR does not decide the form of the rate limit. RFC 135 holds that open
question: the limit, the window, the answer, the audit record, the setting and
its effective value, and the follow-up for the consumer `pulumi`.

No tag shipped the cap, so no data conversion is needed (ADR 73).

## Decision

1. **No cap on the count of open sessions (owner, 2026-11-20).** A principal
   has no maximum number of sessions of direct opens that did not end. A job
   has no maximum number of sessions that did not end. The number of sessions
   is not bounded. A session still ends at the end of its duration or at the
   first expiry of a credential in it, and the schedule still sweeps each
   session that ended (ADR 203 decision 3).
2. **A rate limit on opens replaces the cap (owner, 2026-11-20).** The server
   limits the number of opens for each principal in a window. The rate limit
   is the guard against a runaway loop and against the use of the quota of a
   provider. This ADR does not set the values, the window, the answer or the
   audit record of a refusal. RFC 135 holds them. Until the owner accepts a
   decision on RFC 135, the server has no limit on the count of sessions and
   no rate limit on opens.
3. **What does not change (owner, 2026-11-20).** Only the credential that
   opened a session reads it. Each read checks the action again. The result of
   a session is sealed with the data key of the root profile. A session is not
   in a backup. A refused open writes an audit entry, and an open writes one
   (ADR 203 decision 9).

## Consequences

- The ADR 203 sentences "A principal has at most 100 sessions of direct opens
  that did not end" and "A job has at most 100 sessions that did not end" do
  not apply. The reader of ADR 203 follows its "Changed by" item to this ADR.
- The error code `profile.limit` for the 101st live session has no use for this
  case. The code may stay in the catalog for another limit of a profile.
- Code and specs that state the cap of 100 must change. They are not in this
  ADR. The OpenSpec change `open-profile-serve-open-sessions`, the `Sessions`
  port comment in ADR 200 and the code that counts live sessions in the
  transaction of `Put` are in that list. A later change edits them.
- Until RFC 135 is decided and built, a window with no cap and no rate limit
  exists. The project has no installation and no tag (ADR 73), so no user has
  this window.
