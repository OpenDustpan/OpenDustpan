# 288. The sign-in page looks the same on every installation

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026. Amends ADR 235 (decisions 9 and 10)

## Context

ADR 235 lets an operator set a logo and an accent color. Decision 9 puts them in the public sign-in answer, so the sign-in page can show them. A person who is not signed in can see that page. So can an attacker.

A phishing kit copies the logo of a target first, because a logo makes a fake page look real. A sign-in page that shows the operator's logo helps the kit.

## Decision

1. **One look before sign-in (owner).** The sign-in page looks the same on every installation. It shows the installation name as text. It shows no operator logo and no operator accent color. (owner)
2. **Branding after sign-in (owner).** The console shows the operator's logo and accent color only after a person signs in. (owner)
3. **The public answer holds no logo and no accent.** The public sign-in answer of ADR 235 decision 9 stops holding `accent` and `logo`. It keeps `productName`, `agentName` and the other fields. The logo route of ADR 235 decision 10 asks for a sign-in. It leaves the public list of the route test. This is a defense-in-depth layer: the page does not show the branding, and the server does not serve it before sign-in.
4. **The settings stay.** The branding settings of ADR 235 decision 2 keep their names, their values and their audit entries. Only the place that shows them changes.
5. **The email rule stays.** Emails show the product name as text and load no image (ADR 235 decision 11).

## Consequences

- ADR 235 gets a pointer to this ADR.
- The route test moves the logo route from the public list to the list of routes with an action.
- `OpenDustpan/console` shows the logo and the accent after sign-in only.
- A test sends a request with no credential to the logo route and expects a refusal.
