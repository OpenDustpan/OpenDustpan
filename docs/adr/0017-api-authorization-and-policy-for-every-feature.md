# 17. Every feature has a REST API, authorization and policy

Status: accepted · 2026-10-10. The policy check superseded in part by ADR 224

Changed by:

- ADR 30 changes: an offline restore command as an exception to the API rule.
- ADR 210 extends: the prefix and shape of the tool-neutral API.
- ADR 222 changes: the policy check on every change.
- ADR 224 changes: the policy check on every change.
- ADR 266 extends: the saved view API.
- ADR 305 extends: the operator uses only the REST API.

## Context

RFCs 5 to 56 propose many features: profiles, runners, approvals,
blueprints, drift, audit, and more. Other products ship some features only in
their console, only in their CLI, or only in a configuration file. Teams then
cannot script them, give them to an agent, or control who uses them.

OpenDustpan's clients will include the console, a CLI (RFC 48), providers
that manage OpenDustpan as code (RFC 48), an MCP server (RFC 41), developer
portals (RFC 51) and scripts. Each of them can only do what the API allows.

Today the `Authorizer` port takes a namespace, and the only adapter allows
administrators. ADR 13 says that authorization moves to tool-neutral actions on
units. ADR 5 allows console-only endpoints. No rule says that every endpoint
checks access or that every change can be stopped by a policy.

The project owner made this decision on 2026-10-10.

## Decision

- **A REST API for every feature.** Each feature that OpenDustpan ships has a
  documented REST API. The console, the CLI and every other client use only
  that API. No feature exists only in the console, only in a CLI, or only in a
  configuration file.
- **Each tool keeps its own protocol.** The Pulumi Cloud-compatible API and the
  TFE-compatible API keep the shapes that their tools expect. OpenDustpan's own
  objects (namespaces, users, teams, tokens, profiles, runners, jobs, policies,
  blueprints, the audit log) get an OpenDustpan API with its own route prefix.
  Its shape is a separate RFC.
- **Fine-grained authorization on every endpoint.** Each endpoint, in every
  API, declares the tool-neutral actions that it needs and the object that
  each action applies to: a namespace, a unit, or another object such as a
  profile or a runner. The `Authorizer` checks them before the endpoint acts.
  A list returns only the objects that the caller can read, and says when it
  removed objects (RFC 40).
- **A policy check on every change.** Each action that changes something
  passes a policy check before it takes effect, so a policy can deny it or
  make it wait for approval (RFC 13). With no policy, the check allows the
  action.
- **The shape is open.** This ADR does not choose a role model (RFC 19), an
  authorization model (roles, relations or attributes), or a policy engine
  (RFC 13). Those RFCs must meet this ADR.
- **A test enforces it.** A test lists every route that the server registers
  and fails when a route has no declared action. Routes that need no sign-in
  (service discovery, capabilities, health) are on an explicit list in the same
  test.
- **Exception: process settings.** Settings that the server reads before it
  starts (flags and environment variables, such as the database URL) are not
  features in this sense. Settings that change at run time are.

## Consequences

- ADR 5 allows "console-only endpoints". From now on, an endpoint that the
  console needs is a public API endpoint with its own action. ADR 1 forbids
  edits to ADR 5, so this ADR records the change.
- The `Authorizer` port must take an object reference, not only a namespace.
  ADR 13 already expects this. The current admin-only adapter meets the new
  shape until RFC 19 adds roles.
- Every new endpoint lands with its action, its policy check and its test.
  This makes each feature cost more, and it makes every integration possible.
- The audit log (RFC 22) can record the action and the object of every request,
  because every request declares them.
