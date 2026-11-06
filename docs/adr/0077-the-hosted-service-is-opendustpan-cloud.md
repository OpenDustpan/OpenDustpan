# 77. The name of the hosted service is OpenDustpan Cloud (owner)

Status: accepted · 2026-10-17 · adds to ADR 59

Changed by:

- ADR 311 extends: the header of OpenDustpan Cloud and the account switcher.

## Context

ADR 59 decides that the project runs a hosted service, and that the hosted
service has no feature that the open-source server lacks. ADR 59 calls the
build of the hosted service "the hosted distribution". It gives the service
no name, and it says nothing about domains.

The project owner decided on 2026-10-17 that the name of the hosted service
is "OpenDustpan Cloud". The owner also decided which domain belongs to the
service and which to the project. This ADR records that decision.

## Decision

The project owner decided on 2026-10-17.

1. **The name.** The hosted service is OpenDustpan Cloud. Text that names
   the hosted service writes "OpenDustpan Cloud".
2. **The domains.** `opendustpan.com` is the domain of OpenDustpan Cloud:
   its pricing, its sign-up and its hosted console. `opendustpan.org` is the
   domain of the open-source project: its documentation, its source and its
   community.
3. **One product, not an edition.** OpenDustpan Cloud is the same software
   that the project runs for its customers. The rules of ADR 59 apply to it
   in full. It has no feature that the open-source server lacks, and no plan
   or edition check. "Cloud" names where the server runs, never a set of
   features.
4. **The word "hosted distribution".** The glossary entry "Hosted
   distribution" becomes "OpenDustpan Cloud". ADR 59 keeps its own words,
   because ADR 1 forbids edits to an accepted ADR.

## Consequences

- `GLOSSARY.md` names the entry OpenDustpan Cloud and lists "hosted
  distribution" under _Avoid_.
- Pages of the project link to `opendustpan.com` for pricing and sign-up,
  and pages of OpenDustpan Cloud link to `opendustpan.org` for the
  documentation and the source.
- No code changes. The server holds no product name. The console labels of
  ADR 59 decision 6 can show the name.
