# 211. Optional modules, one switch each, and the capabilities document

Status: accepted · 2026-10-18. Accepts part of RFC 7 and part of RFC 52. Extends ADR 8

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.

[RFC 7](../rfc/0007-one-binary-many-roles.md) asks which features an
operator can turn off, whether a module owns its migrations, and how the
console learns which modules a server serves.
[RFC 52](../rfc/0052-console-core.md) asks how the console finds the
features of the server that it talks to. ADR 8 says that the console needs a
version handshake once the API grows features that the console depends on.

The facts on `main`:

- One binary serves every feature. No feature has a switch of its own.
- The migrations of each dialect are one numbered list (ADR 3, ADR 73). The
  conformance suites and `sqlc diff` cover every table.
- `/api/capabilities` is a route of the Pulumi Cloud-compatible API. The
  Pulumi CLI reads it. It is not a document for the console.

## Decision

1. **The core is never off.** These parts are the core: namespaces,
   identity, authorization, the audit log, secrets, maintenance mode and
   backups. No switch turns a part of the core off.
2. **One declared switch for each optional module.** An optional module is
   a feature outside the core that has its own routes, its own schedules or
   both, for example the Catalog, Dusty or Sweep. Each optional module
   declares one switch. The switch is an installation setting with the
   precedence of ADR 63 decision 1.
3. **An off module answers 404 and runs no schedules.** When a module is
   off, each of its routes answers 404 with the error code `module.off`
   (ADR 182). The arguments name the module and its switch, for example
   `{"module": "agent", "switch": "agent.enabled"}`, so an operator knows
   which switch to turn on. The message says the same. A route of a module
   that is on, and a path that no module has, keep the 404 that they have
   today. The module starts no schedule, no timer and no durable operation
   of its own. A durable operation of the module that runs when the
   operator turns the module off runs to its end. The server refuses each
   new one. Its data stays in the store. When the operator turns the
   module on again, it uses the same data.
4. **Every migration always applies.** The server applies each migration at
   each start, whatever the switches are. So the tables of an off module
   exist, `sqlc diff` stays clean, and the conformance suites need no build
   tag. A module does not own a list of migrations.
5. **The capabilities document.** `GET /api/dustpan/v1/capabilities`
   answers the version of the tool-neutral API and each optional module with
   its state:

   ```json
   {
     "apiVersion": "v1",
     "modules": [
       { "name": "catalog", "enabled": true },
       { "name": "agent", "enabled": false }
     ]
   }
   ```

   The route is on the public list of the route test. A caller with no
   sign-in gets `apiVersion` only, so a client can check the version
   before a person signs in. The module list needs a signed-in caller and
   the action `caller:read` on the caller. A caller with no such grant
   gets the answer with no `modules` field. The route is not the
   `/api/capabilities` route of the Pulumi Cloud-compatible API, which
   does not change.
6. **The console reads it now.** The console reads `apiVersion` before the
   sign-in. It refuses to work with a server whose `apiVersion` it does not
   know, and it shows the two versions. After the sign-in, it reads the
   document again and hides the sections of an off module.

Benchmark for decisions 2 to 6: Google Cloud enabled APIs (a project turns
the surface of one API on or off, lists the APIs that it enabled, and an
API that is off answers an error that names the API and how to enable it)
and Kubernetes discovery (the API server lists the API groups and versions
that it serves, and a client reads the list before it calls a group).

## Not decided here

- The list of optional modules on day one, and the name of each switch.
- Whether a tool adapter, for example the TFE-compatible API, is an optional
  module.
- Whether a switch changes at a reload of the settings file, or only at a
  start.
- What a module that is on does when it reads from a module that is off.
- Whether the document also lists the server version.

## Consequences

- RFC 7 keeps the questions about roles of the binary and
  `opendustpan serve --role`.
- Each optional module adds its switch and its routes to one table. A test
  checks that an off module answers 404 on each route in the table and
  starts no schedule.
- The route test of ADR 17 gets the capabilities route on the public list,
  and a test checks that an anonymous answer holds `apiVersion` only.
- The error code `module.off` gets its entry in the registry of ADR 182.
