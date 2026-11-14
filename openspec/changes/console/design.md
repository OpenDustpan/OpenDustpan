## Context

The server owns members, groups, grants, the access-review export and the token warning field in groups-roles-grants and token-life-cycle-expiry-email. The access endpoint route lives with move-caller-routes-api-server. This change adds no server route. It wires the browser tests of the console repository against the suite server and shows a locked section from the access endpoint answer. The locked-section layout follows draft RFC 52, so the server reason shape stays generic and stable.

## Decisions

1. **Browser-test wiring only.** Run the browser tests of the console repository against the suite server over TLS for add member, make group, give and revoke grant, download export, expiry banner and locked section. The grant, export and banner behaviors are defined and tested in their own changes.
2. **Access endpoint reason without existence leak.** Show locked sections from the access endpoint answer for the section object. On deny the server gives one generic reason that names the grant to ask for and never names an object the caller cannot see, with the same status and body for unseen and nonexistent objects.

## Structure

No new stored type. The change adds no unexported type and keeps the existing access answer shape.

## Risks

- Browser tests run in the console repository and can drift from the server API shape.
- A locked-section text can leak the name of an object that the principal cannot see.
- RFC 52 is draft, so the locked-section layout may change in the console repository.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| reason redaction | Access endpoint answer uses a generic reason that names the needed grant and never names an unseen object, with the same status and body for nonexistent and unseen objects. | Ask for the access of an unseen object and a nonexistent object and check the same status, body and reason with no name leak. |
