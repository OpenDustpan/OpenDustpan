# 393. Output sharing across any boundary: reader identities and linked units (owner)

Status: accepted · 2026-11-21. Decided by the project owner (owner, 2026-11-21, in session). No RFC precedes it

Changes:

- Supersedes ADR 273 decision 3: a sharing rule no longer names a unit or a collection of another namespace as its consumer. A rule names a reader by issuer and stable IDs, and a linked unit carries the read across a boundary.
- Changes ADR 273 decision 5: check 2 (the starter holds `unit:read-outputs` on the producer) applies to reads inside one namespace only. A read from another namespace is decided by the unit identity.
- Changes ADR 216 decision 8: for output sharing, the producer rule and the reader linked unit are the acceptance of both sides. A namespace link and a dependency edge across namespaces still follow decision 8.
- Changes ADR 220 decision 12: the same change as for ADR 216 decision 8. A namespace backup and a blueprint never cross namespaces, as before.

## Context

ADR 273 decision 3 made a rule across namespaces a join in one database. The producer row names a consumer unit row of another namespace. This has three faults:

- A purge of a consumer namespace changes rows of the producer namespace.
- The audit entry exists on one side only.
- The feature works only when both namespaces are on one installation.

The owner asked for one cut that works in four cases. These are: on one installation, across installations, between OpenDustpan Cloud and a self-hosted installation, and between installations that share no database.

The owner decided on 21 November 2026, in session. OpenDustpan has no users, so no rule needs a migration path.

## Decision

1. **A reader is an identity (owner, 2026-11-21).** A sharing rule stores its reader as an issuer and stable-ID claims. The issuer is the OIDC issuer of an OpenDustpan namespace (ADR 204). The claims are the namespace ID, and the unit ID or the collection ID. A rule never stores a name for a match (ADR 391). A reader in the producer namespace is the same kind of rule with the local issuer. There is one rule type for a local reader and for a remote reader.
2. **A reader is an OpenDustpan namespace, on any installation (owner, 2026-11-21).** An issuer that is not an OpenDustpan namespace is not a reader for now. A CI system is an example. A later RFC can add it.
3. **The unit identity decides (owner, 2026-11-21).** A job proves its identity with a short-lived token from the issuer of its namespace. In the token, `sub` is the stable ID of the unit, and `aud` is the producer installation. The rule of the producer decides. The grants of the reader namespace decide who can run the reader unit. The token carries the starter as a claim, for the audit entry of the producer only. The claim does not decide. Check 2 of ADR 273 decision 5 stays for reads inside one namespace only.
4. **Delegated access is visible (owner, 2026-11-21).** A rule with a reader in another namespace is delegated access. Access to the outputs of the producer then depends on an access system that can be outside the control of the producer. The console and the docs say this at the rule. The periodic access review (ADR 52) lists these rules next to the grants of users. It flags them.
5. **What a rule shares (owner, 2026-11-21).** By default, a rule shares each root output that is not secret, also an output that someone adds later. The producer can limit a rule to chosen fields. A secret output is shared only when the rule names it.
6. **A secret output is encrypted for each reader (owner, 2026-11-21).** The reader namespace publishes an encryption key next to its signing keys. At each read, the producer encrypts each shared secret output to that key, as a JWE. The producer never sends a secret in plain text. The reader side decrypts it and wraps it again with its own secrets provider.
7. **Linked units (owner, 2026-11-21).** A linked unit is a read-only stack or workspace in the namespace of the consumer. It points at the URL of the producer unit. This holds on the same installation and on another one.
   - To make a linked unit is to make a request. The producer approves it, and the approval makes the rule.
   - Inside one namespace, `StackReference` and `terraform_remote_state` read the unit directly, as today.
   - The real CLIs read the linked unit through their own backend with no change. The linked unit serves a state that holds only the shared outputs.
   - The reader side fetches from the producer with the existing outputs route, `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs`, with the token of the job.
   - One code path serves a producer on the same installation and a producer on another one.
8. **Versions (owner, 2026-11-21).** A linked unit serves the latest version of the producer by default. The consumer can pin a version, and moves the pin by hand. Each run records the version that it read and the producer apply that made it.
9. **Change events: a thin signed event and polling (owner, 2026-11-21).** The reader subscribes with its token. The subscription needs the same rule as a read. When the producer has a new version, it sends a webhook that holds only the unit and the version. The producer signs it with the issuer key of its namespace. The reader verifies it with the published keys of the producer, and then pulls. The reader also polls with an ETag on a schedule, because a webhook can be lost.
10. **The producer is not reachable (owner, 2026-11-21).** On a network error or a timeout only, the linked unit serves the last version that it read.
    - It never does this after a 401, 403 or 404 from the producer. A removed rule is never bypassed.
    - A namespace setting limits the age of that version. The default is 24 hours.
    - The run record and the console show "stale, read at <time>".
    - Past the limit, the read fails.
11. **Off until enabled (owner, 2026-11-21).** A reader in another namespace needs a namespace setting turned on, on both sides. A reader on another installation also needs the operator to allow the URL of that installation in the egress rules. On OpenDustpan Cloud, sharing between tenants is off until each tenant turns it on.
12. **Each side audits its own side (owner, 2026-11-21).** The producer namespace records each read, each denial and each rule change. Each record holds the claims that the reader presented. The reader namespace records each fetch and the version. A purge or a removal in one namespace never changes a row of another namespace. A rule whose reader no longer exists matches nothing. It stays visible until someone removes it.
13. **No join across namespaces in the data model (owner, 2026-11-21).** This supersedes ADR 273 decision 3. It changes ADR 220 decision 12 and ADR 216 decision 8 for output sharing. Sharing needs the rule of the producer and the linked unit of the reader. These two are the acceptance of both sides. The implementation change removes the code of the cross-namespace consumer rules of ADR 273 decision 3. There is no migration.

## Consequences

These points follow from the decisions. They are not words of the owner.

- The read of a remote reader is security code. It needs one table of hostile cases for each layer, and a test for each guard (ADR 250).
- ADR 391 leaves open how a save learns the stable ID of a consumer in another namespace. This does not apply to output sharing. The request of a linked unit carries the IDs of the reader.
- A trigger that follows a linked unit can use the events of decision 9. This needs no new decision.
- The reader namespace can see a producer only through the outputs that a rule shares. It never reads the state of the producer.
- A linked unit needs its own storage of the last version, the pin and the run records. The implementation change plans it.
- The console and the docs of output sharing change in the same pull request as the code (ADR 214).

## Not decided here

- Issuers that are not OpenDustpan namespaces, for example a CI system. A later RFC can decide them.
