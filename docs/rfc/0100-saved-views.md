---
rfc: 100
title: Saved views of the unit list
status: accepted
created: 2026-10-20
adr: 266
---

# RFC 100. Saved views of the unit list

Status: accepted. Part of [RFC 40](0040-inventory-queries.md). The owner
decided that a saved view is a server object with an API. The product manager
decided the questions on 20 October 2026, under the owner's delegation.
[ADR 266](../adr/0266-a-saved-view-is-a-server-object-with-an-api.md) records
the decisions. The section "Outcome" gives the answers. No question is open.

## Problem

A person filters the unit list and wants to open the same list again later,
from the console, from the CLI or from a link, and to give it to a team. Today
the filters are query values of one request. Nothing keeps them.

Issue #934 lists what a view holds: a name, an owner, a scope, a filter, a sort
and the visible columns. It also lists sharing with a namespace or a
collection. The issue is not a decision. This RFC asks each of these as a
question.

## What is decided

- A saved view is a server object with an API (ADR 266, owner decision 40.3).
- A saved view grants no access (ADR 51 decision 7). A label never grants
  access.
- Every route declares its authorization actions (ADR 17). A mutating route
  takes an `Idempotency-Key` and carries the fence of the namespace (ADR 210
  decision 6).

## What exists today

The unit list `GET /api/dustpan/v1/namespaces/{namespace}/units` has these
inputs: `label` (`key=value`, at most 10), `reference` (a unit reference in the
namespace of the path, at most 20), `pageSize` and `pageToken`, and `at` for a
view at a time. It returns the units that the caller can read, in the order of
the unit ID. It has no sort and no choice of columns. The namespace is part of
the path. A list across namespaces uses `namespaces/-` for a get by unit ID
only.

So the filters that exist today are labels and references. A saved view of
today holds those two and nothing else. The filters for unit states (RFC 26),
the tool and the collection do not exist yet in the list.

## The proposed model

A saved view has:

| Field | Content |
| --- | --- |
| `id` | `view-` and 32 lowercase hex digits. Unique in the installation. It never changes. |
| `namespace` | The namespace of the view. It is part of the path. |
| `name` | 1 to 128 characters, no hidden character (Cc, Cf, Zl, Zp), no space at either end. |
| `owner` | The ID of the principal that made the view (`prn-...`). The ID never changes and no principal gets it again. It is not the subject. |
| `query` | `labels` (key and value) and `references`, with the limits of the unit list. |
| `createdAt` | Time in UTC. |

The view holds a query and **never its results**. The call that reads the
units of a view runs the query again, with the code path of the unit list,
and with the access of the caller. So a view cannot show a unit that the
caller cannot read, and a view that is shared later cannot leak a unit.

The Go types and the store port are in `pkg/savedview`. The conformance suite
`savedviewtest` has the case names and skips until the first adapter exists.

### Routes (proposed)

All paths are relative to `/api/dustpan/v1`.

| Method and path | Action | Object | Answer |
| --- | --- | --- | --- |
| `POST /namespaces/{namespace}/views` | `view:create` | the namespace | The new view |
| `GET /namespaces/{namespace}/views` | `view:read` | the namespace | The views of the caller in the namespace |
| `GET /namespaces/{namespace}/views/{view_id}` | `view:read` | the namespace | One view of the caller |
| `GET /namespaces/{namespace}/views/{view_id}/units` | `view:read`, `namespace:read`, then `unit:read` on each unit | the namespace, each unit | One page of the units of the query |
| `DELETE /namespaces/{namespace}/views/{view_id}` | `view:delete` | the namespace | No body |

A view of another person, an unknown view and a view of another namespace give
the same 404. A caller cannot learn that a view exists. The route for the
units takes `pageSize` and `pageToken` like the unit list, and no other filter.

### Defense in depth (ADR 250)

Each step stops the attack alone, and each has its own test.

| Step | Rule for saved views | Test target |
| --- | --- | --- |
| 1. Check at the boundary | `View.Validate` uses an allowlist for the ID, the name, the owner and each query part. It refuses and never repairs. The reference check refuses a control character, because the parser of the core keeps a unit name opaque. | `savedview_test.go` |
| 2. Encode where used | SQL uses placeholders. The store keeps the query as typed columns or as JSON, and reads it back through `Validate` again. A name is never put into a header or a log line without removal of CR and LF. | the store suite and the adapter tests |
| 3. Limit the effect | Name length, label and reference counts, a body size limit, a number of views for each owner, and a number of views for each namespace. | `savedviewtest` and handler tests |
| 4. Authorize at the point of use | The route checks the action. The store returns only the views of the owner who asks. The units route checks `unit:read` for each unit, with the access of the reader. | `savedviewtest`, the route test and an e2e step |
| 5. Fail closed | A store error, an unknown action or a row that fails `Validate` denies and writes an audit entry. A stale fence changes nothing. | `savedviewtest` and handler tests |

A row that comes from a backup or from another version is untrusted. The read
path checks it again, and a row that fails is not served.

## Questions

### 1. Who owns a view?

Options:

- **A. One person, in one namespace.** The owner is the person who made the
  view. The namespace is the namespace of the path.
- **B. A namespace.** Any writer of the namespace edits the view.
- **C. The installation.** A view works across namespaces.

A gives the least surprise and the smallest effect: nobody else reads or
changes the view. B needs a rule for who edits and for conflicts. C needs a
query that spans namespaces, and the unit list has none (`namespaces/-` lists
no units today).

**Recommendation: A.** A view belongs to one person and one namespace. Sharing
(question 2) widens who reads it, not who owns it. A view across namespaces
waits for a unit list across namespaces.

### 2. How is a view shared?

Options:

- **A. No sharing.** A view is personal.
- **B. Share with the readers of the namespace.**
- **C. Share with a collection.**
- **D. Share with a person or a group.**

Sharing shows the name and the query of the view to the readers, and the
readers run the query with their own access. The query can hold a label value
that names something private. The sharer must know this when they share.

**Recommendation: A in the first slice, then B, then C.** A shared view is a
`visibility` of the view (`private` or `namespace`) and a later `collection`
value. A reader of the scope reads the view. The reader still needs
`unit:read` for each unit in the result. Only the owner edits or deletes the
view. A namespace administrator can delete it too (question 6). D waits until the groups of ADR 52 have a use for it.

### 3. What does a view store?

Options:

- **A. The filters that exist.** Labels and references.
- **B. A. and a sort and a list of columns.**
- **C. An opaque document from the console.**

The unit list has no sort and no choice of columns, so there is nothing to
check B against. C cannot be checked (ADR 250 step 1), and it ties the
server format to the console.

**Recommendation: A in the first slice.** B comes when the list API accepts a
sort and a list of columns, in the same pull request or after it, with an
allowlist of sort keys and columns. C is out. The unit state filters (RFC 26)
join the query when the list accepts them.

### 4. Which limits?

Proposal, each with its reason:

| Limit | Value | Reason |
| --- | --- | --- |
| Name | 1 to 128 characters | A name shows in a list and in a tooltip. |
| Labels, references | 10 and 20 | The limits of the unit list. A view must not hold a query that the list refuses. |
| Views for each owner in a namespace | 100 | A person needs few. A cap stops a loop that fills the database. |
| Views for each namespace | 10 000 | A cap for the whole namespace, so many owners cannot fill it. |
| Request body | 16 KiB | The largest valid body is far smaller. |
| Name | unique for each owner in a namespace | A person must tell two views apart. Another owner can use the same name. |

**Recommendation:** these values. The owner or the product manager can change
a number before the first slice. The numbers are constants of `pkg/savedview`,
not settings.

### 5. Which authorization actions?

Options:

- **A. Reuse `namespace:read` and `unit:read`.** No new action. Every reader
  can then make a stored object, with no way to turn it off for a role.
- **B. A new `view` family:** `view:create`, `view:read`, `view:delete`. Later
  `view:share`.
- **C. A new object kind `view`**, so a grant can name one view.

**Recommendation: B, with the namespace as the object.** The owner rule (a
person reads and deletes only their own view) is a check in the store, and
not a role, so no `view` object kind is needed before sharing. The built-in
roles that hold `namespace:read` get the three actions. C waits for sharing,
when a view must be an object that a grant or a collection names.

### 6. Can an administrator reach a private view?

Options:

- **A. No one but the owner.** An administrator cannot read or delete it.
- **B. An administrator deletes it** with `view:manage` and an audit entry.
  An administrator cannot read it.
- **C. An administrator reads and deletes.**

A private view can hold labels that name a person or a project. A view of a
person who left has no owner who can delete it.

**Recommendation: B**, in the slice after the first. The first slice has no
administrator route. The delete of a principal (question 7) covers the leaver.

### 7. What happens to the views of a person who leaves?

Options:

- **A. The views stay.** Nobody can reach them.
- **B'. The views are deleted by a cleanup of each namespace**, with its own
  fence, and not in the transaction that deletes the principal.
- **C. The views stay and a namespace administrator deletes them.**

**Recommendation: B' for private views, with the owner ID as the control.**
The owner of a view is the ID of the principal, and no principal gets an ID
again. So a new principal with the same subject sees nothing, by
construction. A view whose owner ID is not a local principal matches no
reader, also after a failover. The delete of the views is cleanup: each
namespace runs it with its own fence, for example as a durable operation on
the primary, and not in the transaction that deletes the principal. A private
view that nobody can open has no use, so the cleanup removes it. A shared view
stays when sharing exists (question 2), and its owner becomes the namespace.

### 8. How does a client read a view?

Options:

- **A. Definition only.** The client reads the view and calls the unit list
  with its query.
- **B. Run it.** `GET .../views/{view_id}/units` runs the query on the server.
- **C. Both.**

The acceptance criterion says that a view returns the same units as the filter
it stores. A CLI or a link needs one call.

**Recommendation: C.** B calls the same code as the unit list: the same filter
parser, the same access check and the same paging. A test runs one query
through both routes and compares the pages. So the two cannot differ.

### 9. Backup, restore and replay

A saved view table joins the backup table list. A restore checks each row
again. The idempotency record of a create stores the ID of the view, not the
body (ADR 210 decision 6). No question: this follows from the ADRs.

## First slice

One thin end-to-end path, with no new dependency:

- A migration and the store port over SQLite and PostgreSQL, with the
  conformance suite complete and run on both stores.
- `POST`, `GET` (list and one), `GET .../units` and `DELETE` of a personal view
  of labels and references, with the actions `view:create`, `view:read` and
  `view:delete`.
- The route test, one audit entry for each write, a backup table, an e2e step
  with the real binary, and the docs page `docs/guides/saved-views.md`.
- The label grammar moves to one place, `pkg/inventory`, so the unit list and
  the view share it. This PR repeats the grammar in `pkg/savedview`.

## Slices after the first

1. Share with the readers of a namespace.
2. Share with a collection.
3. Sort and columns (waits for the list API).
4. Filters for unit states (waits for RFC 26).
5. Rename and replace a view.
6. The console uses saved views (in the console repository).

## Outcome

Accepted on 20 October 2026 by the product manager, under the owner's
delegation: [ADR 266](../adr/0266-a-saved-view-is-a-server-object-with-an-api.md).

1. A view has one owner: one person in one namespace. The owner is the ID of the principal.
2. No sharing first, then the namespace, then a collection. A share shows the
   name and the label values to the scope, and the server audits it.
3. A view stores labels and references only. A reference holds the tool, so a
   unit that moves to another tool no longer matches it. A label filter still
   matches.
4. The limits are constants: a name of 128 characters, 100 views for each
   owner, 10 000 views for each namespace, a body of 16 KiB.
5. New actions `view:create`, `view:read` and `view:delete`, with the
   namespace as the object. The handler checks that the owner is the caller,
   in addition to the filter of the store. The route for the units checks
   `unit:read` for each unit. An administrator cannot read the view of another
   person.
6. An administrator deletes a view without reading it (`view:manage`), with an
   audit entry, in a later slice.
7. The owner of a view is the ID of the principal, never the subject. A view
   whose owner is not a local principal matches no reader. The delete of the
   views of a principal is cleanup that each namespace runs with its own fence,
   with one audit entry for each view that holds the view ID only. Only a
   person creates a view. Every other kind of principal gets 403.
8. Both routes: the definition and `/views/{id}/units`. A test compares them.
9. Backup, restore and replay follow the text above.
