---
rfc: 129
title: Concurrent applies on one state, the six open points of ADR 302
status: accepted
adr: 368
created: 2026-11-14
---

# RFC 129. Concurrent applies on one state: the six open points of ADR 302

Status: accepted (ADR 368, ADR 369). The owner accepted points 1 to 3 and 5 to 7 on 14
November 2026 as recommended ([ADR 368](../adr/0368-concurrent-applies-apply-claims-server-serial-fences-and-the-closure-test.md)).
Point 4 was decided the same day ([ADR 369](../adr/0369-the-actor-of-a-job-trust-rules-and-external-service-principals.md)). This is the RFC that ADR 302 decision 6 asks for. It
touches ADR 26 decision 3, ADR 44 decisions 1 and 9, ADR 100 decision 20 and
ADR 299 decision 19. It does not edit an accepted ADR. An owner decision on
it gives a new ADR, and milestone 8 code starts only after that ADR (ADR 302
decision 6).

## Problem

### What depends on this RFC

- The OpenSpec changes `two-disjoint-claims` and
  `concurrent-applies-merge-core` are not spec-ready. Both say "the change
  conflicts with ADR 302: an RFC comes first". `two-disjoint-claims` asks
  for the claim ID format, the principal of each job on the TFE-compatible
  path and the expiry of a claim. `concurrent-applies-merge-core` asks how a
  TFE-compatible request names its claim, and how long a whole-unit wait
  lasts.
- All of milestone 8 waits for the ADR that accepts this RFC (roadmap,
  milestone 8, work item C2).

### What is already decided, and stays

ADR 302 decision 2 (owner) fixes the design. This RFC does not reopen it:

> - A runner job registers its saved plan as a claim before the apply.
> - The server refuses overlapping claims before anything runs. It always
>   refuses an overlap. A runner job turns the refusal into the status
>   "blocked". The local `dustpan` wrapper waits: it tries the claim again
>   after the refusal until the blocking claim ends, or until a timeout.
> - The server serves each job the state version that the plan used
>   as its base.
> - The server merges each upload one resource instance at a time.
> - Work that the server cannot prove disjoint takes turns. The research
>   gives six cases that keep the whole-unit lock: data sources, providers
>   that take their settings from resources, root outputs, client-encrypted
>   state, a version or lineage mismatch, and a local CLI run without the
>   wrapper of decision 4. [...] The RFC of decision 6 may add a case. It
>   may not remove one without a decision of the project owner.

ADR 302 decision 3 (owner): "OpenTofu is first. The Pulumi CLI follows
after its own spike." Decision 6 (owner) gives the scope of this RFC: the
serial, the parent pointer and claim ID, the fence of a claim, one principal
for each job on the TFE-compatible path, the retry interval and timeout of
the local wrapper, and the closure test in place of the replacement rule of
ADR 299. ADR 302 has no "Changed by" entry.

### The facts

The evidence is the research of 1 November 2026 behind ADR 302 (a spike with
two to four real `tofu` v1.11.14 processes on one workspace, and a reading of
the OpenTofu source):

- Two jobs that plan from one state both upload `serial = read serial + 1`.
  The second upload fails the serial check of ADR 26 decision 3, after its
  cloud calls ran [spike s2].
- During an apply, the CLI sends an upload about every 20 seconds, with its
  own serial one higher each time
  (`internal/backend/local/backend_apply.go:33`). The CLI never reads the
  state back after an upload (`internal/states/remote/state.go:244-252`).
  So a version that the server merged is not visible to the job, and the
  base of each later upload of the job is its own earlier upload [spike s7].
- `tofu apply plan.tfplan` stops with "Saved plan is stale" when the serial
  of the state that it reads is not the serial of the plan
  (`internal/backend/local/backend_local.go:360`). This is why the server
  serves each job its base (ADR 302 decision 2).
- On the TFE-compatible path an upload carries no lock ID
  (`internal/cloud/state.go:348-372`). Today the server can tell two
  uploads apart only by their credential (ADR 44 decision 9).
- A job token already identifies its job. ADR 100 decision 20: "The job
  token puts one claim on the principal that it authenticates: the
  **job-ID claim**, the ID of the job." ADR 100 decision 20 is changed by
  ADR 144 (only the job token holds `job:write-cost`, for its own job) and
  ADR 213 (the reads of a job token).

### A word to fix first

"Claim" already means three things in `GLOSSARY.md`: the durable claim of a
phase (ADR 72), the claim of a runner (ADR 130) and the job-ID claim of a
job token (ADR 100). This RFC writes **apply claim** for the claim of ADR
302: the reservation of the resource instances that one saved plan changes
and reads. Decision point 7 asks the owner to accept the term.

## Decision points

Each point has the context, the precedent, the options, a recommendation and
a short explanation. The points depend on each other in this order: 7, 4, 3,
1, 2, 6, 5.

### 1. The serial under an apply claim (change to ADR 26 decision 3)

**Decided:** option A, by the owner on 14 November 2026 (ADR 368 decision 2).

**Context.** ADR 26 decision 3: "A new state version needs the lock of the
caller. It needs the same lineage as the current state. When no state
exists, any lineage is valid. It needs a higher serial than the current
state. A force upload skips the lineage check and the serial check. It never
skips the lock check." Changed by: ADR 44 changes decision 3 (forced and
unlocked writes need an action and a switch), and ADR 335 extends ADR 44
decision 5 (a state version with no lock meets the gates before it is
written). Two accepted apply claims send the same serial, so the rule
"a higher serial than the current state" refuses the second one after the
cloud changed. The merge of ADR 302 needs another rule.

**Precedent.**

- HCP Terraform takes the serial from the client and checks it: the state
  versions API needs `serial`, `lineage` and `md5`
  (<https://developer.hashicorp.com/terraform/cloud-docs/api-docs/state-versions>).
  It can, because each workspace runs one run at a time: "If there's
  already a run in progress, the new run won't start until the current one
  has completely finished"
  (<https://developer.hashicorp.com/terraform/cloud-docs/workspaces/run/remote-operations>).
- Spacelift gives each tracked run an exclusive lock on the stack
  (<https://docs.spacelift.io/concepts/run>). Atlantis locks the directory
  and workspace from the plan to the merge
  (<https://www.runatlantis.io/docs/locking.html>). Pulumi Cloud answers a
  second update with 409 "Another update is currently in progress"
  (<https://www.pulumi.com/docs/iac/operations/troubleshooting/update-conflicts/>).
  None of them merges, so none needs a server serial.
- Systems that accept writes from several clients at a time assign the
  order on the server: the `resourceVersion` of Kubernetes
  (<https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions>)
  and the revision of etcd (<https://etcd.io/docs/v3.5/learning/api/>). The
  client sends the version it read. The server sets the new one.

**Options.**

- **A. The server assigns the serial under an apply claim.** For an upload
  under a live apply claim, the server ignores the serial of the client for
  ordering. The merged version gets `current serial + 1`, and the server
  writes that serial into the stored state document. The client serial is
  checked only inside the claim: it must be higher than the last client
  serial of the same claim, so a replay or an old upload of the job is
  refused. The lineage check stays. An upload with no apply claim keeps ADR
  26 decision 3 and ADR 44 as they are.
  - Gives: the merge works with the real CLI and no change to it.
  - Costs: two serials to explain (the stored one and the one the job sent).
    The history shows both for a merged version.
- **B. Keep the client serial.** The second claim of two always fails its
  upload. This is the "naive" failure of the spike [s3]: the cloud changed,
  and the state does not record it. Never safe.
- **C. A sub-serial for each claim** (for example `n.k`). The state format
  holds an integer serial. The CLI cannot read another form. Not possible.

**Recommendation: A.** It is the only option that the real CLI accepts. It is
the same pattern as the version counters of Kubernetes and etcd. The rule is
narrow: it applies only to an upload under a live apply claim, so the
whole-unit lock path keeps ADR 26 and ADR 44 word for word.

**ELI5.** Two people edit different pages of one notebook at the same time.
Each one writes "page 8" at the bottom, because each saw page 7. The clerk
who files the notebook numbers the pages, not the people. The clerk still
checks that each person's own pages go up, so an old copy cannot sneak in.

### 2. A parent pointer and an apply claim ID for each state version

**Decided:** option A, by the owner on 14 November 2026 (ADR 368 decision 3).

**Context.** A merged version comes from two inputs: the version that was
current at the commit, and the base that the plan of the claim used. The
history must show "merged with job X", and the server must find the base of
each upload (fact: the base of a later upload of a job is its own earlier
upload). ADR 362 decision 1 (owner, 2026-11-08): "Every stored reference to
an entity uses the stable ID of the entity when a user can rename the
entity, or delete and recreate it." A state version and an apply claim are
not renamed, but a serial is no longer unique to one writer under point 1.
So a reference uses the ID, not the serial.

**Precedent.** A git merge commit stores its parents, and git finds the
merge base from them (<https://git-scm.com/book/en/v2/Git-Internals-Git-Objects>).
Kubernetes and etcd commit a write only when the version that the client
read is still current (compare-and-swap on the version). HCP Terraform,
Spacelift, Atlantis and Pulumi Cloud keep a linear history, because they
serialize.

**Options.**

- **A. Three nullable columns on each state version:** `parent_version_id`
  (the version that was current when this one committed; set for every
  write, also on the lock path), `base_version_id` (the version that the
  plan of the apply claim used; set only under a claim) and `claim_id`.
  The commit is a compare-and-swap: it succeeds only when
  `parent_version_id` is still the current version, in the same
  transaction with the namespace fence (ADR 26 decision 7). The parent is
  written only inside that transaction.
  - Gives: one linear chain that the database guards, plus the merge base
    for the merge and for the history. No derived fact is written later.
  - Costs: an edit of the baseline of each dialect (ADR 322).
- **B. An apply claim ID only.** The history finds the order from the
  serial. It cannot find the base of a job's later upload, and two jobs
  can have the same client serial. Not enough for the merge.
- **C. A separate table of parents, with any number of parents.** Needed
  only for a merge of more than two inputs in one commit. ADR 302 merges
  one upload at a time, so each commit has two inputs at most. More shape
  than the problem needs.

**Recommendation: A.** It answers the merge and the history with one column
each, and the compare-and-swap makes "two writers both think they are next"
impossible by construction, not by a later check.

**ELI5.** Each page of the notebook says which page it was glued after, and,
for a merged page, which old page its writer started from. The clerk glues a
page only if the page it says it follows is still the last page.

### 3. A fence for each apply claim

**Decided:** option A, by the owner on 14 November 2026 (ADR 368 decision 4).

**Context.** ADR 44 decision 1: "In the transaction that releases the lock,
`force-unlock` writes a new state version. Its content is the current state
with a new lineage and serial 1 more than the current serial." Changed by:
ADR 239 extends decisions 4 and 6 (client-encrypted lock release), ADR 335
extends decision 5. A new lineage fences every holder at once. With several
apply claims, a stale job must be fenced without fencing the healthy ones.
A job that stops mid-apply also leaves cloud changes that no state records.

**Precedent.**

- A fencing token: each holder of a lease gets a number that only goes up,
  and the store refuses a write with an older number
  (<https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html>).
  OpenDustpan already does this for durable claims (ADR 100 decision 4:
  "The claim, the expiry, the fencing token") and for runner leases (ADR
  130 decision 7: "90 seconds by default, from 30 seconds to 10 minutes",
  renewed after a third of the lease).
- Kubernetes leases have a holder, a duration and a renew time
  (<https://kubernetes.io/docs/concepts/architecture/leases/>).
- HCP Terraform, Spacelift and Pulumi Cloud fence by the single lock or
  the single update: a cancel or a force-unlock ends the one writer.

**Options.**

- **A. Each apply claim has its own lease and ends in the database.** An
  apply claim of a runner job lives as long as the durable claim of its
  apply phase. An apply claim of the local wrapper has a lease of 90
  seconds, renewed every 30 seconds. An apply claim ends at the final
  upload, at the end of the job, at the end of its lease, or by a
  force-release. Each upload checks, in its commit transaction, that its
  apply claim is live. An upload under an ended claim gets 409
  `claim.ended`, and the CLI writes `errored.tfstate` as after an ADR 44
  fence. A force-release needs `unit:manage` and a reason, writes an audit
  entry and names the principal.
  - **Stranded changes.** An apply claim that ends with no final upload
    (crash, expiry, force-release) may have changed the cloud. Its address
    set stays held: a new apply claim that overlaps it is refused, with the
    name of the ended claim, until a person with `unit:manage` clears the
    hold with a reason, or a refresh job on the unit ends. This fails
    closed.
- **B. A new lineage for each force-release (the ADR 44 fence).** It also
  fences every healthy apply claim. Not usable with more than one claim.
- **C. The expiry of the job token only.** A job token has the expiry of
  the runner claim (ADR 100 decision 20). It does not cover the local
  wrapper, and it leaves no hold on stranded changes.

**Recommendation: A.** It reuses the lease and fencing model that runners
and durable operations use today, so an operator meets one model. The hold on
stranded changes is the one new rule. It keeps the safety of ADR 44 (a stale
job cannot write) and adds what ADR 44 cannot see: changes without a record.

**ELI5.** Each writer holds a ticket that the clerk must keep renewing. When
the ticket runs out, the clerk takes no more pages from that writer. If the
writer left before the end, the clerk puts tape over the pages the writer was
working on, and someone must look before anyone else writes there.

### 4. One principal for each job on the TFE-compatible path

**Decided** by the owner on 14 November 2026 ([ADR 369](../adr/0369-the-actor-of-a-job-trust-rules-and-external-service-principals.md)). The credential
mechanics of option A stand: the job token names the apply claim, and the
launcher token of the local wrapper is bound to its apply claim. The identity
is always the actual actor that a stored trust rule resolves, never the job.
ADR 369 adds three layers (identity, grants, conditions on the verified token
claims) and four kinds of actor.

**Context.** ADR 44 decision 9: "The server makes no attempt to tell two
clients with one token apart while one of them holds the lock. Distinct
principals for each job (RFC 20) remain the advice of ADR 25." A
TFE-compatible upload has no lock ID, so the server must find the apply
claim of an upload from its credential. ADR 302 decision 6 names "one
principal for each job" as a point of this RFC.
`concurrent-applies-merge-core` asks the same question.

**Precedent.** HCP Terraform "generates a unique per-run API token and
provides it to the Terraform worker in the CLI config file". The tokens
"become invalid after the run is completed"
(<https://developer.hashicorp.com/terraform/cloud-docs/workspaces/run/run-environment>).
OpenDustpan already does the same: the job token with the job-ID claim
(ADR 100 decision 20). ADR 144 decision 15 already scopes a write to one
job by comparing the job ID with the job-ID claim.

**Options.**

- **A. The job-ID claim names the apply claim.** Each runner job has at most
  one live apply claim. A TFE-compatible request with a job token finds its
  apply claim by the job ID. The principal stays the person who started the
  job, so the grants and the audit do not change. The local wrapper
  registers its apply claim with the person's credential and gets a
  short-lived token for the claim (the same shape as a job token, with the
  ID of the claim, and the expiry of the claim's lease). It gives the token
  to `tofu` through the credentials helper, never in a variable, as a
  runner does.
  - Costs: this reads "one principal for each job" as "one credential that
    names the job". It makes no new principal.
- **B. A new service principal for each job.** A stored principal and a
  stored grant for each job (the "No magic" rule of `CLAUDE.md` asks for
  stored grants). Many principals and grants that live for minutes, and an
  audit trail that names a robot, not the person.
- **C. A header that names the apply claim.** The `cloud` block of
  OpenTofu sends no custom header. Not possible without a change to the
  CLI.

**Recommendation: A.** It tells jobs apart with a credential that exists
today, keeps the person as the principal, and needs no new grant. The owner
should confirm that a separate credential for each job meets the intent of
"one principal for each job", because the words of ADR 302 say "principal".

**ELI5.** Two jobs of one person carry the same name badge, but each badge
has a job number printed on it. The door reads the number. Nobody gets a
second badge.

### 5. The retry interval and the timeout of the local wrapper

**Decided:** option A, by the owner on 14 November 2026 (ADR 368 decision 5).

**Context.** ADR 302 decision 2: the wrapper "tries the claim again after the
refusal until the blocking claim ends, or until a timeout." The values are
open. A retry also needs a rule for a base that changed while it waited: the
saved plan is then stale.

**Precedent.**

- `tofu -lock-timeout` is 0 by default: a held lock fails at once. With a
  timeout, the http backend retries at 1, 2, 4 and 8 seconds [spike s1].
- HCP Terraform queues runs and waits with no timeout. Pulumi Cloud fails at
  once with 409. Spacelift queues and cancels a queued tracked run after 7
  days (<https://docs.spacelift.io/concepts/run>).
- The runner protocol of OpenDustpan: "A network error or a status of 500 or
  more retries with exponential backoff from 1 second to 30 seconds, with
  jitter, until the claim expires" (ADR 131 decision 12). AWS recommends
  capped exponential backoff with jitter
  (<https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/>).

**Options.**

- **A. Backoff from 1 second to 30 seconds with full jitter, a default
  timeout of 30 minutes, and a flag.** `--claim-timeout` takes 0 (fail at
  once) up to 6 hours. The wrapper obeys `Retry-After`. Each wait prints
  the blocking apply claim, its job or person and the time waited, on one
  line. When the claim is refused because the base changed, the wrapper
  plans again and asks for the confirmation again, unless the person gave
  `-auto-approve`.
- **B. The same backoff, with no wait by default (like `tofu`).** The person
  adds a timeout to wait. ADR 302 says the wrapper waits, so a default of
  no wait reads against it.
- **C. A queue on the server.** The server keeps the waiting claims in order
  and answers when one can run. Fair order, but a new queue and a held
  connection for each laptop.

**Recommendation: A.** The interval is the one the runner protocol uses
today. 30 minutes is the wait that ADR 131 (`runner.MaxSessionWait`) and the
attempt deadline of ADR 281 already use, so the product has one number. A
person can turn the wait off with 0, as with `tofu`. A queue (C) can come
later if users ask for fair order.

**ELI5.** If the room is busy, the wrapper knocks again after 1 second, then
2, 4, up to every 30 seconds, with a bit of randomness so that people do not
all knock at once. After 30 minutes it gives up and says who was in the room.
If the room changed while it waited, it makes a new plan and asks you again.

### 6. A closure test in place of "a replacement is never disjoint" (ADR 299)

**Decided:** option C, by the owner on 14 November 2026 (ADR 368 decision 6).

**Context.** ADR 299 decision 19 (owner, with PM details): "Plan conflicts in
place of unit locks (owner). [...] "No conflict" holds only when the check
proves the two closures disjoint (PM)." The accepted text of RFC 114 (part
6, accepted by ADR 299) lists the cases: "These cases are never proven
disjoint: provider changes, replacements, renames through `moved` blocks or
aliases, data sources, deletes, and changes of a parent resource." The
conformance suite has the case `AReplaceIsNeverDisjoint`
(`pkg/reviewrequest/reviewrequesttest/reviewrequesttest.go`). ADR 299 is
changed by ADR 328 and ADR 359. Neither touches decision 19. With this rule,
each plan that replaces one resource takes the whole-unit lock, and
replacements are common (an image tag, an instance type). The change
`two-disjoint-claims` refuses a replace claim with 422 for this reason.

ADR 299 decision 10 (owner), the approval floor, is a separate rule: "a new
delete, a new replace or any operation that the approved set does not hold
stops the apply for a new approval with a fresh authentication." This RFC
does not touch it.

**Precedent.**

- A replace gives the object a new identity, so each object that reads it
  may change. The research found that the plan JSON of OpenTofu lists the
  dependents of a replace as changes ("known after apply"), and that the
  state lists the `dependencies` of each instance [REPORT 3.3]. The spike
  replaced `a` and `b` in parallel and merged them correctly [spike s5].
- Databases with serializable isolation detect a conflict through read and
  write sets, not through a blanket rule on a kind of write
  (<https://www.postgresql.org/docs/current/transaction-iso.html#XACT-SERIALIZABLE>).
- HCP Terraform, Spacelift, Atlantis and Pulumi Cloud have no such test:
  they serialize every apply on a unit.

**Options.**

- **A. Keep the rule.** Every replace takes turns. Safe, but milestone 8
  gives little to a unit that replaces often.
- **B. A closure test for apply claims only.** The review request check of
  ADR 299 keeps the rule. Two checkers with two rules for the same question.
  ADR 302 Consequences ask for one model: the research reuses
  `ConflictChecker` "instead of adding a second conflict model".
- **C. One closure test in the one `ConflictChecker`.** A replace of
  instance X in claim A is disjoint from claim B only when all of these
  hold:
  1. X is not in the write set or the read set of B.
  2. The downstream closure of X is in the write set of A: each instance
     that reads X, directly or through other instances, by a reference in
     the plan, a `depends_on` or the `dependencies` of state.
  3. No instance of the read set or the write set of B is in that closure,
     and no instance of B reads an instance of the closure.
  4. The closure is complete. When an edge cannot be proven (a reference
     through a data source, a module output that the plan does not
     resolve, a provider that takes its settings from X), the test answers
     "conflict".
  The other never-disjoint cases of RFC 114 stay: provider changes,
  renames, data sources, deletes and changes of a parent resource. The
  case `AReplaceIsNeverDisjoint` becomes a set of closure cases, each with
  a fixture.

**Recommendation: C.** It is the change that ADR 302 decision 6 asks for, in
the one checker that ADR 299 and the research chose. It fails closed when the
closure is not proven, which is the rule of ADR 299 decision 41 ("A reader
that the check cannot see counts as a conflict"). The approval floor of ADR
299 decision 10 is not changed: a replace still needs its approval.

**ELI5.** Today, if you swap one brick, the whole wall is closed. With the
test, the server lists every brick that rests on the swapped brick. If none
of them is in the other team's area, both teams can work. If the server
cannot see what rests on the brick, the wall stays closed.

### 7. The term "apply claim"

**Decided:** option A, by the owner on 14 November 2026 (ADR 368 decision 1).

**Context.** "Claim" has three meanings in `GLOSSARY.md` already (see "A word
to fix first"). The glossary lists "Claim (alone)" under _Avoid_ for the
job-ID claim.

**Precedent.** The glossary rule of `CLAUDE.md`: one word per concept, and a
new concept gets a name in `GLOSSARY.md`.

**Options.** A. "Apply claim" in code, docs and API. B. A new word, for
example "reservation". C. "Claim" alone, as ADR 302 writes.

**Recommendation: A.** It keeps the word of ADR 302 and the owner, and it
cannot be read as the other three claims. The UI can say "claim" where the
page is about applies only.

**ELI5.** The word "claim" already means three things here. Call this one
"apply claim" so nobody mixes them up.

## Cases that keep the whole-unit lock

This RFC adds no case to the six of ADR 302 decision 2 and removes none.
Candidates for a later addition, if the first tests show a need: a plan with
`import` or `removed` blocks, and a plan that changes the version of a
provider (each instance then differs, so the claim overlaps every claim
anyway).

## Stable IDs

An apply claim names its workspace by the workspace ID and its base by the
state version ID (ADR 362 decision 1). Resource instance addresses are the
addresses of the tool. A rename through a `moved` block stays never disjoint,
so an address change never passes the closure test.

## What changes in the blocked changes

- `two-disjoint-claims`: the claim ID is a stable ID; the principal is the
  person, the job comes from the job-ID claim (point 4); the expiry is the
  lease of point 3. A replace claim is checked by the closure test (point 6),
  not refused with 422.
- `concurrent-applies-merge-core`: open question 1 takes option A of point 4;
  open question 2 takes point 5 for the wrapper. A runner job does not wait:
  it turns a refusal into "blocked" (ADR 302 decision 2).

## Open questions

None. ADR 368 and ADR 369 decide all points.

## Checklist

- [x] Tolerates asynchronous replication between installations: an apply
  claim is namespace data, written under the fence of its namespace, and no
  write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Points 1 to 3 and 5 to 7 accepted by the owner on 14 November 2026:
[ADR 368](../adr/0368-concurrent-applies-apply-claims-server-serial-fences-and-the-closure-test.md).
Point 4 accepted by the owner on 14 November 2026: [ADR 369](../adr/0369-the-actor-of-a-job-trust-rules-and-external-service-principals.md).
