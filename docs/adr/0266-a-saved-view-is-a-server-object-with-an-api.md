# 266. A saved view is a server object with an API

Status: accepted · 2026-10-18. Records decision 40.3 of the owner. Accepts a part of RFC 40. Extends ADR 17

## Context

[RFC 40](../rfc/0040-inventory-queries.md) asks for search and filters over
the inventory, with saved and shared views. Issue #934 asks where a saved
view lives. A view can live in the browser of one person, in the console, or
on the server.

The owner decided on 18 October 2026 (decision 40.3). ADR 17 gives every
feature a REST API. ADR 51 decision 7 says that a saved view grants nothing.

## Decision

1. **A saved view is a server object with an API (owner decision 40.3).** The
   server stores a saved view. A person, the console, the CLI and other
   clients create, read and delete it through the REST API, and open it with
   a link. A view that only the browser of one person stores is not a saved
   view.

## Not decided here

[RFC 100](../rfc/0100-saved-views.md) holds the rest, with options and a
recommendation for each question: who owns a view, how a view is shared, what
a view stores besides filters, the limits, the authorization actions, and the
routes.

## Consequences

- A saved view has a REST API under the prefix of ADR 210. Each route
  declares its authorization actions, and the route test covers them (ADR 17).
- A saved view needs its docs page in the pull request that makes it visible
  (ADR 214).
- The server code for saved views is in `pkg/savedview`. It has the types and
  the store port, and no adapter yet.
