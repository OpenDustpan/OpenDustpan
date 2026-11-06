# 74. No phone-home, ever

Status: accepted · 2026-10-17

Changed by:

- ADR 315 extends decision 6: no visit counting on the docs site.

## Context

`README.md` and the security model say that the server sends no telemetry.
No ADR records this as a rule. Many open-source products add usage reports,
crash reports or install counts later, often behind an opt-out switch.

The word "telemetry" has two meanings. Products that phone home call their
usage reports "telemetry". OpenTelemetry calls the traces, metrics and logs
of an application "telemetry" too. A rule that says only "no telemetry" can
read as a ban on Prometheus metrics and OpenTelemetry traces. That is not
the rule.

The project owner decided: no phone-home, ever, and no phone-home code in
the open-source server.

## Decision

1. **The server never phones home.** Phone-home is data about an
   installation, its use or its users that leaves the installation for a
   destination that the operator did not name. Examples of such destinations
   are the project, a vendor and any other third party. Examples of such
   data are usage counts, feature use, crash reports, install pings, and
   version checks that send data to a project server.

   The test is the destination. Ask two questions. Who chose the
   destination? Does any data leave the installation before the operator
   names where it goes? The server sends data out only to a destination that
   the operator chose. Data that stays in the server, where the operator
   reads it, is not phone-home. A switch does not change the answer: opt-in phone-home, opt-out
   phone-home and phone-home that is off by default are all phone-home.
   A self-hosted installation sends nothing to the project, and no setting,
   also no opt-in setting, turns on such a report.
2. **No phone-home code.** The open-source server holds no code that collects
   data for phone-home or sends it. This includes code that is off by
   default, an opt-in switch, and a phone-home collector that has no
   destination yet. Reviews refuse it.
3. **The server connects out only to destinations that the operator names.**
   Examples are the database, the object store, sign-in providers, log sinks
   and metrics or trace collectors.
4. **Observability is not phone-home.** Observability is the metrics, logs,
   traces, probes and audit log of an installation, for its operator. It
   stays, and the project wants it. Observability data goes only to
   destinations that the operator configures, or the operator reads it from
   the server. Examples:
   - Prometheus metrics on an endpoint that the operator scrapes.
   - OpenTelemetry traces, metrics or logs that the server exports to an
     endpoint that the operator configures.
   - The log sinks and the audit log sinks that the operator configures.
   - Run data, for example the duration and the failures of jobs, that the
     server keeps in its own database for features of the installation.
5. **Dependencies.** Reviews refuse a dependency that phones home on its own,
   unless the code turns that behavior off. The pull request that adds the
   dependency says how.
6. **The same rule applies to the console that the server ships** and to the
   `opendustpan` commands. The console loads no analytics script.
7. **The hosted distribution** (ADR 59) observes its own installation with
   the tools of decision 4. It adds no phone-home code to the open-source
   server. This is not an exception to the destination test: in the hosted
   distribution the project is the operator, so the test allows its
   observability of the installations that it runs. That data stays in the
   systems of the hosted distribution.

## Consequences

- The project gets no usage data. Decisions about features use issues,
  discussions and the requests of users.
- A version check, if the project adds one, cannot call a project server.
  An operator can compare versions with `opendustpan upgrade check` or with
  the release page.
- Copy says "phone-home" or "usage reporting" for the forbidden thing and
  "metrics", "logs" and "traces" for observability. Copy does not say
  "telemetry" alone, because the word has the two meanings above.
- `README.md` and the security model state this rule in the same words.

Wording clarified on 2026-10-31 by the project owner. The decision did not
change.
