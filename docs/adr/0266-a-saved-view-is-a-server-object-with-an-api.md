# 266. A saved view is a server object with an API

Status: accepted · 2026-10-18. Records decision 40.3 of the owner. Decisions 2 to 9 are decided by the product manager under the owner's delegation of 20 October 2026. Accepts RFC 100. Extends ADR 17

## Context

[RFC 40](../rfc/0040-inventory-queries.md) asks for search and filters over
the inventory, with saved and shared views. Issue #934 asks where a saved
view lives. A view can live in the browser of one person, in the console, or
on the server.

The owner decided on 18 October 2026 (decision 40.3). ADR 17 gives every
feature a REST API. ADR 51 decision 7 says that a saved view grants nothing.

The product manager decided the questions of RFC 100 on 20 October 2026.
Decisions 2 to 9 hold those answers.

## Decision

1. **A saved view is a server object with an API (owner decision 40.3).** The
   server stores a saved view. A person, the console, the CLI and other
   clients create, read and delete it through the REST API, and open it with
   a link. A view that only the browser of one person stores is not a saved
   view.
2. **A view has one owner: one person in one namespace.** The owner is the
   subject of the principal that made the view. The namespace is the namespace
   of the path.
3. **A view is private first.** The order of sharing is: no sharing, then the
   namespace, then a collection. A share shows the name and the label values
   of the view to the scope, and the server writes an audit entry for it. A
   reader of the scope still needs `unit:read` for each unit in the result.
4. **A view stores labels and references only.** A reference holds the tool of
   the unit. A unit that moves to another tool no longer matches the
   reference, and a label filter still matches it. A sort, the columns and
   the unit state filters join the query when the unit list accepts them.
5. **The limits are constants, not settings.** A name has 1 to 128 characters.
   An owner has at most 100 views in a namespace. A namespace has at most
   10 000 views. A request body has at most 16 KiB. A name is unique for each
   owner in a namespace.
6. **The view family has three new actions:** `view:create`, `view:read` and
   `view:delete`. The object of each action is the namespace. The handler
   checks that the owner of the view is the caller, in addition to the
   filter of the store by owner, and each check has its own test. The route
   for the units of a view checks `unit:read` for each unit again. An
   administrator cannot read the view of another person, because the store
   filters by owner. The action constants and the role grants belong to the
   shared identity actions file, in the first implementation slice.
7. **An administrator can delete a view without reading it** with the action
   `view:manage` and an audit entry. This lands in a slice after the first.
8. **The views of a principal are deleted with the principal.** The delete
   happens in the same transaction as the delete of the principal. The audit
   log keeps one entry for each deleted view, with the view ID and not the
   name. The retention of the audit log does not become shorter. A bootstrap
   admin token cannot create a saved view. It gets 403 with a message that
   tells the caller to sign in as a person.
9. **A client reads a view in two ways:** the definition route, and the route
   `/views/{id}/units` that runs the query on the server. A test runs one
   query through both routes and compares the results. Backup, restore and
   replay follow RFC 100: the table of views joins the backup table list, a
   restore checks each row again, and the idempotency record of a create
   stores the ID of the view.

## Consequences

- A saved view has a REST API under the prefix of ADR 210. Each route
  declares its authorization actions, and the route test covers them (ADR 17).
- A saved view needs its docs page in the pull request that makes it visible
  (ADR 214).
- A person who leaves takes the views away with them. A view of a deleted
  principal cannot be read by a new principal with the same subject.
