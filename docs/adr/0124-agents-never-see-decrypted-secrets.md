# 124. Agents never see decrypted secrets, with one gated escape hatch

Status: proposed · 2026-10-17

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
- On the hosted service: an opt-in for each account, then the same second
  stage.

## Decision

### The default

1. **Masked values only.** Each tool answers masked values (ADR 41). The
   only path to a value in plain text is `read_state` with
   `secretValues: true` (ADR 122 decision 5). That path checks
   `unit:read-secrets` on the unit, then the gate of this ADR. The gate is
   in the server, in the `Toolset`, before the read. No setting of a model,
   a prompt or a client can open it.
2. **The gate is a list of stages.** `agent.SecretAccess.Check` answers nil
   only when each stage that applies is open. Otherwise it answers
   `ErrSecretsClosed` and the error of the first closed stage. The stages
   are, in order: the installation stage (decisions 3 to 5, or decision 6
   on the hosted service), the namespace opt-in, the person opt-in, and the
   consent for one use (outside agents only). A table-driven test covers
   each stage.

### Self-hosted: the installation stage

3. **A long flag on the command line only.** The flag is
   `--unsafe-allow-agents-to-read-decrypted-secret-values`. It has no
   environment variable, no entry in the settings file and no settings
   API. The settings layers of ADR 63 never set it. An operator who wants
   it must type it in the command of each start. The server writes a
   warning to the log at each start with the flag.
4. **Maintenance mode at each start.** An instance that starts with the
   flag puts the installation in maintenance mode before it serves, and
   records its start in its version row (ADR 72 decision 11). The tool APIs
   answer 503 until a site administrator accepts.
5. **"I accept" after each start.** The route
   `POST /api/server/agent-secret-access/accept` takes the typed text
   `I accept`. It needs `server:accept-agent-secret-access` (only the site
   administrator holds it) and a console session with a fresh MFA step
   (ADR 56, ADR 71). An access token cannot accept. The acceptance names
   the start of each instance with the flag that runs at that time. It
   then leaves maintenance mode. An instance whose start has no acceptance
   keeps the gate closed, and a new start with the flag enters maintenance
   mode again. A start without the flag closes the gate on that instance.

### Hosted: the installation stage

6. **An opt-in for each account.** The hosted service has no flag. An
   account administrator opts in with the typed text `I accept` and a fresh
   MFA step. The route is `PUT /api/agent/secret-access` on the account,
   with `server:accept-agent-secret-access` on the account. The opt-out is
   the same route, and it closes the gate at once.

### The second stage, everywhere

7. **A namespace opt-in and a person opt-in.**
   - A holder of `namespace:manage-agent` opts a namespace in, with a fresh
     MFA step. The opt-in covers the units of that namespace.
   - A person opts in for itself with `caller:manage-agent-opt-ins`, with
     a fresh MFA step. The opt-in covers the runs and the MCP calls of that
     person.

   An opt-in stays recorded when the installation stage closes, and has
   no effect until it opens again. Each opt-in and each opt-out writes an
   audit entry (ADR 126).
8. **The console shows the risk to every person.** When the installation
   stage is open, the status route (ADR 120 decision 7) answers
   `secretAccess: "exposed"`. The console then uses a muted red theme and
   shows a banner on every page, for every person: "Agents can read secret
   values on this installation." The console repository implements the
   theme. This repository gives the field.
9. **What the agent keeps.** A value in plain text goes to the model in
   the tool result of its call only. The transcript stores that result
   with the values masked, so no table of the agent holds a secret value.
   The loop builds each later turn from the transcript, so the model sees
   the value in that one turn only.
   The model provider receives the value: the accept page and the opt-in
   pages say so.
10. **Consent for each use by an outside agent.** An MCP call and a stdio
    proxy call also need a consent for that one use (ADR 123 decision 10).
    The person approves it in the console with a fresh MFA step. A consent
    holds for one call, one tool, one unit and one token, for 5 minutes.
11. **Each value read is a `reveal` entry.** Each read of secret values in
    plain text writes an audit entry of the kind `reveal` (ADR 41), with
    the channel, the run or the MCP session, and the count of values. A
    read that the gate refuses writes an entry with the outcome `denied`
    and the name of the closed stage.

## Consequences

- To read one secret value through an agent on a self-hosted server, an
  operator restarts with the flag, a site administrator accepts with MFA,
  a namespace administrator opts in with MFA, and the person opts in with
  MFA. An outside agent also needs a consent with MFA for each call.
- Each start of an instance with the flag stops the tool APIs of the whole
  installation until a site administrator accepts. A rolling deploy of N
  instances with the flag needs N acceptances. This cost is on purpose:
  it makes the flag hard to keep on without a decision at each start.
- The flag breaks the rule that each flag has an environment variable
  (ADR 63). This ADR is the one exception.
- ADR 17 says that no feature exists only in a configuration. The flag is
  not configuration: each stage after it has a route and an action, as
  for the token that ADR 57 decision 6 reads before the server starts.

## Not decided here

- Whether a namespace opt-in can name some units only.
- Whether the hosted service allows the escape hatch on every plan.
