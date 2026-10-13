# 50. A forced OpenTofu state write goes through the API, not the CLI

Status: accepted · 2026-10-13

## Context

ADR 44 decision 4 makes a forced state write a capability: it needs the
action `unit:write-forced` and the forced-write switches of the namespace
and the unit. The CLI cannot use it. `tofu state push -force` sends no force
flag through the `cloud` block, the `remote` backend or the `http` backend
(RFC 65). The server sees an ordinary write. When the lineage differs or the
serial is lower, it refuses the write with the error of ADR 26 (422).

The project lead gave two options. Option (a): an RFC for an override that
accepts a lineage mismatch from the CLI, with an action and switches. Option
(b): the CLI path stays refused, and an administrator makes a forced write
through the API. The project lead recommended option (b). On 2026-10-13 the
project owner said: "All RFCs are reviewed and I agree with the
recommendations."

## Decision

1. **The CLI cannot force a write.** A state write without a force flag
   passes the lineage and serial checks of ADR 26, also when the CLI ran
   `tofu state push -force`. The server does not guess that a refused write
   was meant as a forced write.
2. **The API can force a write.** A forced write is a state-version create
   with the force flag set. It needs the action and the switches of ADR 44,
   and it writes the audit entry of ADR 44 decision 7. An administrator, a
   script or the console uses this route.
3. **The error names the way out.** When the server refuses a write for its
   lineage or serial, the error says that a forced write needs the API, the
   action `unit:write-forced` and the forced-write switches.

## Consequences

- A user who must push an old or foreign state asks an administrator, or
  uses the API with a principal that holds the action.
- The guide for OpenTofu state operations shows the API call for a forced
  write.
- If users need a forced write from the CLI, a new RFC can propose option
  (a).
