## Context

Path sets are stored objects at installation, namespace, collection and unit scope. The gate fact producer reads the changed paths of a change order and computes touches and only values for each path set in scope. Absent paths, truncated paths, or absent set versions give typed missing facts. Large path lists follow the 256 KiB rule that empties the largest string sets until the document fits. Saves use PUT with If-Match; the decision record read route belongs to read-route-decision-records-change.

## Decisions

1. **Path set as a checked type.** Store each path set as an unexported type with ID and patterns set only through a constructor that checks ID grammar, pattern count 1 to 64, and narrower scope ID reuse. Fact computation takes a read-only path list value object, never raw strings. Saves carry a version and If-Match; a stale match fails with 412 and stores nothing.
2. **Producer computes facts.** Compute touches and only in the gate fact producer in pkg/gate, behind the existing producer port. Routes and audit entries live in the gate service and HTTP adapter. Truncation empties change.paths under the document limit before fact computation and marks path facts unavailable. No new tool and no custom analyzer.

## Structure

Unexported gate.PathSet with constructor gate.NewPathSet that checks ID grammar and 1 to 64 patterns; fact input as gate.ChangedPaths built only from a cleaned path list.

## Risks

- Glob dialect drift between docs and code.
- Large path lists exceed the 256 KiB fact document limit and truncated only facts read as complete.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port adapter checks ID grammar, pattern count, and scope ID reuse before storage; concurrent saves of one ID serialize on the store row so one wins and the other gets a conflict or invalid error; a save that races a delete of its scope fails closed with invalid or not-found and stores nothing. | Save a path set with 0 patterns, 65 patterns, a bad ID, and a reused wider scope ID; each save is refused. Save one ID at two scopes at the same time; one save wins and the other fails. Delete the scope during a save; the save fails and stores nothing. |
| service | Service checks the read or write grant on the scope object for each route and writes one audit entry per write, with a denial entry on 403. | Read, list, and write a path set with no grant on the scope; each gives 403 and writes a denial audit entry. |
| HTTP handler | HTTP handler checks pattern text size and pattern count limits, returns 400 that names the bad ID or pattern, checks If-Match and returns 412 on a stale version, and maps truncated or absent path lists to available false. | Send an oversize pattern document and evaluate a change with no path list and a change with a truncated path list; the first is refused with 400 and the others give available false. Send a stale If-Match version; the save is refused with 412. |
