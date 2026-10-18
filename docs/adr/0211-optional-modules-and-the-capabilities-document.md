# 211. Optional modules, one switch each, and the capabilities document

Status: accepted · 2026-10-18 · accepts part of RFC 7 and part of RFC 52

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
   off, each of its routes answers 404, as for a route that does not exist.
   The module starts no schedule, no timer and no durable operation of its
   own. Its data stays in the store. When the operator turns the module on
   again, it uses the same data.
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

   The route needs a signed-in caller and the action `caller:read` on the
   caller. It is not the `/api/capabilities` route of the Pulumi
   Cloud-compatible API, which does not change.
6. **The console reads it now.** The console reads the capabilities document
   after the sign-in. It hides the sections of an off module. It refuses to
   work with a server whose `apiVersion` it does not know, and it shows the
   two versions.

Benchmark for decisions 2 to 6: Google Cloud enabled APIs (a project turns
the surface of one API on or off, and lists the APIs that it enabled) and
Kubernetes discovery (the API server lists the API groups and versions that
it serves, and a client reads the list before it calls a group).

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
- The route test of ADR 17 gets the capabilities route with `caller:read`.
