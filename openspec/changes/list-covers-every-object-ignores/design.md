## Context

A scope with All set cannot name namespaces whose IP allowlist refuses the client address, so list calls show those namespaces while Authorize refuses each object of the namespace.

## Decisions

1. **Exclusion list filled per request with fail-closed read.** Add excluded namespaces to the every-object scope and fill it in the IP allowlist gate from ipallow.Store.NamespacesWithList once per request with a limit of 1000 names; on a store error or past the limit the gate fails closed and returns no scope.
2. **Every list consumer applies the list.** Apply the list in the namespace filter of both dialects with a NOT IN clause that skips the clause when the set is empty, in the collection and unit filters including page queries and totals, in the deleted-units read scope, in the namespace list, and in the caller-access API next to all. A direct read of one refused object answers 403 with the allowlist reason. Installation-scoped objects stay visible.

## Structure

Unexported field exceptNamespaces on the scope value made only by NewScopeWithExclusions, whose constructor drops exclusions when All is false and sorts and dedupes names.

## Risks

- A consumer that ignores the exclusion field fails open and shows refused names.
- Past the limit every list call fails for the whole installation until the count drops; a direct read of one named namespace still answers 403 so the administrator can find and remove lists without a list call.
- An allowlist changed between the gate read and the list query can show a refused name in that one request; the next request recomputes exclusions and the window is one request only.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Gate resolves the client address by the trusted-proxy rule, then fills excluded namespaces for each namespace with an allowlist that refuses that address; when the feature is off or the scope did not opt in it fills nothing; on store error or past 1000 names it denies the list. Covers session and access-token callers with All set. | Request a list with an access token with All set from outside the allowlist with a spoofed X-Forwarded-For from an untrusted peer and check the refused namespace is absent. |
| port adapter | Store namespace filter adds a NOT IN clause for excluded namespaces in both dialects and skips the clause when the set is empty; page queries apply it inside each page and in the total. | Call the store namespace list with exclusions on page 2 and check excluded rows are absent from the page and from the total. |
| port adapter | Store collection, unit, and deleted-units filters apply the same excluded-namespace set in both dialects, in each page and in each total. | Call the store collection, unit, and deleted-units lists with one excluded namespace and check no row of that namespace appears in any page or total. |
| HTTP handler | HTTP handler returns the filtered namespace, collection, unit, and deleted-units lists, caller access with exclusions next to all, and 403 with the allowlist reason for a direct read of a refused object by a grant holder. | Call GET namespaces, GET collections, GET units, GET deleted-units, caller access, and GET one refused namespace from outside the allowlist and check the list hides it and the direct read gives 403. |
