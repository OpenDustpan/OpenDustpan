# 74. No telemetry, ever

Status: accepted · 2026-10-17

## Context

`README.md` and the security model say that the server sends no telemetry.
No ADR records this as a rule. Many open-source products add usage reports,
crash reports or install counts later, often behind an opt-out switch.

The project owner decided: no telemetry, ever, and no telemetry code in the
open-source server.

## Decision

1. **The server sends no telemetry.** Telemetry is data about an
   installation, its use or its users that goes to the project, to a vendor
   or to any other party that the operator did not configure. Examples are
   usage counts, feature use, crash reports, install pings and version
   checks that call a project server.
2. **No telemetry code.** The open-source server holds no code that collects
   or sends telemetry. This includes code that is off by default, an opt-in
   switch, and a collector with no target. Reviews refuse it.
3. **The server connects out only to targets that the operator names.**
   Examples are the database, the object store, sign-in providers, log sinks
   and metrics targets.
4. **Observability is not telemetry.** Metrics, logs, traces, probes and the
   audit log stay. They go only to targets that the operator configures, or
   the operator reads them from the server.
5. **Dependencies.** Reviews refuse a dependency that sends data on its own,
   unless the code turns that behavior off. The pull request that adds the
   dependency says how.
6. **The same rule applies to the console that the server ships** and to the
   `opendustpan` commands. The console loads no analytics script.
7. **The hosted distribution** (ADR 59) observes its own installation with
   the tools of decision 4. It adds no telemetry code to the open-source
   server.

## Consequences

- The project gets no usage data. Decisions about features use issues,
  discussions and the requests of users.
- A version check, if the project adds one, cannot call a project server.
  An operator can compare versions with `opendustpan upgrade check` or with
  the release page.
- `README.md` and the security model already state this rule. They do not
  change.
