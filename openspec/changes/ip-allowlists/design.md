## Context

The allowlist check is security code on the request path of console sign-in and access token use. Main checks the installation list before the credential and the namespace list after authentication. The namespace list is on a legacy route with its own column (see "What main lacks" in the proposal). This design moves the namespace list into the settings resource, as ADR 55 decision 8 and ADR 212 decision 7 say, and makes the setting the one switch. The own list of an access token is the change `ip-allowlists-token-lists`, which depends on this change.

## Decisions

1. **Parse once at the boundary.** `ipallow.List` has unexported prefixes. Only `ParseList` and `DecodeList` make a value, and both refuse a bad entry (`pkg/ipallow/list.go:47,171`). Every scope uses this one type, so a list that did not pass the check cannot reach a checker. The namespace key uses it, and so does the change `ip-allowlists-token-lists`. This change adds no second parser.
2. **Two enforcement points here, one rule: deny when any opted-in list denies.** Each point reads the stored list on each request.
   - The installation list runs in `admit` before the credential is read (`pkg/dustpan/ipallow.go:470`). Main has this.
   - The namespace list runs in `ipallow.Gate`, after authentication and before the handler, for the namespace of the object (`pkg/ipallow/gate.go:39`). Main has this. The object is known only there, so the namespace list cannot join the checker of `admit`.
   - The checker of `admit` holds a namespace slot that no production call fills (see the proposal). This change leaves the slot. `ip-allowlists-token-lists` replaces it with a token slot. `TestDenyAnyListDenies` moves to a table through the HTTP handler, so the "any list denies" rule is tested where it holds.
   - One function answers "are the lists on". The checker, the gate and the key reads call it. The setting `ipAllowlist.enabled` alone turns the lists on, and the option `--ip-allowlists` stays as a static lock (owner decision 9, ADR 219 decisions 1 and 2). The change `ip-allowlists-token-lists` reads the same function.
3. **The namespace list has one home: the settings resource.** The key `ipAllowlist.namespace` is a namespace setting (`setting.LevelNamespace`, `Database: true`). It uses `checkList` and `listReducesSecurity` of `pkg/ipallow/settings.go`. The view `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist` groups the keys with the prefix `ipAllowlist.` and adds no rule (ADR 212 decision 7). The column `namespaces.ip_allowlist` is removed from both baselines, so the legacy route and the resource cannot disagree. `ipallow.Store` keeps its two reads (`NamespaceList`, `NamespacesWithList`) over the settings rows. It loses `SetNamespaceList`: `setting.Store.Update` writes the value, the revision and the audit entry in one transaction, and checks the fence there (`pkg/setting/store.go:20-40`). This is a design decision of this change. The conformance suite for the new read comes before the adapters.
4. **The refresh path needs no new code.** `allowlistRequest` already matches the session, access and refresh cookies (`pkg/dustpan/ipallow.go:247-253`). The guard is that one `case`. A test sends the refresh route with the refresh cookie only, from a denied and from an allowed address, and fails when `session.RefreshCookie` leaves the `case`.
5. **Limits.** A list holds at most 64 entries (`ipallow.MaxEntries`) and at most 4096 bytes (owner decision 11). A string setting holds 1024 bytes today (`pkg/setting/setting.go:85`). `setting.Declaration` gets a limit field, the list keys set it to 4096, and the `settings` check of both baselines is edited in place to hold the larger value. The limit applies to the installation key and the namespace key.
6. **Structure over checks.** The bug class "a list that did not pass the check opens a scope" is made impossible by decision 1. The bug class "a derived fact and its source disagree" is made impossible by decision 3: one stored home. The bug class "two switches disagree" is made impossible by decision 2: one function reads the switch. The guards that remain are fail-closed branches. Each one has a mutation test, because no structure removes the need for the branch (an unreadable store or a stored value from a restored backup is outside the type).

## Contract

Names are the design of this change. The builder keeps the signatures and may adjust a name with a note in the pull request.

**Stored schema** (both dialects, baselines edited in place):

| Table | Column | SQLite | PostgreSQL | Notes |
| --- | --- | --- | --- | --- |
| `namespaces` | `ip_allowlist` | removed | removed | Moves to the row `settings (scope_id, key = 'ipAllowlist.namespace')`. |
| `settings` | none | `value` check raised to 4096 | `value` check raised to 4096 | Owner decision 11. The declaration limits each other key to 1024. |

**Namespace view**:

| Item | Value |
| --- | --- |
| Path | `GET`, `PATCH` `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist` |
| Request (`PATCH`) | `application/merge-patch+json`: `{"ipAllowlist.namespace":"10.0.0.0/8,192.0.2.7"}`. `null` removes the list. Header `If-Match` with the `ETag` of the last read. |
| Response | 200 with the keys of the group, each with its effective value and source, and the `ETag` of the resource. |
| Errors | 400 `ip-allowlist.invalid-cidr` (bad entry, more than 64 entries, more than 4096 bytes, empty list); 403 `access.ip-not-allowed`; 403 `mfa.fresh-step-required`; 409 key locked; 412 stale `If-Match`; 404 `namespace.not-found`. |
| Actions | `namespace:read-settings` and `namespace:manage-settings` (ADR 212 decision 7), plus `namespace:manage-ip-allowlist` for a read and a change (owner decision 10). |
| Fresh step | Only for a change that widens or clears the list (`listReducesSecurity`), and only when the installation requires a fresh step (ADR 385). |

**Audit actions**: a denial writes `installation:deny-ip-allowlist` with the scope `installation` or `namespace` (`pkg/dustpan/ipallow.go:418`). The entry carries the client address. It never names the namespace in the response. A change of the namespace key writes the audit entry of the settings resource.

## Structure

Unexported type `allowChecker` with constructor `newAllowChecker` that stores `ipallow.List` values (main: `pkg/dustpan/ipallow.go:62,74`). The type keeps the slots of main. `ip-allowlists-token-lists` adds the token slot.

## Risks

- A wrong client address behind a proxy blocks valid users.
- An installation list that excludes the administrator blocks sign-in. The recovery is in the docs page.
- The limit of 4096 bytes may cut a list that the legacy route accepted. With no data and no installation, no migration is needed.
- Removing the legacy route breaks the console until its change merges (task 3.3).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The settings store refuses a value that does not decode, and the read denies. | Store `not-a-cidr` straight in the row, and check that the request gets 403. |
| port adapter | The settings API refuses an invalid CIDR. | Write 999.999.0.0/16 and check 400. |
| service | The settings check refuses an empty list, 65 entries, a bad entry and a value over 4096 bytes. | Call the service with each and check `ErrInvalidList` or the limit error. |
| service | The checker denies when any opted-in list denies. | Use an address in the namespace list but outside the installation list and check denial with scope `installation`. Use an address in the installation list but outside the namespace list and check scope `namespace`. |
| HTTP handler | The handler answers 403 before credential use for the installation list. | Send a valid token from a denied address and check 403 with no state read. |
| HTTP handler | The handler answers 403 for the namespace list after authentication and writes no other entry. | Send a valid token from an address outside the namespace list and check 403 and the audit entry. |
| HTTP handler | The route needs its action and the step. | Call each route with no action, with an access token and with a stale session. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| availability | A list change applies to 100% of later requests on each instance, with the stored list read on each request. | percent of requests | each server process of one installation |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph Outside[Untrusted network]
 C[Client]
 end
 subgraph Server[Server process]
 H[HTTP handler]
 K[Allow checker]
 A[Authentication]
 G[Namespace gate]
 end
 subgraph Store[Trusted store]
 S[(Stored lists)]
 end
 C --> H
 H --> K
 K --> S
 K --> A
 A --> K
 A --> G
 G --> S
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| network to server | S | A spoofed proxy header bypasses the list. | boundary check | The settings API refuses an invalid CIDR. | Write 999.999.0.0/16 and check 400. |
| network to server | S | A spoofed proxy header bypasses the list. | encode or contain | The checker parses the address to netip.Addr and matches IPv4-mapped forms as IPv4. | Send ::ffff:10.0.0.1 and check it matches as 10.0.0.1. |
| network to server | S | A spoofed proxy header bypasses the list. | limit | A list holds at most 64 entries (`ipallow.MaxEntries`, `pkg/ipallow/list.go:18`). This change keeps the limit of main. | Write 65 entries and check 400. |
| network to server | S | A spoofed proxy header bypasses the list. | authorize at point of use | The checker uses the recorded client address after trusted-proxy handling. | Send a spoofed header from an untrusted source and check the recorded address is used. |
| network to server | S | A spoofed proxy header bypasses the list. | fail closed | A stored list that fails to parse denies sign-in and token requests. | Store a bad list and check 403 for each address. |
| network to server | E | A refresh cookie renews a session from a denied address. | authorize at point of use | `allowlistRequest` matches the refresh cookie (`pkg/dustpan/ipallow.go:249`). | Send the refresh route with the refresh cookie only from a denied address and check 403 and no new cookie. |
| network to server | I | The 403 names a namespace or a token and so leaks that it exists. | encode or contain | The scope is one of fixed values, never text of the request (`pkg/dustpan/ipallow.go:520`). | Deny in a namespace with the name `acme` and check that the body holds no `acme`. |
| network to server | D | Many denied addresses fill the audit log. | limit | The limiter writes one entry for each scope and address in each window (`pkg/dustpan/ipallow.go:295 (record)`). | Send 1000 denials from one address and check one entry plus a count. |
| store to server | T | A restored backup holds a list that does not decode. | fail closed | `DecodeList` refuses the value, and the request is denied (`pkg/dustpan/ipallow.go:214 (installationChecker)`). | Restore a bad value for each of the two scopes and check 403. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| server start | A stored list fails to parse. | Repair the list through the host database path, then restart. | The server refuses sign-in and token requests until repair. |
| request | The database does not answer when a list is read. | Retry the request. | The server answers 503 and writes no denial entry (`pkg/dustpan/ipallow.go:49-55`). |
| list change | The audit entry cannot be written. | Retry the change. | The change does not happen. |
