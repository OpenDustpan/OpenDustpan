# 124. Agents never see decrypted secrets, with one gated escape hatch

Status: accepted · 2026-10-17. Decision 12 superseded in part by ADR 128. Route paths and `server:` action names superseded in part by ADR 210. Decision 6 superseded in part by ADR 216: the opt-in is `namespace:accept-agent-secret-access` on one namespace

Changed by:

- ADR 128 changes decision 12: the job model refuses a start after a reveal.
- ADR 210 changes: the route paths and the server: action names.
- ADR 216 changes decision 6: the opt-in is a namespace action.

## Context

ADR 41 makes the server read state and mask each secret value. ADR 120
records the built-in agent, Dusty, and ADR 123 offers its tools to outside
agents. A secret value that reaches a model leaves the installation to the
provider of the model, and stays in the context of the run.

The project owner decided on 2026-10-17:

- Dusty never sees decrypted secrets.
- One escape hatch exists. On a self-hosted server it needs, in this order:
  a long, explicit command-line flag (no environment variable, no settings
  file), maintenance mode and an "I accept" click on every start, then an
  opt-in of the namespace and an opt-in of the user. The console then shows
  a muted red theme with a warning banner. MCP clients and the CLI proxy
  also need an opt-in for each use.
- **Hosted: per-account opt-in plus the same second stage** (owner
  decision of 2026-10-17, through the project manager). A hosted customer
  cannot set a server flag or maintenance mode, so the account opt-in takes
  their place.

The project manager decided on 2026-10-17 how "every start" works with
several instances: one acceptance for each maintenance window of an
installation covers every instance that started with the flag in that
window.

## Decision

### The default

1. **Masked values only.** Each tool answers masked values (ADR 41). The
   only path to a value in plain text is `read_state` with
   `secretValues: true` (ADR 122 decision 7). That path checks
   `unit:read-secrets` on the unit, then the gate of this ADR. The gate is
   in the server, in the `Toolset`, before the read. No setting of a model,
   a prompt or a client can open it.
2. **The gate is a list of stages.** `agent.SecretAccess.Check` answers nil
   only when each stage that applies is open. Otherwise it answers
   `ErrSecretsClosed` and the error of the first closed stage, and
   `agent.SecretStage` names that stage for the result and the audit
   entry. The stages are, in order: the installation stage (decisions 3 to
   5, or decision 6 on the hosted service), the namespace opt-in, the
   person opt-in, and the consent for one use (outside agents only). A
   table-driven test covers each stage.

### Self-hosted: the installation stage

3. **A long flag on the command line only.** The flag is
   `--unsafe-allow-agents-to-read-decrypted-secret-values`. It has no
   environment variable, no entry in the settings file and no settings
   API. The settings layers of ADR 63 never set it. An operator who wants
   it must type it in the command of each start. The server writes a
   warning to the log at each start with the flag.
4. **One maintenance window for each acceptance.** An instance that starts
   with the flag records its start through `agent.WindowStore.JoinOrOpen`
   before it serves:
   - When no window is open, it opens a new window and puts the
     installation in maintenance mode, in one transaction.
   - When a window is open, the instance joins it.

   The tool APIs answer 503 while the installation is in maintenance mode.
   An instance with the flag keeps the hatch shut until its window has an
   acceptance (`SecretWindow.Covers`). An administrator can end the
   maintenance mode another way: the window then closes with no acceptance
   (`WindowStore.Close`, which appends its audit entry in the same
   transaction), and the hatch of its instances stays shut until they start
   again. An instance that starts without the flag records it with
   `WindowStore.Leave`, so its earlier window no longer counts for it. An
   administrator also calls `Leave` for an instance that stopped for good.
5. **"I accept" once for each window.** The route
   `POST /api/server/agent-secret-access/accept` takes the typed text
   `I accept`. It needs `server:accept-agent-secret-access` (only the site
   administrator holds it) and a console session with a fresh MFA step
   (ADR 56, ADR 71). An access token cannot accept. `agent.Acceptance`
   holds the subject, the text, the time of the MFA step and the time, and
   `Acceptance.Validate` refuses another text or an MFA step older than 5
   minutes. One acceptance covers every instance of the window and ends the
   maintenance mode. A later start with the flag, outside maintenance mode,
   opens a new window, and needs a new acceptance. So a rolling deploy needs
   one acceptance when its instances start in one window. A start without
   the flag closes the hatch on that instance.

### Hosted: the installation stage

6. **An account opt-in, as strong as the self-hosted stage.** This is the
   decision of the owner (2026-10-17): per-account opt-in plus the same
   second stage. The account opt-in replaces the server flag and the
   maintenance mode, which a hosted customer cannot set.
   - Only an account administrator can opt in, with
     `server:accept-agent-secret-access` on the account, in a console
     session with a fresh MFA step. An access token cannot.
   - Each enablement needs the typed text `I accept`: a first opt-in, and
     each opt-in after an opt-out.
   - Each opt-in and each opt-out writes an audit entry (ADR 126 decision
     7). `agent.AccountSecretOptIn` holds the acceptance and the time of
     the opt-out.
   - The opt-out closes the gate at once.

   The route is `PUT /api/agent/secret-access` on the account. Then the
   same second stage applies (decisions 7 to 10): the namespace and person
   opt-ins, the red theme and the banner, and the consent for each use by
   an MCP client or the CLI proxy.

### The second stage, everywhere

7. **A namespace opt-in and a person opt-in.**
   - A holder of `namespace:manage-agent` opts a namespace in, with the
     typed text `I accept` and a fresh MFA step. The opt-in covers the
     units of that namespace.
   - A person opts in for itself with `caller:manage-agent-opt-ins`, with
     the typed text `I accept` and a fresh MFA step. The opt-in covers the
     runs and the MCP calls of that person.

   An opt-in stays recorded when the installation stage closes, and has
   no effect until it opens again. Each opt-in and each opt-out writes an
   audit entry (ADR 126). `agent.SettingsStore` refuses an opt-in whose
   acceptance fails `Acceptance.Validate`.
8. **The console shows the risk to every person.** The exposure is
   installation-wide: the installation is exposed while any instance is
   covered by an accepted window (`WindowStore.Exposed`: an accepted window
   covers an instance whose latest record is its join of that window), or,
   on the hosted service, while the account opt-in is active.
   `agent.Exposure.Exposed` combines the two. It does not depend on the
   instance that answers, on a namespace or on a person: an instance without
   the flag answers "exposed" too while another instance has the hatch
   open. An instance that stops for good stays counted until a `Leave`, so
   the console warns longer than necessary, never shorter. While the
   installation is exposed, the status route (ADR 120 decision 7) answers
   `secretAccess: "exposed"`. The console then uses a muted red theme and
   shows a banner on every page, for every person: "Agents can read secret
   values on this installation." The console repository implements the
   theme. This repository gives the field.
9. **Where a revealed value lives.**
   - The tools step that reads plain values seals the tool result with a
     data key of its own, wraps the data key with the key-encryption key
     (ADR 23), and stores it as an `agent.SealedReveal` in its own table.
     The store writes it in the transaction of the record of the step. The
     transcript gets the result masked.
   - The port `agent.RevealSealer` does the seal and the open. Package
     `secret` implements it: `Seal` makes a fresh data key for each reveal,
     seals the content and the values with it, and wraps it with the
     `secret.Wrapper` of the key-encryption key. The run, the turn and the
     call are the additional data of each seal, so a sealed reveal opens
     only for the call that made it. `Open` answers `ErrRevealOpen` for a
     reveal that changed, with no part of the content in the error.
     `agenttest.RunRevealSealer` checks a seal and an open, no plain value
     in a seal, a fresh key for each seal, a changed reveal, and a reveal
     that is not valid.
   - Each sealed reveal has a hard expiry: its time plus the time limit of
     the run (`SealedReveal.ExpiresAt`, `agent.RevealExpiry`). After it,
     the store answers no reveal, and the reaper deletes it (ADR 120
     decision 10). So a plain value never outlives its run, also when the
     durable runner fails the turn for good and no step shreds it. The
     reaper also ends that run, which shreds its reveals and frees its
     place in the limits of active runs.
   - The model step of the next turn unwraps the sealed result and puts
     the plain values into that one tool result of its request. When
     another instance takes the turn over before that step records its
     answer, it reads the sealed result again, so the value never needs a
     copy outside the store.
   - Before it records the answer, the model step masks each revealed
     value in the text of the answer and in the arguments of its tool calls
     (`agent.MaskMessage`). Its record shreds every sealed reveal of the
     run, in the same transaction: the store deletes the wrapped key and
     the content. The end of the run shreds them too, also an end by the
     reaper. The table of sealed reveals is not in the backup, so a restore
     brings no reveal back.
   - The loop builds each later turn from the transcript, so the model sees
     a value in one model call. A tool call that the model wrote with a
     value runs with the masked arguments.

   The model provider receives the value: the accept page and the opt-in
   pages say so.
10. **Consent for each use by an outside agent.** An MCP call and a stdio
    proxy call also need a consent for that one use (ADR 123 decision 10).
    The person approves it in the console with the typed text `I accept`
    and a fresh MFA step. A consent holds for one call, one tool, one unit,
    one token and one MCP session, for 5 minutes (`Consent.Usable`).
    - A refused read is a successful result with the masked values, the
      closed stage, and the `consentID` and `consentURL` of a pending
      consent (ADR 122 decision 7).
    - The retry passes `consentID`. `agent.ConsentStore.Lookup` finds the
      consent only for the person, the token, the session, the tool and the
      unit that it was made for. `ConsentStore.Use` marks it used in one
      transaction, so of two concurrent uses at most one succeeds, and a
      second use is refused.
    - A principal that cannot sign in to the console has nobody who can
      approve a consent: its secret read fails at once with
      `ErrServicePrincipal`.
11. **Each value read is a `reveal` entry.** Each read of secret values in
    plain text writes an audit entry of the kind `reveal` (ADR 41), with
    the channel, the run or the MCP session, and the count of values. A
    read that the gate refuses writes an entry with the outcome `denied`
    and the name of the closed stage.
12. **No change after a reveal.** A run that read plain values gets
    `Run.ReadPlainSecrets`, and so does each follow-up run of its
    conversation. For an outside agent, the mark is on the access token, not
    only on the MCP session: `ConsentStore.Revealed(tokenID, sessionID)` is
    true after a consent was used with the token or in the session. So a
    new MCP session after an `initialize` with the same token is in the
    same state, and the job routes refuse a start with that token too. The
    mark lasts as long as the token. In that state, `propose_change` fails
    with `ErrProposeAfterReveal`. Masking matches values exactly, so it
    misses a value that the model encodes or splits. A run that knew a
    value never writes to a forge or a unit.

    This mark is defense in depth, not the guarantee. An outside agent that
    read a value can still use another token of its person, another client,
    or text outside OpenDustpan, and no server can see the memory of a
    model. What holds in every case is the confirmation gate: no change of
    an agent applies or reaches a forge before the person who asked reads it
    and passes its confirmation gate in the console with a fresh MFA step
    (ADR 125 decision 5), and a service principal cannot start one at all.

### Untrusted text and exfiltration

13. **The threats, and what stops each one.** Text that someone other than
    the person wrote reaches the model through these paths. The system
    prompt says that it is data, never instructions, but the prompt is not
    the defense. The defense is in the server:

    | Path in | Source of the text | Defense |
    | --- | --- | --- |
    | `read_state`, `describe_unit` | state attributes, outputs, tags of resources | masked values, act-as on each call |
    | `read_run_output` | logs and events of updates and jobs | masked values, `job:read-logs` |
    | `list_history` | commit messages and update messages | act-as, text labeled as written by a person |
    | `describe_unit` | the `opendustpan` output: runbook, summary, links (ADR 127) | strict parse, labeled by source |
    | `describe_unit` | the Pulumi `readme` output and object metadata | labeled by source, size limits |
    | `search_units`, `describe_unit` | labels and names | labeled data, length limits of the parameters |
    | `observe_unit` | events and health messages of the cloud | read-only credentials, masked values |
    | `search_docs` | the docs in the binary | written by the project, versioned |

    | Path out | Risk | Defense |
    | --- | --- | --- |
    | `propose_change` to write-back | an agent writes a value or a link into a commit, a branch name or the text of a review request | the deliver phase waits for the confirmation of the person (ADR 125 decision 5). No propose after a reveal |
    | `propose_change` to a runner | a job that sends data out | the apply phase waits for the confirmation in approve-first. No propose after a reveal |
    | an answer in the console | a remote image or link that a browser loads by itself, with data in its URL | the console renders no remote image and loads no link by itself. A link opens only on a click and shows its full address |
    | a carry across namespaces | text of namespace A reaches a call on namespace B in one run | act-as and `agent:use` on each namespace. The confirmation shows the unit and the full change |
    | an answer to an MCP client | the outside agent gets what its token can read | the same tools and checks as the API. The consent for each secret read |
    | the model provider | the tool results of each run | the connect page says so. A self-hosted model keeps the data inside |

## Consequences

- The status route reads `WindowStore.Exposed` on each call, so the banner
  shows on every instance while one instance has the hatch open.
- To read one secret value through an agent on a self-hosted server, an
  operator restarts with the flag, a site administrator accepts with MFA,
  a namespace administrator opts in with MFA, and the person opts in with
  MFA. An outside agent also needs a consent with MFA for each call.
- Each window stops the tool APIs of the whole installation until a site
  administrator accepts. This cost is on purpose: it makes the flag hard to
  keep on without a decision after each restart.
- On the hosted service, an account administrator makes the same decision
  with MFA and typed text, and the audit log keeps it.
- The flag breaks the rule that each flag has an environment variable
  (ADR 63). This ADR is the one exception.
- ADR 17 says that no feature exists only in a configuration. The flag is
  not configuration: each stage after it has a route and an action, as
  for the token that ADR 57 decision 6 reads before the server starts.
- The window table is a control table (ADR 72 decision 5): a restore does
  not bring an acceptance back.

## Not decided here

- Whether a namespace opt-in can name some units only.
- Whether the hosted service allows the escape hatch on every plan.
