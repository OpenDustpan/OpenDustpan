## Context

Milestone 6 step 5 needs curation of public upstreams. The registry fetches a public release from its upstream host, holds it in cache, and serves it only when the curation setting and rules of the namespace allow it. The curation part of RFC 10 is open, so this change gives the design.

## Decisions

1. **Curation is opt-in with explicit allow rules.** Curation is a per-namespace curation-enabled setting that defaults to off with an effective value in the API, plus explicit allow rules keyed by namespace ID. Rules combine with logical OR and each rule names an upstream host, a registry address prefix matched at a path boundary, and a semver constraint. The address model covers OpenTofu provider and module addresses and Pulumi plugin names. Rule and setting routes declare authorization actions and the console uses only the tool-neutral REST API.
2. **One snapshot per request, curation before cache, fetch then serve, audit before serve.** Each fetch loads one immutable snapshot of the setting and rules of the namespace at request start and uses that snapshot for the whole request. When curation is on, the curation check runs before the cache lookup and deny is the default with no direct upstream fallback. A cache miss for an allowed OpenTofu provider, OpenTofu module, or Pulumi plugin fetches once from the matched upstream host, stores the bytes with checksum in cache, then serves. When curation is off, the server skips the curation check and writes no curation audit entry. Toggling the setting retains stored rules. The audit write for an allow precedes the serve and a failed audit write refuses the fetch.

## Structure

Type curator with unexported fields rules and clock. Constructor newCurator checks each rule for an allowed host that is not an IP literal, a non-empty address prefix, and a parsable semver constraint. No other code path fetches an upstream release.

## Risks

- A broad prefix allows more releases than planned.
- Rule sprawl slows review of what is allowed.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service loads the rule snapshot of the request namespace and allows the fetch only when host, address prefix at a path boundary, and semver constraint of one rule all match. | Request a release with a close host name, a prefix that shares characters across a path boundary, and a version outside the constraint; each case is refused. |
| HTTP handler | HTTP adapter rejects a request with an encoded traversal, an absolute URL, or a path escape in the registry address before the service runs. | Send encoded traversal and absolute URLs as the registry address; the adapter returns 400 without a fetch. |
| port adapter | Fetch adapter allows only the host of the matched rule, rejects an IP literal rule host, and stops on redirect to any other host. | Point a matched rule at a redirect to an unlisted host and submit an IP literal host; the fetch stops and records the refusal. |
