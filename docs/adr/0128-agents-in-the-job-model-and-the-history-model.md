# 128. Agents in the job model and the history model

Status: accepted · 2026-10-18 · supersedes parts of ADRs 100, 103, 120, 122, 124 and 125 (see "What this ADR replaces"). Decisions 11 to 13 superseded in part by ADR 190

## Context

Three designs merged on 2026-10-18 in parallel:

- the shared job model (ADRs 100 to 103),
- Dusty and the MCP endpoint (ADRs 120 to 127),
- unit history, redaction and safe delete (ADRs 150 to 153).

Each design names parts of the others. They do not agree in six places:

- ADR 125 decision 13 asks the job model for a full agent reference. ADR
  103 and the `job` package hold the agent as one string (`Agent string`).
- ADR 125 decision 2 puts a confirmation gate on the deliver phase of a
  write-back job of an agent in every mode. ADR 103 decision 12 gives a
  job in review-request mode no confirmation gate.
- ADR 125 decision 5 accepts a confirmation only from a console session.
  ADR 103 decision 2 accepts a request of the person with a fresh
  authentication, and does not refuse an access token.
- ADR 124 decision 12 refuses a job start with an access token that
  revealed secret values. ADR 103 has no such rule in `job.Service.Start`.
- ADR 103 decision 10 adds the field `Agent` to the hash encoding of an
  audit entry. ADR 126 decision 10 adds one agent block, `agent/1`.
- ADR 103 decision 7 and ADR 125 decision 6 let an agent record a
  rejection.

ADR 103 also keeps open whether a window of 0 of ADR 71 applies to a
confirmation. The history model (ADRs 150 and 151) does not say how a
change of an agent shows in blame, or what a redaction does to a copy of
an item in the transcript of a run.

The project manager decided on 2026-10-18, for the owner, after the review
of the three designs:

- N1: an agent that acts in a job is always identified by a full agent
  reference, never by a name only.
- N2: the confirmation of the deliver phase applies in every delivery
  mode. Where the ADRs do not agree, the table of ADR 125 decision 2 wins.
- N3: a confirmation needs an interactive console session. An access
  token cannot give a confirmation.
- N4: the job model refuses a start by an access token that revealed
  secret values in plain text.
- N5: one audit encoding for the actions of agents: the `agent/1` block of
  ADR 126.
- The maximum age of the authentication of a confirmation is the lower of
  15 minutes and W when W is above 0, else 15 minutes. W is the window of
  ADR 71.

The project owner decided on 2026-10-18:

- Dusty, and any other agent, never rejects a change. It can only leave
  blocking comments. This supersedes part of ADR 103 decision 7.
- A request-changes thread blocks approval until the reviewer or another
  approver resolves it. The author of the change never resolves it, and an
  agent never resolves it. Each dismissal goes to the audit log with a
  reason.

A change order is a later design above jobs. This ADR states the rules
for the approvals of a job. The same rules apply to the approvals of a
change order when that design lands.

ADR 1 forbids edits to the text of an accepted ADR. So this ADR records
what it replaces, and the status line of each ADR that it replaces names
it.

## Decision

### The agent of a job

1. **One agent reference (N1).** `job.Actor` holds the principal and an
   optional agent reference, `job.AgentRef`, in place of `Agent string`.
   `job.AgentRef` has the fields of `agent.Ref` (ADR 125 decision 9):
   - `Channel`: `agent`, `mcp` or `mcp-proxy`,
   - `RunID`: the run, for the channel `agent` only,
   - `SessionID`: the MCP session, for the channels `mcp` and `mcp-proxy`
     only.

   `AgentRef.Validate` has the rules of `Ref.Validate`: it refuses a
   reference with a channel and no ID, and a reference with the ID of the
   other channel. An empty reference means "no agent". `job.Job`,
   `job.Approval`, `job.DecisionInput` and each audit entry of a job carry
   the reference, never a name only. A table-driven test of `pkg/job`
   checks `Validate`.
2. **Neither domain package imports the other.** `pkg/job` defines
   `job.AgentRef` and does not import `pkg/agent`. `pkg/agent` does not
   import `pkg/job`. The internal wiring of the server implements
   `agent.Changes` on `job.Service` (ADR 125 decision 7), and converts
   `agent.Ref` to `job.AgentRef` field by field. A test of the wiring
   checks the conversion for each channel.
3. **The errors of the port.** The adapter of `agent.Changes` maps the
   errors of `job.Service` as follows. A test of the wiring checks each
   row.

   | `job` error | `agent` error |
   | --- | --- |
   | `ErrNotFound` | `ErrChangeNotFound` |
   | `ErrKeyConflict` | `ErrChangeKeyConflict` |
   | `ErrFrozen` | `ErrChangeFrozen` |
   | `ErrPolicyDenied` | `ErrChangePolicyDenied` |
   | `ErrFinished` (a cancel of a job that ended, or a comment on a job that is terminal) | `ErrChangeFinished` |
   | `ErrDelivered` (a cancel after a push or a merge) | `ErrChangeDelivered`, a new error |
   | `ErrAgentDenied` for a service principal | `ErrServicePrincipal` |
   | `ErrAgentDenied` for direct mode with a switch off | `ErrDirectNotAllowed` |
   | `ErrAgentDenied` when no confirmation is possible (decision 9) | `ErrConfirmationUnavailable`, a new error |
   | `ErrAgentDenied` for a cancel of a job of another person (decision 10) | `ErrChangeNotOwn`, a new error |
   | `ErrAgentDenied` for a runner job that changes, or a direct push, in review-request mode (ADR 103 decision 12) | `ErrModeNotSupported` |
   | `ErrAgentDenied` with any other cause | `job.ErrAgentDenied`, not changed |
   | `ErrStarterThread` (a blocking comment by `Job.StartedBy`, decision 11) | `ErrThreadOnOwnChange`, a new error |
   | `ErrTokenRevealed` (decision 8) | `ErrProposeAfterReveal` |
   | `ErrUnitBusy` | `ErrChangeBusy`, a new error |
   | `ErrLimit` | `ErrChangeLimit`, a new error |
   | `ErrUnsupported` | `ErrModeNotSupported` |
   | `ErrInvalid` | `ErrInvalidArguments` (ADR 122) |
   | `identity.ErrDenied` from `Get` | `ErrChangeNotFound`, as ADR 125 decision 7 says |
   | `identity.ErrDenied` from another method | `identity.ErrDenied`, not changed |
   | any other error | the same error, wrapped with `%w`. The tool answers it as an internal error, with no detail. |

   The `job` errors keep the reason in their text. The adapter tells
   `ErrAgentDenied` cases apart with `errors.Is` on a wrapped cause
   (`job.ErrServicePrincipal`, `job.ErrDirectSwitchOff`,
   `job.ErrNoConfirmation`, `job.ErrNotOwnJob`, `job.ErrModeRefused`),
   never by the text.

### The confirmation gate

4. **A confirmation of the deliver phase in every mode (N2).** The plan
   of a change of an agent follows the table of ADR 125 decision 2. A
   write-back job that an agent started has a confirmation gate on its
   deliver phase in each mode: review-request, approve-first, direct, and
   the default (no mode). A runner job of an agent has a confirmation gate
   on each phase that changes (apply, destroy, refresh) in approve-first
   mode and in the default, as ADR 103 decision 2 says, and none in direct
   mode. The column "apply" of the table of ADR 125 names this phase that
   changes. A runner job that only plans has no confirmation gate.
   `job.CheckAgent` gives these gates. Its table-driven test has one row
   for each row of the table of ADR 125, and one error row for a destroy
   in `direct` mode, which fails with `ErrAgentDenied`: a destroy is never
   direct.
5. **Only a console session gives a confirmation (N3).** `job.Service.Confirm`
   accepts a confirmation only when all of these are true:
   - the principal is `Job.StartedBy`, else `job.ErrNotRequester`,
   - the actor has no agent reference,
   - the credential of the request is an interactive console session,
     never an access token (`job.Actor.Credential`, decision 6),
   - the session has a fresh authentication (decision 7).

   An access token, also a personal token of the person who asked, fails
   with the new error `job.ErrSessionRequired`. So an agent that holds a
   token of its person cannot give the confirmation of its own change. A
   request that has an agent reference fails with `ErrAgentDenied`, as in
   ADR 103 decision 7.
6. **The job actor names the credential.** `job.Actor` gets the field
   `Credential`: the kind (`session` or `token`) and the ID of the console
   session or of the access token. The transport sets it from the
   authentication of the request, as the run does (ADR 120 decision 13).
   The job model uses it for decision 5 and decision 8. It stores the kind
   and the ID with the confirmation, never the credential.
7. **The maximum age of the authentication.** The authentication of a
   confirmation is at most the lower of 15 minutes and W when W is above
   0, else 15 minutes. W is the token freshness window of the session
   policy (`tokenFreshnessSeconds`, ADR 71). A window of 0 turns off the
   check of ADR 71 for tokens. It does not turn off the check of a
   confirmation. A session policy that cannot be read gives 15 minutes,
   never no limit. `job.ConfirmMaxAge(window)` holds the rule, and a
   table-driven test covers 0, 60 seconds, 15 minutes, 7 days and a policy
   that cannot be read.
   The same maximum age applies to a new MFA step (ADR 56) and to a new
   SSO authentication. A confirmation with an older authentication fails
   with `ErrAuthStale`. This answers the open question of ADR 103 on a
   window of 0.
8. **No job start after a reveal (N4).** `job.Service.Start` refuses a
   start whose credential is an access token that revealed secret values
   in plain text. The job model asks the new port `job.RevealMarks`, which
   the wiring implements with `agent.ConsentStore.Revealed` (ADR 124
   decision 12). The port gets the token ID from `job.Actor.Credential`
   and the MCP session ID from the agent reference (`SessionID`, empty for
   the channel `agent` and with no agent). The refusal is
   `job.ErrTokenRevealed`. `Start` refuses each actor whose
   `Credential` kind is empty or unknown with `ErrInvalid`, so a missing
   credential never skips the check. A start that the server makes
   itself, for example by the Catalog, sets an explicit credential kind.
   An error of the port fails the start, and the start creates no job:
   the check fails closed. It applies to each
   caller of `job.Service`: the REST API, the Catalog and the agent. A run
   with `Run.ReadPlainSecrets` stays refused by the `Toolset` before the
   port (ADR 124 decision 12). A console session is not marked: the mark
   is on the token and on the MCP session, as ADR 124 says. The
   conformance suite `jobtest` gets a case for a marked token, an
   unmarked one and an error of the port. When the code lands, `jobtest`
   also gets a case for an actor with an empty or unknown `Credential`
   kind: `Start` fails with `ErrInvalid` and creates
   no job.
9. **A person who cannot give a confirmation gets no change of an agent.** With
   decision 4, every change of an agent waits for a confirmation, also in
   review-request mode. When the server cannot ask the person for a fresh
   authentication, or the principal cannot sign in to the console, nobody
   can pass the gate. So `job.Service.Start` refuses each change of an
   agent for that person with `ErrAgentDenied` (cause
   `job.ErrNoConfirmation`) at once, and creates no job. A runner job
   that only plans still starts. ADR 103 decision 12 sent such a person to
   review-request mode. That is no longer a path.

### Agents never reject

Decisions 10 to 14 apply to an object that takes approvals. Today that
object is a job, so the text names the job.

10. **An agent records no verdict.** `job.Service.Approve` refuses each
    verdict, `approve` and `reject`, whose actor has an agent reference,
    with `ErrAgentDenied`, on each gate. `job.Service.Confirm` refuses a
    rejection of a confirmation by an agent in the same way. A cancel is
    not a verdict. An agent cancels only a job whose `StartedBy` is its own
    person, with `job:cancel` as for every principal. A cancel by an agent
    of any other job fails with `ErrAgentDenied` (cause
    `job.ErrNotOwnJob`), also when the person holds `job:cancel` on the
    unit. This narrows the row `cancel_change` of ADR 122 decision 1.
11. **The job model accepts a blocking comment from an agent.**
    `job.Service.Comment` adds a comment to a job that is not terminal.
    A comment on a terminal job fails with `ErrFinished`. A comment holds
    the text (at most 4,000 bytes), the author (the principal and the agent reference,
    if any), the time, and the flag `Blocking`. A comment with
    `Blocking: true` opens a **request-changes thread** on the job. A
    comment needs `job:read` on the unit. A blocking comment also needs
    `job:approve` on the unit, for the person and so for an agent of that
    person. The principal of `Job.StartedBy` cannot open a thread on a job
    that they started: a thread asks the author of the job for a change.
    A blocking comment by that principal fails with the new error
    `job.ErrStarterThread`, also through an agent.
    The server treats the text of a comment as untrusted text under the
    rules of ADR 124 decision 13. Each answer to an agent and the console
    label it with its author, as data, never as instructions.
12. **An open thread blocks approval.** While a job has an open
    request-changes thread, these fail with `job.ErrChangesRequested`, and
    name the thread:
    - an `approve` verdict on an approval gate,
    - a confirmation.

    A `reject` verdict by a person, a cancel and the expiry of a gate
    still apply. The thread does not stop the timeout of a gate. While a
    thread is open, `job.GatesStatus` reports each gate of each phase that
    did not dispatch as `waiting`, with the reason `changes-requested`. The
    dispatch check of the store reads the open threads in the same
    transaction as the gates, so a phase never dispatches while a thread is
    open. An approval that a person recorded before the thread opened
    stays recorded, and counts again when no thread is open. When the
    gate binds approvals to the plan, a replan still clears them (ADR 102
    decision 7).
13. **Who resolves a thread.** `job.Service.ResolveThread` closes a
    request-changes thread. It accepts only:
    - the person who opened the thread, in a request with no agent
      reference, or
    - an approver who may dismiss, in a request with no agent reference:
      a principal who may record an `approve` verdict on an approval gate
      of the job (`job:approve` on the unit and the groups of that gate),
      whatever the status of the gate. That principal is never the author
      whom the thread asks for a change: never `Job.StartedBy`, never an
      agent.

    It refuses `Job.StartedBy` with the new error `job.ErrSelfResolve`,
    also when that person holds `job:approve`. It refuses each actor with
    an agent reference with `ErrAgentDenied`, also for a thread that an
    agent of the same person opened. So an agent
    cannot clear a block, also not its own. A thread that an agent opened
    has the person of the agent as its author: that person can resolve it
    in a request of their own, unless that person is `Job.StartedBy`.
    With no approval gate, only the person who opened the thread resolves
    it, so the job waits until a gate expires, if it has one, or someone cancels it.
14. **A dismissal needs a reason.** A resolve by anyone other than the
    author of the thread is a **dismissal**. A dismissal needs a reason of
    1 to 1000 characters of plain text with no control characters, as for
    the reason of ADR 151 decision 27, else it fails with `ErrInvalid`. A
    resolve by the author can have a reason with the same limits.
15. **Audit entries for comments and threads.** Each of these writes one
    audit entry of the kind `change` in the transaction of the change, with
    the unit reference as the object, the job ID, and the agent block of
    decision 17 when an agent acted:

    | Event | Subject | Action |
    | --- | --- | --- |
    | Comment, blocking or not | the author | `job:read`, or `job:approve` for a blocking comment |
    | Resolve by the author | the author | `job:approve` |
    | Dismissal | the person who dismisses | `job:approve`, with the reason in the field `reason` |

    The entry never holds the text of the comment. The job record holds
    it. A comment is namespace data with the fence of its job (ADR 100
    decision 13), and the backup of a namespace holds it.
16. **Not in the tool catalog today.** `agent.Changes` gets the method
    `Comment` for a later tool. The catalog of ADR 122 has 12 tools, and a
    13th tool needs an ADR (ADR 122 decision 14). So no agent tool comments
    today. An agent that finds a problem in a job tells its person in its
    answer.

### One audit encoding

17. **The `agent/1` block for each entry of an agent (N5).** The job model
    writes the agent of each of its audit entries in the agent block of ADR
    126 decision 10, never in a field of its own. `audit.Entry` gets no
    field `Agent`. The job model writes the block only when the actor has
    an agent reference. For a job entry with an agent:
    - `agentRun` is the run ID for the channel `agent`, else empty,
    - `agentSession` is the MCP session ID, or the run ID for the channel
      `agent`, as ADR 126 decision 2 says,
    - `breakGlass` is true when the credential belongs to a break-glass
      session,
    - `tool`, `argsDigest` and `secretStage` are empty.

    The field `channel` of the entry holds the channel of the agent. So an
    entry with no agent hashes as before, and one encoding covers the
    entries of the `Toolset` and of the job model. The test of ADR 126
    decision 10 gets a case for a job entry with an agent.

### History

18. **Blame names the agent through the job.** Each change of an agent
    reaches the state of a unit through a job (ADR 125 decision 7). The
    actor of a history item (ADR 150 decision 3) holds the job ID, and the
    job holds the agent reference. So `history.Actor` gets no agent field.
    Blame and the point-in-time views show the agent of a change from the
    job of its item. A state write with no job ID has no agent.
19. **A transcript copy goes with its history item.** ADR 120 decision 22
    stores the masked tool results of a transcript in a plain table, and
    seals only reveals. Decision 19 changes that table for one kind of
    result. A tool result in a transcript can hold content of a history
    item:
    `read_state` holds content of one or two state items, and
    `read_run_output` holds content of a job log or of an update. The store
    records with each such result the item keys of its history items (ADR
    151 decision 1). It seals the result with a fresh data key. It wraps
    the data key with the first item key, then wraps that output with the
    next item key, and so on. So a read of the result needs every item key,
    and a shred of any one of them makes the result unreadable. This is
    also true for the copy of the transcript in a backup. A test of the
    store seals a result with two item keys, shreds one of them, and checks
    that the result cannot be read.
    - A redaction or a purge (ADR 151, ADR 153) of an item shreds the item
      key, so the transcript copies of the item cannot be read. The
      transcript route answers such a result as `redacted`, with no part of
      its content.
    - A result of any other tool holds no content of a history item. It
      stays in the plain table, as ADR 120 decision 22 says.
    - The model provider got the content when the run ran. No redaction
      reaches that copy. The redaction page says so for an item that a run
      read.
20. **Agents never redact, purge or delete.** No agent tool needs
    `unit:redact`, `unit:purge` or `unit:delete` (ADR 151, ADR 153). A new
    tool with one of these actions needs an ADR (ADR 122 decision 14).
21. **The retention of transcripts stays.** A transcript keeps its own
    retention of 30 days (ADR 120 decision 22). The retention of plans and
    job logs (ADR 152) does not change it. A result whose item expires
    becomes unreadable at the shred of its item key, as in decision 19.

### Open items of the three reviews

22. **The open items of the final reviews, and what this ADR does with
    them.** The three pull requests that added these ADRs had no review
    left open on the forge. Their descriptions list open items. Each item
    here concerns more than one design:

    | Item | From | Resolution |
    | --- | --- | --- |
    | The full agent reference in place of `Agent string` on `job.Actor`, `Job`, `Approval` and `DecisionInput` | ADR 125 decision 13 | Decisions 1 and 2 |
    | A confirmation gate kind that only `StartedBy` passes, from a console session with a fresh authentication | ADR 125 decision 13 | Decisions 4 and 5. The gate kind exists (`job.GateConfirmation`). |
    | A start of a service principal through an agent fails, mapped to `ErrServicePrincipal` | ADR 125 decision 13 | Decisions 3 and 9 |
    | The agent fields in each audit entry of a job | ADR 125 decision 13, ADR 103 decision 10 | Decision 17 |
    | A cancel of a job that ended answers `ErrFinished` | ADR 125 decision 7 | Decision 3. `job.CheckCancel` already answers it. |
    | The job routes refuse a start with a token that used a consent | ADR 124 decision 12 | Decision 8 |
    | Does a window of 0 of ADR 71 apply to a confirmation? | ADR 103, open question | Decision 7 |
    | Redaction and purge cannot reach copies outside the item | ADR 151 | Decision 19 for transcripts. Copies outside the server stay out of reach, as ADR 151 says. |
    | Status notes on ADRs 13, 20, 28, 30, 37, 38, 41, 43 and 47 for ADRs 150 and 151 | ADR 150, ADR 151 | Done before this ADR. Each of these ADRs has the note. |
    | The `reason` field of `audit.Entry` (ADR 151 decision 26) | ADR 151 | A dismissal (decision 14) puts its reason in that field only, with the limits of ADR 151 decision 27. |
    | Masking matches exact strings only | ADR 124 decision 12 | No change. The confirmation of decision 4 is the guarantee, in every mode. |

## What this ADR replaces

- **ADR 100, decision 17:** "Each method takes a `job.Actor`: the
  principal of the request, and the agent that acts for it, if any, with
  the mode of the agent." The agent is a `job.AgentRef` (decision 1), and
  the actor also names its credential (decision 6). `Actor.Mode` stays. `Comment` and
  `ResolveThread` join the methods of `job.Service`.
- **ADR 103, decision 2:** "one confirmation by the starter, in a request
  of their own with no agent, with a fresh authentication". It also needs
  a console session (decision 5). "The authentication is at most 15
  minutes old (`job.ConfirmFreshness`)" becomes the lower of 15 minutes
  and W when W is above 0, else 15 minutes (decision 7).
  `job.ConfirmFreshness` becomes `job.ConfirmMaxAge`. "for a job that an
  agent started in approve-first mode": decision 4 widens it to the
  deliver phase of a write-back job in every mode. "When the server can
  ask the person for neither, for example in an installation with static
  tokens only, approve-first mode is not available for the agent jobs of
  that person": decision 9 replaces it, and `Start` refuses each change of
  an agent for that person.
- **ADR 103, decision 7:** "An agent can record a rejection, because a
  rejection only stops a change." An agent records no verdict, and can
  leave a blocking comment (decisions 10 to 16).
- **ADR 103, decision 9:** the rows "the agent of a rejection" in the
  table. An agent records no rejection. The table gets the rows of
  decision 15.
- **ADR 103, decision 10:** the field `Agent` of `audit.Entry` and its
  place in the hash encoding. Decision 17 uses the agent block of ADR 126.
  Also the last sentence: "An agent needs no audit channel of its own: the
  channel stays where the request came from." The field `channel` of an
  entry of an agent holds the channel of the agent (decision 17).
- **ADR 103, decision 12:** "The review of the forge is the check of a
  person, so the job gets no confirmation gate", and the rule that sends
  a person with no fresh authentication to review-request mode. Decisions
  4 and 9 replace them.
- **ADR 103, Consequences:** "In an installation with static tokens only,
  approve-first mode is not available: an agent there works in
  review-request mode". Decision 9 replaces it: an agent there starts no
  change.
- **ADR 120, decision 22:** "The transcript holds only masked values" in
  a plain table. A tool result that holds content of a history item is
  sealed with the item keys of its items (decision 19).
- **ADR 122, decision 1:** the row `cancel_change`, "cancels a change that
  did not end", with `job:cancel`. An agent cancels only a job that its
  person started (decision 10).
- **ADR 124, decision 12:** "the job routes refuse a start with that
  token too". The job model refuses it in `job.Service.Start`, for each
  caller (decision 8). "passes its confirmation gate in the console with
  a fresh MFA step (ADR 125 decision 5)": the fresh authentication is an
  MFA step or an SSO authentication, with the maximum age of decision 7.
- **ADR 125, decision 5:** "with a fresh MFA step". The fresh
  authentication is an MFA step or an SSO authentication, as decision 7
  says.
- **ADR 125, decision 6:** "An agent may record a rejection". An agent
  records no verdict (decision 10).
- **ADR 125, decision 7:** the row `Approve`, "the job model refuses an
  approve verdict of an agent and accepts a reject", and the row `Cancel`
  with `job:cancel` only. The job model refuses both verdicts of an agent,
  and an agent cancels only a job that its person started (decision 10).
- **ADR 125, decision 13:** "A reject verdict of an agent is allowed".
  Decision 10 replaces it.

## Consequences

- `pkg/job` changes: `job.AgentRef`, `job.Actor.Credential`,
  `job.ConfirmMaxAge`, the port `job.RevealMarks`, the methods `Comment`
  and `ResolveThread`, and the errors `ErrSessionRequired`,
  `ErrTokenRevealed`, `ErrChangesRequested`, `ErrSelfResolve`,
  `ErrStarterThread`, and the
  causes `ErrServicePrincipal`, `ErrDirectSwitchOff`, `ErrNoConfirmation`,
  `ErrNotOwnJob` and `ErrModeRefused`. `jobtest` gets a case for
  each rule of decisions 4 to 15.
- `pkg/agent` gets the errors `ErrChangeDelivered`, `ErrChangeBusy`,
  `ErrChangeLimit`, `ErrConfirmationUnavailable`, `ErrChangeNotOwn` and
  `ErrThreadOnOwnChange`, and
  the method `Changes.Comment`.
  `agenttest.RunChanges` gets cases for an agent reject (refused), a
  blocking comment, and a confirmation from a token (refused).
- `pkg/audit` gets no field `Agent`. The job model writes the agent block.
- Each SQL store gets a table of job comments and threads. It is a data
  table.
- In an installation with static tokens only, an agent can plan and read,
  and cannot change a unit. A person changes the unit without the agent.
- A team that wants a second person on each change of an agent still adds
  a policy gate (ADR 103). A request-changes thread is the way for a
  reviewer, or an agent of a reviewer, to stop a job without a rejection.
- `GLOSSARY.md` gets the terms Request-changes thread and Dismissal, and
  the terms Confirmation gate, Agent mode and Agent ref change.

## Not decided here

- A tool for agents that comments on a job.
- Comments that are not blocking on a Catalog request outside its job.
- Whether a forge review comment of a review request opens a
  request-changes thread on its job.
