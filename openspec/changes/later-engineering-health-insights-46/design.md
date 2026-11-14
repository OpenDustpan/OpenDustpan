## Context

Issue #2038 reserves engineering-health insights for later work after Milestone 7. The next step is a full RFC 46 with the research as its evidence.

## Decisions

1. **Defer all behavior to RFC 46.** Record only the boundary from the issue: team-level only, minimum group 5, no per-person filter or ranking, no error or log text, all opt-in off by default with a stored visible setting. Metric definitions, new records, survey shape, published definitions with record links, and API shape wait for RFC 46.

## Structure

No new unexported type or field and no behavior change. RFC 46 defines the structure that makes a privacy breach impossible.

## Risks

- An insight view built before RFC 46 is accepted creates privacy and scope risk without an accepted definition.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | No behavior change; targets are set in RFC 46 |  |  |
