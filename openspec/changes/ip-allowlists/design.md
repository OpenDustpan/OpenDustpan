## Context

IP allowlist checks are security code on the request path for console sign-in and access token use.

## Decisions

1. **Parse lists once at the boundary.** Hold parsed CIDR lists in one unexported checker built once per change of settings, so a bad entry never reaches request handling. The constructor parses each entry to netip.Prefix and refuses the settings write when any entry fails to parse.
2. **Two-stage check order.** Check the installation list before credential validation and the namespace and token lists after authentication, so a denied network learns nothing about credentials and per-scope lists still resolve. Stored lists live in the store so each instance reads the same lists.

## Structure

Unexported type allowChecker with constructor newAllowChecker that parses and stores netip.Prefix values; request code holds only the built value.

## Risks

- Wrong client address behind a proxy blocks valid users or lets a denied address pass.
- An installation list that excludes the administrator blocks all console sign-in; repair uses the host database path outside the HTTP check.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Settings API refuses an entry that is not a valid IPv4 or IPv6 CIDR or single address. | Send 999.999.0.0/16 and ::/999 to the settings route and check 400. |
| service | Checker uses the recorded client address after trusted-proxy handling and denies when any opted-in list denies. | Call the service with an address inside the namespace list but outside the installation list and check denial. |
| HTTP handler | HTTP handler checks the installation list before credential use and the namespace and token lists after authentication, and answers 403 with error code ip-allowlist.denied. | Send a console sign-in and a CLI request from a denied address with a valid credential and check 403 with no session or state read. |
