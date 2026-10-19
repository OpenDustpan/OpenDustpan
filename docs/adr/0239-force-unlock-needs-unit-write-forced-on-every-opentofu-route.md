# 239. A force-unlock needs `unit:write-forced` on every OpenTofu route

Status: accepted · 2026-10-18. Supersedes in part ADR 25 decision 6 and ADR 28 decision 6. Extends ADR 44 decisions 4 and 6 and ADR 49 decision 6

## Context

The console design for the OpenTofu workspace page
(`OpenDustpan/console`, `docs/design/opentofu-workspace-page.md`, section
3.2) lists the server changes that the page needs, as gaps G1 to G12. This
ADR records gap G11 and lists the other gaps that need no ADR. ADR 240
records gap G7. RFC 80 holds gap G6, which is not decided.

The two OpenTofu adapters give a force-unlock different actions:

- The TFE-compatible route `POST /api/v2/workspaces/{id}/actions/force-unlock`
  needs `unit:manage` (ADR 25 decision 6, ADR 28 decision 6).
- An `UNLOCK` on the http backend by a principal that does not hold the lock
  is a force-unlock. It needs `unit:write-forced` (ADR 49 decision 6).

The built-in role `maintainer` has `unit:manage`. Only `admin` has
`unit:write-forced` (ADR 44 decision 4). So a maintainer can release a lock
of another principal through the TFE-compatible API, and not through the
http backend. The two routes release the same workspace lock of the same
unit (ADR 49 decision 3).

A force-unlock writes a fence version with a new lineage (ADR 44 decision
1). The server cannot set the lineage of a state that the client encrypted.
A release of such a lock writes no fence. So on `main` it is an override
that needs the switch and `unit:write-forced` (ADR 44 decision 4). When
the switch is off, it answers 409. The text of that 409 says that the
server cannot fence the state. It does not name the switch or the action.

The workspace document has the attribute `permissions.can-force-unlock`. On
`main` it follows `unit:manage`.

No tag shipped, so no shipped shape changes (ADR 73).

A decision of the product manager has the label "(product manager)" and a
"Benchmark:" line. The product manager decided under the owner's rule:
follow what Google, AWS, PCI DSS, SOC 2 and ISO 27001 would do.

## Decision

1. **One action for each force-unlock (product manager).** A force-unlock
   needs `unit:write-forced` on the workspace unit, on the TFE-compatible
   API and on the http backend. The TFE-compatible route declares
   `unit:write-forced` in its route table, not `unit:manage`. This
   replaces "force-unlock" in the list of `unit:manage` in ADR 25 decision
   6, and the action of the OpenTofu force-release in ADR 28 decision 6.
   Workspace creation keeps `unit:manage`.

   Benchmark: HCP Terraform gives force-unlock to workspace admins only. A
   force-unlock can make a running job fail, so the action goes to the
   smallest set of principals (least privilege).
2. **The TFE route narrows from `maintainer` to `admin`.** With the
   built-in roles, only `admin` can force-unlock through either adapter. A
   maintainer who could force-unlock through the TFE-compatible API on
   `main` gets 403. No release shipped the old action, so no installation
   loses a working grant.
3. **A custom role can grant it.** A custom role (ADR 52 decision 7) can
   hold `unit:write-forced`. An administrator gives the force-unlock to an
   on-call group with a grant of that role on a namespace, a collection or
   a unit. The built-in role set does not change.
4. **A client-encrypted state also needs the switch, as on `main`.** The
   release of a lock on a client-encrypted state writes no fence version.
   It stays an override, as a forced write is (ADR 44 decision 4). So it
   needs `unit:write-forced` and the effective forced-write switch: the
   switch of the namespace and the switch of the unit (ADR 44 decision 6).
   When the switch is off, the release answers 409 and changes nothing. A
   release with the switch on writes the `override` audit entry of ADR 44
   decision 7. This applies to both adapters. The only new work is the
   error text: the 409 names the switch and the action (ADR 44 decision
   8).
5. **`can-force-unlock` follows the same action.** The attribute
   `permissions.can-force-unlock` of the workspace document is true only
   when the caller has `unit:write-forced` on the unit. It does not read
   the switch: a client sees a refused release of a client-encrypted state
   from the 409 and its error code.
6. **The route test and the e2e suite.** The route test finds
   `unit:write-forced` on the TFE-compatible route. The e2e suite gets one
   step for each OpenTofu version in its matrix: a principal with
   `maintainer` gets 403 on `tofu force-unlock` through the `cloud` block,
   and a principal with a custom role that holds `unit:write-forced`
   releases the lock. The step also checks that the CLI output of the
   403 names the action.

## The other gaps of the workspace page

These gaps follow decisions that exist. This ADR does not decide them
again. The console design, section 3.2, gives the route, the field and the
action of each gap. This list is the one server-side source for the
implementation briefs.

- **G1. A reason on a force-unlock.** The route accepts the optional
  `data.attributes.reason`, 1 to 500 Unicode code points, as the manual
  lock of ADR 213 decision 10. The "500 characters" of ADR 213 means the
  same unit: Unicode code points. The reason stays optional on the wire,
  because `tofu force-unlock` sends no body.
- **G2. An audit entry for each force-unlock.** One entry of the kind
  `change` with the action `unit:write-forced` (ADR 60 decision 4). It
  holds the released lock holder, the lock time, the new state version and
  the reason, in a new optional field (ADR 60 decision 5). The store writes
  it in the transaction of the release. The `override` entry of a
  client-encrypted state stays. The change covers both adapters.
- **G3. The expected lock.** The domain already has the check:
  `ForceUnlockLock` refuses a lock that does not match with
  `ErrLockMismatch`, and the http backend uses it. Only the TFE route and
  the document attribute are new. The workspace document gets
  `attributes.lock-id`. The TFE force-unlock accepts
  `data.attributes.lock-id` and `data.attributes.lock-holder`. A lock that
  does not match answers 409 and changes nothing.
- **G4. Change counts and the resource count** of a state version, from
  the resource change rows of ADR 150. The value is `null` while the blame
  cursor has not passed the version, and for an `opaque` version (ADR 150
  decisions 8 and 9).
- **G5. Fence fields** on the state version document:
  `attributes.fence-by` and `attributes.unlocked-write`.
- **G9. Stable error codes** on the TFE-compatible API, under ADR 182
  decisions 1, 3 and 7. The page needs at least `workspace.not-locked`,
  `workspace.lock-mismatch` and `workspace.client-encrypted`.
- **G10. `Dustpan-Channel` in the CORS list** of
  `Access-Control-Allow-Headers`.
- **G12. `Cache-Control: no-store`** on
  `GET /api/v2/state-versions/{id}/download`.

G8 (manual locks) waits for the manual locks of ADR 213 decision 10, and
ADR 210 covers its route. G6 is open in RFC 80. G7 is ADR 240.

## Consequences

- ADR 1 forbids edits to an accepted ADR. So the status lines of ADRs 25,
  28, 44 and 49 name this ADR. Their text does not change.
- The two force-release actions of ADR 28 still differ: `unit:write-forced`
  for OpenTofu and `unit:write` for the Pulumi cancel (ADR 20). RFC 19 can
  still remove the difference.
- The route table of `internal/api/tfe`, the route test, the attribute
  `can-force-unlock` and the 409 text of a client-encrypted release change in
  one change, with its e2e step first (ADR 12).
- The security model guide and the OpenTofu guide say which action a
  force-unlock needs, in the same change (ADR 214).
